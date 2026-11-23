package world

import (
	"cmp"
	"slices"
	"sync"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/mcdb"
)

// Entity is the part every entity shares (API_DESIGN.md, "Players and
// entities"): its IDs, position and rotation. It belongs to the region of
// the chunk it stands in; a player's entity belongs to the region of its
// Viewer and sees the entities around it.
//
// Any other entity lives in loaded chunks only, as in BDS: it cannot move
// into a chunk that is not loaded, goes to the region that owns its new
// chunk at the end of the tick it moved there, and is unloaded with its
// chunk (the Unloaded hook keeps it, to be added again later).
type Entity struct {
	d    *Dimension
	conf EntityConfig

	// region is the region the entity belongs to. Guarded by d.mu.
	region *Region

	// removed is set from anywhere by Remove.
	inMu    sync.Mutex
	removed bool

	// Owned by the entity's region.
	pos      mgl32.Vec3
	rot      Rotation
	onGround bool
	tick     uint64
	chunk    mcdb.ChunkPos
	moved    bool
	gone     bool
	// box is the actor's box as its owner last set it (SetActorBox). actors is the chunk
	// list it is in and slot its place there, where a copy of box is kept with the others'
	// for AppendActorsTouching; nil while it is in none.
	box    actorBox
	actors *actorList
	slot   int
	// past are its earlier positions (AppendPast).
	past pastMoves
	// frozen is set for this tick when the entity's chunk is not ticking: it neither thinks
	// nor takes input (tickEntities).
	frozen bool
	// known are the entities this player was shown, and whether the last
	// scan saw them. Players only.
	known map[*Entity]known
	// spawn caches the spawn packets for the tick they were built in. spawnMu guards it:
	// players see entities in parallel (updateVisibility).
	spawnMu   sync.Mutex
	spawn     []packet.Packet
	spawnTick uint64
}

// outItem is a packet the movement phase sends: to the players who see from, or to the
// player to alone.
type outItem struct {
	from, to *Entity
	pk       packet.Packet
	// raw is pk encoded once for everyone (encodeOutbox), nil if it could not be.
	raw []byte
}

type known struct {
	id   int64
	seen bool
}

// Rotation is an entity's rotation in degrees.
type Rotation struct {
	Pitch, Yaw, HeadYaw float32
}

// Movement is where an entity moved to.
type Movement struct {
	Pos      mgl32.Vec3
	Rot      Rotation
	OnGround bool
	// Tick is the client's tick of a player's movement.
	Tick uint64
}

