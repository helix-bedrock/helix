package village

import (
	"math"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// BDS 1.26.60's village numbers (VillageManager and Village, read in the 1.21.3 names).
const (
	// queryLimit is how many dwellers may wait for a POI scan.
	queryLimit = 64
	// scanPerTick is how many blocks the scan reads in one tick.
	scanPerTick = 1024
	// scanXZ and scanBelow/scanAbove are the scan box around the dweller: x and z from -16
	// to +15, y from -4 to +3.
	scanXZ, scanBelow, scanAbove = 16, 4, 3
	// joinRange is how far a POI or a dweller looks for a village to join.
	joinRange = 64
	// foundMargin is how far past a village's radius a bed founds a village of its own.
	foundMargin = 16
	// claimEvery and claimAt: claims run on ticks where tick % 30 == 15.
	claimEvery, claimAt = 30, 15
	// inactiveTicks is how long a dweller stays a member without coming near; nearRange
	// is how near counts.
	inactiveTicks = 24000
	nearRange     = 48
	// deadTicks is how long a village without beds or dwellers lasts.
	deadTicks = 120
	// radiusScale is getApproximateRadius's factor on the half sizes.
	radiusScale = 0.6
	// happyEvent is the happy villager particle level event a claim shows four times.
	happyEvent = 2009
	happyCount = 4
)

// Env is the engine's side: what a block is and which dwellers are in a region. Runs: the
// region's goroutine.
type Env interface {
	// POIAt is the POI the block at pos is, false for none. loaded is false when the
	// region does not hold the block.
	POIAt(r *world.Region, pos cube.Pos) (d *Def, loaded bool)
	// Dweller is the dweller with the ID in r, false when it is not there.
	Dweller(r *world.Region, id behavior.ID) (Dweller, bool)
	// LevelEvent shows a level event at p.
	LevelEvent(r *world.Region, id int32, p behavior.Vec3)
}

// Dweller is a mob with minecraft:dweller as the village sees it. The engine's mob
// implements it. Runs: the mob's region, outside its think.
type Dweller interface {
	ID() behavior.ID
	Pos() behavior.Vec3
	// HeadY is the height of the mob's head above its feet.
	HeadY() float32
	// Baby reports a baby.
	Baby() bool
	// Role is dweller_role; only "inhabitant" takes job sites.
	Role() string
	// Nitwit reports the family job sites are kept from (BDS's family at village +696).
	Nitwit() bool
	// Profession is the preferred profession (DwellerComponent +8), "" for none.
	Profession() string
	SetProfession(name string)
	// Locked reports trades a player has used (trade experience or tier above 0): the
	// profession stays when the job site goes.
	Locked() bool
	// Fire runs a definition event on the mob.
	Fire(event string)
	// SetPOI gives the mob its POI of the kind, or takes it (ok false).
	SetPOI(kind Kind, p behavior.POI, ok bool)
	// SetVillage tells the mob its village, nil for none.
	SetVillage(v *Village)
}

// POI is one point of interest a village holds.
type POI struct {
	Pos cube.Pos
	Def *Def
	// owners are the dwellers that claimed it, in claim order.
	owners []behavior.ID
}

// full reports a POI with no room for another owner.
func (p *POI) full() bool { return int32(len(p.owners)) >= p.Def.Capacity }

// view is the POI as a goal sees it.
func (p *POI) view() behavior.POI {
	v := behavior.POI{Pos: behavior.Pos{X: int32(p.Pos[0]), Y: int32(p.Pos[1]), Z: int32(p.Pos[2])}, Radius: p.Def.Radius, UseBox: p.Def.UseBox, Sound: p.Def.Sound} //nolint:gosec // world positions fit
	b := v.Pos
	v.Box = behavior.Box{Min: behavior.Vec3{X: float32(b.X), Y: float32(b.Y), Z: float32(b.Z)}, Max: behavior.Vec3{X: float32(b.X) + 1, Y: float32(b.Y) + 1, Z: float32(b.Z) + 1}}
	return v
}

// member is one dweller of a village.
type member struct {
	id behavior.ID
	// seen is the tick the dweller was last near the village.
	seen int64
	pois [kinds]*POI
	// lostJob is set when its job site went while it was not loaded: the job loss runs
	// once it is found.
	lostJob bool
}

// Village is one village: its POIs and dwellers. Its fields are the manager's, under the
// manager's lock; goals read it through its published snapshot (Bounds, FreePOI), which
// is safe from any goroutine.
type Village struct {
	ID       uuid.UUID
	pois     [kinds][]*POI
	members  []*member
	dead     int32
	min, max cube.Pos
	snap     atomic.Pointer[snapshot]
}

// snapshot is what goals read of a village while mobs think.
type snapshot struct {
	box  behavior.Box
	free [kinds][]behavior.POI
}

// Bounds is the box around the village's POIs. Safe from any goroutine.
func (v *Village) Bounds() behavior.Box {
	if s := v.snap.Load(); s != nil {
		return s.box
	}
	return behavior.Box{}
}

// FreePOI is the nearest POI of the kind to p that has room for another owner. Safe from
// any goroutine.
func (v *Village) FreePOI(kind Kind, p behavior.Pos) (behavior.POI, bool) {
	s := v.snap.Load()
	if s == nil || kind < 0 || int(kind) >= kinds {
		return behavior.POI{}, false
	}
	best, bestD := -1, int64(math.MaxInt64)
	for i, f := range s.free[kind] {
		dx, dy, dz := int64(f.Pos.X-p.X), int64(f.Pos.Y-p.Y), int64(f.Pos.Z-p.Z)
		if d := dx*dx + dy*dy + dz*dz; d < bestD {
			best, bestD = i, d
		}
	}
	if best < 0 {
		return behavior.POI{}, false
	}
	return s.free[kind][best], true
}

// centre is the middle of the village's POI box.
func (v *Village) centre() behavior.Vec3 {
	return behavior.Vec3{X: float32(v.min[0]+v.max[0]+1) / 2, Y: float32(v.min[1]+v.max[1]+1) / 2, Z: float32(v.min[2]+v.max[2]+1) / 2}
}

// radius is getApproximateRadius: the half sizes of the POI box, scaled.
func (v *Village) radius() float32 {
	return (float32(v.max[0]-v.min[0]+1)/2 + float32(v.max[2]-v.min[2]+1)/2) * radiusScale
}

// chunk is the chunk of the village's centre: the region holding it ticks the village.
func (v *Village) chunk() mcdb.ChunkPos {
	c := v.centre()
	return mcdb.ChunkPos{int32(math.Floor(float64(c.X))) >> 4, int32(math.Floor(float64(c.Z))) >> 4}
}

// publish rebuilds the snapshot after a change.
func (v *Village) publish() {
	s := &snapshot{box: behavior.Box{
		Min: behavior.Vec3{X: float32(v.min[0]), Y: float32(v.min[1]), Z: float32(v.min[2])},
		Max: behavior.Vec3{X: float32(v.max[0] + 1), Y: float32(v.max[1] + 1), Z: float32(v.max[2] + 1)},
	}}
	for k := range kinds {
		for _, p := range v.pois[k] {
			if !p.full() {
				s.free[k] = append(s.free[k], p.view())
			}
		}
	}
	v.snap.Store(s)
}

// grow recomputes the POI box.
func (v *Village) grow() {
	first := true
	for k := range kinds {
		for _, p := range v.pois[k] {
			if first {
				v.min, v.max, first = p.Pos, p.Pos, false
				continue
			}
			for i := range 3 {
				v.min[i], v.max[i] = min(v.min[i], p.Pos[i]), max(v.max[i], p.Pos[i])
			}
		}
	}
}

func (v *Village) member(id behavior.ID) *member {
	for _, m := range v.members {
		if m.id == id {
			return m
		}
	}
	return nil
}

func (v *Village) beds() int { return len(v.pois[Bed]) }

// query is a dweller waiting for a POI scan around its position.
type query struct {
	id  behavior.ID
	pos cube.Pos
}

// Manager is one dimension's villages (BDS's VillageManager). Its methods run on the
// goroutine of the region they are given; a lock keeps regions that tick at once apart.
type Manager struct {
	env   Env
	table *Table
	mu    sync.Mutex
	// villages in founding order; byDweller finds a dweller's.
	villages  []*Village
	byDweller map[behavior.ID]*Village
	// known are the POI blocks villages hold.
	known map[cube.Pos]*POI
	// queue waits for scans; scanning is the one being read, at cursor i.
	queue    []query
	scanning bool
	scan     query
	cursor   int
	// tick is the world tick of the last scan step: one per tick, whichever region runs it.
	tick int64
	// chunks are the chunks with work: village centres and the scan's dweller. Read
	// without the lock by every chunk tick.
	chunks atomic.Pointer[map[mcdb.ChunkPos]struct{}]
	// list is the villages as published, for goals reading while mobs think.
	list atomic.Pointer[[]*Village]
	rnd  func(n int32) int32
}

// NewManager makes a dimension's village manager. rnd draws a number in [0, n).
func NewManager(env Env, table *Table, rnd func(n int32) int32) *Manager {
	m := &Manager{env: env, table: table, byDweller: map[behavior.ID]*Village{}, known: map[cube.Pos]*POI{}, rnd: rnd}
	m.reindex()
	return m
}

// Table is the POI table the manager runs on.
func (m *Manager) Table() *Table { return m.table }

// reindex republishes the chunks with work. Call with the lock held.
func (m *Manager) reindex() {
	c := make(map[mcdb.ChunkPos]struct{}, len(m.villages)+1)
	for _, v := range m.villages {
		c[v.chunk()] = struct{}{}
	}
	if q, ok := m.current(); ok {
		c[mcdb.ChunkPos{int32(q.pos[0] >> 4), int32(q.pos[2] >> 4)}] = struct{}{} //nolint:gosec // chunk positions fit
	}
	m.chunks.Store(&c)
	list := slices.Clone(m.villages)
	m.list.Store(&list)
}

// NearestView is the village whose published box is centred nearest p within limit blocks
// (no limit when limit is negative). Safe from any goroutine: it reads only what villages
// published.
func (m *Manager) NearestView(p behavior.Vec3, limit float32) (*Village, bool) {
	l := m.list.Load()
	if l == nil {
		return nil, false
	}
	var best *Village
	bestD := float32(math.MaxFloat32)
	if limit >= 0 {
		bestD = limit * limit
	}
	for _, v := range *l {
		b := v.Bounds()
		dx, dy, dz := (b.Min.X+b.Max.X)/2-p.X, (b.Min.Y+b.Max.Y)/2-p.Y, (b.Min.Z+b.Max.Z)/2-p.Z
		if d := dx*dx + dy*dy + dz*dz; d <= bestD {
			best, bestD = v, d
		}
	}
	return best, best != nil
}

// current is the scan to run: the one under way, else the first queued.
func (m *Manager) current() (query, bool) {
	if m.scanning {
		return m.scan, true
	}
	if len(m.queue) > 0 {
		return m.queue[0], true
	}
	return query{}, false
}

// Submit queues a POI scan around a dweller (submitFindPOIQuery): at most 64 wait, one each.
// Runs: the dweller's region.
func (m *Manager) Submit(id behavior.ID, p behavior.Vec3) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queue) >= queryLimit || m.scanning && m.scan.id == id || slices.ContainsFunc(m.queue, func(q query) bool { return q.id == id }) {
		return
	}
	m.queue = append(m.queue, query{id: id, pos: blockOf(p)})
	m.reindex()
}

