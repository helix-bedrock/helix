package player

import (
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Numbers of BDS 1.26.60's damage.
const (
	// hurtTicks is how long a hurt lasts: only what is above it hurts until then.
	hurtTicks = 10
	// Armour: what toughness is worth, the shares it gives, and its limit.
	toughnessShare  = 0.25
	toughnessBase   = 2
	armorPerPoint   = 25
	armorMinShare   = 0.2
	armorFullPoints = 20
	armorMaxShare   = 0.8
	// helmetFalling is what a helmet leaves of damage from a falling block.
	helmetFalling = 0.75
	// Armour enchantments: counted until maxProtectionCount, used up to
	// protectionLimit, each point worth protectionPoint.
	maxProtectionCount = 26
	protectionLimit    = 20
	protectionPoint    = 0.04
	// resistancePerLevel and resistanceFullLevel are Resistance.
	resistancePerLevel  = 0.2
	resistanceFullLevel = 3
	// armorWearShare and armorWearMin are what a hurt wears each piece by.
	armorWearShare = 4
	armorWearMin   = 1
)

// Bit sets of damage causes, as BDS's damage sources test them.
const (
	causesNoArmor     = 0x8821F291
	causesFallingHit  = 0x100A0000
	causesResisted    = 0xCFFE
	causesFire        = 0x4001C0
	causesNoProtect   = 0x13000
	causeMagicLimit   = 32
	causeFalling      = 29
	causeEnchantsSkip = 31
	causeResistedFrom = 17
	causeFallingLimit = 29
	causeFireLimit    = 23
	causeProtectLimit = 17
	causeBlockBlast   = 10
	causeEntityBlast  = 11
	causeProjectile   = 3
)

// The kinds of armour enchantment, with what each multiplies its level by.
const (
	protectAll = iota
	protectFire
	protectFall
	protectBlast
	protectProjectile
)

const (
	protectFireScale = 2
	protectFallScale = 3
	protectHitScale  = 2
)

// Vanilla enchantment and effect IDs damage reads (BDS 1.26.60).
const (
	EnchantProtection           int16 = 0
	EnchantFireProtection       int16 = 1
	EnchantFeatherFalling       int16 = 2
	EnchantBlastProtection      int16 = 3
	EnchantProjectileProtection int16 = 4

	EffectResistance int32 = 11
)

// hurtState is what is left of the player's last hurt.
type hurtState struct {
	// ticks count down to 0; last is the damage dealt while they run.
	ticks int32
	last  float32
}

// PlayerHurtEvent fires before a player takes damage. Damage may be changed; Cancel
// stops the hurt.
type PlayerHurtEvent struct {
	Cancellable
	Player *Player
	Damage float32
	Source DamageSource
}

// OnPlayerHurt listens to players being hurt.
func OnPlayerHurt(s *Setup, fn func(e *PlayerHurtEvent), p Priority) error {
	return addListener(s, &s.h.game.hurtEv, fn, p)
}

// Health returns the player's health. Runs: the player's region.
func (p *Player) Health() float32 { return p.attributes.get(attrHealth) }

// SetHealth sets the player's health, clamped to 0 to TuneMaxHealth, and tells
// the player. Runs: the player's region.
func (p *Player) SetHealth(v float32) { p.setAttributes(attrHealth, v) }

// invulnerable reports whether nothing but the void hurts the player:
// creative and spectator, as BDS's game modes have it.
func (p *Player) invulnerable() bool {
	mode := p.gameMode
	if mode == GameModeDefault {
		mode = p.s.level.GameMode
	}
	return mode == GameModeCreative || mode == GameModeSpectator
}

// tickHurt counts the player's hurt down. Runs: the player's region, once a tick.
func (p *Player) tickHurt() {
	if p.hurt.ticks > 0 {
		p.hurt.ticks--
	}
}

// vanillaHurt is BDS's hurt through the health attribute: the event, what armour and
// effects leave, the hurt time letting only what is above the last hurt through,
// absorption first and health for the rest, then the hurt effects (hurt time, knockback,
// the hurt event), armour wear and the damage exhaustion. At no health left the Die
// system runs. Shields, totems and damage sensors are not built yet. Returns whether
// the player was hurt.
func vanillaHurt(p *Player, damage float32, src DamageSource) bool {
	if damage <= 0 || p.dead() || p.invulnerable() && src.Cause != DamageVoid {
		return false
	}
	g := p.s.game
	if g.hurtEv.Has() {
		e := PlayerHurtEvent{Player: p, Damage: damage, Source: src}
		g.hurtEv.fire(p.s.log, &e)
		if e.Cancelled() || e.Damage <= 0 {
			return false
		}
		damage = e.Damage
	}
	damage *= p.Tuning(TuneDamageTakenScale)
	if damage <= 0 {
		return false
	}
	raw := damage
	damage = g.sys.ReduceDamage(p, damage, src)
	if p.hurt.ticks > 0 && src.Cause != DamageNone && src.Cause != DamageSelfDestruct {
		if damage <= p.hurt.last {
			return false
		}
		damage -= p.hurt.last
	}
	p.takeHealth(damage, src.Cause)
	if p.hurt.ticks > 0 {
		p.hurt.last += damage
	} else {
		p.hurt.last = damage
	}
	p.hurt.ticks = int32(p.Tuning(TuneHurtTicks))
	if src.Knockback && src.Attacker != nil {
		g.sys.Knockback(p, src.Attacker, src.Extra)
	}
	if r := p.entity.Region(); r != nil {
		r.Broadcast(p.blockPos(), &packet.ActorEvent{EntityRuntimeID: p.runtimeID, EventType: packet.ActorEventHurt}, nil)
	}
	p.wearArmor(raw, src.Cause)
	g.sys.Exhaust(p, p.Tuning(TuneExhaustDamage))
	if p.dead() {
		g.sys.Die(p, src)
	}
	p.flush()
	return true
}

// takeHealth takes damage off absorption, then health.
func (p *Player) takeHealth(damage float32, cause DamageCause) {
	if abs := p.attributes.get(attrAbsorption); abs > 0 && cause != DamageSelfDestruct {
		used := min(abs, damage)
		damage -= used
		p.setAttributes(attrAbsorption, abs-used)
	}
	p.SetHealth(p.Health() - damage)
}

// vanillaReduceDamage is BDS's damage after armour, then its enchantments, then each
// effect's Resist for causes it applies to.
func vanillaReduceDamage(p *Player, damage float32, src DamageSource) float32 {
	damage = p.afterArmor(damage, src.Cause)
	damage = p.afterProtection(damage, src.Cause)
	if resisted(src.Cause) {
		for _, a := range p.effects {
			if e, ok := p.s.game.effects[a.ID]; ok && e.Resist != nil {
				damage = e.Resist(a.Amplifier, damage)
			}
		}
	}
	return damage
}

// resistanceDamage is BDS's Resistance: a fifth off per level, none left past level 4.
func resistanceDamage(amplifier int32, damage float32) float32 {
	if amplifier > resistanceFullLevel {
		return 0
	}
	return damage * (float32(amplifier+1)*-resistancePerLevel + 1)
}

// armorBypassed reports whether armour does nothing against cause.
func armorBypassed(cause DamageCause) bool {
	return cause >= 0 && cause < causeMagicLimit && causesNoArmor>>cause&1 != 0 || cause == DamageFall || cause == causeFalling
}

// resisted reports whether effects apply to cause.
func resisted(cause DamageCause) bool {
	return cause >= causeResistedFrom || cause >= 0 && causesResisted>>cause&1 != 0
}

// afterArmor is BDS's armour: a helmet takes a quarter off falling blocks; armour and
// toughness then take a share.
func (p *Player) afterArmor(damage float32, cause DamageCause) float32 {
	if armorBypassed(cause) {
		return damage
	}
	inv := &p.inventory
	if !inv.armor[0].Empty() && cause >= 0 && cause < causeFallingLimit && causesFallingHit>>cause&1 != 0 {
		damage *= helmetFalling
	}
	var armor, tough float32
	for _, s := range inv.armor {
		if s.Empty() {
			continue
		}
		if v, ok := p.s.game.combat.armor[s.Name]; ok {
			armor += float32(v.Protection)
			tough += float32(v.Toughness)
		}
	}
	armor = max(armor+p.Tuning(TuneArmor), 0)
	tough = max(tough+p.Tuning(TuneToughness), 0)
	v := damage/(tough*toughnessShare+toughnessBase) + armor
	share := p.Tuning(TuneArmorMaxShare)
	if v <= armorFullPoints {
		share = max(v, armor*armorMinShare) / armorPerPoint
	}
	return damage * (1 - share)
}

// afterProtection is BDS's armour enchantments: each piece's points count, up to a
// limit, each taking a share off.
func (p *Player) afterProtection(damage float32, cause DamageCause) float32 {
	if cause == causeEnchantsSkip {
		return damage
	}
	var points int32
	for _, s := range p.inventory.armor {
		if s.Empty() || points >= maxProtectionCount {
			continue
		}
		enchantLevels(s, func(id, level int16) {
			if e, ok := p.s.game.enchants[id]; ok && e.Protection != nil && points < maxProtectionCount {
				points += e.Protection(level, cause)
			}
		})
	}
	counted := max(min(float32(points)+p.Tuning(TuneProtection), p.Tuning(TuneProtectionLimit)), 0)
	return damage * (counted*-p.Tuning(TuneProtectionPoint) + 1)
}

// protectionKind is BDS's armour enchantment of one kind: its level, times what the
// kind gives against cause. The void, self destruction and starving get through all.
func protectionKind(kind int) func(level int16, cause DamageCause) int32 {
	return func(level int16, cause DamageCause) int32 {
		if cause >= 0 && cause < causeProtectLimit && causesNoProtect>>cause&1 != 0 {
			return 0
		}
		scale := int32(0)
		switch kind {
		case protectAll:
			scale = 1
		case protectFire:
			if cause >= 0 && cause < causeFireLimit && causesFire>>cause&1 != 0 {
				scale = protectFireScale
			}
		case protectFall:
			if cause == DamageFall || cause == causeFalling {
				scale = protectFallScale
			}
		case protectBlast:
			if cause == causeBlockBlast || cause == causeEntityBlast {
				scale = protectHitScale
			}
		case protectProjectile:
			if cause == causeProjectile {
				scale = protectHitScale
			}
		}
		return int32(level) * scale
	}
}

// wearArmor is BDS's armour wear: each piece takes a quarter of the damage, at least
// one, for causes armour applies to.
func (p *Player) wearArmor(damage float32, cause DamageCause) {
	if armorBypassed(cause) {
		return
	}
	n := max(int32(damage)/armorWearShare, armorWearMin)
	changed := false
	for i := range p.inventory.armor {
		if p.hurtStack(&p.inventory.armor[i], n) {
			changed = true
		}
	}
	if changed {
		p.send(p.inventory.armorPacket(p.s.reg))
		p.showArmor()
	}
}

// setAttributes sets attributes by index and value pairs and sends the
// player the ones that changed in one UpdateAttributes.
func (p *Player) setAttributes(kv ...any) {
	var changed []protocol.Attribute
	health := false
	for i := 0; i+1 < len(kv); i += 2 {
		idx, ok := kv[i].(int)
		v, okv := kv[i+1].(float32)
		if ok && okv && p.attributes.set(idx, v) {
			changed = append(changed, p.attributes.entry(idx))
			health = health || idx == attrHealth
		}
	}
	if len(changed) > 0 {
		p.send(&packet.UpdateAttributes{EntityRuntimeID: p.runtimeID, Attributes: changed, Tick: p.entity.Tick()})
	}
	if health {
		p.showHealth()
	}
}

// showHealth tells those who see the player its health. Their clients keep a player
// that died as dead until they hear otherwise, and do not let it be hit. BDS sends
// every changed attribute to those who see the actor; only health goes so far, as
// exhaustion changes with every step.
func (p *Player) showHealth() {
	p.entity.Send(&packet.UpdateAttributes{EntityRuntimeID: p.runtimeID, Attributes: []protocol.Attribute{p.attributes.entry(attrHealth)}, Tick: p.entity.Tick()})
}