// EntityConfig describes an entity added with [Dimension.AddEntity]. The
// hooks run on the entity's region tick and must not block.
type EntityConfig struct {
	UniqueID  int64
	RuntimeID uint64
	Pos       mgl32.Vec3
	Rot       Rotation
	// Viewer makes the entity a player's: it moves with the viewer between
	// regions and is shown every entity within BDS's 57-chunk disc.
	Viewer *Viewer
	// Owner is what the entity belongs to, for whoever finds it with
	// [Region.AppendEntitiesIn].
	Owner any
	// Actor puts the entity in the lists [Region.AppendActorsIn] reads, besides the full
	// ones: set it for the entities area searches for living things want (players, mobs),
	// so those searches skip the items, clouds and projectiles around them. It is read when
	// the entity is added.
	Actor bool
	// Spawn returns the packets that show the entity to a player seeing it
	// for the first time. Built once per tick and shared by every player. In a
	// crowded region players are shown entities in parallel, so Spawn, SpawnFor,
	// VisibleTo and StaySeen may run while other entities' do; they must only
	// read, and Spawn is never run twice at once for one entity.
	Spawn func(e *Entity) []packet.Packet
	// Move returns the packet that moves the entity for the players who see
	// it, built once per tick it moved; nil sends nothing.
	Move func(e *Entity) packet.Packet
	// Physics runs every tick before any entity's Think, for every entity at once (on several
	// cores in a crowded region), with Think's rules: it moves the entity by what it decided
	// last tick. BDS runs its movement systems over every actor before its AI systems, so an
	// entity's AI sees the others where they moved to this tick.
	Physics func(e *Entity)
	// Publish runs after every entity's Physics and before any Think, for every entity at
	// once: it updates what the others read of the entity while they think, and must not
	// read any other entity.
	Publish func(e *Entity)
	// Serial runs after every entity's Publish and before any Think, one entity at a time
	// on the region's goroutine, the newest entity first, as BDS's systems visit actors one
	// after another. Unlike Think it may change other entities' state directly, and the
	// entities after it see that change. Mob goals run here.
	Serial func(e *Entity)
	// Think runs every tick just before the input phase, for every entity at once: a crowded
	// region spreads the calls over idle cores (Region.Parallel), so Think may only read the
	// region and other entities and change the entity's own state. What it does to anything
	// else it queues for Input, which applies it in runtime ID order. Mob AI thinks here.
	Think func(e *Entity)
	// Input runs every tick in the region's player input phase, entities
	// in runtime ID order: a player handles the packets it queued here.
	Input func(e *Entity)
	// TickAnywhere runs Physics, Publish, Serial, Think and Input even while the entity's chunk is not ticking. Without
	// it an entity that is not a player's stands still where no simulation zone or ticking
	// ticket reaches, as BDS ticks actors only in ticking chunks; a player's always ticks.
	TickAnywhere bool
	// Removed runs once the entity was removed, with its last state.
	Removed func(e *Entity)
	// Unloaded runs when the chunk of an entity that is not a player's
	// unloads, with its last state. The entity is then gone from the world,
	// as if removed, and is added again with AddEntity when wanted. Nil
	// drops it.
	Unloaded func(e *Entity)
	// Save returns what the entity is saved as with its chunk; false saves
	// nothing. Nil means the entity is not saved. It runs on its region's
	// tick, and the result must not be changed afterwards.
	Save func(e *Entity) (chunk.Entity, bool)
	// VisibleTo decides which players see the entity; nil is everyone in
	// range. It runs each tick for each player in range, so a rule that
	// changes its answer takes effect by itself: a player who may not see the
	// entity is sent nothing about it, and one who loses sight of it is told
	// it is gone. Change it with [Entity.SetVisibleTo].
	VisibleTo func(player *Entity) bool
	// StaySeen, if set, keeps the entity shown to a player who already sees it after it
	// leaves that player's disc, for as long as it returns true and the two share a region.
	StaySeen func(player *Entity) bool
	// SpawnFor replaces Spawn with packets built for one player, not shared.
	// Change it with [Entity.SetSpawnFor].
	SpawnFor func(player *Entity) []packet.Packet
}

// SetVisibleTo changes the entity's VisibleTo. Runs: owner.
func (e *Entity) SetVisibleTo(fn func(player *Entity) bool) { e.conf.VisibleTo = fn }

// SetSpawnFor changes the entity's SpawnFor. Runs: owner.
func (e *Entity) SetSpawnFor(fn func(player *Entity) []packet.Packet) { e.conf.SpawnFor = fn }

// Send sends pk to the players who see the entity, and no one else. Runs:
// owner.
func (e *Entity) Send(pk packet.Packet) {
	if r := e.region; r != nil && r.fanout {
		r.outbox = append(r.outbox, outItem{from: e, pk: pk})
		return
	}
	e.EachSeer(func(p *Entity) { p.conf.Viewer.sendEntity(pk) })
}

// SendTo sends pk to one player, given as its entity. Runs: owner of that
// entity.
func (e *Entity) SendTo(pk packet.Packet) {
	if e.conf.Viewer == nil {
		return
	}
	if r := e.region; r != nil && r.fanout {
		r.outbox = append(r.outbox, outItem{to: e, pk: pk})
		return
	}
	e.conf.Viewer.sendEntity(pk)
}

// EachSeer runs fn for each player who sees the entity. Regions keep further
// apart than a player sees, so all of them are in its region. Runs: owner.
func (e *Entity) EachSeer(fn func(player *Entity)) {
	r := e.region
	if r == nil {
		return
	}
	for _, p := range r.players {
		if _, ok := p.known[e]; ok {
			fn(p)
		}
	}
}

// Sees reports whether the player, given as its entity, has been shown other. Runs:
// owner of both (one region).
func (e *Entity) Sees(other *Entity) bool {
	_, ok := e.known[other]
	return ok
}

