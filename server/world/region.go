package world

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"

	"github.com/helix-bedrock/helix/server/block/cube"
)

// Regions (Helix).
//
// A World is split into regions that tick in parallel, each on its own
// goroutine, the way Folia splits a Minecraft world. Chunks are grouped into
// square sections of SectionSize chunks. Every loaded chunk belongs to the
// section it is in, and every section to exactly one region. A region owns
// the chunks, entities, scheduled block updates, redstone state and pending
// neighbour updates of its sections; only its own goroutine touches them,
// through the *Tx it runs transactions and ticks with.
//
// Regions keep a gap of at least regionGap empty sections between each
// other. A region that wants a section closer than that to another region
// asks for the two to merge, and treats the chunk as unavailable until they
// have. Because of the gap, block updates, water, redstone and entities
// almost never reach another region: whatever a tick touches within a
// couple of sections of the area it owns is its own. When they do reach
// further, the chunk reads as air and writes are dropped, as if it were not
// loaded.
//
// A region whose sections fall apart into groups further than the gap from
// each other splits into one region per group. Regions that end up with no
// sections and no entities stop.
//
// Lock order: region run locks (by region id), then World.layoutMu, then a
// region's queue lock. A region holds its own run lock while it runs a
// transaction, and may take layoutMu briefly; it never waits for another
// region's run lock.

// DefaultSectionSize is the width of a section in chunks.
const DefaultSectionSize = 8

// regionGap is how many sections apart two regions must stay. Sections this
// close to each other belong to the same region.
const regionGap = 2

// splitInterval is how often, in ticks, a region checks whether it can split.
const splitInterval = 40

type secPos [2]int32

func (s secPos) near(o secPos) bool {
	dx, dz := s[0]-o[0], s[1]-o[1]
	return max(dx, -dx) <= regionGap && max(dz, -dz) <= regionGap
}

// region is a part of a World that ticks on its own goroutine. It embeds the
// World, so code moved from World methods to region methods keeps working.
type region struct {
	*World
	id uint64

	// Owned by the region: only touched while mu is held.
	chunks           map[ChunkPos]*Column
	chunkRequests    map[ChunkPos]*chunkRequest
	entities         map[*EntityHandle]ChunkPos
	scheduledUpdates *scheduledTickQueue
	redstone         *redstoneEngine
	neighbourUpdates []neighbourUpdate
	r                *rand.Rand
	ticked           int64

	// sections is guarded by World.layoutMu.
	sections map[secPos]struct{}

	// mu is held while the region runs a transaction or tick.
	mu sync.Mutex

	qmu     sync.Mutex
	q       []transaction
	merged  *region // set when merged into another region, under qmu
	stopped bool    // set when the region stops, under qmu
	wake    chan struct{}
	ticking bool // a tick is queued, under qmu
}

func (w *World) newRegion() *region {
	w.nextRegion++
	w.set.Lock()
	tick := w.set.CurrentTick
	w.set.Unlock()
	r := &region{
		World:            w,
		id:               w.nextRegion,
		chunks:           make(map[ChunkPos]*Column),
		chunkRequests:    make(map[ChunkPos]*chunkRequest),
		entities:         make(map[*EntityHandle]ChunkPos),
		scheduledUpdates: newScheduledTickQueue(tick),
		redstone:         newRedstoneEngine(tick),
		r:                rand.New(rand.NewPCG(w.seedRand.Uint64(), w.seedRand.Uint64())),
		sections:         make(map[secPos]struct{}),
		wake:             make(chan struct{}, 1),
	}
	w.regions[r] = struct{}{}
	return r
}

// start runs the region's goroutine. Synchronous worlds run everything on
// the caller instead.
func (r *region) start() {
	if r.conf.Synchronous {
		return
	}
	r.running.Add(1)
	go r.run()
}

func (r *region) String() string { return fmt.Sprintf("%v region %d", r.conf.Dim, r.id) }

