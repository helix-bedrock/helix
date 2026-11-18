package player

import (
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// The eye of ender, as BDS 1.26.60 does it. Using one asks the
// dimension for the stronghold it points to (StrongholdFeature's search; only the
// Overworld's generator places strongholds, so it does nothing in the Nether and the End),
// swings the arm, spawns an eye_of_ender_signal at the player's position + 0.5 on each axis,
// aims it (signalTo) and uses the item up. The eye flies by its own tick
// and after 80 ticks (its normal tick) drops as an item 4 times in 5, or else
// shatters: LevelEvent 2003 (data 10) and 1041.
//
// Not done: the game rule BDS checks first (index 40, not registered with the others, so not
// found), the gameplay event and game event 40 it posts, its particles (drawn by clients), and
// saving an eye in flight (it is gone when its chunk unloads).
const (
	nameEnderEyeSignal = "minecraft:eye_of_ender_signal"

	// enderEyeHalf is half the signal's 0.25 box.
	enderEyeHalf = 0.125
	// enderEyeSpawnOffset is added to each axis of the thrower's position.
	enderEyeSpawnOffset = 0.5
	// enderEyeReach is how far ahead it aims at most; a far target is aimed at that far ahead
	// and enderEyeRise above.
	enderEyeReach = 12
	enderEyeRise  = 8
	// enderEyeLife is the age after which it drops or shatters.
	enderEyeLife = 80
	// enderEyeSurviveOdds is 1 in this many eyes shattering.
	enderEyeSurviveOdds = 5
	// enderEyeSteer is how much of the gap to the target's distance its speed closes each tick,
	// enderEyeClimb how much of the gap to a climb or fall of 1 its vertical speed closes.
	enderEyeSteer = 0.0025
	enderEyeClimb = 0.015
	// enderEyeSlow slows it once it is within a block of the target.
	enderEyeSlow = 0.8
	// enderEyePickupDelay is the pick-up delay of the item it drops (assumed: not read from BDS).
	enderEyePickupDelay = 10
	// enderEyeShatterData is LevelEvent 2003's data.
	enderEyeShatterData = 10
	// levelEventEnderEyeShatter is the sound LevelEvent BDS sends with it (1041).
	levelEventEnderEyeShatter = 1041
)

// EnderEye is a thrown eye of ender flying toward a stronghold. Runs: owner of its entity.
type EnderEye struct {
	s        *Host
	entity   *world.Entity
	vel      mgl32.Vec3
	target   mgl32.Vec3
	life     int32
	survives bool
	gone     bool
	moves    actorMoves
}

// Entity returns its entity.
func (y *EnderEye) Entity() *world.Entity { return y.entity }

// throwEnderEye matches BDS: false, and nothing happens, when the dimension has no
// stronghold to point to.
func (p *Player) throwEnderEye(held ItemStack) bool {
	if held.Name != nameEnderEye || p.entity == nil {
		return false
	}
	r := p.entity.Region()
	if r == nil {
		return false
	}
	// A click on a block (an end portal frame taking it) and a click on the air may both arrive
	// for one use.
	now := r.CurrentTick()
	if now == p.thrownAt {
		return true
	}
	pos := p.entity.Position()
	target, ok := r.Dimension().EnderEyeTarget(int32(math.Floor(float64(pos[0]))), int32(math.Floor(float64(pos[2]))))
	if !ok {
		return false
	}
	r.Broadcast(p.blockPos(), &packet.Animate{
		ActionType: packet.AnimateActionSwingArm, EntityRuntimeID: p.runtimeID, SwingSource: packet.AnimateSwingSourceUseItem,
	}, p.viewer)
	p.thrownAt = now
	at := pos.Add(mgl32.Vec3{enderEyeSpawnOffset, enderEyeSpawnOffset, enderEyeSpawnOffset})
	y := &EnderEye{s: p.s}
	y.signalTo(at, mgl32.Vec3{float32(target[0]), float32(target[1]), float32(target[2])})
	// The throw sound snowballs and pearls make, heard on BDS (not found).
	playSound(r, p.blockPos(), soundEvent{name: packet.SoundEventThrow, pos: p.AttachPos(AttachDrop), extra: -1, entity: "minecraft:player"}, nil)
	y.entity = r.Dimension().AddEntity(y.config(itemUniqueIDBase|rand.Int64N(itemUniqueIDBase), p.s.runtimeIDs.Add(1), at)) //nolint:gosec // an entity ID, not a secret
	if !p.creative() {
		p.useHeld()
	}
	return true
}

// signalTo is BDS's signalTo: a target within reach is aimed at; a farther one 12 blocks
// toward it, 8 above the eye. Four eyes in five will drop.
func (y *EnderEye) signalTo(from, target mgl32.Vec3) {
	dx, dz := target[0]-from[0], target[2]-from[2]
	dist := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	if dist <= enderEyeReach {
		y.target = target
	} else {
		y.target = mgl32.Vec3{from[0] + dx/dist*enderEyeReach, from[1] + enderEyeRise, from[2] + dz/dist*enderEyeReach}
	}
	y.life = 0
	y.survives = rand.IntN(enderEyeSurviveOdds) > 0 //nolint:gosec // gameplay chance, not a secret
}

func (y *EnderEye) config(uniqueID int64, runtimeID uint64, pos mgl32.Vec3) world.EntityConfig {
	y.moves.start(pos, world.Rotation{}, actorMoveDropped, actorMoveFar)
	return world.EntityConfig{
		UniqueID: uniqueID, RuntimeID: runtimeID, Pos: pos,
		Owner: y, Spawn: y.spawnPackets, Move: y.movePacket, Input: y.tick,
	}
}

func (y *EnderEye) spawnPackets(e *world.Entity) []packet.Packet {
	meta := protocol.EntityMetadata{
		protocol.EntityDataKeyWidth: float32(2 * enderEyeHalf), protocol.EntityDataKeyHeight: float32(2 * enderEyeHalf),
		protocol.EntityDataKeyScale: float32(1),
	}
	return []packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: nameEnderEyeSignal,
		Position: e.Position(), Velocity: y.vel, EntityMetadata: meta,
	}}
}

