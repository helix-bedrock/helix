package mob

import "github.com/helix-bedrock/entity/behavior"

// The hooks of eat_mob (behavior.MobEater, the frog) and drop_item_for (behavior.Gifter, the
// cat). BDS 1.26.60's DropItemForGoal::tick (0x5524130) rolls the loot table with the level's
// Random and the mob as the looting entity and spawns each item at the mob's position with
// the goal's pickup delay.

// LootHooks is what an engine that drops loot for goals implements on its Hooks.
type LootHooks interface {
	// DropLoot rolls the loot table for the mob and spawns what it gives at at. killed is
	// the entity the mob killed for it (entity_killed), NoID for none; pickupDelay is the
	// ticks before the items can be picked up, below 0 for a death drop's.
	DropLoot(m *Mob, table string, at behavior.Vec3, killed behavior.ID, pickupDelay int32)
}

// ClockEnv is what an engine with a day clock implements on its Env.
type ClockEnv interface {
	// TimeOfDay is BDS's Dimension::getTimeOfDay, 0 to 1 with 0 at noon.
	TimeOfDay() float32
}

// TimeOfDay implements behavior.Gifter; 0 without a clock.
func (m *Mob) TimeOfDay() float32 {
	if e, ok := m.rt.Env.(ClockEnv); ok {
		return e.TimeOfDay()
	}
	return 0
}

// DropGift implements behavior.Gifter. BDS then posts a script event, which Helix has not.
func (m *Mob) DropGift(table string, pickupDelay int32, _ behavior.ID) {
	m.do(op{kind: opDropLoot, name: table, at: m.Pos(), id: behavior.NoID, n: pickupDelay})
}

// DropLoot implements behavior.MobEater: the prey's loot drops at p, at Apply.
func (m *Mob) DropLoot(table string, p behavior.Vec3, prey behavior.ID) {
	m.do(op{kind: opDropLoot, name: table, at: p, id: prey, n: -1})
}

// SetVelocityOf implements behavior.MobEater: the other mob's motion is set at Apply.
func (m *Mob) SetVelocityOf(id behavior.ID, v behavior.Vec3) {
	m.do(op{kind: opSetVelocityOf, id: id, at: v})
}

// RemoveActor implements behavior.MobEater: the other mob despawns at Apply and leaves on
// its next tick.
func (m *Mob) RemoveActor(id behavior.ID) { m.do(op{kind: opRemoveActor, id: id}) }

// runOtherOp runs an op that changes another mob, or drops loot, at Apply.
func (m *Mob) runOtherOp(o *op) {
	r, env := m.v.r, m.rt.Env
	switch o.kind {
	case opDropLoot:
		if h, ok := m.hooks.(LootHooks); ok {
			h.DropLoot(m, o.name, o.at, o.id, o.n)
		}
		return
	case opSpawnProjectile:
		if h, ok := m.hooks.(ProjectileHooks); ok && !m.Dead() {
			h.ShootAlong(m, o.name, o.at, o.dir)
		}
		return
	case opLevelEventData:
		env.LevelEventData(r, o.n, o.at, int32(o.other)) //nolint:gosec // carried from an int32
		return
	case opDropHorn:
		if h, ok := m.hooks.(HornHooks); ok {
			h.DropHorn(m, o.at)
		}
		return
	}
	t := env.Mob(r, o.id)
	if t == nil {
		return
	}
	switch o.kind {
	case opSetVelocityOf:
		t.Knock(o.at)
	case opRemoveActor:
		t.despawned = true
	case opMakeJump:
		t.Jump()
	case opSetMingle:
		t.mingle, t.hasMingle = o.mingle, true
	}
	t.snapshot()
}