func (r *region) run() {
	defer r.running.Done()
	for {
		select {
		case <-r.wake:
		case <-r.closing:
			return
		}
		for {
			r.qmu.Lock()
			if r.merged != nil || r.stopped {
				r.qmu.Unlock()
				return
			}
			if len(r.q) == 0 {
				r.qmu.Unlock()
				break
			}
			t := r.q[0]
			r.q[0] = nil
			r.q = r.q[1:]
			r.qmu.Unlock()

			r.mu.Lock()
			if m := r.mergedInto(); m != nil {
				// Merged between taking t and starting it: m runs it.
				r.mu.Unlock()
				m.post(t)
				return
			}
			t.Run(r)
			r.mu.Unlock()
		}
	}
}

// mergedInto returns the region r was merged into, or nil.
func (r *region) mergedInto() *region {
	r.qmu.Lock()
	defer r.qmu.Unlock()
	return r.merged
}

// post queues t on the region, following merges. Synchronous worlds run t
// at once.
func (r *region) post(t transaction) {
	if r.conf.Synchronous {
		t.Run(r)
		return
	}
	for {
		r.qmu.Lock()
		if r.merged != nil {
			m := r.merged
			r.qmu.Unlock()
			r = m
			continue
		}
		if r.stopped {
			r.qmu.Unlock()
			r = r.World.defaultRegion()
			continue
		}
		r.q = append(r.q, t)
		r.qmu.Unlock()
		select {
		case r.wake <- struct{}{}:
		default:
		}
		return
	}
}

// postTick queues a tick unless one is still waiting, so a slow region
// skips ticks instead of piling them up.
func (r *region) postTick(f func(tx *Tx)) {
	r.qmu.Lock()
	if r.ticking || r.merged != nil || r.stopped {
		r.qmu.Unlock()
		return
	}
	r.ticking = true
	r.qmu.Unlock()
	r.post(normalTransaction{c: make(chan struct{}), f: func(tx *Tx) {
		r.qmu.Lock()
		r.ticking = false
		r.qmu.Unlock()
		f(tx)
	}})
}

// secOf returns the section of a chunk.
func (w *World) secOf(pos ChunkPos) secPos {
	s := w.sectionSize
	return secPos{floorDiv(pos[0], s), floorDiv(pos[1], s)}
}

func floorDiv(a, b int32) int32 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// owns reports whether r owns the chunk at pos. Runs on r.
func (r *region) owns(pos ChunkPos) bool {
	if r.conf.Synchronous {
		return true
	}
	r.layoutMu.Lock()
	defer r.layoutMu.Unlock()
	return r.sectionOwner[r.secOf(pos)] == r
}

// claim makes r the owner of the section of pos if it is free and no other
// region is within the gap. If another region is too close, it asks for a
// merge and returns false. Runs on r.
func (r *region) claim(pos ChunkPos) bool { return r.claimOrMerge(pos, true) }

// claimOrMerge is claim; mergeOwner says whether to ask for a merge with
// the region that already owns the section.
func (r *region) claimOrMerge(pos ChunkPos, mergeOwner bool) bool {
	if r.conf.Synchronous {
		return true
	}
	s := r.secOf(pos)
	r.layoutMu.Lock()
	defer r.layoutMu.Unlock()
	if o := r.sectionOwner[s]; o != nil {
		if o != r && mergeOwner {
			// Something here reached into another region's chunk: merge, so
			// it works next time.
			r.requestMergeLocked([]*region{r, o})
		}
		return o == r
	}
	near := r.regionsNearLocked(s, r)
	if len(near) > 0 {
		r.requestMergeLocked(append(near, r))
		return false
	}
	r.sectionOwner[s] = r
	r.sections[s] = struct{}{}
	return true
}

// regionsNearLocked returns the regions other than except that own a
// section within the gap of s.
func (w *World) regionsNearLocked(s secPos, except *region) []*region {
	var out []*region
	for dx := int32(-regionGap); dx <= regionGap; dx++ {
		for dz := int32(-regionGap); dz <= regionGap; dz++ {
			if o := w.sectionOwner[secPos{s[0] + dx, s[1] + dz}]; o != nil && o != except && !slices.Contains(out, o) {
				out = append(out, o)
			}
		}
	}
	return out
}

