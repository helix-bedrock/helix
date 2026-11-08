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

// vanillaHurt is a hurt of damage from src with no attacker parts: a Hit with damage as
// its base, taken by the TakeHit system. Returns whether the player was hurt.
func vanillaHurt(p *Player, damage float32, src DamageSource) bool {
	if damage <= 0 {
		return false
	}
	return p.s.game.sys.TakeHit(NewHit(p, damage, src))
}

// takeHealth takes damage off absorption (when absorb), then health. A hit that would kill goes to
// the totem first: BDS's health only changes for those who see the player at the end
// of its tick, so they never see it at 0 when a totem saves it.
func (p *Player) takeHealth(damage float32, src DamageSource, absorb bool) {
	if abs := p.attributes.get(attrAbsorption); abs > 0 && absorb {
		used := min(abs, damage)
		damage -= used
		p.setAttributes(attrAbsorption, abs-used)
	}
	left := p.Health() - damage
	if left <= 0 && p.s.game.sys.Totem(p, src) {
		return
	}
	p.SetHealth(left)
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

// wearArmor is BDS's armour wear (0xBFE91C0): each piece takes a quarter of the damage,
// at least one. The PartArmorWear of a hit runs it for causes armour applies to.
func (p *Player) wearArmor(damage float32) {
	n := max(int32(damage)/armorWearShare, armorWearMin)
	changed := false
	for i := range p.inventory.armor {
		if p.hurtStack(&p.inventory.armor[i], n) {
			changed = true
		}
	}
	if changed {
		p.sendArmour()
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
