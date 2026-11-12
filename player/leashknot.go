package player

import (
	"maps"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Leash knots, BDS 1.26.60's LeashKnot (a HangingActor, vtable 0xFAF64D8). A player who
// uses a fence while holding leashed mobs ties every one of them within its leash preset's
// max distance of the fence's centre to the fence's knot, made if there is none
// (LeashFenceKnotActor's bind 0xBFCBDD0, the fence's use 0xD29E7B0; any item, and the use
// counts only when a mob was tied), with the leashknot.place sound. The knot stands at the
// block's x and z centre and its y + 0.25 (its setPos 0xBFCB250). Hitting or using it takes
// it away with leashknot.break (0xBFCAE90), and every mob tied to it drops its lead. Every
// 80 to 120 ticks it checks its fence is still there (HangingActor's tick 0xBFC44D0).
//
// Not read from BDS: which blocks take a knot (BlockLeashableDescription is attached by
// code not found): every *_fence block but gates; the fence's own leash offset, taken as 0.

const (
	leashKnotID = "minecraft:leash_knot"
	// leashKnotY is the knot's height above its block's bottom.
	leashKnotY = 0.25
	// leashKnotSize is the knot's box, as the client draws it; not read from BDS.
	leashKnotSize = 0.375
)

// LeashKnot is a knot on a fence that leashed mobs are tied to. Runs: owner of its entity.
type LeashKnot struct {
	s      *Host
	entity *world.Entity
	saved  map[string]any
	gone   bool
	check  int32
}

// isLeashFence reports a block a leash knot goes on.
func isLeashFence(name string) bool {
	return strings.HasSuffix(name, "_fence") && !strings.HasSuffix(name, "fence_gate")
}

func (k *LeashKnot) config(uid int64, rid uint64, pos mgl32.Vec3) world.EntityConfig {
	return world.EntityConfig{
		UniqueID: uid, RuntimeID: rid, Pos: pos,
		Owner: k, Spawn: k.spawnPackets, Input: k.tick, Save: k.saveEntity,
	}
}

func (k *LeashKnot) spawnPackets(e *world.Entity) []packet.Packet {
	return []packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: leashKnotID,
		Position: e.Position(), EntityMetadata: protocol.EntityMetadata{},
	}}
}

func (k *LeashKnot) saveEntity(e *world.Entity) (chunk.Entity, bool) {
	if k.gone {
		return chunk.Entity{}, false
	}
	data := maps.Clone(k.saved)
	if data == nil {
		data = map[string]any{}
	}
	pos := e.Position()
	data["identifier"] = leashKnotID
	data["UniqueID"] = e.UniqueID()
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Rotation"] = []float32{0, 0}
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
}

// loadKnot brings a saved knot back.
func (s *Host) loadKnot(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity) bool {
	pos, ok := floats(saved.Data["Pos"])
	if !ok || len(pos) != 3 {
		return false
	}
	k := &LeashKnot{s: s, saved: saved.Data}
	k.entity = r.AddLoadedEntity(at, k.config(saved.ID, s.runtimeIDs.Add(1), mgl32.Vec3{pos[0], pos[1], pos[2]}))
	return true
}

// block is the fence the knot is on.
func (k *LeashKnot) block() cube.Pos { return floorPos(k.entity.Position()) }

// tick takes the knot away once its fence is gone.
func (k *LeashKnot) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || k.gone {
		return
	}
	if k.check--; k.check >= 0 {
		return
	}
	low := int32(math.Floor(paintingCheck * (1 - paintingCheckSpread)))
	high := int32(math.Ceil(paintingCheck * (1 + paintingCheckSpread)))
	k.check = low + rand.Int32N(high-low) //nolint:gosec // a delay, not a secret
	b, ok := r.Block(k.block(), 0)
	if ok && !isLeashFence(k.s.reg.Blocks.Name(b)) {
		k.Break(r)
	}
}

// Break takes the knot away: its sound, and every mob tied to it drops its lead.
func (k *LeashKnot) Break(r *world.Region) {
	if k.gone {
		return
	}
	k.gone = true
	pos := k.entity.Position()
	playSound(r, k.block(), soundEvent{name: packet.SoundEventBreakLeashKnot, pos: pos, extra: -1}, nil)
	id := behavior.ID(k.entity.UniqueID())
	for _, o := range k.s.leashedTo(r, id) {
		o.m.Unleash(true)
	}
	k.entity.Remove()
}

// leashedTo is the mobs of r whose leash the entity holds.
func (s *Host) leashedTo(r *world.Region, holder behavior.ID) []*Mob {
	var out []*Mob
	for _, e := range r.AppendEntities(nil) {
		if o, ok := e.Owner().(*Mob); ok && !o.gone && o.m.LeashHolder() == holder {
			out = append(out, o)
		}
	}
	return out
}

// knotAt is the knot on the fence at pos, nil for none.
func (s *Host) knotAt(r *world.Region, pos cube.Pos) *LeashKnot {
	c := blockCenter(pos)
	for _, e := range r.AppendEntitiesIn(nil, c[0]-1, c[2]-1, c[0]+1, c[2]+1) {
		if k, ok := e.Owner().(*LeashKnot); ok && !k.gone && floorPos(e.Position()) == pos {
			return k
		}
	}
	return nil
}