func blockOf(p behavior.Vec3) cube.Pos {
	return cube.Pos{int(math.Floor(float64(p.X))), int(math.Floor(float64(p.Y))), int(math.Floor(float64(p.Z)))}
}

// ChunkTick runs the village work of one ticking chunk: the scan step when the scan's
// dweller stands in it, and the tick of each village centred in it. Runs: the chunk's
// region, once per world tick.
func (m *Manager) ChunkTick(r *world.Region, pos mcdb.ChunkPos) {
	if c := m.chunks.Load(); c == nil {
		return
	} else if _, ok := (*c)[pos]; !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := r.CurrentTick()
	if q, ok := m.current(); ok && q.pos[0]>>4 == int(pos[0]) && q.pos[2]>>4 == int(pos[1]) && m.tick != now {
		m.tick = now
		m.scanStep(r)
	}
	changed := false
	for i := 0; i < len(m.villages); i++ {
		v := m.villages[i]
		if v.chunk() != pos {
			continue
		}
		if m.tickVillage(r, v, now) {
			m.removeVillage(r, i)
			i--
		}
		changed = true
	}
	if changed {
		m.reindex()
	}
}

// scanStep reads up to 1024 blocks of the current scan (_addPOIWithinRadius).
func (m *Manager) scanStep(r *world.Region) {
	if !m.scanning {
		m.scan, m.queue = m.queue[0], m.queue[1:]
		m.scanning, m.cursor = true, 0
	}
	const sx, sy = 2 * scanXZ, scanBelow + scanAbove + 1
	total := sx * sy * sx
	end := min(m.cursor+scanPerTick, total)
	for ; m.cursor < end; m.cursor++ {
		i := m.cursor
		p := cube.Pos{m.scan.pos[0] - scanXZ + i%sx, m.scan.pos[1] - scanBelow + (i/sx)%sy, m.scan.pos[2] - scanXZ + i/(sx*sy)}
		if _, ok := m.known[p]; ok {
			continue
		}
		if d, _ := m.env.POIAt(r, p); d != nil {
			m.found(r, p, d)
		}
	}
	if m.cursor >= total {
		m.scanning = false
	}
	m.reindex()
}

