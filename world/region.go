package world

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"

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

	inMu sync.Mutex
	in   []entry
	run  []entry

	// Owned by the region's tick, and by the dimension while it does not
	// tick.
	chunks       map[mcdb.ChunkPos]*Chunk
	holds        map[mcdb.ChunkPos]int
	sectionHolds map[sectionPos]int
	loading      map[mcdb.ChunkPos]bool
	awaiting     map[mcdb.ChunkPos][]func(*Region)
	viewers      []*Viewer
	entities     []*Entity // ordered by runtime ID
	byChunk      map[mcdb.ChunkPos][]*Entity
	ticks        uint64
}

// RegionInfo describes a region, as of the end of its last tick.
type RegionInfo struct {
	ID       uint64
	Sections int
	Chunks   int
	Viewers  int
	LastTick time.Duration
}

// entry is work queued for a region's next tick. Work for a chunk position
// or a viewer follows them when regions merge and split.
type entry struct {
	fn  func(*Region)
	pos mcdb.ChunkPos
	at  bool
	v   *Viewer
}

// ID returns the region's ID, unique within its dimension. Runs: anywhere.
func (r *Region) ID() uint64 { return r.id }

// Dimension returns the region's dimension. Runs: anywhere.
func (r *Region) Dimension() *Dimension { return r.d }

// Block returns the block at pos on layer, and false if its chunk is not
// loaded in this region or the call is made outside the region's tick.
// Runs: owner.
func (r *Region) Block(pos cube.Pos, layer uint8) (uint32, bool) {
	c, ok := r.chunkAt(pos)
	if !ok {
		return 0, false
	}
	return c.col.Chunk.Block(uint8(pos[0]&15), int16(pos[1]), uint8(pos[2]&15), layer), true //nolint:gosec // masked and range-checked
}

// SetBlock sets the block at pos on layer. It returns false, changing
// nothing, if the chunk is not loaded in this region or the call is made
// outside the region's tick. Runs: owner.
func (r *Region) SetBlock(pos cube.Pos, layer uint8, block uint32) bool {
	c, ok := r.chunkAt(pos)
	if !ok {
		return false
	}
	c.col.Chunk.SetBlock(uint8(pos[0]&15), int16(pos[1]), uint8(pos[2]&15), layer, block) //nolint:gosec // masked and range-checked
	c.dirty, c.net = true, nil
	c.version++
	r.d.cache.drop(c.pos)
	return true
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
// then what players see (BDS's order: entities entering and leaving view,
// then movement), one flush per player, and saving.
func (r *Region) tick() {
	r.ticks++
	r.drain()
	r.tickEntities()
	r.viewers = slices.DeleteFunc(r.viewers, func(v *Viewer) bool { return !r.updateViewer(v) })
	r.updateVisibility()
	r.sendMovement()
	for _, v := range r.viewers {
		if v.unflushed {
			v.unflushed = false
			v.flush()
		}
	}
	r.saveChanged()
}

// drain runs the work queued for this tick. It reuses its buffers.
func (r *Region) drain() {
	r.inMu.Lock()
	r.run, r.in = r.in, r.run[:0]
	r.inMu.Unlock()
	for i := range r.run {
		r.run[i].fn(r)
		r.run[i] = entry{}
	}
}

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
		r.save(c)
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
func (r *Region) arrive(pos mcdb.ChunkPos, col *chunk.Column, nc *NetChunk, discard bool) {
	delete(r.loading, pos)
	if _, ok := r.chunks[pos]; ok || r.holds[pos] == 0 || !r.owns(pos) {
		return
	}
	r.chunks[pos] = &Chunk{pos: pos, col: col, net: nc, discard: discard}
	if fns, ok := r.awaiting[pos]; ok {
		delete(r.awaiting, pos)
		for _, fn := range fns {
			fn(r)
			r.release(pos)
		}
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
	snap, version := c.col.Chunk.Snapshot(), c.version
	d.pool.do(pos, func() {
		nc := encodeNetChunk(pos, d.conf.Dim, snap)
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

// saveChanged saves every chunk changed this tick. Only the sub chunks
// changed since the last save are copied: the snapshot shares the rest.
func (r *Region) saveChanged() {
	for _, c := range r.chunks {
		r.save(c)
	}
}

func (r *Region) save(c *Chunk) {
	if !c.dirty || c.discard {
		return
	}
	c.dirty = false
	d, pos, snap := r.d, c.pos, *c.col
	snap.Chunk = c.col.Chunk.Snapshot()
	d.pool.do(pos, func() {
		if err := d.conf.Store.StoreColumn(pos, d.conf.Dim, &snap); err != nil {
			d.log.Error("save chunk", "pos", pos, "err", err)
		}
	})
}

// idle reports whether the region has nothing left to do. Under d.mu.
func (r *Region) idle() bool {
	r.inMu.Lock()
	queued := len(r.in)
	r.inMu.Unlock()
	return len(r.sections) == 0 && len(r.sectionHolds) == 0 && len(r.viewers) == 0 && len(r.entities) == 0 &&
		len(r.awaiting) == 0 && queued == 0
}
