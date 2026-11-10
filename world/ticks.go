package world

import (
	"cmp"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
	"math"
	"slices"
)

// Block ticks, as BDS 1.26.60 runs them (API_DESIGN.md, "Regions and
// ticking"). Every chunk a player's simulation zone or a ticking ticket
// holds ticks once per tick: random ticks first, then its two tick queues. The queues are
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
	// Placed runs when SetBlock put block at pos on layer in place of
	// another state, as BDS's onPlace.
	Placed(r *Region, pos cube.Pos, block uint32, layer uint8)
	// NeighbourChanged runs for block at pos on layer when UpdateNeighbours
	// is called for its neighbour from, as BDS's neighborChanged.
	NeighbourChanged(r *Region, pos, from cube.Pos, block uint32, layer uint8)
}

// RegionTicker is a BlockTicker that also works per region and per chunk, for
// systems such as circuits that span blocks. Runs: owner of the region.
type RegionTicker interface {
	// BeforeBlockTicks runs once per region tick, before its chunks tick.
	BeforeBlockTicks(r *Region)
	// ChunkTicked runs after a chunk's block ticks.
	ChunkTicked(r *Region, pos mcdb.ChunkPos)
	// ChunkArrived runs when a chunk has loaded into the region.
	ChunkArrived(r *Region, pos mcdb.ChunkPos)
	// RegionsMerged runs when dst took over everything src held. Neither is
	// ticking and src ends after this.
	RegionsMerged(dst, src *Region)
	// RegionSplit runs when n took the chunks in says from r. Neither is ticking.
	RegionSplit(r, n *Region, in func(pos mcdb.ChunkPos) bool)
}

// ChunkSpawner is the natural mob spawner's part of a chunk's tick. Runs: owner of the region.
type ChunkSpawner interface {
	// SpawnTick runs on the chunk at pos once per world tick, after its block ticks.
	SpawnTick(r *Region, pos mcdb.ChunkPos)
	// PopulationSpawn runs once on a newly generated chunk, on its first tick: the herd
	// BDS's post-process spawn gives it.
	PopulationSpawn(r *Region, pos mcdb.ChunkPos)
}

