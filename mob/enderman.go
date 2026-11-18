package mob

import "github.com/helix-bedrock/entity/behavior"

// The enderman: BDS 1.26.60 writes it as its own class (EnderMan). On top of its
// components:
//
//   - newServerAiStep hurts it by 1 drowning damage every tick it is in water or
//     rain, and lets go of a target a player made it angry at once that player is dead;
//   - _hurt teleports it at random 9 times in 10 when a hit would not kill it,
//     before the hit lands (even one the hurt time then ignores), and marks a player's hit;
//   - hurtEffects, which BDS's code calls once the damage is dealt, teleports it
//     at random again when it lived through the hit.
//
// The random teleports are minecraft:teleport's (BDS's code),
// so a riding enderman, which has no teleport component, stays.
const (
	endermanType       = "minecraft:enderman"
	damageDrowning     = 9
	endermanWaterHurt  = 1
	endermanHurtChance = 10
)

// endermanTick is the enderman's newServerAiStep. Runs: think.
func (m *Mob) endermanTick() {
	if m.typ != endermanType {
		return
	}
	if m.aggroedByPlayer {
		if t := m.actor(m.target); t != nil && !t.Alive() {
			m.SetTarget(behavior.NoID)
			m.aggroedByPlayer = false
		}
	}
	if m.Wet() {
		m.do(op{kind: opHurt, n: damageDrowning, amount: endermanWaterHurt})
	}
	if m.actor(m.target) == nil {
		m.aggroedByPlayer = false
	}
}

// endermanHurt matches BDS before the damage: a player's hit marks it, and 9 times in
// 10 a hit it would live through teleports it. Runs: whoever hurts the mob.
func (m *Mob) endermanHurt(amount float32, attacker behavior.Actor) {
	if m.typ != endermanType {
		return
	}
	if attacker != nil && attacker.IsPlayer() {
		m.aggroedByPlayer = true
	}
	if m.stateEnv.World == nil || m.stateEnv.World.Random().NextInt(endermanHurtChance) == 0 {
		return
	}
	if float32(m.Health()) > amount {
		m.randomTeleport()
	}
}

// endermanHurtEffects matches BDS after damage was dealt: it teleports when it
// lived through the hit. Runs: whoever hurts the mob.
func (m *Mob) endermanHurtEffects(dealt float32) {
	if m.typ == endermanType && dealt > 0 && float32(m.Health()) > dealt {
		m.randomTeleport()
	}
}

// randomTeleport is minecraft:teleport's random teleport, when the mob has the component.
func (m *Mob) randomTeleport() {
	if m.states == nil || m.states.Teleport == nil || m.stateEnv.Self == nil {
		return
	}
	m.states.Teleport.Random(&m.stateEnv, stateActions{m})
}

// projectileTeleportTries is how many random spots TeleportComponent's projectile dodge tries.
const projectileTeleportTries = 64

// DodgeProjectile is TeleportComponent's dodge in BDS's code ():
// a mob whose minecraft:teleport has teleports_on_projectile_hit tries up to 64
// random teleports, and when one works the projectile's hit does nothing. The component's
// filter at +8 is not read (vanilla writes none). Runs: the projectile's tick, owner of the
// region.
func (m *Mob) DodgeProjectile() bool {
	if m.Dead() || m.states == nil || m.states.Teleport == nil || m.stateEnv.Self == nil {
		return false
	}
	t := m.states.Teleport
	if !t.Config().TeleportsOnProjectileHit {
		return false
	}
	for range projectileTeleportTries {
		if t.Random(&m.stateEnv, stateActions{m}) {
			return true
		}
	}
	return false
}