// Respawn shows the entity again to the players who see it, for a change only
// a spawn carries. Runs: owner.
func (e *Entity) Respawn() {
	r := e.region
	if r == nil {
		return
	}
	e.spawn = nil
	gone := &packet.RemoveActor{EntityUniqueID: e.conf.UniqueID}
	e.EachSeer(func(p *Entity) {
		p.conf.Viewer.sendEntity(gone)
		for _, pk := range r.spawnPacketsFor(e, p) {
			p.conf.Viewer.sendEntity(pk)
		}
	})
}

// EntityLoader takes a saved entity of the chunk at pos that just loaded into
// r, normally with [Region.AddLoadedEntity]. False leaves it saved in the chunk
// as it is, as for entity types the engine does not run yet.
type EntityLoader func(r *Region, pos mcdb.ChunkPos, saved chunk.Entity) bool

// SetEntityLoader sets who turns saved entities into live ones. Set it before
// any player joins: chunks loaded earlier keep their entities saved. Runs:
// anywhere.
func (d *Dimension) SetEntityLoader(fn EntityLoader) { d.entityLoader.Store(&fn) }

// AddLoadedEntity adds an entity from the saved data of the chunk at pos,
// which it stays in whatever its position says, until it moves. Runs: only in
// an EntityLoader.
func (r *Region) AddLoadedEntity(pos mcdb.ChunkPos, conf EntityConfig) *Entity {
	e := &Entity{d: r.d, conf: conf, pos: conf.Pos, rot: conf.Rot, chunk: pos}
	r.addEntity(e)
	return e
}

// loadEntities offers the saved entities of a chunk that just loaded to the
// EntityLoader and leaves those it did not take. It reports whether it took
// any.
func (r *Region) loadEntities(c *Chunk) bool {
	load := r.d.entityLoader.Load()
	if load == nil {
		return false
	}
	took := false
	kept := c.col.Entities[:0]
	for _, saved := range c.col.Entities {
		if (*load)(r, c.pos, saved) {
			took = true
			continue
		}
		kept = append(kept, saved)
	}
	c.col.Entities = kept
	return took
}

// holdsSaved reports whether a live entity in the chunk at pos is saved with
// it.
func (r *Region) holdsSaved(pos mcdb.ChunkPos) bool {
	for _, e := range r.byChunk[pos] {
		if e.conf.Save != nil {
			return true
		}
	}
	return false
}

// entitiesToSave returns the chunk's saved entities the engine does not run
// plus the live ones, and whether there were any live ones.
func (r *Region) entitiesToSave(c *Chunk) ([]chunk.Entity, bool) {
	all := slices.Clip(c.col.Entities)
	live := false
	for _, e := range r.byChunk[c.pos] {
		if e.conf.Save == nil {
			continue
		}
		if saved, ok := e.conf.Save(e); ok {
			all = append(all, saved)
			live = true
		}
	}
	return all, live
}

// AddEntity adds an entity, in its viewer's region for a player, else in
// the region that owns its chunk, on that region's next tick. An entity
// that is not a player's is unloaded at once (see EntityConfig.Unloaded)
// if nothing else keeps its chunk loaded. Runs: anywhere.
func (d *Dimension) AddEntity(conf EntityConfig) *Entity {
	e := &Entity{d: d, conf: conf, pos: conf.Pos, rot: conf.Rot, chunk: chunkOf(conf.Pos)}
	if conf.Viewer != nil {
		e.known = map[*Entity]known{}
		d.postViewer(conf.Viewer, func(r *Region) { r.addEntity(e) })
		return e
	}
	d.RunAt(e.chunk, func(r *Region) { r.addEntity(e) })
	return e
}

// UniqueID returns the entity's unique ID. Runs: anywhere.
func (e *Entity) UniqueID() int64 { return e.conf.UniqueID }

// RuntimeID returns the entity's runtime ID. Runs: anywhere.
func (e *Entity) RuntimeID() uint64 { return e.conf.RuntimeID }

// Owner returns the EntityConfig's Owner. Runs: anywhere.
func (e *Entity) Owner() any { return e.conf.Owner }

// Position returns where the entity is. Runs: owner.
func (e *Entity) Position() mgl32.Vec3 { return e.pos }

// Chunk returns the chunk the entity is kept and saved in. Runs: owner.
func (e *Entity) Chunk() mcdb.ChunkPos { return e.chunk }

