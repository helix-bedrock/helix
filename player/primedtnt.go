package player

import (
	"maps"
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// BDS 1.26.60's primed TNT and its definition in the behaviour pack.
const (
	nameTNT           = "minecraft:tnt"
	nameFlintSteel    = "minecraft:flint_and_steel"
	nameFireCharge    = "minecraft:fire_charge"
	enchantFireAspect = int16(13)
	stateExplodeBit   = "explode_bit"

	// tntPower is its radius, tntFuse its fuse in ticks. One an explosion lit gets a fuse
	// from tntBlastFuseMin to tntBlastFuseMax.
	tntPower        = 4
	tntFuse         = 80
	tntBlastFuseMin = 10
	tntBlastFuseMax = 40
	// tntHalfSize is half of its 0.98 box, tntBlastOffset how far above its feet it
	// explodes.
	tntHalfSize    = 0.49
	tntBlastOffset = 0.06125
	// A new one hops: up, and a little to a random side.
	tntHop    = 0.2
	tntSpread = 0.02

	tntGravity    = 0.04
	tntDrag       = 0.98
	tntGroundDrag = 0.7
	tntBounce     = -0.5

	// tntBlockCenter is the middle of a block across; tntDispenseUp is how far above the
	// block's floor a dispensed one's feet are.
	tntBlockCenter = 0.5
	tntDispenseUp  = 0.5

	tntFlags = 1<<protocol.EntityDataFlagHasGravity | 1<<protocol.EntityDataFlagHasCollision | 1<<protocol.EntityDataFlagIgnited

	tntKeyFuse = "Fuse"
	tntKeyLit  = "IsFuseLit"
)

// PrimedTNT is lit TNT: BDS's PrimedTnt with its explode component. Its numbers are its
// own, so a plugin can make any: see [Host.SpawnTNT]. Runs: owner of its entity.
type PrimedTNT struct {
	s      *Host
	entity *world.Entity
	vel    mgl32.Vec3

	fuse     int32
	lit      bool
	onGround bool
	gone     bool
	// power is the explosion's radius; fire whether it sets fires.
	power float32
	fire  bool
	// owner is the player who lit it, if anyone.
	owner *Player
	saved map[string]any
}

// Entity returns the TNT's entity.
func (t *PrimedTNT) Entity() *world.Entity { return t.entity }

// Fuse returns the ticks left.
func (t *PrimedTNT) Fuse() int32 { return t.fuse }

// SetFuse sets the ticks left.
func (t *PrimedTNT) SetFuse(ticks int32) { t.fuse = ticks }

// SetPower sets the explosion's radius.
func (t *PrimedTNT) SetPower(radius float32) { t.power = radius }

// SetFire sets whether its explosion sets fires.
func (t *PrimedTNT) SetFire(fire bool) { t.fire = fire }

// SpawnTNT adds lit TNT with its feet at feet and BDS's hop.
func (s *Host) SpawnTNT(d *world.Dimension, feet mgl32.Vec3, fuse int32, owner *Player) *PrimedTNT {
	angle := rand.Float64() * 2 * math.Pi //nolint:gosec // a hop, not a secret
	t := &PrimedTNT{
		s: s, fuse: fuse, lit: true, power: tntPower, owner: owner,
		vel: mgl32.Vec3{float32(-math.Sin(angle)) * tntSpread, tntHop, float32(-math.Cos(angle)) * tntSpread},
	}
	pos := mgl32.Vec3{feet[0], feet[1] + tntHalfSize, feet[2]}
	t.entity = d.AddEntity(t.config(itemUniqueIDBase|rand.Int64N(itemUniqueIDBase), s.runtimeIDs.Add(1), pos, world.Rotation{})) //nolint:gosec // an entity ID, not a secret
	return t
}

func (t *PrimedTNT) config(uniqueID int64, runtimeID uint64, pos mgl32.Vec3, rot world.Rotation) world.EntityConfig {
	return world.EntityConfig{
		UniqueID: uniqueID, RuntimeID: runtimeID, Pos: pos, Rot: rot,
		Owner: t, Spawn: t.spawnPackets, Move: t.movePacket, Input: t.tick, Save: t.saveEntity,
	}
}

// spawnPackets show it to a player. Not yet compared with what BDS sends.
func (t *PrimedTNT) spawnPackets(e *world.Entity) []packet.Packet {
	meta := protocol.EntityMetadata{
		protocol.EntityDataKeyFlags: int64(tntFlags), protocol.EntityDataKeyFuseTime: t.fuse,
		protocol.EntityDataKeyWidth: float32(2 * tntHalfSize), protocol.EntityDataKeyHeight: float32(2 * tntHalfSize),
		protocol.EntityDataKeyScale: float32(1),
	}
	if t.owner != nil && t.owner.entity != nil {
		meta[protocol.EntityDataKeyOwner] = t.owner.entity.UniqueID()
	}
	return []packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: nameTNT,
		Position: e.Position(), Velocity: t.vel, EntityMetadata: meta,
	}}
}

