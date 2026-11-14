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
	pos   cube.Pos
	block uint32
	// typ is the block type of block, which with pos makes the tick's key.
	typ      uint32
	time     int64
	priority int32
	// later is the latest time another tick was asked for at the same key while this one
	// waited, or no later than time if none was: the tick is put back for it when it runs.
	later int64
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

// tickKey is what makes two ticks the same tick: the block position and the block type.
type tickKey struct {
	pos cube.Pos
	typ uint32
}

// tickQueue is one of a chunk's two tick queues: a heap ordered by time,
// then priority. clock is the tick it last ran at, which new ticks are
// scheduled from.
//
// Beyond BDS, which keeps every tick asked for (lead 2026-11-14): a queue holds at most one
// tick per block position and type. A tick asked for while one waits at the same key is
// merged into it: the waiting tick takes the earlier time and remembers the latest one, and is
// put back for that when it runs. Every block that asked for a tick still gets its first and
// its last one; only ticks asked for in between merge, and each of those would re-check the
// block as it is when it ran anyway. So a queue is never longer than its chunk has blocks,
// however fast a contraption asks for ticks or however many a world file lists, and no block
// is cut off.
type tickQueue struct {
	clock int64
	ticks []scheduledTick
	// index is the heap slot of each key's tick.
	index map[tickKey]int32
	seq   uint64
}

// keptTicks is how many slots an empty queue keeps for its next ticks: a queue that grew past
// it in a burst gives its memory back once it is empty.
const keptTicks = 1024

// load fills the queue from a saved one. typeOf gives a block's type and present reports
// whether a block of that type is at pos: a tick for a block no longer there would do nothing
// when it ran, so it is left out instead of taking memory until then.
func (q *tickQueue) load(ticks []chunk.ScheduledBlockUpdate, clock int64, typeOf func(block uint32) uint32, present func(pos cube.Pos, typ uint32) bool) {
	q.clock = clock
	for _, t := range ticks {
		if typ := typeOf(t.Block); present(t.Pos, typ) {
			q.push(t.Pos, t.Block, typ, t.Tick, 0)
		}
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
		q.ticks[i].later -= ahead
	}
}

// save returns the queue as the world library stores it, in heap order as BDS saves it. A
// merged tick is saved as its two times, which load merges again.
func (q *tickQueue) save() []chunk.ScheduledBlockUpdate {
	if len(q.ticks) == 0 {
		return nil
	}
	out := make([]chunk.ScheduledBlockUpdate, 0, len(q.ticks))
	for _, t := range q.ticks {
		out = append(out, chunk.ScheduledBlockUpdate{Pos: t.pos, Block: t.block, Tick: t.time})
	}
	for _, t := range q.ticks {
		if t.later > t.time {
			out = append(out, chunk.ScheduledBlockUpdate{Pos: t.pos, Block: t.block, Tick: t.later})
		}
	}
	return out
}

// push asks for a tick of block, of type typ, at pos at time, merged into the tick waiting at
// the same key if there is one.
func (q *tickQueue) push(pos cube.Pos, block, typ uint32, time int64, priority int32) {
	q.pushLater(pos, block, typ, time, priority, time)
}

// pushLater is push for a tick that also still waits for a later time.
func (q *tickQueue) pushLater(pos cube.Pos, block, typ uint32, time int64, priority int32, later int64) {
	k := tickKey{pos, typ}
	if i, ok := q.index[k]; ok {
		q.merge(int(i), block, time, priority, later)
		return
	}
	if q.index == nil {
		q.index = make(map[tickKey]int32)
	}
	q.seq++
	q.ticks = append(q.ticks, scheduledTick{pos: pos, block: block, typ: typ, time: time, priority: priority, later: later, seq: q.seq})
	i := len(q.ticks) - 1
	q.index[k] = int32(i) //nolint:gosec // at most a chunk of ticks
	q.up(i)
}

// merge folds a tick asked for at time, still waiting for later, into the one in slot i.
func (q *tickQueue) merge(i int, block uint32, time int64, priority int32, later int64) {
	t := &q.ticks[i]
	switch {
	case time < t.time:
		t.later = max(t.later, t.time, later)
		t.time, t.priority, t.block = time, priority, block
		q.seq++
		t.seq = q.seq
		q.up(i)
	case time > t.time:
		t.later = max(t.later, later, time)
	default:
		t.later = max(t.later, later)
		if priority < t.priority {
			t.priority = priority
			q.up(i)
		}
	}
}

// place puts t in slot i and records it in the index.
func (q *tickQueue) place(i int, t scheduledTick) {
	q.ticks[i] = t
	q.index[tickKey{t.pos, t.typ}] = int32(i) //nolint:gosec // at most a chunk of ticks
}

