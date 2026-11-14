package world

import (
	"github.com/helix-bedrock/protocol/packet"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// sectionPos is a section's position: a square of 1<<SectionShift chunks a
// side. Regions are made of sections (RFC 0002, "Sections and regions").
type sectionPos [2]int32

type regionState uint8

const (
	stateReady regionState = iota
	stateTicking
	// stateParked waits, without ticking, to merge with a region that is
	// ticking, or for a parked region to take it.
	stateParked
	stateDead
)

// Region is a group of sections close together that ticks on its own, in
// parallel with other regions (RFC 0002). It owns the chunks it holds and
// the viewers standing in it. Regions merge when they come within
// MergeDistance sections of each other and split when their sections no
// longer touch, between ticks only.
type Region struct {
	d   *Dimension
	id  uint64
	seq uint64
	// ticking is set while the region's tick runs, for owner checks.
	ticking atomic.Bool
	// parallel is set while a Parallel phase runs, when reads must not write; job is that
	// phase, reused.
	parallel atomic.Bool
	job      parallelJob
	// thinkers are the entities with a Think this tick, and thinkFn runs one (made once, so
	// the phase allocates nothing).
	thinkers []*Entity
	thinkFn  func(i int)
	// seers are the players for the visibility and movement phases, visR2 and visN the view
	// disc, visFn and fanFn those phases' per-player parts.
	seers        []*Entity
	visR2, visN  int32
	visFn, fanFn func(i int)
	// fanout is set while the movement phase builds moves: Send and SendTo then queue in
	// outbox, sent to each player after.
	fanout bool
	outbox []outItem
	// raw encodes each outbox packet once for every player, into rawArena (rawEnds where
	// each one stops), reused tick after tick.
	raw      *packet.RawWriter
	rawArena []byte
	rawEnds  []int
	// plans and planned are this tick's random tick plans (randomplan.go); planFn draws one.
	plans   []chunkPlan
	planned []plannedTick
	planFn  func(i int)
	// viewPlanFn is planViewer, made once.
	viewPlanFn func(i int)
	// lightClusters, lightCells and lightSky are the relight split up by distance
	// (lightparallel.go); lightFn relights one cluster.
	lightClusters []lightCluster
	lightCells    map[[2]int32]int
	lightOrder    [][2]int32
	lightSky      bool
	lightFn       func(i int)
	// packetViewers are the players sendChunkPackets goes through, packetFn their part.
	packetViewers []*Viewer
	packetFn      func(i int)

	// Guarded by d.mu. sections, added, reshaped and contested are also
	// changed by the region's own tick, under d.mu.
	state    regionState
	next     time.Time
	sections map[sectionPos]struct{}
	// added are sections claimed since the last tick ended.
	added []sectionPos
	// reshaped means sections were added or given up: the region may need
	// to split.
	reshaped bool
	// contested means the region holds chunks in a section another region
	// owns: the two must merge.
	contested bool
	info      RegionInfo
	lastWarn  time.Time
	// meter times the region's ticks for TPS and MSPT.
	meter TickMeter
	// phases is how long each part of the last tick took, for the over-budget warning.
	phases tickPhases
	// late is drain's scratch list of the work it leaves for the next tick.
	late []entry

	inMu sync.Mutex
	in   []entry
	run  []entry

	// Owned by the region's tick, and by the dimension while it does not
	// tick.
	chunks map[mcdb.ChunkPos]*Chunk
	// light is the working space for relighting.
	light        *lightWork
	holds        map[mcdb.ChunkPos]int
	sectionHolds map[sectionPos]int
	loading      map[mcdb.ChunkPos]bool
	awaiting     map[mcdb.ChunkPos][]func(*Region)
	viewers      []*Viewer
	entities     []*Entity // ordered by runtime ID
	byChunk      map[mcdb.ChunkPos][]*Entity
	// leaving are the entities that moved out of the region's sections this
	// tick, handed off at its end.
	leaving []*Entity
	// tickets are the ticket holds in the region's sections, by ticket.
	tickets []ticketHold
	// lcg is BDS's random tick position generator, one per region so
	// regions tick in parallel; due is reused by runQueue.
	lcg   uint32
	due   []scheduledTick
	ticks uint64
	// chunkPackets are the packets for the players who have a chunk, block
	// changes among them, sent at the end of the tick.
	chunkPackets []chunkPacket
	// locals is the working state systems keep on the region (Local).
	locals map[any]any
}

// RegionInfo describes a region, as of the end of its last tick.
type RegionInfo struct {
	ID       uint64
	Sections int
	Chunks   int
	Viewers  int
	LastTick time.Duration
	// TPS is the region's ticks per second over its last 5 seconds (Region.TPS), MSPT the
	// mean time those ticks took.
	TPS  float64
	MSPT time.Duration
}

// entry is work queued for a region's next tick. Work for a chunk position
// or a viewer follows them when regions merge and split.
type entry struct {
	fn  func(*Region)
	pos mcdb.ChunkPos
	at  bool
	v   *Viewer
	// every goes to each region a split makes, as well as staying.
	every bool
	// arrival is a loaded chunk joining the region, which drain spreads over ticks.
	arrival bool
}

// ID returns the region's ID, unique within its dimension. Runs: anywhere.
func (r *Region) ID() uint64 { return r.id }

// Dimension returns the region's dimension. Runs: anywhere.
func (r *Region) Dimension() *Dimension { return r.d }

// Block returns the block at pos on layer, and false if its chunk is not
// loaded in this region or the call is made outside the region's tick. Above
// and below the dimension's range it is air, as BDS's BlockSource answers
// there, so entities fall through the bottom into the void instead of
// stopping as at an unloaded chunk. Runs: owner.
func (r *Region) Block(pos cube.Pos, layer uint8) (uint32, bool) {
	if pos[1] < r.d.r[0] || pos[1] > r.d.r[1] {
		if !r.ticking.Load() {
			return 0, false
		}
		if _, ok := r.chunks[mcdb.ChunkPos{int32(pos[0] >> 4), int32(pos[2] >> 4)}]; !ok { //nolint:gosec // block X and Z fit int32 chunks
			return 0, false
		}
		return r.d.conf.Blocks.Air(), true
	}
	c, ok := r.chunkAt(pos)
	if !ok {
		return 0, false
	}
	return c.col.Chunk.Block(uint8(pos[0]&15), int16(pos[1]), uint8(pos[2]&15), layer), true //nolint:gosec // masked and range-checked
}

// Biome returns the ID of the biome at pos, and false if its chunk is not loaded in this region.
// Above and below the dimension's range it is the biome at the nearest height inside it. Runs:
// owner.
func (r *Region) Biome(pos cube.Pos) (int16, bool) {
	pos[1] = min(max(pos[1], r.d.r[0]), r.d.r[1])
	c, ok := r.chunkAt(pos)
	if !ok {
		return 0, false
	}
	return int16(c.col.Chunk.Biome(uint8(pos[0]&15), int16(pos[1]), uint8(pos[2]&15))), true //nolint:gosec // masked, clamped, and biome IDs fit int16
}

// SetBlock sets the block at pos on layer, as BDS's chunk does: setting
// air on layer 0 over a block holding something in its second layer (water
// in a waterlogged block) moves that down to layer 0 instead. A block that can
// hold a liquid, set where a water source is, keeps it in its second layer, and
// keeps water already there; any other block clears the second layer. When the
// state changes, the Ticker's Placed runs for the new block. Neighbours
// are not told; call UpdateNeighbours for that. It returns false, changing
// nothing, if the chunk is not loaded in this region or the call is made
// outside the region's tick. Runs: owner.
func (r *Region) SetBlock(pos cube.Pos, layer uint8, block uint32) bool {
	c, ok := r.chunkAt(pos)
	if !ok {
		return false
	}
	air := r.d.conf.Blocks.Air()
	if layer == 0 && block == air {
		if extra, _ := r.Block(pos, 1); extra != air {
			r.set(c, pos, 1, air)
			block = extra
		}
	}
	old, _ := r.Block(pos, layer)
	if layer == 0 && block != air {
		r.keepWater(c, pos, old, block)
	}
	r.set(c, pos, layer, block)
	if layer == 0 && old != block {
		r.blockTypeChanged(c, pos, old, block)
	}
	if t := r.d.conf.Ticker; t != nil && old != block {
		t.Placed(r, pos, block, layer)
	}
	return true
}

// keepWater is BDS's chunk setting a block over a second layer: old, the block being
// replaced, moves into the second layer if block holds it; else what is there stays
// only if block holds it.
func (r *Region) keepWater(c *Chunk, pos cube.Pos, old, block uint32) {
	air := r.d.conf.Blocks.Air()
	extra, _ := r.Block(pos, 1)
	switch {
	case r.holdsLiquid(block, old):
		if extra != old {
			r.set(c, pos, 1, old)
			if t := r.d.conf.Ticker; t != nil {
				t.Placed(r, pos, old, 1)
			}
		}
	case extra != air && !r.holdsLiquid(block, extra):
		r.set(c, pos, 1, air)
	}
}

// holdsLiquid is BDS's test of block keeping liquid in its second layer: a block that
// can contain liquid, and a water source.
func (r *Region) holdsLiquid(block, liquid uint32) bool {
	blocks := r.d.conf.Blocks
	if !blocks.Properties(block).CanContainLiquid || blocks.Properties(liquid).Material != registry.MaterialWater {
		return false
	}
	depth, _ := blocks.States(liquid)["liquid_depth"].(int32)
	return depth == 0
}

// set changes the block at pos on layer and queues its update.
func (r *Region) set(c *Chunk, pos cube.Pos, layer uint8, block uint32) {
	old := c.col.Chunk.Block(uint8(pos[0]&15), int16(pos[1]), uint8(pos[2]&15), layer) //nolint:gosec // masked and range-checked
	r.lightChanged(c, pos, old, block)
	c.col.Chunk.SetBlock(uint8(pos[0]&15), int16(pos[1]), uint8(pos[2]&15), layer, block) //nolint:gosec // masked and range-checked
	r.updateBlock(pos, layer, block)
	c.dirty, c.net = true, nil
	c.version++
	r.d.cache.drop(c.pos)
}

// UpdateNeighbours tells the six neighbours of pos that it changed, as
// BDS's updateNeighborsAt: for each, in BDS's order, the Ticker's
// NeighbourChanged runs for its block on layer 0, then for the one in its
// second layer, skipping air. Neighbours outside the region are skipped.
// Runs: owner.
func (r *Region) UpdateNeighbours(pos cube.Pos) {
	t := r.d.conf.Ticker
	if t == nil {
		return
	}
	air := r.d.conf.Blocks.Air()
	for _, f := range direction.Updates() {
		n := pos.Side(f)
		for layer := range uint8(2) {
			if b, ok := r.Block(n, layer); ok && b != air {
				t.NeighbourChanged(r, n, pos, b, layer)
			}
		}
	}
}

// Local returns the value systems keep on the region under key, made with
// newFn the first time. It is for working state of a system that runs on
// region ticks, which run one at a time per region; it is not saved, and
// regions that merge or split start with none. Runs: owner.
func (r *Region) Local(key any, newFn func() any) any {
	if v, ok := r.locals[key]; ok {
		return v
	}
	if r.locals == nil {
		r.locals = map[any]any{}
	}
	v := newFn()
	r.locals[key] = v
	return v
}

func (r *Region) chunkAt(pos cube.Pos) (*Chunk, bool) {
	if !r.ticking.Load() || pos[1] < r.d.r[0] || pos[1] > r.d.r[1] {
		return nil, false
	}
	c, ok := r.chunks[mcdb.ChunkPos{int32(pos[0] >> 4), int32(pos[2] >> 4)}] //nolint:gosec // block X and Z fit int32 chunks
	return c, ok
}

// post queues e for the region's next tick.
func (r *Region) post(e entry) {
	r.inMu.Lock()
	r.in = append(r.in, e)
	r.inMu.Unlock()
}

// tick runs one tick, in RFC 0002's order. Only the phases that exist so
// far: queued work (finished loads, RunAt), player input, chunk sending,
// block ticks, then what players see (BDS's order: entities entering and
// leaving view,
// then movement), one flush per player, saving, and handing the entities
// that left to their new region.
func (r *Region) tick() {
	t := &r.phases
	t.start()
	r.ticks++
	r.drain()
	t.mark(PhaseDrain)
	r.tickEntities()
	t.mark(PhaseEntities)
	r.planViewers()
	r.viewers = slices.DeleteFunc(r.viewers, func(v *Viewer) bool { return !r.updateViewer(v) })
	t.mark(PhaseViewers)
	if rt, ok := r.d.conf.Ticker.(RegionTicker); ok {
		rt.BeforeBlockTicks(r)
	}
	t.mark(PhaseBeforeBlocks)
	r.tickBlocks()
	t.mark(PhaseBlocks)
	r.flushLight()
	t.mark(PhaseLight)
	r.sendChunkPackets()
	t.mark(PhaseChunks)
	r.updateVisibility()
	r.sendMovement()
	t.mark(PhaseMovement)
	for _, v := range r.viewers {
		if v.unflushed {
			v.unflushed = false
			v.flush()
		}
	}
	t.mark(PhaseFlush)
	r.saveRound()
	t.mark(PhaseSave)
	r.flushLight()
	r.handOff()
	t.mark(PhaseHandOff)
}

// drain runs the work queued for this tick. It reuses its buffers.
func (r *Region) drain() {
	r.inMu.Lock()
	r.run, r.in = r.in, r.run[:0]
	r.inMu.Unlock()
	start := time.Now()
	late := r.late[:0]
	for i := range r.run {
		e := r.run[i]
		r.run[i] = entry{}
		// Once joining chunks has used the tick's share, the chunks left join next tick, and so
		// does any later work at their positions, so it still finds them joined.
		if (e.arrival || e.at && r.lateAt(late, e.pos)) && time.Since(start) > arriveBudget {
			late = append(late, e)
			continue
		}
		e.fn(r)
	}
	r.late = late[:0]
	if len(late) == 0 {
		return
	}
	r.inMu.Lock()
	r.in = append(append(r.run[:0:0], late...), r.in...)
	r.inMu.Unlock()
	clear(late)
}

// lateAt reports a chunk arrival at pos among the work left for the next tick.
func (r *Region) lateAt(late []entry, pos mcdb.ChunkPos) bool {
	for _, e := range late {
		if e.arrival && e.pos == pos {
			return true
		}
	}
	return false
}

// arriveBudget is how long one tick spends joining loaded chunks before leaving the rest for
// the next: a burst of loads, such as a player joining, otherwise stalls one tick for all.
const arriveBudget = 10 * time.Millisecond

// owns reports whether the chunk at pos is in one of the region's sections.
func (r *Region) owns(pos mcdb.ChunkPos) bool {
	_, ok := r.sections[r.d.sectionOf(pos)]
	return ok
}

// hold keeps the chunk at pos loaded, claiming its section.
func (r *Region) hold(pos mcdb.ChunkPos) {
	r.holds[pos]++
	if r.holds[pos] > 1 {
		return
	}
	s := r.d.sectionOf(pos)
	r.sectionHolds[s]++
	if r.sectionHolds[s] == 1 {
		r.d.mu.Lock()
		r.d.claim(r, s)
		r.d.mu.Unlock()
	}
	r.request(pos)
}

// release drops a hold; the last one unloads the chunk, saving it first if
// it changed, and gives up the section once nothing in it is held.
func (r *Region) release(pos mcdb.ChunkPos) {
	n, ok := r.holds[pos]
	if !ok {
		return
	}
	if n > 1 {
		r.holds[pos] = n - 1
		return
	}
	delete(r.holds, pos)
	if c, ok := r.chunks[pos]; ok {
		r.save(c, true, true)
		r.unloadChunkEntities(pos)
		delete(r.chunks, pos)
	}
	s := r.d.sectionOf(pos)
	if r.sectionHolds[s] > 1 {
		r.sectionHolds[s]--
		return
	}
	delete(r.sectionHolds, s)
	if _, ok := r.sections[s]; ok {
		r.d.mu.Lock()
		delete(r.d.sections, s)
		delete(r.sections, s)
		r.reshaped = true
		r.d.mu.Unlock()
	}
}

// request loads a held chunk of an owned section if it is not loaded or
// loading. A chunk in a section another region owns waits for the merge.
func (r *Region) request(pos mcdb.ChunkPos) {
	if _, ok := r.chunks[pos]; ok || r.loading[pos] || !r.owns(pos) {
		return
	}
	r.loading[pos] = true
	r.d.poolLoad(pos)
}

// requestMissing requests every held chunk not loaded, after the region
// took over sections.
func (r *Region) requestMissing() {
	for pos := range r.holds {
		r.request(pos)
	}
}

// arrive takes a chunk a worker loaded.
func (r *Region) arrive(pos mcdb.ChunkPos, col *chunk.Column, nc *NetChunk, discard bool, light *chunkLight, settle []chunk.ScheduledBlockUpdate, live []loadedBlockEntity) {
	delete(r.loading, pos)
	if _, ok := r.chunks[pos]; ok || r.holds[pos] == 0 || !r.owns(pos) {
		return
	}
	c := &Chunk{pos: pos, col: col, net: nc, discard: discard, tickedAt: neverTicked, plannedAt: -1, light: light, settle: settle}
	c.ticks.load(col.ScheduledBlocks, col.Tick)
	c.ticks.rebase(r.d.sched.CurrentTick())
	c.randomTicks.load(col.RandomTicks, col.RandomTick)
	col.ScheduledBlocks, col.RandomTicks = nil, nil
	r.chunks[pos] = c
	r.joinLight(c)
	c.savedLive = r.loadEntities(c)
	r.loadBlockEntities(c, live)
	if fns, ok := r.awaiting[pos]; ok {
		delete(r.awaiting, pos)
		for _, fn := range fns {
			fn(r)
			r.release(pos)
		}
	}
	if rt, ok := r.d.conf.Ticker.(RegionTicker); ok {
		rt.ChunkArrived(r, pos)
	}
}

// runAt runs fn once the chunk at pos is loaded here.
func (r *Region) runAt(pos mcdb.ChunkPos, fn func(*Region)) {
	if _, ok := r.chunks[pos]; ok {
		fn(r)
		return
	}
	r.awaiting[pos] = append(r.awaiting[pos], fn)
	r.hold(pos)
}

// produce makes the encoded chunk at pos for players waiting for it.
func (r *Region) produce(pos mcdb.ChunkPos) {
	if c, ok := r.chunks[pos]; ok {
		switch {
		case c.net != nil:
			r.d.publish(pos, c.net)
		case !c.encoding:
			r.encode(c)
		}
		return
	}
	if r.loading[pos] {
		return
	}
	if r.holds[pos] > 0 && r.owns(pos) {
		r.loading[pos] = true
	}
	r.d.poolLoad(pos)
}

// netChunk returns the encoded chunk at pos if one is ready, starting an
// encode of a changed chunk of this region.
func (r *Region) netChunk(pos mcdb.ChunkPos) *NetChunk {
	if c, ok := r.chunks[pos]; ok {
		if c.net == nil && !c.encoding {
			r.encode(c)
		}
		return c.net
	}
	return r.d.cache.get(pos)
}

// encode rebuilds the network form of a changed chunk on a worker, from a
// snapshot, so the tick never waits for it.
func (r *Region) encode(c *Chunk) {
	c.encoding = true
	d, pos := r.d, c.pos
	snap, version, entities := c.col.Chunk.Snapshot(), c.version, c.networkBlockEntities()
	d.pool.do(pos, func() {
		nc := encodeNetChunk(pos, d.conf.Dim, snap, entities)
		if !d.postAt(pos, func(o *Region) { o.encoded(c, nc, version) }) {
			d.encodedUnloaded(c, nc, version)
		}
	})
}

func (r *Region) encoded(c *Chunk, nc *NetChunk, version uint64) {
	switch cur, ok := r.chunks[c.pos]; {
	case !ok:
		r.d.encodedUnloaded(c, nc, version)
	case cur != c:
		// Reloaded meanwhile, with its own network form.
	case c.version != version:
		r.encode(c) // changed again: encode the newer state
	default:
		c.encoding, c.net = false, nc
		r.d.publish(c.pos, nc)
	}
}

// saveChanged saves every chunk whose blocks changed this tick, and with
// ticks set, every chunk whose tick queues changed. Only the sub chunks
// changed since the last save are copied: the snapshot shares the rest.
func (r *Region) saveChanged(ticks bool) {
	for _, c := range r.chunks {
		r.save(c, ticks, ticks)
	}
}

// DefaultAutoSaveTicks is Config.AutoSaveTicks when it is 0: 30 seconds.
const DefaultAutoSaveTicks = 600

// saveRound is the tick's save: every chunk whose blocks changed, and the chunks whose
// turn of the auto-save interval this is with their entities and tick queues too.
func (r *Region) saveRound() {
	every := r.d.conf.AutoSaveTicks
	if every == 0 {
		every = DefaultAutoSaveTicks
	}
	for _, c := range r.chunks {
		full := every > 0 && (r.ticks+autoSavePhase(c.pos))%uint64(every) == 0
		r.save(c, full, false)
	}
}

// autoSavePhase spreads chunks over the auto-save interval by their position.
func autoSavePhase(pos mcdb.ChunkPos) uint64 {
	return uint64(uint32(pos[0])*autoSaveSpreadX + uint32(pos[1])) //nolint:gosec // a hash, wrapping is fine
}

// autoSaveSpreadX keeps neighbouring rows of chunks apart in the interval.
const autoSaveSpreadX = 31

const (
	// DefaultSaveDelayTicks is Config.SaveDelayTicks when it is 0: 5 seconds.
	DefaultSaveDelayTicks = 100
	// saveCostShare is how many times its last save's time a chunk waits before the
	// next, so saving one chunk takes at most about 1/saveCostShare of a worker: a
	// chunk of chests stuffed with books, moved by pistons, is hundreds of megabytes
	// each save, and saving it every few seconds would keep the disk and a core busy.
	saveCostShare = 100
	// maxSaveDelayTicks caps how long the slowest chunk waits: 10 minutes.
	maxSaveDelayTicks = 12000
	// tickNanos is one tick at 20 TPS.
	tickNanos = int64(time.Second / 20)
)

// save saves c if its blocks changed, or with ticks set, if its tick queues
// changed or it has or had live entities: they are saved with the blocks but
// do not make a save of their own every tick. A chunk saved less than its delay
// ago waits, the auto-save too (which then happens once it may), unless force:
// unloading and Close.
func (r *Region) save(c *Chunk, ticks, force bool) {
	ticks = ticks || c.fullPending
	if c.saveWait > 0 {
		c.saveWait--
		if !force {
			c.fullPending = ticks
			return
		}
	}
	if c.discard || !c.dirty && (!ticks || !c.ticksDirty && !c.savedLive && !r.holdsSaved(c.pos)) {
		return
	}
	// Block entities change only with a change that marks the chunk dirty; a save for
	// entities or tick queues alone keeps the stored ones instead of encoding them again.
	keepBlockEntities := !c.dirty
	c.dirty, c.ticksDirty, c.fullPending = false, false, false
	c.saveWait = r.saveDelay(c)
	d, pos, snap := r.d, c.pos, *c.col
	snap.Chunk = c.col.Chunk.Snapshot()
	snap.Entities, c.savedLive = r.entitiesToSave(c)
	snap.KeepBlockEntities = keepBlockEntities
	if !keepBlockEntities {
		snap.BlockEntities = c.savedBlockEntities()
	}
	snap.ScheduledBlocks, snap.Tick = c.ticks.save(), c.ticks.clock
	snap.RandomTicks, snap.RandomTick = c.randomTicks.save(), c.randomTicks.clock
	d.pool.do(pos, func() {
		start := time.Now()
		if err := d.conf.Store.StoreColumn(pos, d.conf.Dim, &snap); err != nil {
			d.log.Error("save chunk", "pos", pos, "err", err)
		}
		c.saveCost.Store(int64(time.Since(start)))
	})
}

// saveDelay is how many ticks c, just saved, waits before the next save: the set
// delay, or longer for a chunk whose last save was slow.
func (r *Region) saveDelay(c *Chunk) int32 {
	base := r.d.conf.SaveDelayTicks
	switch {
	case base == 0:
		base = DefaultSaveDelayTicks
	case base < 0:
		return 0
	}
	byCost := c.saveCost.Load() * saveCostShare / tickNanos
	return int32(min(max(int64(base), byCost), maxSaveDelayTicks)) //nolint:gosec // capped
}

// idle reports whether the region has nothing left to do. Under d.mu.
func (r *Region) idle() bool {
	r.inMu.Lock()
	queued := len(r.in)
	r.inMu.Unlock()
	return len(r.sections) == 0 && len(r.sectionHolds) == 0 && len(r.viewers) == 0 && len(r.entities) == 0 && len(r.tickets) == 0 &&
		len(r.awaiting) == 0 && queued == 0
}