func (t *PrimedTNT) movePacket(e *world.Entity) packet.Packet {
	rot := e.Rotation()
	var flags byte
	if e.OnGround() {
		flags = packet.MoveFlagOnGround
	}
	return &packet.MoveActorAbsolute{
		EntityRuntimeID: e.RuntimeID(), Flags: flags, Position: e.Position(), Rotation: mgl32.Vec3{rot.Pitch, rot.Yaw, rot.HeadYaw},
	}
}

// tick is BDS's movement for primed TNT, then its fuse. They are separate systems in
// BDS; which one runs first in a tick was not worked out.
func (t *PrimedTNT) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || t.gone {
		return
	}
	t.move(r, e)
	if !t.lit {
		return
	}
	if t.fuse > 0 {
		t.fuse--
		e.Send(&packet.SetActorData{EntityRuntimeID: e.RuntimeID(), EntityMetadata: protocol.EntityMetadata{protocol.EntityDataKeyFuseTime: t.fuse}})
		return
	}
	t.gone = true
	if t.s.level.TNTExplodes {
		t.s.Explode(r, t.explosion(e))
	}
	e.Remove()
}

// explosion is what it explodes as.
func (t *PrimedTNT) explosion(e *world.Entity) *Explosion {
	at := e.Position()
	x := NewExplosion(mgl32.Vec3{at[0], at[1] - tntHalfSize + tntBlastOffset, at[2]}, t.power)
	x.Source, x.Attacker, x.Fire, x.Cause = e, t.owner, t.fire, DamageEntityExplosion
	if !t.s.level.TNTExplosionDropDecay {
		x.DropRadius = 1
	}
	return x
}

// move applies gravity, moves through the blocks' collision boxes and slows down.
func (t *PrimedTNT) move(r *world.Region, e *world.Entity) {
	t.vel[1] -= tntGravity
	pos, hitY := e.Position(), false
	for _, axis := range [...]int{1, 0, 2} {
		d := t.s.clipMove(r, pos, tntHalfSize, axis, t.vel[axis])
		if d != t.vel[axis] {
			if axis == 1 {
				hitY = true
			} else {
				t.vel[axis] = 0
			}
		}
		pos[axis] += d
	}
	t.onGround = hitY && t.vel[1] < 0
	if hitY {
		t.vel[1] = 0
	}
	if !e.Apply(world.Movement{Pos: pos, Rot: e.Rotation(), OnGround: t.onGround, Tick: e.Tick()}) {
		t.vel = mgl32.Vec3{}
		return
	}
	t.vel = t.vel.Mul(tntDrag)
	if t.onGround {
		t.vel = mgl32.Vec3{t.vel[0] * tntGroundDrag, t.vel[1] * tntBounce, t.vel[2] * tntGroundDrag}
	}
}

// BlastBox is its box.
func (t *PrimedTNT) BlastBox() (lo, hi mgl32.Vec3, ok bool) {
	if t.gone || t.entity == nil {
		return lo, hi, false
	}
	at := t.entity.Position()
	half := mgl32.Vec3{tntHalfSize, tntHalfSize, tntHalfSize}
	return at.Sub(half), at.Add(half), true
}

// BlastHit does nothing: explosions do not hurt it.
func (t *PrimedTNT) BlastHit(float32, DamageSource) {}

// BlastPush adds to its speed.
func (t *PrimedTNT) BlastPush(push mgl32.Vec3) {
	t.vel = t.vel.Add(push)
	pushed(t.entity, t.vel)
}

func (t *PrimedTNT) saveEntity(e *world.Entity) (chunk.Entity, bool) {
	if t.gone {
		return chunk.Entity{}, false
	}
	data := maps.Clone(t.saved)
	if data == nil {
		data = map[string]any{}
	}
	pos, rot := e.Position(), e.Rotation()
	data["identifier"] = nameTNT
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Motion"] = []float32{t.vel[0], t.vel[1], t.vel[2]}
	data["Rotation"] = []float32{rot.Yaw, rot.Pitch}
	data[tntKeyFuse] = uint8(min(max(t.fuse, 0), math.MaxUint8)) //nolint:gosec // clamped
	var lit uint8
	if t.lit {
		lit = 1
	}
	data[tntKeyLit] = lit
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
}