// up moves the tick in slot i towards the root. Ticks it passes shift down into the hole it
// leaves, so each moved tick writes the index once instead of twice per swap.
func (q *tickQueue) up(i int) {
	t := q.ticks[i]
	start := i
	for i > 0 {
		parent := (i - 1) / 2
		if !t.before(&q.ticks[parent]) {
			break
		}
		q.place(i, q.ticks[parent])
		i = parent
	}
	if i != start {
		q.place(i, t)
	}
}

// down moves the tick in slot i towards the leaves, as up does the other way.
func (q *tickQueue) down(i int) {
	n := len(q.ticks)
	t := q.ticks[i]
	start := i
	for {
		l, small := 2*i+1, -1
		if l < n && q.ticks[l].before(&t) {
			small = l
		}
		if r := l + 1; r < n && q.ticks[r].before(&t) && (small < 0 || q.ticks[r].before(&q.ticks[l])) {
			small = r
		}
		if small < 0 {
			break
		}
		q.place(i, q.ticks[small])
		i = small
	}
	if i != start {
		q.place(i, t)
	}
}

// remove takes the tick in slot i out of the queue and returns it.
func (q *tickQueue) remove(i int) scheduledTick {
	t := q.ticks[i]
	last := len(q.ticks) - 1
	delete(q.index, tickKey{t.pos, t.typ})
	if i != last {
		q.place(i, q.ticks[last])
	}
	q.ticks = q.ticks[:last]
	if i < last {
		q.down(i)
		q.up(i)
	}
	if last == 0 && cap(q.ticks) > keptTicks {
		q.ticks, q.index = nil, nil
	}
	return t
}

func (q *tickQueue) pop() scheduledTick { return q.remove(0) }

// at returns the tick waiting at pos for blocks of type typ, and its heap slot.
func (q *tickQueue) at(pos cube.Pos, typ uint32) (*scheduledTick, int, bool) {
	i, ok := q.index[tickKey{pos, typ}]
	if !ok {
		return nil, 0, false
	}
	return &q.ticks[i], int(i), true
}

// has reports whether a tick for block's type is waiting at pos.
func (q *tickQueue) has(r *Region, pos cube.Pos, block uint32) bool {
	_, _, ok := q.at(pos, r.blockType(block))
	return ok
}

// blockType returns the type of block, the second half of a tick's key.
func (r *Region) blockType(block uint32) uint32 {
	typ, _ := r.d.conf.Blocks.Type(block)
	return typ
}

// loadTicks fills c's tick queues from its column, leaving out ticks outside the chunk and
// ticks for blocks no longer there.
func (r *Region) loadTicks(c *Chunk, col *chunk.Column) {
	air := r.d.conf.Blocks.Air()
	present := func(pos cube.Pos, typ uint32) bool {
		if pos[0]>>4 != int(c.pos[0]) || pos[2]>>4 != int(c.pos[1]) || pos[1] < r.d.r[0] || pos[1] > r.d.r[1] {
			return false
		}
		x, y, z := uint8(pos[0]&15), int16(pos[1]), uint8(pos[2]&15) //nolint:gosec // masked and range-checked
		for layer := range uint8(2) {
			if b := col.Chunk.Block(x, y, z, layer); b != air && r.blockType(b) == typ {
				return true
			}
		}
		return false
	}
	c.ticks.load(col.ScheduledBlocks, col.Tick, r.blockType, present)
	c.ticks.rebase(r.d.sched.CurrentTick())
	c.randomTicks.load(col.RandomTicks, col.RandomTick, r.blockType, present)
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
	if _, i, ok := c.ticks.at(pos, r.blockType(block)); ok {
		c.ticks.remove(i)
		c.ticksDirty = true
	}
}

// ScheduledTickAt reports whether a tick for block's type is waiting at pos
// on the ticking queue, due in exactly delay ticks. Runs: owner.
func (r *Region) ScheduledTickAt(pos cube.Pos, block uint32, delay int32) bool {
	c, ok := r.chunkAt(pos)
	if !ok {
		return false
	}
	t, _, ok := c.ticks.at(pos, r.blockType(block))
	at := c.ticks.clock + int64(delay)
	return ok && (t.time == at || t.later == at)
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
	q.push(pos, block, r.blockType(block), q.clock+int64(delay), priority)
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
			q.pushLater(t.pos, t.block, t.typ, q.clock, t.priority, t.later)
			continue
		}
		if t.later > t.time {
			// Put back first, so ticks the block asks for now merge into it.
			q.push(t.pos, t.block, t.typ, t.later, t.priority)
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
	if c.settle != nil {
		r.settleEdges(c)
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