// found places a POI the scan found (tryCreatePOI, then _tryAssignPOIOrCreateVillage for
// beds, _assignPOIOnly for the rest).
func (m *Manager) found(r *world.Region, p cube.Pos, d *Def) {
	poi := &POI{Pos: p, Def: d}
	at := behavior.Vec3{X: float32(p[0]) + 0.5, Y: float32(p[1]), Z: float32(p[2]) + 0.5}
	v, dist := m.closest(at, joinRange)
	if d.Kind == Bed && (v == nil || dist > v.radius()+foundMargin && v.beds() > 0 && !m.inBox(v, p)) {
		v = &Village{ID: uuid.New()}
		m.villages = append(m.villages, v)
	}
	if v == nil {
		return
	}
	v.pois[d.Kind] = append(v.pois[d.Kind], poi)
	m.known[p] = poi
	v.grow()
	v.publish()
	_ = r
}

// inBox reports p inside the village's POI box.
func (m *Manager) inBox(v *Village, p cube.Pos) bool {
	for i := range 3 {
		if p[i] < v.min[i] || p[i] > v.max[i] {
			return false
		}
	}
	return true
}

// closest is the village whose centre is nearest p within limit blocks, and how far.
func (m *Manager) closest(p behavior.Vec3, limit float32) (*Village, float32) {
	var best *Village
	bestD := limit
	for _, v := range m.villages {
		c := v.centre()
		dx, dy, dz := c.X-p.X, c.Y-p.Y, c.Z-p.Z
		if d := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz))); d <= bestD {
			best, bestD = v, d
		}
	}
	return best, bestD
}