func (y *EnderEye) movePacket(e *world.Entity) packet.Packet {
	y.moves.update(e, y.vel)
	return nil
}

// tick moves it by its speed, then steers: its speed along the ground closes on the distance
// left, slower within a block, and it climbs or sinks toward the target's height. It passes
// through blocks. Past its life it drops or shatters.
func (y *EnderEye) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || y.gone {
		return
	}
	pos := e.Position().Add(y.vel)
	y.steer(pos)
	if !e.Apply(world.Movement{Pos: pos, Rot: e.Rotation(), Tick: e.Tick()}) {
		y.vel = mgl32.Vec3{}
	}
	y.life++
	if y.life > enderEyeLife {
		y.finish(r, e)
	}
}

// steer works out the next speed from pos.
func (y *EnderEye) steer(pos mgl32.Vec3) {
	dx, dz := y.target[0]-pos[0], y.target[2]-pos[2]
	dist := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	angle := math.Atan2(float64(dz), float64(dx))
	speed := float32(math.Sqrt(float64(y.vel[0]*y.vel[0] + y.vel[2]*y.vel[2])))
	speed += (dist - speed) * enderEyeSteer
	vy := y.vel[1]
	if dist < 1 {
		speed *= enderEyeSlow
		vy *= enderEyeSlow
	}
	climb := float32(-1)
	if y.target[1] > pos[1] {
		climb = 1
	}
	vy += (climb - vy) * enderEyeClimb
	y.vel = mgl32.Vec3{float32(math.Cos(angle)) * speed, vy, float32(math.Sin(angle)) * speed}
}

// finish ends it: the eye item where it is, or the shatter.
func (y *EnderEye) finish(r *world.Region, e *world.Entity) {
	pos := e.Position()
	y.gone = true
	e.Remove()
	if y.survives {
		y.s.spawnItem(r.Dimension(), y.s.freshStack(ItemStack{Name: nameEnderEye, Count: 1}), pos, mgl32.Vec3{}, world.Rotation{}, enderEyePickupDelay, 0)
		return
	}
	at := vecPos(pos)
	levelEvent(r, at, pos, packet.LevelEventParticlesEyeOfEnderDeath, enderEyeShatterData)
	levelEvent(r, at, pos, levelEventEnderEyeShatter, 0)
}
