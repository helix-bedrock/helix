package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/helix/world"
)

// The warden goals' hooks (entity's behavior.Warden, Digger and SonicBoomer): the
// anger_level component (entity's state.AngerLevel, BDS 1.26.60 AngerLevelComponent), the
// suspect_tracking location, and the sonic boom's hit and push, which run at Apply through
// WardenHooks. The vibration_listener that also raises anger is not hooked yet
// (HeardVibration answers none).

// attrKnockbackResistance is the attribute component BDS's code scales by.
const attrKnockbackResistance = "minecraft:knockback_resistance"

// WardenHooks is what an engine that runs the sonic boom implements on its Hooks. Both run
// in the mob's Apply.
type WardenHooks interface {
	// SonicHit hurts the entity with the sonic boom cause (31) and the mob as attacker.
	SonicHit(m *Mob, target behavior.ID, damage float32)
	// Knockback is BDS 1.26.60's BDS's code on the entity with the mob as
	// source.
	Knockback(m *Mob, target behavior.ID, k behavior.Knockback)
}

// WardenEnv is what an engine that shows the sonic boom implements on the Env.
type WardenEnv interface {
	// LevelEventXYZ broadcasts LevelEventGeneric id with x, y and z float tags.
	LevelEventXYZ(r *world.Region, id int32, p behavior.Vec3)
}

// suspicion is SuspectTrackingComponent: a block position and the tick it was set, each
// optional (BDS 1.26.60 writes both).
type suspicion struct {
	pos     behavior.Pos
	tick    uint64
	hasPos  bool
	hasTick bool
}

// anger is the mob's anger_level component, nil without one.
func (m *Mob) anger() *state.AngerLevel {
	if m.states == nil {
		return nil
	}
	return m.states.AngerLevel
}

// Suspects appends the anger_level suspects in the component's set order.
func (m *Mob) Suspects(dst []behavior.ID) []behavior.ID {
	if a := m.anger(); a != nil {
		return a.Suspects(dst)
	}
	return dst
}

// ValidSuspect is the anger_level suspect test; true without the component.
func (m *Mob) ValidSuspect(a behavior.Actor) bool {
	if g := m.anger(); g != nil {
		return g.Valid(&m.stateEnv, a)
	}
	return true
}

// Calm reports the entity is no suspect or below angry_threshold.
func (m *Mob) Calm(id behavior.ID) bool {
	if a := m.anger(); a != nil {
		return a.Calm(id)
	}
	return true
}

// AddAnger raises the anger at a by default_annoyingness or angry_boost.
func (m *Mob) AddAnger(a behavior.Actor, by behavior.Anger, sound bool) {
	g := m.anger()
	if g == nil {
		return
	}
	c := g.Config()
	amount := c.DefaultAnnoyingness
	if by == behavior.AngerBoost {
		amount = c.AngryBoost
	}
	g.Increase(&m.stateEnv, a, amount, sound)
}

// hasSuspectTracking reports minecraft:suspect_tracking.
func (m *Mob) hasSuspectTracking() bool {
	return m.states != nil && m.states.Markers[markerSuspectTracking]
}

// markerSuspectTracking is minecraft:suspect_tracking.
const markerSuspectTracking = "minecraft:suspect_tracking"

// Suspicion is the suspect_tracking location.
func (m *Mob) Suspicion() (behavior.Pos, bool) {
	if !m.hasSuspectTracking() || !m.suspect.hasPos {
		return behavior.Pos{}, false
	}
	return m.suspect.pos, true
}

// SuspicionTick is the tick the location was last set.
func (m *Mob) SuspicionTick() (uint64, bool) {
	if !m.hasSuspectTracking() || !m.suspect.hasTick {
		return 0, false
	}
	return m.suspect.tick, true
}

// SetSuspicion sets the location and its tick.
func (m *Mob) SetSuspicion(p behavior.Pos, tick uint64) {
	if m.hasSuspectTracking() {
		m.suspect = suspicion{pos: p, tick: tick, hasPos: true, hasTick: true}
	}
}

// ClearSuspicion unsets the location and keeps its tick.
func (m *Mob) ClearSuspicion() { m.suspect.hasPos = false }

// Named reports a name tag.
func (m *Mob) Named() bool { return m.nameTag != "" }

// InDaylight stands for BDS's code: day with the sky above
// the eyes. BDS's brightness test (above 0.5 after the rain's darkening) is not ported.
func (m *Mob) InDaylight() bool { return stateMob{m}.CanSeeDaylight() }

// SonicHit hurts the entity with the sonic boom cause at Apply.
func (m *Mob) SonicHit(a behavior.Actor, damage float32) {
	m.do(op{kind: opSonicHit, id: a.ID(), amount: damage})
}

// Knockback pushes the entity at Apply.
func (m *Mob) Knockback(t behavior.Actor, k behavior.Knockback) {
	m.do(op{kind: opKnockback, id: t.ID(), knock: k})
}

// SonicCooldown is the warden's shared sonic boom cooldown.
func (m *Mob) SonicCooldown() (uint64, bool) { return m.sonicCooldown, m.hasSonicCooldown }

// SetSonicCooldown sets it.
func (m *Mob) SetSonicCooldown(until uint64) { m.sonicCooldown, m.hasSonicCooldown = until, true }

// LevelEventXYZ broadcasts the level event with x, y and z tags at Apply.
func (m *Mob) LevelEventXYZ(id int32, p behavior.Vec3) {
	m.do(op{kind: opLevelEventXYZ, n: id, at: p})
}

// KnockbackResistance is the knockback_resistance attribute's value, 0 without one.
func (m *Mob) KnockbackResistance() float32 {
	if m.states == nil {
		return 0
	}
	return m.states.Attributes[attrKnockbackResistance].Value.Min
}

// runWardenOp runs a sonic boom op at Apply.
func (m *Mob) runWardenOp(o *op) {
	switch o.kind {
	case opSonicHit:
		if h, ok := m.hooks.(WardenHooks); ok && !m.Dead() {
			h.SonicHit(m, o.id, o.amount)
		}
	case opKnockback:
		if h, ok := m.hooks.(WardenHooks); ok {
			h.Knockback(m, o.id, o.knock)
		}
	case opLevelEventXYZ:
		if e, ok := m.rt.Env.(WardenEnv); ok {
			e.LevelEventXYZ(m.v.r, o.n, o.at)
		}
	}
}

// AngerNuisances are the anger_level suspects for saving; ok is false without the component.
func (m *Mob) AngerNuisances() ([]state.Nuisance, bool) {
	if a := m.anger(); a != nil {
		return a.Nuisances(), true
	}
	return nil, false
}
