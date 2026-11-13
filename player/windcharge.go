package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
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
// trapdoors, gates, levers and buttons through blockuse (powered doors, trapdoors and
// gates left alone), bells rung, lit candles and candle cakes put out. Runs: owner of the
// region.
func (s *Host) windToggle(r *world.Region, pos cube.Pos) {
	b, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	powered := func(at cube.Pos) bool {
		signal := s.circuits.Signal
		return signal != nil && signal(r, at) > 0
	}
	if blockuse.WindToggle(s.reg.Blocks, r, pos, b, powered) {
		return
	}
	if ring := s.circuits.WindBell; ring != nil && ring(r, pos, b) {
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

// reflectTicksPerSecond turns reflect_immunity into ticks, as BDS's floor(seconds * 20).
const reflectTicksPerSecond = 20

// useOnProjectile is a player hitting a projectile, BDS's ProjectileComponent hurt
// (0x7146660): one whose definition reflects on hurt and that has flown at least its
// reflect immunity turns to fly along the attacker's view at speed 1, with the attacker as
// its owner. True if the target was a projectile.
func (p *Player) useOnProjectile(d *protocol.UseItemOnEntityTransactionData) bool {
	t := p.projectileNear(d.TargetEntityRuntimeID)
	if t == nil {
		return false
	}
	if d.ActionType != protocol.UseItemOnEntityActionAttack || !t.spec.ReflectOnHurt || t.age < t.spec.ReflectTicks {
		return true
	}
	if p.entity.Position().Sub(t.entity.Position()).Len() > attackRange {
		return true
	}
	t.vel = lookDirection(p.entity.Rotation())
	t.owner, t.separated = p, false
	t.planned = false
	return true
}

// projectileNear finds a projectile near the player by its runtime ID, in its region.
func (p *Player) projectileNear(id uint64) *Projectile {
	r := p.entity.Region()
	if r == nil {
		return nil
	}
	pos := p.entity.Position()
	p.touching = r.AppendEntitiesIn(p.touching[:0], pos[0]-attackScan, pos[2]-attackScan, pos[0]+attackScan, pos[2]+attackScan)
	defer clear(p.touching)
	for _, e := range p.touching {
		if e.RuntimeID() != id {
			continue
		}
		if t, ok := e.Owner().(*Projectile); ok && !t.gone && !t.stuck {
			return t
		}
	}
	return nil
}