// Nearest is the village nearest p within limit blocks, for a mob with none. Runs: the
// caller's region; it takes the lock, so never while mobs think.
func (m *Manager) Nearest(p behavior.Vec3, limit float32) (*Village, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, _ := m.closest(p, limit)
	return v, v != nil
}

// DwellerTick is a dweller's periodic update (_tickDwellerComponent): it asks for a POI
// scan when it can find POIs, joins the nearest village when it has none, marks itself
// near its village, and gets back its village and POIs (after a reload, or a job loss
// that happened while it was away). Runs: the dweller's region, outside its think.
func (m *Manager) DwellerTick(r *world.Region, d Dweller, findPOI, migrate bool) {
	if findPOI {
		m.Submit(d.ID(), d.Pos())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := r.CurrentTick()
	v := m.byDweller[d.ID()]
	if v == nil && migrate {
		if v, _ = m.closest(d.Pos(), joinRange); v != nil {
			v.members = append(v.members, &member{id: d.ID(), seen: now})
			m.byDweller[d.ID()] = v
		}
	}
	m.sync(r, d, v, now)
}

// sync gives the dweller its village and POIs as the village has them.
func (m *Manager) sync(r *world.Region, d Dweller, v *Village, now int64) {
	d.SetVillage(v)
	var mem *member
	if v != nil {
		mem = v.member(d.ID())
	}
	if mem == nil {
		for k := range Kind(kinds) {
			d.SetPOI(k, behavior.POI{}, false)
		}
		return
	}
	c := v.centre()
	p := d.Pos()
	if dx, dz := c.X-p.X, c.Z-p.Z; dx*dx+dz*dz <= (v.radius()+nearRange)*(v.radius()+nearRange) {
		mem.seen = now
	}
	if mem.lostJob {
		mem.lostJob = false
		m.loseJob(d, Job, unskilled)
	}
	for k := range Kind(kinds) {
		if p := mem.pois[k]; p != nil {
			d.SetPOI(k, p.view(), true)
		} else {
			d.SetPOI(k, behavior.POI{}, false)
		}
	}
	_ = r
}

// loseJob is the job loss of _updateClaimedPOIs (0xBEE19A0): a dweller not locked into its
// profession forgets it and runs the job site's end event.
func (m *Manager) loseJob(d Dweller, kind Kind, end string) {
	d.SetPOI(kind, behavior.POI{}, false)
	if kind != Job || d.Locked() {
		return
	}
	d.SetProfession("")
	if end != "" {
		d.Fire(end)
	}
}

// tickVillage is one tick of a village (Village::tick): the POIs still there, then every
// 30 ticks the dwellers that left, and the claims. It reports a village to remove.
func (m *Manager) tickVillage(r *world.Region, v *Village, now int64) bool {
	changed := m.checkPOIs(r, v)
	if now%claimEvery == claimAt {
		changed = m.dropInactive(v, now) || changed
		changed = m.claim(r, v) || changed
	}
	if changed {
		v.grow()
		v.publish()
	}
	if v.beds() == 0 || len(v.members) == 0 {
		v.dead++
		return v.dead >= deadTicks
	}
	v.dead = 0
	return false
}

// checkPOIs drops POIs whose block is no longer theirs, with the claims on them.
func (m *Manager) checkPOIs(r *world.Region, v *Village) bool {
	changed := false
	for k := range kinds {
		list := v.pois[k]
		for i := 0; i < len(list); i++ {
			p := list[i]
			d, loaded := m.env.POIAt(r, p.Pos)
			if !loaded || d == p.Def {
				continue
			}
			m.releasePOI(r, v, p)
			delete(m.known, p.Pos)
			list = slices.Delete(list, i, i+1)
			i--
			changed = true
		}
		v.pois[k] = list
	}
	return changed
}

// releasePOI takes a POI from its owners, running the job loss on those loaded here.
func (m *Manager) releasePOI(r *world.Region, v *Village, p *POI) {
	for _, id := range p.owners {
		mem := v.member(id)
		if mem == nil {
			continue
		}
		mem.pois[p.Def.Kind] = nil
		if d, ok := m.env.Dweller(r, id); ok {
			m.loseJob(d, p.Def.Kind, p.Def.EndEvent)
		} else if p.Def.Kind == Job {
			mem.lostJob = true
		}
	}
	p.owners = nil
}

// dropInactive removes dwellers that have not been near for a day.
func (m *Manager) dropInactive(v *Village, now int64) bool {
	changed := false
	v.members = slices.DeleteFunc(v.members, func(mem *member) bool {
		if now-mem.seen <= inactiveTicks {
			return false
		}
		m.forget(v, mem)
		changed = true
		return true
	})
	return changed
}

// forget takes a member's claims back and removes it from the dweller index.
func (m *Manager) forget(v *Village, mem *member) {
	for k, p := range mem.pois {
		if p != nil {
			p.owners = slices.DeleteFunc(p.owners, func(id behavior.ID) bool { return id == mem.id })
			mem.pois[k] = nil
		}
	}
	if m.byDweller[mem.id] == v {
		delete(m.byDweller, mem.id)
	}
}

// removeVillage drops a dead village and tells its loaded dwellers.
func (m *Manager) removeVillage(r *world.Region, i int) {
	v := m.villages[i]
	for _, mem := range v.members {
		m.forget(v, mem)
		if d, ok := m.env.Dweller(r, mem.id); ok {
			m.sync(r, d, nil, 0)
		}
	}
	for k := range kinds {
		for _, p := range v.pois[k] {
			delete(m.known, p.Pos)
		}
	}
	m.villages = slices.Delete(m.villages, i, i+1)
}

// claim hands free POIs to the dwellers loaded in r (_claimUnclaimedPOIs, then
// _findAvailablePOI and _claimPOI).
func (m *Manager) claim(r *world.Region, v *Village) bool {
	changed := false
	for _, mem := range v.members {
		d, ok := m.env.Dweller(r, mem.id)
		if !ok {
			continue
		}
		for k := range Kind(kinds) {
			if mem.pois[k] != nil {
				continue
			}
			p := m.available(v, d, k)
			if p == nil {
				continue
			}
			m.give(r, d, mem, p)
			changed = true
		}
	}
	return changed
}

// available picks the POI a dweller would take: the first free bed or bell; for a job
// site, the first free one of its preferred profession, else a weighted random free one.
func (m *Manager) available(v *Village, d Dweller, k Kind) *POI {
	if k != Job {
		for _, p := range v.pois[k] {
			if !p.full() {
				return p
			}
		}
		return nil
	}
	if d.Baby() || d.Role() != "inhabitant" || d.Nitwit() {
		return nil
	}
	if pref := d.Profession(); pref != "" {
		for _, p := range v.pois[Job] {
			if !p.full() && p.Def.Name == pref {
				return p
			}
		}
		return nil
	}
	if d.Locked() {
		return nil
	}
	var total int32
	for _, p := range v.pois[Job] {
		if !p.full() {
			total += max(p.Def.Weight, 0)
		}
	}
	if total <= 0 {
		return nil
	}
	n := m.rnd(total)
	for _, p := range v.pois[Job] {
		if p.full() {
			continue
		}
		if n -= max(p.Def.Weight, 0); n < 0 {
			return p
		}
	}
	return nil
}

// give is _claimPOI (0xBEDFC60): the POI goes to the dweller, its init event runs, a job
// site sets the preferred profession, and happy villager particles show at the head.
func (m *Manager) give(r *world.Region, d Dweller, mem *member, p *POI) {
	p.owners = append(p.owners, mem.id)
	mem.pois[p.Def.Kind] = p
	d.SetPOI(p.Def.Kind, p.view(), true)
	if p.Def.InitEvent != "" {
		d.Fire(p.Def.InitEvent)
	}
	if p.Def.Kind == Job && !d.Locked() {
		d.SetProfession(p.Def.Name)
	}
	at := d.Pos()
	at.Y += d.HeadY()
	for range happyCount {
		m.env.LevelEvent(r, happyEvent, at)
	}
}
