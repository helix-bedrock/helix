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
type Entity struct {
	d    *Dimension
	conf EntityConfig

	// region is the region the entity belongs to. Guarded by d.mu.
	region *Region

	// The latest movement from outside the tick (a player's input), applied
	// at the start of the next tick.
	inMu    sync.Mutex
	in      Movement
	hasIn   bool
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
	// Removed runs once the entity was removed, with its last state.
	Removed func(e *Entity)
}

// AddEntity adds an entity, in its viewer's region for a player, else in
// the region that owns its chunk, on that region's next tick. Runs:
// anywhere.
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

// Move moves the entity at the start of its region's next tick; only the
// latest movement counts. It is how a player's input reaches the world.
// Runs: anywhere.
func (e *Entity) Move(m Movement) {
	e.inMu.Lock()
	e.in, e.hasIn = m, true
	e.inMu.Unlock()
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

// viewDisc are the chunk offsets around a player whose entities it sees:
// BDS 1.26.60's 57 chunks with dx*dx+dz*dz <= 17, whatever the view and tick
// distances.
const viewDiscRadius2 = 17

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

// tickEntities applies the movement and removals queued since the last
// tick.
func (r *Region) tickEntities() {
	removed := false
	for _, e := range r.entities {
		e.inMu.Lock()
		m, has, rem := e.in, e.hasIn, e.removed
		e.hasIn = false
		e.inMu.Unlock()
		if rem {
			removed = true
			continue
		}
		if !has {
			continue
		}
		if m.Pos != e.pos || m.Rot != e.rot || m.OnGround != e.onGround {
			e.moved = true
		}
		e.pos, e.rot, e.onGround, e.tick = m.Pos, m.Rot, m.OnGround, m.Tick
		if c := chunkOf(m.Pos); c != e.chunk {
			r.unindex(e)
			e.chunk = c
			r.byChunk[c] = append(r.byChunk[c], e)
		}
	}
	if removed {
		r.entities = slices.DeleteFunc(r.entities, func(e *Entity) bool {
			e.inMu.Lock()
			rem := e.removed
			e.inMu.Unlock()
			if rem {
				r.removeEntity(e)
			}
			return rem
		})
	}
}

// removeEntity tells the players who see e, and runs its Removed hook.
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
	if e.conf.Removed != nil {
		e.conf.Removed(e)
	}
}

// updateVisibility shows each player the entities that came into its disc
// and removes those that left it, as BDS does every tick.
func (r *Region) updateVisibility() {
	for _, p := range r.entities {
		if p.known == nil {
			continue
		}
		for dx := int32(-4); dx <= 4; dx++ {
			for dz := int32(-4); dz <= 4; dz++ {
				if dx*dx+dz*dz > viewDiscRadius2 {
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