// regionAt returns the region owning the chunk at pos, claiming its section
// for a nearby region or a new one if nobody owns it.
func (w *World) regionAt(pos ChunkPos) *region {
	if w.conf.Synchronous {
		return w.main
	}
	s := w.secOf(pos)
	w.layoutMu.Lock()
	if o := w.sectionOwner[s]; o != nil {
		w.layoutMu.Unlock()
		return o
	}
	near := w.regionsNearLocked(s, nil)
	var r *region
	if len(near) == 0 {
		r = w.newRegion()
		defer r.start()
	} else {
		r = near[0]
		if len(near) > 1 {
			w.requestMergeLocked(near)
		}
	}
	w.sectionOwner[s] = r
	r.sections[s] = struct{}{}
	w.layoutMu.Unlock()
	return r
}

// defaultRegion is where world tasks with no position run: the region at
// the world spawn.
func (w *World) defaultRegion() *region {
	return w.regionAt(chunkPosFromBlockPos(w.Spawn()))
}

// regionOfEntity returns the region an entity is in, or nil.
func (w *World) regionOfEntity(h *EntityHandle) *region {
	if w.conf.Synchronous {
		return w.main
	}
	w.layoutMu.Lock()
	defer w.layoutMu.Unlock()
	return w.entityOwner[h]
}

func (w *World) setEntityRegion(h *EntityHandle, r *region) {
	if w.conf.Synchronous {
		return
	}
	w.layoutMu.Lock()
	if r == nil {
		delete(w.entityOwner, h)
	} else {
		w.entityOwner[h] = r
	}
	w.layoutMu.Unlock()
}

// postAt runs f on the region owning pos.
func (w *World) postAt(pos ChunkPos, f func(tx *Tx)) {
	w.regionAt(pos).post(normalTransaction{c: make(chan struct{}), f: f})
}

// allRegions returns the running regions.
func (w *World) allRegions() []*region {
	if w.conf.Synchronous {
		return []*region{w.main}
	}
	w.layoutMu.Lock()
	defer w.layoutMu.Unlock()
	out := make([]*region, 0, len(w.regions))
	for r := range w.regions {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b *region) int { return int(a.id - b.id) })
	return out
}

// execAll runs f on every region and returns a channel closed when all
// are done.
func (w *World) execAll(f func(tx *Tx)) <-chan struct{} {
	done := make(chan struct{})
	rs := w.allRegions()
	if len(rs) == 0 {
		rs = []*region{w.defaultRegion()}
	}
	var wg sync.WaitGroup
	wg.Add(len(rs))
	for _, r := range rs {
		c := make(chan struct{})
		r.post(normalTransaction{c: c, f: f})
		go func() { <-c; wg.Done() }()
	}
	go func() { wg.Wait(); close(done) }()
	return done
}

// requestMergeLocked asks the merge goroutine to merge rs. Runs with
// layoutMu held.
func (w *World) requestMergeLocked(rs []*region) {
	w.mergeQueue = append(w.mergeQueue, slices.Clone(rs))
	select {
	case w.mergeWake <- struct{}{}:
	default:
	}
}

// mergeLoop merges regions that asked to be merged.
func (w *World) mergeLoop() {
	defer w.running.Done()
	for {
		select {
		case <-w.mergeWake:
		case <-w.closing:
			return
		}
		for {
			w.layoutMu.Lock()
			if len(w.mergeQueue) == 0 {
				w.layoutMu.Unlock()
				break
			}
			rs := w.mergeQueue[0]
			w.mergeQueue = w.mergeQueue[1:]
			w.layoutMu.Unlock()
			w.merge(rs)
		}
	}
}

// resolve follows merges to the live region.
func resolve(r *region) *region {
	for {
		m := r.mergedInto()
		if m == nil {
			return r
		}
		r = m
	}
}