// SetActorBox sets the box the entity's owner gives for it as an actor, min and max corners,
// for AppendActorsTouching. The owner must set it again each time that box changes, or the
// search misses the entity. Runs: owner, not in a Parallel phase other than its own Publish
// (which no search runs in).
func (e *Entity) SetActorBox(minimum, maximum mgl32.Vec3) {
	e.box = actorBox{lo: minimum, hi: maximum, set: true}
	if e.actors != nil {
		e.actors.boxes[e.slot] = e.box
	}
}

// Rotation returns the entity's rotation. Runs: owner.
func (e *Entity) Rotation() Rotation { return e.rot }

// Region returns the region the entity belongs to. Runs: owner (the
// entity's region tick, where it cannot change).
func (e *Entity) Region() *Region {
	e.d.mu.Lock()
	defer e.d.mu.Unlock()
	return e.region
}

// OnGround reports whether the entity stands on the ground. Runs: owner.
func (e *Entity) OnGround() bool { return e.onGround }

// Tick returns the client tick of a player's last movement. Runs: owner.
func (e *Entity) Tick() uint64 { return e.tick }

// Apply moves the entity; the players who see it get the movement at the
// end of the tick. An entity that is not a player's stays where it is, and
// Apply returns false, if the new chunk is neither loaded here nor in a
// section another region owns. Runs: owner.
func (e *Entity) Apply(m Movement) bool {
	r := e.region
	if r == nil || !r.ticking.Load() || e.gone {
		return false
	}
	c := chunkOf(m.Pos)
	if c != e.chunk && e.conf.Viewer == nil && !r.canEnter(c) {
		return false
	}
	if m.Pos != e.pos {
		e.past.keep(e.pos, r.CurrentTick())
	}
	if m.Pos != e.pos || m.Rot != e.rot || m.OnGround != e.onGround {
		e.moved = true
	}
	e.pos, e.rot, e.onGround, e.tick = m.Pos, m.Rot, m.OnGround, m.Tick
	if c != e.chunk {
		r.unindex(e)
		e.chunk = c
		r.indexChunk(e)
		if e.conf.Viewer == nil && !r.owns(c) {
			r.leaving = append(r.leaving, e)
		}
	}
	return true
}

// canEnter reports whether an entity may move into the chunk at pos: it is
// loaded here, or its section belongs to another region, which checks the
// chunk when the entity arrives.
func (r *Region) canEnter(pos mcdb.ChunkPos) bool {
	if _, ok := r.chunks[pos]; ok {
		return true
	}
	if r.owns(pos) {
		return false
	}
	r.d.mu.Lock()
	_, ok := r.d.sections[r.d.sectionOf(pos)]
	r.d.mu.Unlock()
	return ok
}

// Remove removes the entity on its region's next tick; the players who see
// it are told. Runs: anywhere.
func (e *Entity) Remove() {
	e.inMu.Lock()
	e.removed = true
	e.inMu.Unlock()
}

func chunkOf(pos mgl32.Vec3) mcdb.ChunkPos {
	return ChunkPos(floor(pos[0]), floor(pos[2]))
}

func floor(f float32) int32 {
	i := int32(f)
	if f < float32(i) {
		i--
	}
	return i
}

// addEntity takes an entity into the region.
func (r *Region) addEntity(e *Entity) {
	if e.gone {
		return
	}
	r.d.mu.Lock()
	e.region = r
	r.d.mu.Unlock()
	r.insertEntity(e)
}

// insertEntity puts e in the region's lists, which stay ordered by runtime
// ID so every pass runs in a fixed order.
func (r *Region) insertEntity(e *Entity) {
	i, _ := slices.BinarySearchFunc(r.entities, e, byRuntimeID)
	r.entities = slices.Insert(r.entities, i, e)
	r.indexChunk(e)
	if e.known != nil {
		j, _ := slices.BinarySearchFunc(r.players, e, byRuntimeID)
		r.players = slices.Insert(r.players, j, e)
	}
}

// syncPlayers rebuilds players from entities, after entities were taken out of it.
func (r *Region) syncPlayers() {
	clear(r.players)
	r.players = r.players[:0]
	for _, e := range r.entities {
		if e.known != nil {
			r.players = append(r.players, e)
		}
	}
}