// loadTNT brings back saved TNT with the fields BDS's explode component reads.
func (s *Host) loadTNT(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity) bool {
	pos, ok := floats(saved.Data["Pos"])
	if !ok || len(pos) != 3 {
		return false
	}
	t := &PrimedTNT{s: s, power: tntPower, saved: saved.Data}
	if v, isList := floats(saved.Data["Motion"]); isList && len(v) == 3 {
		t.vel = mgl32.Vec3{v[0], v[1], v[2]}
	}
	var rot world.Rotation
	if v, isList := floats(saved.Data["Rotation"]); isList && len(v) == 2 {
		rot = world.Rotation{Yaw: v[0], Pitch: v[1], HeadYaw: v[0]}
	}
	fuse, _ := saved.Data[tntKeyFuse].(uint8)
	lit, _ := saved.Data[tntKeyLit].(uint8)
	t.fuse, t.lit = int32(fuse), lit != 0
	t.entity = r.AddLoadedEntity(at, t.config(saved.ID, s.runtimeIDs.Add(1), mgl32.Vec3{pos[0], pos[1], pos[2]}, rot))
	return true
}

// tntBlock is what vanilla's TNT block does, set as its behaviour so a plugin can
// replace it.
type tntBlock struct{}

// Use lights it with flint and steel, a fire charge or a Fire Aspect item, which wears
// by one if it can wear. BDS's code does not use up the fire charge here.
func (tntBlock) Use(u *BlockUse) bool {
	p := u.Player
	r := p.entity.Region()
	if r == nil || !ignites(p.inventory.Held()) {
		return false
	}
	if p.s.PrimeBlock(r, u.Pos, p) {
		p.hurtHeld(1)
	}
	return true
}

// ignites reports whether an item lights TNT.
func ignites(s ItemStack) bool {
	if s.Name == nameFlintSteel || s.Name == nameFireCharge {
		return true
	}
	for _, e := range s.Enchants() {
		if e.ID == enchantFireAspect {
			return true
		}
	}
	return false
}

// Exploded lights it with a short fuse; the explosion that took it removes the block.
func (tntBlock) Exploded(s *Host, r *world.Region, pos cube.Pos, _ uint32, e *Explosion) {
	if !s.level.TNTExplodes {
		return
	}
	fuse := int32(tntBlastFuseMin + rand.IntN(tntBlastFuseMax-tntBlastFuseMin+1)) //nolint:gosec // a fuse, not a secret
	s.SpawnTNT(r.Dimension(), tntFeet(pos), fuse, e.Attacker)
}

// tntFeet is where TNT lit in a block stands.
func tntFeet(pos cube.Pos) mgl32.Vec3 {
	return mgl32.Vec3{float32(pos[0]) + tntBlockCenter, float32(pos[1]), float32(pos[2]) + tntBlockCenter}
}

// PrimeBlock removes the TNT block at pos and lights it, by owner if a player did it.
// False if the tntexplodes game rule is off. Runs: owner of the region.
func (s *Host) PrimeBlock(r *world.Region, pos cube.Pos, owner *Player) bool {
	if !s.level.TNTExplodes {
		return false
	}
	r.SetBlock(pos, 0, s.reg.Blocks.Air())
	r.UpdateNeighbours(pos)
	s.lightTNT(r, pos, owner)
	return true
}

// lightTNT is BDS's TNT block lighting: the entity, and the fuse sound where it is.
func (s *Host) lightTNT(r *world.Region, pos cube.Pos, owner *Player) {
	t := s.SpawnTNT(r.Dimension(), tntFeet(pos), tntFuse, owner)
	levelEvent(r, pos, t.entity.Position(), packet.LevelEventSoundFuse, 0)
}

// PowerTNT is redstone reaching a TNT block. Runs: owner of the region.
func (s *Host) PowerTNT(r *world.Region, pos cube.Pos) { s.PrimeBlock(r, pos, nil) }

// dispenseTNT lights TNT dispensed into the block front.
func (s *Host) dispenseTNT(r *world.Region, front cube.Pos) {
	feet := tntFeet(front)
	feet[1] += tntDispenseUp
	t := s.SpawnTNT(r.Dimension(), feet, tntFuse, nil)
	levelEvent(r, front, t.entity.Position(), packet.LevelEventSoundFuse, 0)
}

// brokenTNT lights TNT carrying the explode bit as a player breaks it.
func (s *Host) brokenTNT(r *world.Region, pos cube.Pos, block uint32, p *Player) {
	blocks := s.reg.Blocks
	if blocks.Name(block) != nameTNT || !s.level.TNTExplodes {
		return
	}
	if v, _ := blocks.States(block)[stateExplodeBit].(uint8); v != 0 {
		s.lightTNT(r, pos, p)
	}
}
