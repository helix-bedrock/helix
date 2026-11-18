package mob

import (
	"math"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/move"
)

// The squid motion numbers.
const (
	squidSteer    = 0.22 // the velocity moves this share of the way to the swim direction a tick
	squidTurn     = 0.1  // the body turns this share of the way to the movement a tick
	squidGravity  = 0.08 // out of water
	squidAirDrag  = 0.98
	radToDeg      = 180 / math.Pi
	squidParticle = 3 // random floats the move particle draws after its chance
)

// The mob types BDS gives SquidFlagComponent.
const (
	typeSquid     = "minecraft:squid"
	typeGlowSquid = "minecraft:glow_squid"
)

// The jump components that give a mob BDS's JumpControlComponent.
const (
	jumpStatic  = "minecraft:jump.static"
	jumpDynamic = "minecraft:jump.dynamic"
)

// isSquid reports whether a mob type moves as a squid.
func isSquid(typ string) bool { return typ == typeSquid || typ == typeGlowSquid }

// SwimDir implements behavior.Squid: the swim direction the squid goals set.
func (m *Mob) SwimDir() behavior.Vec3 { return m.swimDir }

// SetSwimDir implements behavior.Squid.
func (m *Mob) SetSwimDir(v behavior.Vec3) { m.swimDir = v }

// Collided implements behavior.Squid: CollisionFlagComponent, set when the last move hit a
// block on any axis.
func (m *Mob) Collided() bool {
	return m.body.HorizontalCollision[0] || m.body.HorizontalCollision[1] || m.body.VerticalCollision
}

// MoveTurn implements behavior.Slime: the move control's max_turn.
func (m *Mob) MoveTurn() (float32, bool) { return m.ctl.MaxTurn, m.hasMove }

// KeepMoving implements behavior.Slime: the move control keeps its wanted position and
// moves again at speed.
func (m *Mob) KeepMoving(speed float32) {
	if m.hasMove {
		m.ctl.HasWanted, m.ctl.Speed = true, speed
	}
}

// HasJumpControl implements behavior.Slime.
func (m *Mob) HasJumpControl() bool { return m.jumpCtl }

// LookYaw implements behavior.Slime: the look control turns to yaw with pitch 0.
func (m *Mob) LookYaw(yaw, speed float32) bool {
	m.look.HasYaw, m.look.Yaw, m.look.Pitch = true, yaw, 0
	m.look.YawSpeed, m.look.PitchSpeed = speed, speed
	return true
}

// squidPreStep is SquidPreAiStepSystem: a squid's strafe and up input are cleared before
// its AI runs, so only the swim direction moves it sideways.
func (m *Mob) squidPreStep() {
	if m.body.Squid {
		m.body.Input.X, m.body.Input.Y = 0, 0
	}
}

// squidStep is the server half of BDS's code: in water the
// velocity eases toward the swim direction, out of it the squid stops and sinks; the body
// then turns toward the movement. Levitation (effect 24) counts as out of water for the
// velocity; mobs have no effects yet, so it never applies. The tentacle animation is the
// client's.
func (m *Mob) squidStep() {
	if !m.body.Squid {
		return
	}
	in := m.body.WasInWater
	v := &m.body.Vel
	if in {
		v.X += (m.swimDir.X - v.X) * squidSteer
		v.Y += (m.swimDir.Y - v.Y) * squidSteer
		v.Z += (m.swimDir.Z - v.Z) * squidSteer
	} else {
		v.X, v.Z = 0, 0
		m.swimDir.Y = 0
		v.Y = (v.Y - squidGravity) * squidAirDrag
	}
	dir := m.swimDir
	if in {
		dir = *v
	}
	h := float32(math.Sqrt(float64(dir.X*dir.X + dir.Z*dir.Z)))
	yaw := m.bodyYaw + (float32(-math.Atan2(float64(dir.X), float64(dir.Z)))*radToDeg-m.bodyYaw)*squidTurn
	m.bodyYaw, m.yaw = yaw, yaw
	m.squidPitch += (float32(-math.Atan2(float64(h), float64(dir.Y)))*radToDeg - m.squidPitch) * squidTurn
	m.pitch = m.squidPitch
	if in {
		m.squidMoveParticle(h)
	}
}

// squidMoveParticle makes the move particle's draws on the level's random: its chance is
// the horizontal speed. The particle itself (minecraft:squid_move_particle) is not sent.
func (m *Mob) squidMoveParticle(speed float32) {
	r := m.v.rnd
	if speed <= r.NextFloat() {
		return
	}
	for range squidParticle {
		r.NextFloat()
	}
}

// slimeMove runs movement.jump's slime move control with the definition's jump delay.
func (m *Mob) slimeMove(b *move.Body) {
	j := move.SlimeJump{Min: m.movement.JumpDelay.Min, Max: m.movement.JumpDelay.Max}
	move.Slime(b, &m.ctl, &m.slimer, &j, m.v.rnd.NextFloat)
}