// WeatherTicker is weather's part of a chunk's tick (BDS's LevelChunk tick before its random
// ticks: lightning, then freezing and snow, randomtickspeed times). Runs: owner of the region.
type WeatherTicker interface {
	// ChunkWeather runs on the chunk at pos once per world tick, before its random ticks,
	// with the dimension's random tick speed.
	ChunkWeather(r *Region, pos mcdb.ChunkPos, speed int32)
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

// rebase moves the queue back to now if its clock is ahead of it, which happens when the world
// tick went back because the server was killed before saving it. Each tick keeps how long it had
// left: otherwise it would wait for however long was lost. Beyond BDS, and only for this.
func (q *tickQueue) rebase(now int64) {
	ahead := q.clock - now
	if ahead <= 0 {
		return
	}
	q.clock = now
	for i := range q.ticks {
		q.ticks[i].time -= ahead
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

// CurrentTick returns the world's tick. Runs: anywhere.
func (r *Region) CurrentTick() int64 { return r.d.sched.CurrentTick() }

// CancelTick removes every tick for block's type waiting at pos on the ticking
// queue, as a lamp that gets power again does. Runs: owner.
func (r *Region) CancelTick(pos cube.Pos, block uint32) {
	c, ok := r.chunkAt(pos)
	if !ok {
		return
	}
	typ, _ := r.d.conf.Blocks.Type(block)
	q := &c.ticks
	kept := q.ticks[:0]
	for _, t := range q.ticks {
		if tt, _ := r.d.conf.Blocks.Type(t.block); t.pos != pos || tt != typ {
			kept = append(kept, t)
		}
	}
	if len(kept) == len(q.ticks) {
		return
	}
	q.ticks = kept
	// A sorted list is a valid heap.
	slices.SortFunc(q.ticks, func(a, b scheduledTick) int {
		if a.before(&b) {
			return -1
		}
		return 1
	})
	c.ticksDirty = true
}

// ScheduledTickAt reports whether a tick for block's type is waiting at pos
// on the ticking queue, due in exactly delay ticks. Runs: owner.
func (r *Region) ScheduledTickAt(pos cube.Pos, block uint32, delay int32) bool {
	c, ok := r.chunkAt(pos)
	if !ok {
		return false
	}
	q := &c.ticks
	typ, _ := r.d.conf.Blocks.Type(block)
	for i := range q.ticks {
		t := &q.ticks[i]
		if tt, _ := r.d.conf.Blocks.Type(t.block); t.pos == pos && tt == typ && t.time == q.clock+int64(delay) {
			return true
		}
	}
	return false
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

// tickBlocks ticks every chunk in a player's simulation zone or held by a
// ticking ticket once, in the order of the players and their zones, then
// of the tickets.
func (r *Region) tickBlocks() {
	now := r.d.sched.CurrentTick()
	pass := int64(r.ticks) //nolint:gosec // a tick count
	speed := r.d.randomTickSpeed.Load()
	r.planRandomTicks(pass, speed)
	defer clear(r.plans)
	for _, v := range r.viewers {
		if v.lastRadius < 0 {
			continue
		}
		for _, pos := range v.scratch {
			r.tickChunk(pos, now, pass, speed)
		}
	}
	for _, h := range r.tickets {
		if !h.t.ticking {
			continue
		}
		for _, pos := range h.chunks {
			r.tickChunk(pos, now, pass, speed)
		}
	}
}

// neverTicked is a chunk's tickedAt before its first tick, far enough below any pass that
// rebasing it between regions cannot make it look recent.
const neverTicked = math.MinInt64 / 2

// tickChunk gives the chunk at pos its block ticks, once per region pass. now is the world
// tick the queues run by.
func (r *Region) tickChunk(pos mcdb.ChunkPos, now, pass int64, speed int32) {
	c, ok := r.chunks[pos]
	if !ok || c.tickedAt == pass {
		return
	}
	c.tickedAt = pass
	if c.col.Finalisation == chunk.FinalisationNeedsPopulation {
		// BDS's LevelChunk tick: a chunk that needs population gets the spawner's
		// post-process spawn before anything else ticks, then counts as done.
		c.col.Finalisation = chunk.FinalisationDone
		c.dirty = true
		if sp := r.d.conf.Spawner; sp != nil {
			sp.PopulationSpawn(r, pos)
			if c, ok = r.chunks[pos]; !ok {
				return
			}
		}
	}
	if w := r.d.conf.Weather; w != nil {
		w.ChunkWeather(r, pos, speed)
		if c, ok = r.chunks[pos]; !ok {
			return
		}
	}
	if !r.runPlannedTicks(c, pass, speed) {
		r.randomTicks(c, speed)
	}
	r.runQueue(c, &c.ticks, now)
	r.runQueue(c, &c.randomTicks, c.randomTicks.clock+int64(speed))
	r.tickBlockEntities(c)
	if rt, ok := r.d.conf.Ticker.(RegionTicker); ok {
		rt.ChunkTicked(r, pos)
	}
	if sp := r.d.conf.Spawner; sp != nil {
		if _, ok := r.chunks[pos]; ok {
			sp.SpawnTick(r, pos)
		}
	}
}

// ChunkTicked reports whether the chunk at pos ticked in this region's current or previous
// tick (BDS's ticking chunk: anything that runs before the block ticks of this tick sees the
// last tick's), and loaded false if the region does not hold it. It counts the region's own
// ticks, so a late tick never makes chunks look unticked. Runs: owner, or a parallel think.
func (r *Region) ChunkTicked(pos mcdb.ChunkPos) (recent, loaded bool) {
	c, ok := r.chunks[pos]
	if !ok {
		return false, false
	}
	return c.tickedAt >= int64(r.ticks)-1, true //nolint:gosec // a tick count
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
// EachChunk calls fn for each chunk the region holds, in a repeatable order.
// Runs: owner.
func (r *Region) EachChunk(fn func(pos mcdb.ChunkPos)) {
	list := make([]mcdb.ChunkPos, 0, len(r.chunks))
	for pos := range r.chunks {
		list = append(list, pos)
	}
	slices.SortFunc(list, func(a, b mcdb.ChunkPos) int {
		if c := cmp.Compare(a[0], b[0]); c != 0 {
			return c
		}
		return cmp.Compare(a[1], b[1])
	})
	for _, pos := range list {
		fn(pos)
	}
}

// FindBlocks calls fn for each block of the chunk at pos on layer 0 that want
// accepts. want is asked once per palette entry, so a chunk without any is cheap.
// fn may change blocks. Runs: owner.
func (r *Region) FindBlocks(pos mcdb.ChunkPos, want func(block uint32) bool, fn func(p cube.Pos, block uint32)) {
	c, ok := r.chunks[pos]
	if !ok {
		return
	}
	type found struct {
		p     cube.Pos
		block uint32
	}
	var hits []found
	baseX, baseZ := int(pos[0])<<4, int(pos[1])<<4
	for i, sub := range c.col.Chunk.Sub() {
		if sub == nil || len(sub.Layers()) == 0 {
			continue
		}
		layer := sub.Layers()[0]
		palette := layer.Palette()
		any := false
		for j := range palette.Len() {
			if want(palette.Value(uint16(j))) { //nolint:gosec // palettes are small
				any = true
				break
			}
		}
		if !any {
			continue
		}
		for x := range byte(16) {
			for z := range byte(16) {
				for y := range byte(16) {
					if b := layer.At(x, y, z); want(b) {
						hits = append(hits, found{cube.Pos{baseX + int(x), r.d.r[0] + i<<4 + int(y), baseZ + int(z)}, b})
					}
				}
			}
		}
	}
	for _, h := range hits {
		fn(h.p, h.block)
	}
}

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

// ColumnTop walks the column x, z down from the top of its highest sub chunk holding a block
// and returns the Y above the first cell for which stop, given the cell's two layers, is true;
// the bottom of the world if none is. BDS's rain height map is found this way. false if the
// chunk is not loaded in this region. Runs: owner.
func (r *Region) ColumnTop(x, z int, stop func(layer0, layer1 uint32) bool) (int, bool) {
	c, ok := r.chunks[mcdb.ChunkPos{int32(x >> 4), int32(z >> 4)}] //nolint:gosec // block X and Z fit int32 chunks
	if !ok {
		return 0, false
	}
	air := r.d.conf.Blocks.Air()
	subs := c.col.Chunk.Sub()
	bx, bz := byte(x&15), byte(z&15) //nolint:gosec // 0 to 15
	for i := len(subs) - 1; i >= 0; i-- {
		if subEmpty(subs[i], air) {
			continue
		}
		layers := subs[i].Layers()
		for y := 15; y >= 0; y-- {
			b0, b1 := layers[0].At(bx, byte(y), bz), air //nolint:gosec // 0 to 15
			if len(layers) > 1 {
				b1 = layers[1].At(bx, byte(y), bz) //nolint:gosec // 0 to 15
			}
			if stop(b0, b1) {
				return i<<4 + y + 1 + r.d.r[0], true
			}
		}
	}
	return r.d.r[0], true
}
