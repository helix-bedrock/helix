package world

import (
	"cmp"
	"slices"
	"sync"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol/packet"
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
	// known are the entities this player was shown, and whether the last
	// scan saw them. Players only.
	known map[*Entity]known
	// spawn caches the spawn packets for the tick they were built in.
	spawn     []packet.Packet
	spawnTick uint64
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
	// Spawn returns the packets that show the entity to a player seeing it
	// for the first time. Built once per tick and shared by every player.
	Spawn func(e *Entity) []packet.Packet
	// Move returns the packet that moves the entity for the players who see
	// it, built once per tick it moved; nil sends nothing.
	Move func(e *Entity) packet.Packet
	// Input runs every tick in the region's player input phase, entities
	// in runtime ID order: a player handles the packets it queued here.
	Input func(e *Entity)
	// Removed runs once the entity was removed, with its last state.
	Removed func(e *Entity)
	// Unloaded runs when the chunk of an entity that is not a player's
	// unloads, with its last state. The entity is then gone from the world,
	// as if removed, and is added again with AddEntity when wanted. Nil
	// drops it.
	Unloaded func(e *Entity)
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

// Position returns where the entity is. Runs: owner.
func (e *Entity) Position() mgl32.Vec3 { return e.pos }

// Rotation returns the entity's rotation. Runs: owner.
func (e *Entity) Rotation() Rotation { return e.rot }

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
	if m.Pos != e.pos || m.Rot != e.rot || m.OnGround != e.onGround {
		e.moved = true
	}
	e.pos, e.rot, e.onGround, e.tick = m.Pos, m.Rot, m.OnGround, m.Tick
	if c != e.chunk {
		r.unindex(e)
		e.chunk = c
		r.byChunk[c] = append(r.byChunk[c], e)
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
	r.byChunk[e.chunk] = append(r.byChunk[e.chunk], e)
}

func byRuntimeID(a, b *Entity) int { return cmp.Compare(a.conf.RuntimeID, b.conf.RuntimeID) }

// unindex takes e out of the chunk index.
func (r *Region) unindex(e *Entity) {
	list := slices.DeleteFunc(r.byChunk[e.chunk], func(o *Entity) bool { return o == e })
	if len(list) == 0 {
		delete(r.byChunk, e.chunk)
		return
	}
	r.byChunk[e.chunk] = list
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
	}
	// Input may move entities between chunks, never add or remove them.
	for _, e := range r.entities {
		if e.conf.Input != nil {
			e.conf.Input(e)
		}
	}
}

// removeEntity takes e out of the world and tells the players who see it.
func (r *Region) removeEntity(e *Entity) {
	e.gone = true
	r.unindex(e)
	pk := &packet.RemoveActor{EntityUniqueID: e.conf.UniqueID}
	for _, p := range r.entities {
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
	for _, p := range r.entities {
		if p.known == nil {
			continue
		}
		for dx := -n; dx <= n; dx++ {
			for dz := -n; dz <= n; dz++ {
				if dx*dx+dz*dz > r2 {
					continue
				}
				for _, o := range r.byChunk[mcdb.ChunkPos{p.chunk[0] + dx, p.chunk[1] + dz}] {
					if o == p {
						continue
					}
					if k, ok := p.known[o]; ok {
						k.seen = true
						p.known[o] = k
						continue
					}
					p.known[o] = known{id: o.conf.UniqueID, seen: true}
					for _, pk := range r.spawnPackets(o) {
						p.conf.Viewer.sendEntity(pk)
					}
				}
			}
		}
		for o, k := range p.known {
			if !k.seen {
				delete(p.known, o)
				p.conf.Viewer.sendEntity(&packet.RemoveActor{EntityUniqueID: k.id})
				continue
			}
			k.seen = false
			p.known[o] = k
		}
	}
}

// spawnPackets returns o's spawn packets, built once per tick.
func (r *Region) spawnPackets(o *Entity) []packet.Packet {
	if o.conf.Spawn == nil {
		return nil
	}
	if o.spawn == nil || o.spawnTick != r.ticks {
		o.spawn, o.spawnTick = o.conf.Spawn(o), r.ticks
	}
	return o.spawn
}

// sendMovement sends each entity that moved this tick to the players who
// see it: one packet per entity, shared by all of them.
func (r *Region) sendMovement() {
	for _, e := range r.entities {
		if !e.moved {
			continue
		}
		e.moved = false
		if e.conf.Move == nil {
			continue
		}
		pk := e.conf.Move(e)
		if pk == nil {
			continue
		}
		for _, p := range r.entities {
			if _, ok := p.known[e]; ok {
				p.conf.Viewer.sendEntity(pk)
			}
		}
	}
}