// tieToFence is the fence's use: the player's leashed mobs near it go to its knot. True if
// any was tied.
func (p *Player) tieToFence(r *world.Region, u *BlockUse) bool {
	if !isLeashFence(p.s.reg.Blocks.Name(u.Block)) {
		return false
	}
	me := actorOf(p.entity)
	if me == nil {
		return false
	}
	centre := blockCenter(u.Pos)
	k := p.s.knotAt(r, u.Pos)
	tied := false
	for _, o := range p.s.leashedTo(r, me.ID()) {
		reach := o.m.LeashMaxDistance(me)
		if o.entity.Position().Sub(centre).LenSqr() > reach*reach {
			continue
		}
		if k == nil {
			k = &LeashKnot{s: p.s}
			pos := mgl32.Vec3{float32(u.Pos[0]) + 0.5, float32(u.Pos[1]) + leashKnotY, float32(u.Pos[2]) + 0.5}
			uid := itemUniqueIDBase | rand.Int64N(itemUniqueIDBase) //nolint:gosec // an entity ID, not a secret
			k.entity = r.Dimension().AddEntity(k.config(uid, p.s.runtimeIDs.Add(1), pos))
		}
		o.m.LeashToHolder(behavior.ID(k.entity.UniqueID()))
		tied = true
	}
	if tied {
		playSound(r, u.Pos, soundEvent{name: packet.SoundEventPlaceLeashKnot, pos: centre, extra: -1}, nil)
	}
	return tied
}

// knotNear finds a knot near the player by its runtime ID.
func (p *Player) knotNear(id uint64) *LeashKnot {
	r := p.entity.Region()
	if r == nil {
		return nil
	}
	pos := p.entity.Position()
	p.touching = r.AppendEntitiesIn(p.touching[:0], pos[0]-attackScan, pos[2]-attackScan, pos[0]+attackScan, pos[2]+attackScan)
	defer clear(p.touching)
	for _, e := range p.touching {
		if k, ok := e.Owner().(*LeashKnot); ok && e.RuntimeID() == id && !k.gone {
			return k
		}
	}
	return nil
}

// useOnKnot is a hit or use on a knot: either takes it away. False if not aimed at one.
func (p *Player) useOnKnot(d *protocol.UseItemOnEntityTransactionData) bool {
	k := p.knotNear(d.TargetEntityRuntimeID)
	if k == nil {
		return false
	}
	if r := p.entity.Region(); r != nil && p.entity.Position().Sub(k.entity.Position()).Len() <= cartReach {
		k.Break(r)
	}
	return true
}

// knotActor is a knot as the mob runtime sees it, so a mob finds its holder.
type knotActor struct {
	id  behavior.ID
	pos behavior.Vec3
}

func (a knotActor) ID() behavior.ID    { return a.id }
func (a knotActor) Pos() behavior.Vec3 { return a.pos }
func (a knotActor) Box() behavior.Box {
	h := float32(leashKnotSize / 2)
	return behavior.Box{
		Min: behavior.Vec3{X: a.pos.X - h, Y: a.pos.Y, Z: a.pos.Z - h},
		Max: behavior.Vec3{X: a.pos.X + h, Y: a.pos.Y + leashKnotSize*2, Z: a.pos.Z + h},
	}
}
func (a knotActor) Eyes() behavior.Vec3                { return a.pos }
func (a knotActor) Alive() bool                        { return true }
func (a knotActor) Removed() bool                      { return false }
func (a knotActor) Pickable() bool                     { return false }
func (a knotActor) IsMob() bool                        { return false }
func (a knotActor) IsPlayer() bool                     { return false }
func (a knotActor) Flag(int) bool                      { return false }
func (a knotActor) Type() string                       { return leashKnotID }
func (a knotActor) GameMode() int                      { return -1 }
func (a knotActor) Riders() []behavior.ID              { return nil }
func (a knotActor) Vehicle() behavior.ID               { return behavior.NoID }
func (a knotActor) Owner() behavior.ID                 { return behavior.NoID }
func (a knotActor) Team() int                          { return 0 }
func (a knotActor) Dimension() int                     { return 0 }
func (a knotActor) HeadRotation() (pitch, yaw float32) { return 0, 0 }
func (a knotActor) BodyYaw() float32                   { return 0 }
func (a knotActor) Invulnerable() bool                 { return true }
func (a knotActor) HasHealth() bool                    { return false }
func (a knotActor) Targetable() bool                   { return false }
func (a knotActor) Monster() bool                      { return false }
func (a knotActor) ArmourCoverage() float32            { return 0 }
func (a knotActor) WearsHeadOf(string) bool            { return false }
func (a knotActor) Target() behavior.ID                { return behavior.NoID }
func (a knotActor) HijacksMount() bool                 { return false }
func (a knotActor) LastHurtByMob() behavior.ID         { return behavior.NoID }
func (a knotActor) LastHurtMob() (behavior.ID, int32)  { return behavior.NoID, 0 }
func (a knotActor) BlocksSpace() bool                  { return false }
func (a knotActor) AttachPos(int) behavior.Vec3        { return a.pos }

var _ behavior.Actor = knotActor{}
