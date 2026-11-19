package player

import "github.com/helix-bedrock/helix/world"

// Tipped arrows, from BDS 1.26.60's arrow_effect on hit subcomponent. An arrow's aux value
// is its potion's ID plus one; what it hits takes that potion's effects through the
// mob_effect subcomponent's rules: an eighth of each duration (at least one tick), instant
// ones at full strength.

const (
	// arrowEffectShare divides a potion effect's duration on a tipped arrow, BDS's 8.
	arrowEffectShare = 8
	// arrowAuxPotions is how many aux values name a potion: BDS reads aux 1 to 64.
	arrowAuxPotions = 64
	// mobEffectMinTicks is the shortest effect mob_effect gives: none of 20 ticks or less.
	mobEffectMinTicks = 20
)

// arrowEffects is BDS's arrow_effect on the entity an arrow hit. A shield up toward
// the arrow stops all of it unless the definition says otherwise, and a mob that cannot take
// one of the effects takes none. Harmful effects from a player's arrow pass players by while
// pvp is off, as the mob_effect subcomponent's check.
func (t *Projectile) arrowEffects(e *world.Entity, hit projectileHit) {
	if !t.spec.ArrowEffect || t.aux == 0 || t.aux > arrowAuxPotions || hit.entity == nil {
		return
	}
	pot, ok := t.s.Potion(int32(t.aux) - 1)
	if !ok || len(pot.Effects) == 0 {
		return
	}
	target, ok := effectTakerOf(hit.entity)
	if !ok {
		return
	}
	if p, isPlayer := target.(*Player); isPlayer && !t.spec.ArrowEffectOnBlocking {
		at := e.Position()
		if p.blocksFrom(causeProjectile, &at) {
			return
		}
	}
	if o, isMob := target.(*Mob); isMob {
		for _, eff := range pot.Effects {
			if !o.canBeAffected(eff.ID) {
				return
			}
		}
	}
	t.arrowEffectsOn(target, pot.Effects)
}

// arrowEffectsOn gives target the arrow's effects: instant ones at full strength, the others
// for an eighth of their time, dropped when too short.
func (t *Projectile) arrowEffectsOn(target effectTaker, effects []ActiveEffect) {
	g := t.s.game
	_, player := target.(*Player)
	for _, eff := range effects {
		def, known := g.effects[eff.ID]
		if !known {
			continue
		}
		if player && def.Harmful && t.owner != nil && !t.s.level.PVP {
			continue
		}
		if def.Instant {
			target.takeInstant(eff.ID, eff.Amplifier, 1)
			continue
		}
		ticks := eff.Duration
		if ticks != EffectInfinite {
			ticks = max(ticks/arrowEffectShare, 1)
			if ticks <= mobEffectMinTicks {
				continue
			}
		}
		target.takeEffect(ActiveEffect{ID: eff.ID, Amplifier: eff.Amplifier, Duration: ticks, Ambient: eff.Ambient, Hidden: eff.Hidden})
	}
}