func byRuntimeID(a, b *Entity) int { return cmp.Compare(a.conf.RuntimeID, b.conf.RuntimeID) }

// unindex takes e out of the chunk index.
func (r *Region) unindex(e *Entity) {
	dropFrom(r.byChunk, e.chunk, e)
	if e.conf.Actor {
		r.dropActor(e)
	}
}

// actorList is a chunk's actors in order, with each one's box (SetActorBox) kept beside it
// in one flat slice, so a search reads boxes without visiting the entities.
type actorList struct {
	ents  []*Entity
	boxes []actorBox
}

// actorBox is an actor's box as its owner set it; set is false until it has.
type actorBox struct {
	lo, hi mgl32.Vec3
	set    bool
}

// misses reports that b is set and does not overlap minimum to maximum, edges touching
// not counted.
func (b *actorBox) misses(minimum, maximum mgl32.Vec3) bool {
	return b.set && (b.lo[0] >= maximum[0] || b.hi[0] <= minimum[0] || b.lo[1] >= maximum[1] || b.hi[1] <= minimum[1] ||
		b.lo[2] >= maximum[2] || b.hi[2] <= minimum[2])
}

// addActor puts e at the end of its chunk's actor list.
func (r *Region) addActor(e *Entity) {
	l := r.actorsByChunk[e.chunk]
	if l == nil {
		l = &actorList{}
		r.actorsByChunk[e.chunk] = l
	}
	e.actors, e.slot = l, len(l.ents)
	l.ents = append(l.ents, e)
	l.boxes = append(l.boxes, e.box)
}

// dropActor takes e out of its chunk's actor list, keeping the others' order, and deletes
// the list once empty.
func (r *Region) dropActor(e *Entity) {
	l := e.actors
	if l == nil {
		return
	}
	i := e.slot
	l.ents = slices.Delete(l.ents, i, i+1)
	l.boxes = slices.Delete(l.boxes, i, i+1)
	for j := i; j < len(l.ents); j++ {
		l.ents[j].slot = j
	}
	e.actors = nil
	if len(l.ents) == 0 && r.actorsByChunk[e.chunk] == l {
		delete(r.actorsByChunk, e.chunk)
	}
}

// indexChunk puts e in the lists of its chunk.
func (r *Region) indexChunk(e *Entity) {
	r.byChunk[e.chunk] = append(r.byChunk[e.chunk], e)
	if e.conf.Actor {
		r.addActor(e)
	}
}

// dropFrom takes e out of the chunk's list in lists, deleting the chunk's entry when that
// empties it.
func dropFrom(lists map[mcdb.ChunkPos][]*Entity, pos mcdb.ChunkPos, e *Entity) {
	list := slices.DeleteFunc(lists[pos], func(o *Entity) bool { return o == e })
	if len(list) == 0 {
		delete(lists, pos)
		return
	}
	lists[pos] = list
}

// handOff gives the entities that moved into another region's section this
// tick to that region, for its next tick. The players here who saw them
// lose them in the next visibility pass. Runs at the end of the tick.
func (r *Region) handOff() {
	for i, e := range r.leaving {
		r.leaving[i] = nil
		if e.gone || e.region != r || r.owns(e.chunk) {
			continue
		}
		r.unindex(e)
		r.entities = slices.DeleteFunc(r.entities, func(o *Entity) bool { return o == e })
		if e.known != nil {
			r.syncPlayers()
		}
		r.d.mu.Lock()
		e.region = nil
		r.d.mu.Unlock()
		if !r.d.postAt(e.chunk, func(o *Region) { o.arriveEntity(e) }) {
			unload(e)
		}
	}
	r.leaving = r.leaving[:0]
}

// arriveEntity takes an entity handed over by another region. One whose
// chunk is not loaded here is unloaded.
func (r *Region) arriveEntity(e *Entity) {
	if _, ok := r.chunks[e.chunk]; !ok || !r.owns(e.chunk) {
		unload(e)
		return
	}
	r.addEntity(e)
}