// merge merges rs into one region.
func (w *World) merge(rs []*region) {
	for {
		var live []*region
		for _, r := range rs {
			r = resolve(r)
			if !r.isStopped() && !slices.Contains(live, r) {
				live = append(live, r)
			}
		}
		if len(live) < 2 {
			return
		}
		slices.SortFunc(live, func(a, b *region) int { return int(a.id - b.id) })
		for _, r := range live {
			r.mu.Lock()
		}
		w.layoutMu.Lock()
		changed := false
		for _, r := range live {
			if r.mergedInto() != nil || r.isStopped() {
				changed = true
			}
		}
		if !changed {
			target := live[0]
			for _, r := range live[1:] {
				if len(r.chunks) > len(target.chunks) {
					target = r
				}
			}
			for _, r := range live {
				if r != target {
					target.absorbLocked(r)
				}
			}
		}
		w.layoutMu.Unlock()
		for i := len(live) - 1; i >= 0; i-- {
			live[i].mu.Unlock()
		}
		if !changed {
			return
		}
	}
}

func (r *region) isStopped() bool {
	r.qmu.Lock()
	defer r.qmu.Unlock()
	return r.stopped
}

// absorbLocked moves everything of o into r and retires o. Both run locks
// and layoutMu are held.
func (r *region) absorbLocked(o *region) {
	// Do not split again straight away: a global task may be about to run.
	r.ticked = 1
	r.moveLocked(o, func(ChunkPos) bool { return true })
	for s := range o.sections {
		r.sectionOwner[s] = r
		r.sections[s] = struct{}{}
	}
	clear(o.sections)
	delete(r.regions, o)

	o.qmu.Lock()
	o.merged = r
	q := o.q
	o.q = nil
	o.qmu.Unlock()
	select {
	case o.wake <- struct{}{}:
	default:
	}
	for _, t := range q {
		r.post(t)
	}
}

// moveLocked moves the chunks for which has returns true, and everything in
// them, from o to r. Both run locks and layoutMu are held.
func (r *region) moveLocked(o *region, has func(ChunkPos) bool) {
	for pos, c := range o.chunks {
		if has(pos) {
			r.chunks[pos] = c
			delete(o.chunks, pos)
		}
	}
	for pos, req := range o.chunkRequests {
		if has(pos) {
			r.chunkRequests[pos] = req
			delete(o.chunkRequests, pos)
		}
	}
	for h, pos := range o.entities {
		if has(pos) {
			r.entities[h] = pos
			delete(o.entities, h)
			r.entityOwner[h] = r
		}
	}
	var moved []scheduledTick
	o.scheduledUpdates.ticks = slices.DeleteFunc(o.scheduledUpdates.ticks, func(t scheduledTick) bool {
		if has(chunkPosFromBlockPos(t.pos)) {
			moved = append(moved, t)
			return true
		}
		return false
	})
	for idx := range o.scheduledUpdates.furthestTicks {
		if has(chunkPosFromBlockPos(idx.pos)) {
			delete(o.scheduledUpdates.furthestTicks, idx)
		}
	}
	r.scheduledUpdates.add(moved)
	o.redstone.moveTo(r.redstone, has)
	o.neighbourUpdates = slices.DeleteFunc(o.neighbourUpdates, func(u neighbourUpdate) bool {
		if has(chunkPosFromBlockPos(u.pos)) {
			r.neighbourUpdates = append(r.neighbourUpdates, u)
			return true
		}
		return false
	})
}

// trySplit splits r into one region per group of sections further than the
// gap apart. Runs on r at the end of a tick.
func (r *region) trySplit() {
	if r.conf.Synchronous {
		return
	}
	r.layoutMu.Lock()
	defer r.layoutMu.Unlock()
	r.releaseEmptySectionsLocked()
	if len(r.sections) == 0 && len(r.entities) == 0 {
		r.stopLocked()
		return
	}
	groups := r.groupsLocked()
	if len(groups) < 2 {
		return
	}
	// The biggest group stays; every other group becomes a new region.
	slices.SortFunc(groups, func(a, b map[secPos]struct{}) int { return len(b) - len(a) })
	for _, g := range groups[1:] {
		n := r.newRegion()
		n.mu.Lock()
		n.moveLocked(r, func(p ChunkPos) bool { _, ok := g[r.secOf(p)]; return ok })
		for s := range g {
			delete(r.sections, s)
			n.sections[s] = struct{}{}
			r.sectionOwner[s] = n
		}
		n.mu.Unlock()
		n.start()
	}
}

