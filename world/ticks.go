package world

import (
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Block ticks, as BDS 1.26.60 runs them (API_DESIGN.md, "Regions and
// ticking"). Every chunk a player's simulation zone holds ticks once per
// tick: random ticks first, then its two tick queues. The queues are
// BDS's: the pending ticks, on the world's tick clock, and the random
// ticking queue, whose clock advances by the random tick speed each tick.
// Both are saved with the chunk in BDS's format.

// BlockTicker gives blocks their tick behaviour. Its methods run on the
// region's tick and must not block. A nil BlockTicker ticks nothing.
type BlockTicker interface {
	// RandomTicks reports whether block takes random ticks on layer (0, or
	// 1 for the extra layer, such as water in a waterlogged block). It is
	// asked for every random tick, so it must be cheap.
	RandomTicks(block uint32, layer uint8) bool
	// RandomTick runs a random tick of block at pos on layer.
	RandomTick(r *Region, pos cube.Pos, block uint32, layer uint8)
	// QueuedTick runs a tick scheduled with ScheduleTick or
	// ScheduleRandomTick, for block at pos.
	QueuedTick(r *Region, pos cube.Pos, block uint32)
}

const (
	// maxQueuedTicks is how many due ticks one queue runs per tick; the
	// rest wait for the next.
	maxQueuedTicks = 100
	// tickNeighbourhood is how far around a queued tick, in blocks, every
	// chunk must be loaded for it to run. Otherwise it waits a tick.
	tickNeighbourhood = 8
	// randomTicksPerSubChunk is BDS's random ticks per sub chunk up to the
	// highest one holding a block, at random tick speed 1.
	randomTicksPerSubChunk = 2.5
	// randomTickSeed is the start of BDS's random tick position generator,
	// x = 3x + 1013904223.
	randomTickSeed = 0x283AE83
	// DefaultRandomTickSpeed is BDS's randomtickspeed game rule default.
	DefaultRandomTickSpeed = 1
)

// scheduledTick is a tick waiting in a queue.
type scheduledTick struct {
	pos      cube.Pos
	block    uint32
	time     int64
	priority int32
	// seq orders ticks with the same time and priority by when they were
	// added.
	seq uint64
}

func (a *scheduledTick) before(b *scheduledTick) bool {
	if a.time != b.time {
		return a.time < b.time
	}
	if a.priority != b.priority {
		return a.priority < b.priority
	}
	return a.seq < b.seq
}

// tickQueue is one of a chunk's two tick queues: a heap ordered by time,
// then priority. clock is the tick it last ran at, which new ticks are
// scheduled from.
type tickQueue struct {
	clock int64
	ticks []scheduledTick
	seq   uint64
}

func (q *tickQueue) load(ticks []chunk.ScheduledBlockUpdate, clock int64) {
	q.clock = clock
	for _, t := range ticks {
		q.push(t.Pos, t.Block, t.Tick, 0)
	}
}

// save returns the queue as the world library stores it, in heap order as
// BDS saves it.
func (q *tickQueue) save() []chunk.ScheduledBlockUpdate {
	if len(q.ticks) == 0 {
		return nil
	}
	out := make([]chunk.ScheduledBlockUpdate, len(q.ticks))
	for i, t := range q.ticks {
		out[i] = chunk.ScheduledBlockUpdate{Pos: t.pos, Block: t.block, Tick: t.time}
	}
	return out
}

func (q *tickQueue) push(pos cube.Pos, block uint32, time int64, priority int32) {
	q.seq++
	q.ticks = append(q.ticks, scheduledTick{pos: pos, block: block, time: time, priority: priority, seq: q.seq})
	for i := len(q.ticks) - 1; i > 0; {
		parent := (i - 1) / 2
		if !q.ticks[i].before(&q.ticks[parent]) {
			break
		}
		q.ticks[i], q.ticks[parent] = q.ticks[parent], q.ticks[i]
		i = parent
	}
}

func (q *tickQueue) pop() scheduledTick {
	top := q.ticks[0]
	last := len(q.ticks) - 1
	q.ticks[0] = q.ticks[last]
	q.ticks = q.ticks[:last]
	for i := 0; ; {
		l, small := 2*i+1, i
		if l < last && q.ticks[l].before(&q.ticks[small]) {
			small = l
		}
		if r := l + 1; r < last && q.ticks[r].before(&q.ticks[small]) {
			small = r
		}
		if small == i {
			break
		}
		q.ticks[i], q.ticks[small] = q.ticks[small], q.ticks[i]
		i = small
	}
	return top
}

// has reports whether a tick for block's type is waiting at pos.
func (q *tickQueue) has(r *Region, pos cube.Pos, block uint32) bool {
	typ, _ := r.d.conf.Blocks.Type(block)
	for i := range q.ticks {
		if t, _ := r.d.conf.Blocks.Type(q.ticks[i].block); q.ticks[i].pos == pos && t == typ {
			return true
		}
	}
	return false
}

// ScheduleTick asks for a queued tick of block at pos in delay ticks, as
// BDS's ticking queue: ticks due at the same time run lowest priority
// first. A negative delay runs the tick at once when the chunks around pos
// are loaded. False if the chunk at pos is not loaded here. Runs: owner.
func (r *Region) ScheduleTick(pos cube.Pos, block uint32, delay, priority int32) bool {
	return r.schedule(pos, block, delay, priority, false)
}

// ScheduleRandomTick is ScheduleTick on BDS's random ticking queue, whose
// clock advances by the random tick speed each tick. Runs: owner.
func (r *Region) ScheduleRandomTick(pos cube.Pos, block uint32, delay, priority int32) bool {
	return r.schedule(pos, block, delay, priority, true)
}

// HasScheduledTick reports whether a queued tick for block's type is
// waiting at pos, on either queue. Runs: owner.
func (r *Region) HasScheduledTick(pos cube.Pos, block uint32) bool {
	c, ok := r.chunkAt(pos)
	return ok && (c.ticks.has(r, pos, block) || c.randomTicks.has(r, pos, block))
}

func (r *Region) schedule(pos cube.Pos, block uint32, delay, priority int32, random bool) bool {
	c, ok := r.chunkAt(pos)
	if !ok {
		return false
	}
	if delay < 0 && r.neighboursLoaded(pos, tickNeighbourhood) {
		r.queuedTick(pos, block)
		return true
	}
	q := &c.ticks
	if random {
		q = &c.randomTicks
	}
	q.push(pos, block, q.clock+int64(delay), priority)
	c.ticksDirty = true
	return true
}

// neighboursLoaded reports whether every chunk within n blocks of pos, in X
// and Z, is loaded here.
func (r *Region) neighboursLoaded(pos cube.Pos, n int) bool {
	for x := (pos[0] - n) >> 4; x <= (pos[0]+n)>>4; x++ {
		for z := (pos[2] - n) >> 4; z <= (pos[2]+n)>>4; z++ {
			if _, ok := r.chunks[mcdb.ChunkPos{int32(x), int32(z)}]; !ok { //nolint:gosec // chunk coordinates fit int32
				return false
			}
		}
	}
	return true
}

// queuedTick runs a due tick if its block is still there: on layer 0 the
// block now there gets the tick, on layer 1 the block the tick was
// scheduled for, as BDS does. Both layers are read first.
func (r *Region) queuedTick(pos cube.Pos, block uint32) {
	ticker, blocks := r.d.conf.Ticker, r.d.conf.Blocks
	if ticker == nil {
		return
	}
	typ, _ := blocks.Type(block)
	b0, _ := r.Block(pos, 0)
	b1, _ := r.Block(pos, 1)
	air := blocks.Air()
	if t, _ := blocks.Type(b0); t == typ && b0 != air {
		ticker.QueuedTick(r, pos, b0)
	}
	if t, _ := blocks.Type(b1); t == typ && b1 != air {
		ticker.QueuedTick(r, pos, block)
	}
}

// runQueue runs the ticks of q due at now, at most maxQueuedTicks, in
// order. A tick whose neighbourhood is not loaded is put back for the next
// tick.
func (r *Region) runQueue(c *Chunk, q *tickQueue, now int64) {
	if q.clock == now {
		return
	}
	q.clock = now
	r.due = r.due[:0]
	for n := min(maxQueuedTicks, len(q.ticks)); len(q.ticks) > 0 && len(r.due) < n && q.ticks[0].time <= now; {
		r.due = append(r.due, q.pop())
	}
	if len(r.due) == 0 {
		return
	}
	c.ticksDirty = true
	for _, t := range r.due {
		if !r.neighboursLoaded(t.pos, tickNeighbourhood) {
			q.push(t.pos, t.block, q.clock, t.priority)
			continue
		}
		r.queuedTick(t.pos, t.block)
	}
}

// tickBlocks ticks every chunk in a player's simulation zone once, in the
// order of the players and their zones.
func (r *Region) tickBlocks() {
	now := r.d.sched.CurrentTick()
	speed := r.d.randomTickSpeed.Load()
	for _, v := range r.viewers {
		if v.lastRadius < 0 {
			continue
		}
		for _, pos := range v.scratch {
			c, ok := r.chunks[pos]
			if !ok || c.tickedAt == now {
				continue
			}
			c.tickedAt = now
			r.randomTicks(c, speed)
			r.runQueue(c, &c.ticks, now)
			r.runQueue(c, &c.randomTicks, c.randomTicks.clock+int64(speed))
		}
	}
}

// randomTicks gives random blocks of c a random tick: speed times 2.5 per
// sub chunk up to the highest one holding a block, at positions from BDS's
// generator.
func (r *Region) randomTicks(c *Chunk, speed int32) {
	ticker := r.d.conf.Ticker
	if ticker == nil || speed <= 0 {
		return
	}
	subs := c.col.Chunk.Sub()
	air := r.d.conf.Blocks.Air()
	top := -1
	for i := len(subs) - 1; i >= 0; i-- {
		if !subEmpty(subs[i], air) {
			top = i
			break
		}
	}
	if top < 0 {
		return
	}
	n := int32(float32(top+1)*randomTicksPerSubChunk) * speed
	height := uint16(16 * (top + 1)) //nolint:gosec // at most a few hundred
	baseX, baseZ := int(c.pos[0])<<4, int(c.pos[1])<<4
	for ; n > 0; n-- {
		r.lcg = 3*r.lcg + 1013904223
		x, z := uint8(r.lcg>>2)&15, uint8(r.lcg>>10)&15
		y := uint16(r.lcg>>18) % height
		sub := subs[y>>4]
		if sub == nil {
			continue
		}
		pos := cube.Pos{baseX + int(x), r.d.r[0] + int(y), baseZ + int(z)}
		layers := sub.Layers()
		if len(layers) > 0 {
			if b := layers[0].At(x, uint8(y&15), z); ticker.RandomTicks(b, 0) {
				ticker.RandomTick(r, pos, b, 0)
			}
		}
		if len(layers) > 1 {
			if b := layers[1].At(x, uint8(y&15), z); ticker.RandomTicks(b, 1) {
				ticker.RandomTick(r, pos, b, 1)
			}
		}
	}
}

// subEmpty reports whether every layer of sub is all air.
func subEmpty(sub *chunk.SubChunk, air uint32) bool {
	if sub == nil {
		return true
	}
	for _, l := range sub.Layers() {
		if p := l.Palette(); p.Len() != 1 || p.Value(0) != air {
			return false
		}
	}
	return true
}
