package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Wind charges, from BDS 1.26.60: the item throws wind_charge_projectile (its throwable and
// projectile components) with a 0.5 second use cooldown; the projectile's
// wind_burst_on_hit (WindBurstOnHitSubcomponent 0x71573C0) explodes it with its
// minecraft:explode component (ExplodeComponent 0x725A720) where it hits, then removes it.
const (
	nameWindCharge           = "minecraft:wind_charge"
	nameWindChargeProjectile = "minecraft:wind_charge_projectile"

	// windChargeCooldown is the item's minecraft:cooldown: category and ticks (0.5 s).
	windChargeCooldownCategory = "wind_charge"
	windChargeCooldownTicks    = 10

	// windBurstLift is how far off the hit face a burst on a block starts (0x71573C0).
	windBurstLift = 0.25

	// The particle_effect names of minecraft:explode and the level events they send.
	burstParticleWind   = "wind_burst"
	burstParticleBreeze = "breeze_wind_burst"
)

// windBurst is BDS's wind_burst_on_hit: the definition's explosion at the projectile, or
// a quarter block off the face of the block it hit, with its shooter as the attacker. The
// burst is measured from where it went off, so the shooter is pushed too (a wind charge
// jump). Runs: owner of the region.
func (t *Projectile) windBurst(r *world.Region, hit projectileHit, kind projectileHitKind) {
	at := hit.pos
	if kind == projectileHitBlock {
		at = at.Add(hit.normal.Mul(windBurstLift))
	}
	b := t.spec.Burst
	ex := &Explosion{
		Pos: at, Radius: b.Power, Kind: ExplosionWindCharge, Attacker: t.owner,
		Fire: b.CausesFire, BreaksBlocks: b.BreaksBlocks, AllowUnderwater: b.AllowUnderwater,
		MaxResistance: noResistance, DamageScale: b.DamageScale, KnockbackScale: b.KnockbackScale,
		Cause: DamageEntityExplosion, Particle: burstParticle(b.ParticleEffect), Sound: b.SoundEffect,
		ToggleBlocks: b.TogglesBlocks, NegatesFallDamage: b.NegatesFallDamage,
	}
	if ex.Sound == "" {
		ex.Sound = packet.SoundEventExplode
	}
	t.s.Explode(r, ex)
}

// burstParticle is the level event of an explode component's particle_effect.
func burstParticle(name string) int32 {
	switch name {
	case burstParticleWind:
		return packet.LevelEventParticlesWindExplosion
	case burstParticleBreeze:
		return packet.LevelEventParticlesBreezeWindExplosion
	}
	return packet.LevelEventParticlesExplosion
}

// windToggle is a wind burst reaching the block at pos (BDS's BlockType slot 132): doors,
// trapdoors, gates, levers and buttons through blockuse, lit candles and candle cakes put
// out. Bells are not rung yet. Runs: owner of the region.
func (s *Host) windToggle(r *world.Region, pos cube.Pos) {
	b, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	if blockuse.WindToggle(s.reg.Blocks, r, pos, b) {
		return
	}
	if name := s.reg.Blocks.Name(b); (isCandleBlock(name) || isCandleCake(name)) && s.fire != nil {
		s.fire.ExtinguishCandle(r, pos)
	}
}

// hitParticles is BDS's particle_on_hit (ParticleOnHitSubcomponent 0x7156E10): the
// definition's particle, once a particle, as level event 2009 at the projectile, which BDS
// has moved to where it hit by then. Runs: owner of the region.
func (t *Projectile) hitParticles(r *world.Region, pos mgl32.Vec3, kind projectileHitKind) {
	p, ok := particleType(t.spec.Particle)
	if !ok {
		return
	}
	if kind == projectileHitEntity && !t.spec.ParticleOnEntity || kind != projectileHitEntity && !t.spec.ParticleOnOther {
		return
	}
	for range t.spec.ParticleCount {
		levelEvent(r, floorPos(pos), pos, packet.LevelEventParticleGenericSpawn, p)
	}
}

// particleType is a definition's particle_type name as BDS's ParticleType. Only the names
// vanilla projectiles use with no item are known so far.
func particleType(name string) (int32, bool) {
	switch name {
	case "snowballpoof":
		return packet.ParticleTypeSnowballPoof, true
	}
	return 0, false
}

// throwableCooldown is the use cooldown of a thrown item, if it has one.
func throwableCooldown(item string) (string, int32, bool) {
	if item == nameWindCharge {
		return windChargeCooldownCategory, windChargeCooldownTicks, true
	}
	return "", 0, false
}
