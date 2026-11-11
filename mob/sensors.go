package mob

import (
	"maps"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/component"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/sensor"
)

// Sensors: the definition's timers, scheduler, environment, entity, target and damage sensors,
// run by the entity library's sensor package, and the event components (on_hurt, on_death and
// the rest) the mob fires at their moments. Their events run on the mob's own definition;
// triggers aimed at another subject wait for Trigger to reach other mobs (see host.go).

// The event components the mob fires itself.
const (
	onHurt           = "minecraft:on_hurt"
	onHurtByPlayer   = "minecraft:on_hurt_by_player"
	onDeath          = "minecraft:on_death"
	onTargetAcquired = "minecraft:on_target_acquired"
	onTargetEscape   = "minecraft:on_target_escape"
)

// readSensors builds the sensors from the definition's components. A sensor whose JSON did not
// change keeps its state, so a group change does not restart an unrelated timer.
func (m *Mob) readSensors(c component.Context, comps definition.Group) {
	if m.sensorEnv.Self == nil {
		m.sensorEnv = sensor.Env{
			World: goalWorld{&m.v}, Self: m, Events: m.inst,
			Tester: func(other behavior.Actor) definition.Tester { return m.tester(other) },
		}
	}
	s, errs := component.Sensors(c, &m.sensorEnv, m.sensors, m.sensorComps, comps)
	for _, err := range errs {
		m.rt.Log.Debug("mob sensor", "type", m.typ, "err", err)
	}
	m.sensors = s
	m.sensorComps = maps.Clone(comps)
}

// sensorTick runs the sensors once. Runs: think.
func (m *Mob) sensorTick() {
	m.sensors.Tick(&m.sensorEnv)
}

// fire runs an event component's trigger with other as the other subject.
func (m *Mob) fire(name string, other behavior.Actor) {
	m.sensors.Fire(&m.sensorEnv, name, other)
}

// damageSensed runs the damage sensor on damage of a cause from other (nil for none): the
// damage its trigger leaves, and whether it is dealt at all.
func (m *Mob) damageSensed(amount float32, cause int, other behavior.Actor) (float32, bool) {
	if m.sensors == nil || m.sensors.Damage == nil {
		return amount, true
	}
	ctx := m.tester(other)
	amount = m.sensors.Damage.Adjust(cause, amount, ctx)
	return amount, m.sensors.Damage.Damage(&m.sensorEnv, cause, amount, m.health, ctx, false) == sensor.DealsYes
}

// SetTarget changes the mob's target and fires on_target_acquired or on_target_escape.
func (m *Mob) SetTarget(id behavior.ID) {
	if id == m.target {
		return
	}
	old := m.target
	m.target = id
	// Mob::setTarget writes the target's unique ID to actor data 6 (1.21.3 names), which
	// the client's query.has_target reads: a skeleton raises its bow by it.
	m.flagsChanged()
	switch {
	case id != behavior.NoID:
		m.fire(onTargetAcquired, m.actor(id))
	case old != behavior.NoID:
		m.fire(onTargetEscape, m.actor(old))
	}
}

// actor is the entity with the ID as the mob sees it, nil when gone.
func (m *Mob) actor(id behavior.ID) behavior.Actor {
	if id == behavior.NoID || m.v.r == nil {
		return nil
	}
	return m.rt.Env.Actor(m.v.r, id)
}