// groupsLocked returns r's sections grouped by the gap.
func (r *region) groupsLocked() []map[secPos]struct{} {
	seen := make(map[secPos]bool, len(r.sections))
	var groups []map[secPos]struct{}
	for s := range r.sections {
		if seen[s] {
			continue
		}
		g := map[secPos]struct{}{}
		stack := []secPos{s}
		seen[s] = true
		for len(stack) > 0 {
			c := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			g[c] = struct{}{}
			for o := range r.sections {
				if !seen[o] && c.near(o) {
					seen[o] = true
					stack = append(stack, o)
				}
			}
		}
		groups = append(groups, g)
	}
	return groups
}

// releaseEmptySectionsLocked gives up sections with no loaded or loading
// chunks and no entities.
func (r *region) releaseEmptySectionsLocked() {
	used := make(map[secPos]bool, len(r.sections))
	for pos := range r.chunks {
		used[r.secOf(pos)] = true
	}
	for pos := range r.chunkRequests {
		used[r.secOf(pos)] = true
	}
	for _, pos := range r.entities {
		used[r.secOf(pos)] = true
	}
	for s := range r.sections {
		if !used[s] {
			delete(r.sections, s)
			delete(r.sectionOwner, s)
		}
	}
}

// stopLocked stops an empty region.
func (r *region) stopLocked() {
	delete(r.regions, r)
	r.qmu.Lock()
	r.stopped = true
	q := r.q
	r.q = nil
	r.qmu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
	if len(q) > 0 {
		// Run whatever was still queued at the spawn, after the lock is
		// released.
		go func() {
			for _, t := range q {
				r.World.defaultRegion().post(t)
			}
		}()
	}
}

// RegionCount returns how many regions the world has.
func (w *World) RegionCount() int {
	if w.conf.Synchronous {
		return 1
	}
	w.layoutMu.Lock()
	defer w.layoutMu.Unlock()
	return len(w.regions)
}

// DoGlobal runs f with the whole world in one region: every region is
// merged first, so f sees every chunk, entity and player. The regions split
// apart again on their own afterwards. Merging costs time proportional to
// the loaded world, so DoGlobal is for rare work: commands, sleeping
// through the night. It is safe to call from anywhere and returns at once.
func (w *World) DoGlobal(f func(tx *Tx)) *Task {
	if w.conf.Synchronous || w.RegionCount() <= 1 {
		return w.Do(f)
	}
	task := newTask()
	go func() {
		w.merge(w.allRegions())
		rs := w.allRegions()
		r := w.defaultRegion()
		if len(rs) > 0 {
			r = rs[0]
		}
		r.post(scheduledTransaction{task: task, f: func(tx *Tx) error {
			f(tx)
			return nil
		}})
	}()
	return task
}

// RegionInfo describes one region, for debugging.
type RegionInfo struct {
	ID       uint64
	Chunks   int
	Entities int
	Sections [][2]int32
}

// Regions describes the world's regions, for debugging. The chunk and
// entity counts are read without the region running, so they may be a
// tick stale.
func (w *World) Regions() []RegionInfo {
	rs := w.allRegions()
	out := make([]RegionInfo, 0, len(rs))
	for _, r := range rs {
		r.mu.Lock()
		info := RegionInfo{ID: r.id, Chunks: len(r.chunks), Entities: len(r.entities)}
		r.mu.Unlock()
		w.layoutMu.Lock()
		for s := range r.sections {
			info.Sections = append(info.Sections, [2]int32(s))
		}
		w.layoutMu.Unlock()
		out = append(out, info)
	}
	return out
}

// SectionSize returns the width of a section in chunks.
func (w *World) SectionSize() int { return int(w.sectionSize) }

var _ = cube.Pos{}
