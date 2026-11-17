package mob

import "github.com/helix-bedrock/entity/behavior"

// guardian_attack's hooks (behavior.GuardianAttacker). BDS 1.26.60's elder guardian factory
// (0xBC8CB10) makes a Guardian and turns on actor flag 33 (elder); Guardian's save writes it
// as Elder (0xBD400D0) and its load reads it back (0xBD401E0).

// typeElderGuardian is the type BDS's factory makes elder.
const typeElderGuardian = "minecraft:elder_guardian"

// GuardianHooks is what an engine that runs guardian_attack implements on its Hooks.
type GuardianHooks interface {
	// HurtBy hurts the entity with the cause from the mob, its hurt time skipped when
	// ignoreCooldown is set. It runs in the mob's Apply.
	HurtBy(m *Mob, target behavior.ID, cause int, damage float32, ignoreCooldown bool)
}

// markElder sets the elder flag on a mob BDS's factory makes elder.
func (m *Mob) markElder() {
	if m.typ == typeElderGuardian {
		m.flags.Set(behavior.FlagElder, true)
	}
}

// AttackDamage implements behavior.GuardianAttacker: the attack component's damage range.
func (m *Mob) AttackDamage() (lo, hi float32, ok bool) {
	return m.attackMin, m.attackDamage, m.hasAttack
}

// MeleeDamage is the damage a melee hit deals: the top of the attack range.
func (m *Mob) MeleeDamage() float32 { return m.attackDamage }

// HurtBy implements behavior.GuardianAttacker: the hit lands at Apply.
func (m *Mob) HurtBy(a behavior.Actor, cause int, damage float32, ignoreCooldown bool) {
	var skip behavior.ID
	if ignoreCooldown {
		skip = 1
	}
	m.do(op{kind: opHurtBy, id: a.ID(), n: int32(cause), amount: damage, other: skip}) //nolint:gosec // causes are small
}

// EntityEvent implements behavior.GuardianAttacker.
func (m *Mob) EntityEvent(id int) { m.do(op{kind: opActorEvent, n: int32(id)}) } //nolint:gosec // event IDs are small

// ResetAttack implements behavior.GuardianAttacker. Helix keeps no attack timer of its own;
// the beam the client draws follows the target, which the goal clears.
func (m *Mob) ResetAttack() {}
