package mob

import (
	"math"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/state"
)

// Steering a ridden mob with an item, as BDS 1.26.60: controlled_by_player (entity's goal)
// turns and moves a pig or strider where its rider looks while the rider holds one of
// item_controllable's control items, and minecraft:boostable speeds it up for a while when
// the rider uses one of its boost items (a carrot or warped fungus on a stick).

// The mob runs controlled_by_player.
var _ behavior.PlayerControlled = (*Mob)(nil)

// rideAirFriction is the friction controlled_by_player takes for an air block (BDS 1.26.60
// qword).
const rideAirFriction = 0.6

// The stairs states WalkThrough reads.
const (
	stateStairsDirection = "weirdo_direction"
	stateUpsideDown      = "upside_down_bit"
)

// The facings BDS 1.26.60 compares: the side of the stairs the mob is on, and a
// stairs' facing, 5 minus its weirdo_direction.
const (
	facingNorth      = 2
	facingSouth      = 3
	facingWest       = 4
	facingEast       = 5
	stairsDirections = 4
)

// boostModifier is minecraft:boostable's movement modifier, "Boosting speed boost", on the
// base value; its UUID is made at BDS's start, so Helix names it.
var boostModifier = attrModifier{id: "helix:boosting_speed_boost", op: modMultiplyBase}

// boosting is BoostableComponent's state: on while boosted, the ticks so far and in all.
type boosting struct {
	on          bool
	time, total int32
}

// ControlItems implements behavior.PlayerControlled: item_controllable's control_items.
func (m *Mob) ControlItems() ([]string, bool) {
	if m.states == nil || m.states.ItemControllable == nil {
		return nil, false
	}
	return m.states.ItemControllable.ControlItems, true
}

// YawOf implements behavior.PlayerControlled: another actor's yaw.
func (m *Mob) YawOf(id behavior.ID) float32 {
	if m.v.r == nil {
		return 0
	}
	a := m.rt.Env.Actor(m.v.r, id)
	if a == nil {
		return 0
	}
	if y, ok := a.(interface{ Yaw() float32 }); ok {
		return y.Yaw()
	}
	_, yaw := a.HeadRotation()
	return yaw
}

// SetSpeed implements behavior.PlayerControlled: the mob's speed setting, which the travel
// reads.
func (m *Mob) SetSpeed(s float32) { m.speedSetting = s }

// ResetMovement implements behavior.PlayerControlled: the movement attribute is worked out
// again from its base value and modifiers.
func (m *Mob) ResetMovement() { m.applySpeed() }

// RideFriction implements behavior.PlayerControlled: 0.6 for air, else the block's
// friction.
func (m *Mob) RideFriction(p behavior.Pos) float32 {
	b := physWorld{&m.v}.Block(p)
	if b.Air {
		return rideAirFriction
	}
	return b.Friction
}

// MaxAutoStep implements behavior.PlayerControlled: every Helix mob has an auto step.
func (m *Mob) MaxAutoStep() (float32, bool) { return m.body.MaxAutoStep, true }

// MoveRelativeSpeed implements behavior.PlayerControlled.
func (m *Mob) MoveRelativeSpeed() float32 { return m.body.RelativeSpeed(physWorld{&m.v}) }

// WalkThrough implements behavior.PlayerControlled: the block at p
// is stairs, not upside down, that the mob comes at from its low side, so it walks up
// without jumping.
func (m *Mob) WalkThrough(p behavior.Pos) bool {
	st := m.rt.Blocks.reg.States(m.v.block(path.Pos(p), 0))
	dir, ok := st[stateStairsDirection].(int32)
	if !ok || stateSet(st[stateUpsideDown]) || dir < 0 || dir >= stairsDirections {
		return false
	}
	dx := m.body.Pos.X - (float32(p.X) + 0.5)
	dz := m.body.Pos.Z - (float32(p.Z) + 0.5)
	side := facingSouth
	switch {
	case math.Abs(float64(dx)) > math.Abs(float64(dz)) && dx > 0:
		side = facingWest
	case math.Abs(float64(dx)) > math.Abs(float64(dz)):
		side = facingEast
	case dz > 0:
		side = facingNorth
	}
	return facingEast-int(dir) == side
}

// stateSet reports a true bool block state, whichever type the registry keeps it as.
func stateSet(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case uint8:
		return b != 0
	case int32:
		return b != 0
	}
	return false
}

// Boost is a rider using it on the mob, as BDS 1.26.60's boost item use: a mob
// with minecraft:boostable that is not boosting yet, and one of its boost_items matching,
// starts boosting: the boost sound, and the speed modifier until the duration
// runs out. It returns the boost item, whose damage and replace_item the engine applies to
// the used stack. Runs: the region's goroutine, outside the think phase.
func (m *Mob) Boost(it behavior.Item) (state.BoostItem, bool) {
	if m.states == nil || m.states.Boostable == nil || m.boost.on || m.Dead() {
		return state.BoostItem{}, false
	}
	b := m.states.Boostable
	for _, bi := range b.Items {
		if !m.ItemMatches(bi.Item, it) {
			continue
		}
		m.boost = boosting{on: true, total: b.Duration}
		m.PlaySound(soundBoost)
		mod := boostModifier
		mod.amount = b.SpeedMultiplier
		m.addSpeedModifier(mod)
		return bi, true
	}
	return state.BoostItem{}, false
}

// soundBoost is LevelSoundEvent 20, "boost".
const soundBoost = 20

// boostTick matches BDS: once the boost has run its
// duration it ends and the modifier goes; the mob sprints while it lasts.
func (m *Mob) boostTick() {
	if !m.boost.on {
		return
	}
	t := m.boost.time
	if t >= m.boost.total {
		m.boost.on = false
		m.removeSpeedModifier(boostModifier.id)
	}
	m.boost.time = t + 1
	m.SetSprinting(t < m.boost.total)
}
