package mob

import (
	"math"

	"github.com/helix-bedrock/entity/behavior"
)

// The hooks of ram_attack (behavior.Rammer, the goat) and stalk_and_pounce_on_target
// (behavior.Pouncer, the fox). BDS 1.26.60's RamAttackGoal hurts the rammed mob through
// BDS's code with damage cause 30, and keeps a RamGoalItemDropperImpl (goats)
// whose checkForHornDropOnCollision notes whether the block past the target is one
// that breaks a horn off, tryDropHorn drops the horn when the ram ends against it,
// and dontDropHorn forgets it. BDS's code turns the mob itself, not
// its look control; BDS's code posts LevelEvent 9810 with the honey
// block's runtime ID at the mob's x and z and its box bottom.

const (
	// damageRam is the ram's damage cause.
	damageRam = 30
	// attachHead is the attach point BDS's code aims from and at.
	attachHead = 3
	// levelEventJumpPrevented is the level event of a jump a honey block stopped.
	levelEventJumpPrevented = 9810
	// degrees turns radians into degrees as BDS does.
	degrees = 57.295776
)

// HornHooks is what an engine that breaks goat horns off implements on its Hooks.
type HornHooks interface {
	// HornBlock reports that the block at p breaks a horn off a goat that rams it.
	HornBlock(m *Mob, p behavior.Vec3) bool
	// DropHorn drops one of the goat's horns at p, if it has one left.
	DropHorn(m *Mob, p behavior.Vec3)
}

// ResetLook implements behavior.Rammer: BDS's code drops the look target.
func (m *Mob) ResetLook() { m.look.Has = false }

// RamHit implements behavior.Rammer: the mob's attack damage with the ram's cause, at Apply.
// BDS's code's own swing and knockback are not run: the goal knocks the mob back
// itself.
func (m *Mob) RamHit(a behavior.Actor) {
	m.do(op{kind: opHurtBy, id: a.ID(), n: damageRam, amount: m.attackDamage})
}

// RamBlockAhead implements behavior.Rammer (checkForHornDropOnCollision).
func (m *Mob) RamBlockAhead(p behavior.Vec3) {
	h, ok := m.hooks.(HornHooks)
	m.ramHorn = ok && h.HornBlock(m, p)
}

// RamImpact implements behavior.Rammer (tryDropHorn): the horn drops at Apply.
func (m *Mob) RamImpact(p behavior.Vec3) {
	if m.ramHorn {
		m.do(op{kind: opDropHorn, at: p})
	}
}

// RamReset implements behavior.Rammer (dontDropHorn).
func (m *Mob) RamReset() { m.ramHorn = false }

// FaceActor implements behavior.Pouncer: BDS's code. It aims from the mob's head at a mob's
// head or another entity's box middle, and turns the pitch and yaw by at most pitchMax and
// yawMax. The pitch comes out negated, as BDS writes it.
func (m *Mob) FaceActor(a behavior.Actor, yawMax, pitchMax float32) {
	if a == nil || a.Removed() {
		return
	}
	p, from := a.Pos(), m.AttachPos(attachHead)
	x, z := p.X-m.body.Pos.X, p.Z-m.body.Pos.Z
	y := a.Eyes().Y
	if !a.IsMob() {
		b := a.Box()
		y = (b.Min.Y + b.Max.Y) * 0.5
	}
	yawTo := float32(math.Atan2(float64(z), float64(x)))*degrees - 90
	pitchTo := float32(math.Atan2(float64(y-from.Y), math.Sqrt(float64(x*x+z*z)))) * degrees
	dp := max(-pitchMax, min(lookWrap(pitchTo-m.pitch), pitchMax))
	dy := max(-yawMax, min(lookWrap(yawTo-m.yaw), yawMax))
	m.pitch = -(m.pitch + dp)
	m.yaw += dy
}

// lookWrap matches BDS's angle wrap: fmod(d, 360), moved into [0, 360), less 180.
func lookWrap(d float32) float32 {
	w := float32(math.Mod(float64(d+180), 360))
	if w < 0 {
		w += 360
	}
	return w - 180
}

// JumpMultiplier implements behavior.Pouncer.
func (m *Mob) JumpMultiplier() float32 { return m.body.JumpMultiplier(physWorld{&m.v}) }

// JumpPrevented implements behavior.Pouncer: the jump prevented level event over a honey
// block, at Apply.
func (m *Mob) JumpPrevented() {
	if !m.body.OnGround {
		return
	}
	p, ok := m.body.LowJumpBlock(physWorld{&m.v})
	if !ok {
		return
	}
	at := behavior.Vec3{X: m.body.Pos.X, Y: m.body.Box.Min.Y, Z: m.body.Pos.Z}
	m.LevelEventData(levelEventJumpPrevented, at, int32(m.v.block(p, 0))) //nolint:gosec // runtime IDs fit
}

// SetFallImmune implements behavior.Pouncer: CurrentlyImmuneToFallDamageComponent. Helix's
// mobs take no fall damage yet, so nothing reads it.
func (m *Mob) SetFallImmune(on bool) { m.fallImmune = on }