// unloadChunkEntities unloads the entities of a chunk that unloads, except
// players', which go with their viewers.
func (r *Region) unloadChunkEntities(pos mcdb.ChunkPos) {
	var out []*Entity
	for _, e := range r.byChunk[pos] {
		if e.conf.Viewer == nil {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return
	}
	r.entities = slices.DeleteFunc(r.entities, func(e *Entity) bool { return slices.Contains(out, e) })
	r.syncPlayers()
	for _, e := range out {
		r.removeEntity(e)
		r.d.mu.Lock()
		e.region = nil
		r.d.mu.Unlock()
		unload(e)
	}
}

// unload ends an entity that is in no region's lists any more.
func unload(e *Entity) {
	e.gone = true
	if e.conf.Unloaded != nil {
		e.conf.Unloaded(e)
	}
}

// tickEntities removes the entities removed since the last tick, then runs
// the player input phase.
func (r *Region) tickEntities() {
	removed := false
	for _, e := range r.entities {
		e.inMu.Lock()
		removed = removed || e.removed
		e.inMu.Unlock()
	}
	if removed {
		r.entities = slices.DeleteFunc(r.entities, func(e *Entity) bool {
			e.inMu.Lock()
			rem := e.removed
			e.inMu.Unlock()
			if rem {
				r.removeEntity(e)
				if e.conf.Removed != nil {
					e.conf.Removed(e)
				}
			}
			return rem
		})
		r.syncPlayers()
	}
	r.thinkers = r.thinkers[:0]
	for _, e := range r.entities {
		e.frozen = !r.entityTicks(e)
		if (e.conf.Physics != nil || e.conf.Publish != nil || e.conf.Serial != nil || e.conf.Think != nil) && !e.frozen {
			r.thinkers = append(r.thinkers, e)
		}
	}
	if len(r.thinkers) > 0 {
		r.runThinkers()
	}
	// Input may move entities between chunks, never add or remove them.
	for _, e := range r.entities {
		if e.conf.Input != nil && !e.frozen {
			e.conf.Input(e)
		}
	}
}

// entityTicks reports whether e ticks this tick: a player's entity always, any other while
// its chunk ticked last tick (BDS ticks actors only in ticking chunks) or with TickAnywhere.
func (r *Region) entityTicks(e *Entity) bool {
	if e.conf.Viewer != nil || e.conf.TickAnywhere {
		return true
	}
	recent, _ := r.ChunkTicked(e.chunk)
	return recent
}

// runThinkers runs the thinking entities' Physics, Publish, Serial and Think, each phase
// over all of them before the next starts.
func (r *Region) runThinkers() {
	if r.thinkFn == nil {
		r.physicsFn, r.publishFn, r.thinkFn = r.physics, r.publish, r.think
	}
	n := len(r.thinkers)
	r.Parallel(n, r.physicsFn)
	r.Parallel(n, r.publishFn)
	r.runSerial()
	r.Parallel(n, r.thinkFn)
	clear(r.thinkers)
}

// runSerial runs the thinking entities' Serial one at a time, newest first: the region
// keeps entities by runtime ID, which grows with each new entity, and BDS's systems visit
// the newest actor first.
func (r *Region) runSerial() {
	for i := len(r.thinkers) - 1; i >= 0; i-- {
		if e := r.thinkers[i]; e.conf.Serial != nil {
			e.conf.Serial(e)
		}
	}
}

// physics runs the Physics of the i-th thinking entity.
func (r *Region) physics(i int) {
	if e := r.thinkers[i]; e.conf.Physics != nil {
		e.conf.Physics(e)
	}
}

// publish runs the Publish of the i-th thinking entity.
func (r *Region) publish(i int) {
	if e := r.thinkers[i]; e.conf.Publish != nil {
		e.conf.Publish(e)
	}
}

// think runs the Think of the i-th thinking entity.
func (r *Region) think(i int) {
	if e := r.thinkers[i]; e.conf.Think != nil {
		e.conf.Think(e)
	}
}

// removeEntity takes e out of the world and tells the players who see it.
func (r *Region) removeEntity(e *Entity) {
	e.gone = true
	r.unindex(e)
	pk := &packet.RemoveActor{EntityUniqueID: e.conf.UniqueID}
	for _, p := range r.players {
		if _, ok := p.known[e]; ok {
			delete(p.known, e)
			p.conf.Viewer.sendEntity(pk)
		}
	}
	clear(e.known)
}

// updateVisibility shows each player the entities that came into its disc
// and removes those that left it, as BDS does every tick.
func (r *Region) updateVisibility() {
	r2 := r.d.entityView.Load()
	n := int32(0)
	for (n+1)*(n+1) <= r2 {
		n++
	}
	r.visR2, r.visN = r2, n
	r.collectSeers()
	if r.visFn == nil {
		r.visFn = r.visibility
	}
	// Each player's scan only changes what that player knows and sends to that player, so a
	// crowded region scans its players on several cores.
	r.Parallel(len(r.seers), r.visFn)
	clear(r.seers)
}

// collectSeers lists the players in the region (the entities that are shown others).
func (r *Region) collectSeers() {
	r.seers = r.seers[:0]
	for _, p := range r.entities {
		if p.known != nil {
			r.seers = append(r.seers, p)
		}
	}
}

// visibility is one player's part of updateVisibility. VisibleTo, StaySeen, Spawn and SpawnFor
// run here, maybe for several players at once.
func (r *Region) visibility(i int) {
	p, r2, n := r.seers[i], r.visR2, r.visN
	for dx := -n; dx <= n; dx++ {
		for dz := -n; dz <= n; dz++ {
			if dx*dx+dz*dz > r2 {
				continue
			}
			for _, o := range r.byChunk[mcdb.ChunkPos{p.chunk[0] + dx, p.chunk[1] + dz}] {
				if o == p || o.conf.VisibleTo != nil && !o.conf.VisibleTo(p) {
					continue
				}
				if k, ok := p.known[o]; ok {
					k.seen = true
					p.known[o] = k
					continue
				}
				p.known[o] = known{id: o.conf.UniqueID, seen: true}
				for _, pk := range r.spawnPacketsFor(o, p) {
					p.conf.Viewer.sendEntity(pk)
				}
			}
		}
	}
	for o, k := range p.known {
		if !k.seen {
			if o.conf.StaySeen != nil && o.region == r && o.conf.StaySeen(p) {
				continue
			}
			delete(p.known, o)
			p.conf.Viewer.sendEntity(&packet.RemoveActor{EntityUniqueID: k.id})
			continue
		}
		k.seen = false
		p.known[o] = k
	}
}

// spawnPacketsFor returns the packets that show o to the player p: its own if
// it has SpawnFor, else the ones shared by every player.
func (r *Region) spawnPacketsFor(o, p *Entity) []packet.Packet {
	if o.conf.SpawnFor != nil {
		return o.conf.SpawnFor(p)
	}
	return r.spawnPackets(o)
}

// spawnPackets returns o's spawn packets, built once per tick.
func (r *Region) spawnPackets(o *Entity) []packet.Packet {
	if o.conf.Spawn == nil {
		return nil
	}
	o.spawnMu.Lock()
	defer o.spawnMu.Unlock()
	if o.spawn == nil || o.spawnTick != r.ticks {
		o.spawn, o.spawnTick = o.conf.Spawn(o), r.ticks
	}
	return o.spawn
}

// sendMovement sends each entity that moved this tick to the players who
// see it: one packet per entity, shared by all of them.
func (r *Region) sendMovement() {
	// The moves are built in runtime ID order on the region's goroutine; what they send is
	// kept in one ordered list, then every player takes its share of it, in parallel when
	// the region is crowded. Each player gets the same packets in the same order as when
	// sent one by one.
	r.fanout = true
	for _, e := range r.entities {
		if !e.moved {
			continue
		}
		e.moved = false
		if e.conf.Move == nil {
			continue
		}
		if pk := e.conf.Move(e); pk != nil {
			r.outbox = append(r.outbox, outItem{from: e, pk: pk})
		}
	}
	r.fanout = false
	if len(r.outbox) == 0 {
		return
	}
	r.collectSeers()
	r.encodeOutbox()
	if r.fanFn == nil {
		r.fanFn = r.fanOut
	}
	r.Parallel(len(r.seers), r.fanFn)
	clear(r.seers)
	clear(r.outbox)
	r.outbox = r.outbox[:0]
}

// encodeOutbox encodes each packet meant for more than one player once, so the fan-out
// hands every player the same bytes instead of encoding a packet per player: with 400
// players in view that is 400 encodes of each move saved, and no allocation.
func (r *Region) encodeOutbox() {
	if r.raw == nil {
		r.raw = packet.NewRawWriter(r.d.conf.ShieldID)
	}
	r.rawArena, r.rawEnds = r.rawArena[:0], r.rawEnds[:0]
	for _, it := range r.outbox {
		if it.to == nil {
			if arena, err := r.raw.Append(r.rawArena, it.pk); err == nil {
				r.rawArena = arena
			}
		}
		r.rawEnds = append(r.rawEnds, len(r.rawArena))
	}
	// The views are taken once the arena stops growing.
	start := 0
	for i, end := range r.rawEnds {
		if end > start {
			r.outbox[i].raw = r.rawArena[start:end:end]
		}
		start = end
	}
}

// fanOut sends one player the movement phase's packets meant for it.
func (r *Region) fanOut(i int) {
	p := r.seers[i]
	for _, it := range r.outbox {
		if it.to != nil {
			if it.to == p {
				p.conf.Viewer.sendEntity(it.pk)
			}
			continue
		}
		if _, ok := p.known[it.from]; ok {
			p.conf.Viewer.sendEntityRaw(it.pk, it.raw)
		}
	}
}

// AppendEntities appends every entity in this region to dst, so a caller reusing dst
// allocates nothing. Runs: owner.
func (r *Region) AppendEntities(dst []*Entity) []*Entity { return append(dst, r.entities...) }

// AppendPlayers appends the players' entities in this region (those with a Viewer) to dst,
// in runtime ID order, without walking the region's other entities. Runs: owner, or a
// phase that runs entities in parallel (the list changes only between phases).
func (r *Region) AppendPlayers(dst []*Entity) []*Entity { return append(dst, r.players...) }

// AppendEntitiesIn appends the entities in this region standing in the chunks
// the area touches to dst, so a caller reusing dst allocates nothing. The
// caller checks positions. Runs: owner.
func (r *Region) AppendEntitiesIn(dst []*Entity, minX, minZ, maxX, maxZ float32) []*Entity {
	lo, hi := chunkOf(mgl32.Vec3{minX, 0, minZ}), chunkOf(mgl32.Vec3{maxX, 0, maxZ})
	for x := lo[0]; x <= hi[0]; x++ {
		for z := lo[1]; z <= hi[1]; z++ {
			dst = append(dst, r.byChunk[mcdb.ChunkPos{x, z}]...)
		}
	}
	return dst
}

// AppendActorsTouching is AppendActorsIn for the chunks under minimum to maximum widened by
// grow on x and z, leaving out the actors whose box (SetActorBox) misses minimum to maximum,
// in the same order. An actor with no box set is always kept. Searches in crowds (mobs
// pushing each other) then never ask the owners of the many actors far off. Runs: owner,
// or a phase that runs entities in parallel.
func (r *Region) AppendActorsTouching(dst []*Entity, minimum, maximum mgl32.Vec3, grow float32) []*Entity {
	lo := chunkOf(mgl32.Vec3{minimum[0] - grow, 0, minimum[2] - grow})
	hi := chunkOf(mgl32.Vec3{maximum[0] + grow, 0, maximum[2] + grow})
	for x := lo[0]; x <= hi[0]; x++ {
		for z := lo[1]; z <= hi[1]; z++ {
			l := r.actorsByChunk[mcdb.ChunkPos{x, z}]
			if l == nil {
				continue
			}
			for i := range l.boxes {
				if !l.boxes[i].misses(minimum, maximum) {
					dst = append(dst, l.ents[i])
				}
			}
		}
	}
	return dst
}

// AppendActorsIn is AppendEntitiesIn for the entities added with EntityConfig.Actor alone,
// in the same order, so a search for living things near thousands of items or clouds
// walks only the living. Runs: owner, or a phase that runs entities in parallel.
func (r *Region) AppendActorsIn(dst []*Entity, minX, minZ, maxX, maxZ float32) []*Entity {
	lo, hi := chunkOf(mgl32.Vec3{minX, 0, minZ}), chunkOf(mgl32.Vec3{maxX, 0, maxZ})
	for x := lo[0]; x <= hi[0]; x++ {
		for z := lo[1]; z <= hi[1]; z++ {
			if l := r.actorsByChunk[mcdb.ChunkPos{x, z}]; l != nil {
				dst = append(dst, l.ents...)
			}
		}
	}
	return dst
}
