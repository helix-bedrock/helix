package player

import (
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// An empty boat's own movement, as BDS 1.26.60's systems run it for a boat no client
// steers, each tick in this order:
//   - IncreaseBuoyancyTimer (0x7926470): the wave timer.
//   - CheckAndAddBuoyancyFloatRequest: whether it floats or must come up.
//   - BuoyancyGravity (0x79280F0): apply_gravity pulls it down.
//   - BuoyancyFloat (0x7925140): the water lifts it to its waterline, rocking on the timer.
//   - BoatMoveFriction (0x75EE370): water, the ground under it or nothing slows it.
//
// Then it moves through the blocks' collision boxes. The numbers of the buoyant component
// are entities/boat.json's: base_buoyancy 1, movement_type waves, big_wave_probability 0.03,
// big_wave_speed 10, liquid_blocks water. Which way BDS orders the friction system against
// the move is not read yet; Helix slows it before moving, as Java's boat did.
const (
	boatBuoyancy     = 1.0
	boatBigWaveOdds  = 0.03
	boatBigWaveSpeed = 10
	// The timer grows by (horizontal speed * 30 + 1) * 0.05 a tick.
	boatTimerSpeed = 30
	boatTimerStep  = 0.05
	// Gravity: (vy - 0.04) * 0.98.
	boatGravity     = 0.04
	boatGravityDrag = 0.98
	// The float: how far under its waterline it is, scaled 0.9 and lifted 0.1, the waves'
	// (sin(timer) + 1) * 0.035, and the climb (submerged - waves - 0.1) * 0.15 at most
	// vy * 0.7 + 0.05.
	boatFloatScale  = 0.9
	boatFloatLift   = 0.1
	boatWaveHeight  = 0.035
	boatFloatRate   = 0.15
	boatFloatKeep   = 0.7
	boatFloatSpeed  = 0.05
	boatWaterDrag   = 0.9
	boatStillSpeed2 = 1e-8
)

// boatMotion is what moves an empty boat: its velocity (StateVectorComponent's position
// delta) and the buoyancy timer.
type boatMotion struct {
	vel   mgl32.Vec3
	timer float64
}

// physics is one tick of an empty boat's movement. Region tick.
func (b *Boat) physics(r *world.Region, e *world.Entity) {
	m := &b.motion
	pos := e.Position()
	m.timer += float64((float32(math.Sqrt(float64(m.vel[0]*m.vel[0]+m.vel[2]*m.vel[2])))*boatTimerSpeed + 1) * b.bigWave() * boatTimerStep)
	at := floorPos(pos)
	floats, resurface := b.s.boatFloat(r, at, pos[1])
	m.vel[1] = (m.vel[1] - boatGravity) * boatGravityDrag
	if floats || resurface {
		sub, waves := float32(1), float32(0)
		if floats {
			sub = min(max((boatBuoyancy-(pos[1]-float32(math.Floor(float64(pos[1])))))*boatFloatScale+boatFloatLift, 0), 1)
			waves = float32(math.Sin(m.timer)+1) * boatWaveHeight
		}
		if sub != 0 {
			m.vel[1] = min((sub-waves-boatFloatLift)*boatFloatRate, m.vel[1]*boatFloatKeep+boatFloatSpeed)
		}
	}
	f := b.s.boatFriction(r, at, floats || resurface, e.OnGround())
	m.vel[0] *= f
	m.vel[2] *= f
	if m.vel.LenSqr() < boatStillSpeed2 && e.OnGround() {
		m.vel = mgl32.Vec3{}
		return
	}
	lo, hi := boatBox(pos)
	hitY := false
	for _, axis := range [...]int{1, 0, 2} {
		d := b.s.clipSweep(r, lo, hi, axis, m.vel[axis])
		if d != m.vel[axis] {
			hitY = hitY || axis == 1
			if axis != 1 {
				m.vel[axis] = 0
			}
		}
		lo[axis] += d
		hi[axis] += d
		pos[axis] += d
	}
	onGround := hitY && m.vel[1] < 0
	if hitY {
		m.vel[1] = 0
	}
	if !e.Apply(world.Movement{Pos: pos, Rot: e.Rotation(), OnGround: onGround, Tick: e.Tick()}) {
		m.vel = mgl32.Vec3{}
	}
}

// bigWave is big_wave_speed when BDS's draw falls under big_wave_probability, else 1.
func (b *Boat) bigWave() float32 {
	if rand.Float32() <= boatBigWaveOdds { //nolint:gosec // gameplay odds
		return boatBigWaveSpeed
	}
	return 1
}

// boatFloat is BuoyancyComponent's canFloat and needToResurface for a boat whose feet are
// at y in block at: it floats when the water there reaches above its feet with no water
// over it, and comes up when water is over it too.
func (s *Host) boatFloat(r *world.Region, at cube.Pos, y float32) (floats, resurface bool) {
	if !s.boatWater(r, at) {
		return false, false
	}
	if s.boatWater(r, at.Add(cube.Pos{0, 1, 0})) {
		return false, true
	}
	return s.liquidTop(r, at) > y, false
}

// boatWater reports whether the block at pos is one of the boat's liquid_blocks: water.
func (s *Host) boatWater(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && s.reg.Blocks.Properties(b).Material == registry.MaterialWater
}

// boatFriction is BoatMoveFrictionSystem's slow down: 0.9 floating or coming up; in any
// other block, water 0.9 or on the ground the friction of what it rests on; in air, on
// the ground the friction of the block below, over water 0.9, else none.
func (s *Host) boatFriction(r *world.Region, at cube.Pos, inWater, onGround bool) float32 {
	if inWater {
		return boatWaterDrag
	}
	b, ok := r.Block(at, 0)
	if !ok {
		return 1
	}
	below := at.Add(cube.Pos{0, -1, 0})
	if b != s.reg.Blocks.Air() {
		switch {
		case s.reg.Blocks.Properties(b).Material == registry.MaterialWater:
			return boatWaterDrag
		case onGround:
			// BoatFrictionUtility::getFrictionBasedOnCollision: the block's own friction,
			// or the one below when it has no collision box.
			if len(s.reg.Blocks.Properties(b).Collision) == 0 {
				return s.frictionOf(r, below)
			}
			return s.reg.Blocks.Properties(b).Friction
		}
		return 1
	}
	if onGround {
		return s.frictionOf(r, below)
	}
	if s.boatWater(r, below) {
		return boatWaterDrag
	}
	return 1
}

// frictionOf is the friction of the block at pos, air's when it is not loaded.
func (s *Host) frictionOf(r *world.Region, pos cube.Pos) float32 {
	b, ok := r.Block(pos, 0)
	if !ok {
		b = s.reg.Blocks.Air()
	}
	return s.reg.Blocks.Properties(b).Friction
}
