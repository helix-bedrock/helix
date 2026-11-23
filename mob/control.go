package mob

import (
	"math"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/state"
)

// Look and body control as BDS 1.26.60's LookControl and BodyControl (server side), run in
// that order after the move and jump controls.

const (
	// headMaxTurn is how far the head may turn from the body, in degrees.
	headMaxTurn = 75
	// lookReset is how far a tick the look control turns the head and pitch back when
	// nothing is looked at.
	lookReset = 10
	// bodyFollowTurn is how far a tick the body turns toward the movement or the head when
	// it follows them.
	bodyFollowTurn = 25
	// headStillRange is how far the head may move and still count as still.
	headStillRange = 15
	// stillTicksBeforeTurn is how long the head must stay still before the body's turn
	// limit starts shrinking; it reaches 0 after as many ticks again.
	stillTicksBeforeTurn = 10
	// movingSpeed2 is the horizontal speed squared above which the body follows the
	// movement: BDS's 0.00000025000003.
	movingSpeed2 = 0.00000025000003
	radToDeg32   = float32(180 / math.Pi)
)

// wrapDegrees is BDS's angle wrap into [-180, 180): fmodf(d+180, 360), plus 360 below 0,
// minus 180, in float32.
func wrapDegrees(d float32) float32 {
	m := float32(math.Mod(float64(d+180), 360))
	if m < 0 {
		m += 360
	}
	return m - 180
}

// turnToward is BDS's rotation step: from plus the wrapped way to target, at most limit
// degrees either side.
func turnToward(from, target, limit float32) float32 {
	return max(-limit, min(limit, wrapDegrees(target-from))) + from
}

// lookControl is BDS's LookControl tick: the pitch and head turn toward what a goal looks
// at, or to the rotation a control asked for, else back to level and to the body 10 degrees
// a tick; the head then stays within 75 degrees of the body.
func (m *Mob) lookControl() {
	switch {
	case m.look.Has:
		m.look.Has = false
		// BDS aims from the mob's breathing attach point; mobs have no attach offsets in
		// Helix yet, so the eyes stand in for it.
		dx := m.look.Pos.X - m.body.Pos.X
		dy := m.look.Pos.Y - (m.body.Pos.Y + m.eyeHeight())
		dz := m.look.Pos.Z - m.body.Pos.Z
		h := float32(math.Sqrt(float64(dx*dx + dz*dz)))
		yaw := float32(math.Atan2(float64(dz), float64(dx)))*radToDeg32 - 90
		pitch := float32(math.Atan2(float64(dy), float64(h))) * -radToDeg32
		m.pitch = turnToward(m.pitch, pitch, m.look.PitchSpeed)
		m.headYaw = turnToward(m.headYaw, yaw, m.look.YawSpeed)
	case m.look.HasYaw:
		m.look.HasYaw = false
		m.pitch = turnToward(m.pitch, m.look.Pitch, m.look.PitchSpeed)
		m.headYaw = turnToward(m.headYaw, m.look.Yaw, m.look.YawSpeed)
	default:
		m.pitch = turnToward(m.pitch, 0, lookReset)
		m.headYaw = turnToward(m.headYaw, m.bodyYaw, lookReset)
	}
	m.headYaw = m.limitHead(m.headYaw)
}

// limitHead keeps the head within headMaxTurn of the body, as BDS's look control does.
func (m *Mob) limitHead(head float32) float32 {
	d := wrapDegrees(head - m.bodyYaw)
	switch {
	case d > headMaxTurn:
		return m.bodyYaw + headMaxTurn
	case d < -headMaxTurn:
		return m.bodyYaw - headMaxTurn
	}
	return head
}

// bodyControl is BDS's BodyControl tick on the server.
func (m *Mob) bodyControl() {
	riding := m.ride.vehicle != behavior.NoID
	turns := !riding && !m.flags.Has(state.FlagBodyRotationBlocked)
	switch {
	case m.flags.Has(state.FlagBodyRotationAxisAligned):
		m.alignBody(riding)
	case m.movingFlat() || m.flags.Has(behavior.FlagLeashed):
		if turns {
			m.bodyYaw = turnToward(m.bodyYaw, m.yaw, bodyFollowTurn)
		}
	case m.flags.Has(state.FlagBodyRotationAlwaysFollowsHead):
		if turns {
			m.bodyYaw = turnToward(m.bodyYaw, m.headYaw, bodyFollowTurn)
		}
	default:
		limit := m.stillTurnLimit()
		if turns {
			m.bodyYaw = turnToward(m.headYaw, m.bodyYaw, limit)
		}
		return
	}
	m.lastHeadYaw, m.stillTicks = m.headYaw, 0
}

// alignBody is the body control of rotation_axis_aligned: the body turns toward the
// movement direction, and while body_rotation_blocked only when it is not on a multiple of
// 90 degrees.
func (m *Mob) alignBody(riding bool) {
	if !riding {
		blocked := m.flags.Has(state.FlagBodyRotationBlocked)
		if !blocked || float32(math.Mod(float64(m.bodyYaw), 90)) != 0 {
			m.bodyYaw = turnToward(m.bodyYaw, m.yaw, bodyFollowTurn)
		}
	}
	m.lastHeadYaw, m.stillTicks = m.headYaw, 0
}

// movingFlat reports whether the mob moves sideways: BDS's server reads the horizontal
// velocity the movement left, not the change in position.
func (m *Mob) movingFlat() bool {
	v := m.body.Vel
	return v.X*v.X+v.Z*v.Z > movingSpeed2
}

// stillTurnLimit counts how long the head has kept still and gives how far the body may
// stay from it: 75 degrees, shrinking to 0 from the 11th still tick to the 20th, and 0 at
// once for a mob facing its ranged attack target.
func (m *Mob) stillTurnLimit() float32 {
	if float32(math.Abs(float64(m.headYaw-m.lastHeadYaw))) > headStillRange {
		m.lastHeadYaw, m.stillTicks = m.headYaw, 0
		return headMaxTurn
	}
	m.stillTicks++
	if m.flags.Has(behavior.FlagRangedAttacking) {
		return 0
	}
	if m.stillTicks > stillTicksBeforeTurn {
		return max(0, float32(m.stillTicks-stillTicksBeforeTurn)/-stillTicksBeforeTurn+1) * headMaxTurn
	}
	return headMaxTurn
}
