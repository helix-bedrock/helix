package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// BDS 1.26.60's effects, from MobEffect::initEffects (0xCAB20B0) and the attribute
// modifiers, buffs and amplifiers its static init makes (0xBC53980). Every effect BDS
// has is registered, with its ID, name, harmfulness and colour; what each does is in its
// hooks. Effects whose engine part Helix lacks (raids, trial spawners, the client's own
// movement and screen effects) are registered with no hooks, so they can be given, saved,
// shown and cleared, and do what the client does with them.

// Effect is a status effect's entry. Vanilla's carry BDS's numbers. Each hook may be nil.
type Effect struct {
	// ID is BDS's effect ID, sent in MobEffect and saved; Name its name without the
	// "minecraft:" prefix being needed by commands.
	ID   int32
	Name string
	// Description is the translation key of its name, as /effect prints it.
	Description string
	// Harmful marks the bad effects. Color is the colour of its particles, 0xRRGGBB.
	Harmful bool
	Color   uint32
	// Instant effects do their work once, as Apply, when given and are never kept.
	Instant bool
	// Modifiers change the player's attributes while it has the effect, each by its
	// Amount times the level (amplifier + 1), as BDS's getAttributeModifierValue.
	Modifiers []EffectModifier
	// BreakSpeed changes the break speed while a player has the effect at
	// amplifier (0 is level I).
	BreakSpeed func(b *BreakInfo, amplifier int32, speed float32) float32
	// BreakRate changes the progress a tick, applied after BreakSpeed. BDS
	// applies Haste and Mining Fatigue at both stages.
	BreakRate func(b *BreakInfo, amplifier int32, rate float32) float32
	// Attack changes the damage of the player's hits.
	Attack func(amplifier int32, damage float32) float32
	// Resist changes the damage the player takes, for causes it applies to.
	Resist func(amplifier int32, damage float32) float32
	// Added runs when the player gets the effect, Removed when it loses it, and Tick
	// each tick it has it, with the ticks left before this one counts (for an endless
	// effect, minus the ticks it has had).
	Added   func(p *Player, amplifier int32)
	Removed func(p *Player)
	Tick    func(p *Player, amplifier, left int32)
	// Apply is an instant effect's work; scale is how much of it lands (1 drunk, less
	// for a splash further away).
	Apply func(p *Player, amplifier int32, scale float32)
	// Died runs when a player with the effect dies (BDS's MobEffect slot 5), Hurt when
	// it is hurt (slot 6).
	Died func(p *Player, amplifier int32)
	Hurt func(p *Player, amplifier int32, src DamageSource)
}

// EffectModifier is one attribute change of an effect, BDS's AttributeModifier.
type EffectModifier struct {
	// Attribute is the attribute's name, such as "minecraft:movement".
	Attribute string
	// Amount is the change at level I.
	Amount float32
	// Operation is how the change is made; OnMax changes the attribute's maximum
	// instead of its value (BDS's operand).
	Operation ModifierOperation
	OnMax     bool
}

// ModifierOperation is BDS's AttributeModifierOperation.
type ModifierOperation int32

// BDS's attribute modifier operations.
const (
	// ModifierAdd adds the amount.
	ModifierAdd ModifierOperation = iota
	// ModifierMultiplyBase adds amount times the base.
	ModifierMultiplyBase
	// ModifierMultiplyTotal multiplies the total by 1 + amount.
	ModifierMultiplyTotal
)

// Vanilla effect IDs (BDS 1.26.60's initEffects).
const (
	EffectSpeed               int32 = 1
	EffectSlowness            int32 = 2
	EffectHaste               int32 = 3
	EffectMiningFatigue       int32 = 4
	EffectStrength            int32 = 5
	EffectInstantHealth       int32 = 6
	EffectInstantDamage       int32 = 7
	EffectJumpBoost           int32 = 8
	EffectNausea              int32 = 9
	EffectRegeneration        int32 = 10
	EffectResistance          int32 = 11
	EffectFireResistance      int32 = 12
	EffectWaterBreathing      int32 = 13
	EffectInvisibility        int32 = 14
	EffectBlindness           int32 = 15
	EffectNightVision         int32 = 16
	EffectHunger              int32 = 17
	EffectWeakness            int32 = 18
	EffectPoison              int32 = 19
	EffectWither              int32 = 20
	EffectHealthBoost         int32 = 21
	EffectAbsorption          int32 = 22
	EffectSaturation          int32 = 23
	EffectLevitation          int32 = 24
	EffectFatalPoison         int32 = 25
	EffectConduitPower        int32 = 26
	EffectSlowFalling         int32 = 27
	EffectBadOmen             int32 = 28
	EffectVillageHero         int32 = 29
	EffectDarkness            int32 = 30
	EffectTrialOmen           int32 = 31
	EffectWindCharged         int32 = 32
	EffectWeaving             int32 = 33
	EffectOozing              int32 = 34
	EffectInfested            int32 = 35
	EffectRaidOmen            int32 = 36
	EffectBreathOfTheNautilus int32 = 37
	EffectFreezing            int32 = 38
)

// Numbers of BDS 1.26.60's effects.
const (
	// speedAmount and slownessAmount are the movement modifiers a level (0x91AEAA56 and
	// 0x7107DE5E, multiply total). healthBoostAmount is Health Boost's added maximum
	// health a level (0x5D6F0BA2, add, on the maximum).
	speedAmount       = 0.2
	slownessAmount    = -0.15
	healthBoostAmount = 4
	// regenerationBase, poisonBase and witherBase are shifted down by the level for the
	// ticks between their health changes (BDS's spaced amplifiers 50, 25 and 40).
	regenerationBase = 50
	poisonBase       = 25
	witherBase       = 40
	// effectHealthStep is what one of those changes heals or hurts.
	effectHealthStep = 1
	// poisonFloor is the health Poison leaves; Fatal Poison goes on past it.
	poisonFloor = 1
	// absorptionPerLevel is the extra health Absorption gives per level.
	absorptionPerLevel = 4
	// hungerExhaustion is Hunger's exhaustion a tick, per level (linear amplifier 0.025).
	hungerExhaustion = 0.025
	// instantHealthBase and instantDamageBase are shifted up by the level for what the
	// instant effects heal and hurt (BDS's instantaneous amplifiers 4 and 6).
	instantHealthBase = 4
	instantDamageBase = 6
	// saturationModifier is Saturation's food modifier (its "Saturation" buff of 1).
	saturationModifier = 1
)

// Numbers of the effects that act when a player dies or is hurt (BDS 1.26.60).
const (
	// windChargedRadius + rand * windChargedSpread is the death burst's power
	// (WindChargedMobEffect slot 5, 0xCABC5E0); windChargedKnockback its knockback.
	windChargedRadius    = 3
	windChargedSpread    = 2
	windChargedKnockback = 1
	// weavingMin + rand(weavingExtra) cobwebs, in at most weavingTries spots
	// (WeavingMobEffect slot 5, 0xCABC1A0).
	weavingMin   = 2
	weavingExtra = 2
	weavingTries = 15
	// oozingSlimes medium slimes come out of the dead (OozingMobEffect slot 5, 0xCABB5C0),
	// oozingLift above the feet.
	oozingSlimes = 2
	oozingLift   = 0.5
	oozingEvent  = "minecraft:slime_medium"
	// infestedChance is the odds a hurt lets out 1 + rand(infestedExtra) silverfish
	// (InfestedMobEffect slot 6, 0xCAB19E0), thrown at infestedSpeed along the player's
	// view, turned by up to a quarter turn either way, infestedLift times faster upward.
	infestedChance = 0.1
	infestedExtra  = 2
	infestedSpeed  = 0.3
	infestedLift   = 1.5

	nameCobweb     = "minecraft:web"
	nameSlime      = "minecraft:slime"
	nameSilverfish = "minecraft:silverfish"
)

// Damage causes of the effects (BDS's ActorDamageCause).
const (
	DamageMagic  DamageCause = 14
	DamageWither DamageCause = 15
)

// vanillaEffects are BDS's effects. Haste and Conduit Power do not add up for breaking:
// the higher one counts (Haste on a tie).
func vanillaEffects() []Effect {
	speed := []EffectModifier{{Attribute: attrMovementName, Amount: speedAmount, Operation: ModifierMultiplyTotal}}
	slow := []EffectModifier{{Attribute: attrMovementName, Amount: slownessAmount, Operation: ModifierMultiplyTotal}}
	boost := []EffectModifier{{Attribute: attrHealthName, Amount: healthBoostAmount, Operation: ModifierAdd, OnMax: true}}
	return []Effect{
		{ID: EffectSpeed, Name: "speed", Description: "potion.moveSpeed", Color: 0x33EBFF, Modifiers: speed},
		{ID: EffectSlowness, Name: "slowness", Description: "potion.moveSlowdown", Harmful: true, Color: 0x8BAFE0, Modifiers: slow},
		{ID: EffectHaste, Name: "haste", Description: "potion.digSpeed", Color: 0xD9C043, BreakSpeed: hasteSpeed(EffectHaste), BreakRate: hasteRate(EffectHaste)},
		{ID: EffectMiningFatigue, Name: "mining_fatigue", Description: "potion.digSlowDown", Harmful: true, Color: 0x4A4217, BreakSpeed: fatigueSpeed, BreakRate: fatigueRate},
		{ID: EffectStrength, Name: "strength", Description: "potion.damageBoost", Color: 0xFFC700, Attack: strengthAttack},
		{ID: EffectInstantHealth, Name: "instant_health", Description: "potion.heal", Color: 0xF82423, Instant: true, Apply: instantHealth},
		{ID: EffectInstantDamage, Name: "instant_damage", Description: "potion.harm", Harmful: true, Color: 0xA9656A, Instant: true, Apply: instantDamage},
		// Jump Boost and Levitation move the player on its own client; the fall reads Jump Boost.
		{ID: EffectJumpBoost, Name: "jump_boost", Description: "potion.jump", Color: 0xFDFF84},
		{ID: EffectNausea, Name: "nausea", Description: "potion.confusion", Harmful: true, Color: 0x551D4A},
		{ID: EffectRegeneration, Name: "regeneration", Description: "potion.regeneration", Color: 0xCD5CAB, Tick: regenerationTick},
		{ID: EffectResistance, Name: "resistance", Description: "potion.resistance", Color: 0x9146F0, Resist: resistanceDamage},
		{ID: EffectFireResistance, Name: "fire_resistance", Description: "potion.fireResistance", Color: 0xFF9900},
		{ID: EffectWaterBreathing, Name: "water_breathing", Description: "potion.waterBreathing", Color: 0x98DAC0},
		{ID: EffectInvisibility, Name: "invisibility", Description: "potion.invisibility", Color: 0xF6F6F6, Added: flagsChanged, Removed: flagsGone},
		{ID: EffectBlindness, Name: "blindness", Description: "potion.blindness", Harmful: true, Color: 0x1F1F23},
		{ID: EffectNightVision, Name: "night_vision", Description: "potion.nightVision", Color: 0xC2FF66},
		{ID: EffectHunger, Name: "hunger", Description: "potion.hunger", Harmful: true, Color: 0x587653, Tick: hungerTick},
		{ID: EffectWeakness, Name: "weakness", Description: "potion.weakness", Harmful: true, Color: 0x484D48, Attack: weaknessAttack},
		{ID: EffectPoison, Name: "poison", Description: "potion.poison", Harmful: true, Color: 0x87A363, Tick: poisonTick(false)},
		{ID: EffectWither, Name: "wither", Description: "potion.wither", Harmful: true, Color: 0x736156, Tick: witherTick},
		{ID: EffectHealthBoost, Name: "health_boost", Description: "potion.healthBoost", Color: 0xF87D23, Modifiers: boost},
		{ID: EffectAbsorption, Name: "absorption", Description: "potion.absorption", Color: 0x2552A5, Added: absorptionAdded, Removed: absorptionRemoved},
		{ID: EffectSaturation, Name: "saturation", Description: "potion.saturation", Color: 0xF82423, Instant: true, Apply: saturationApply},
		{ID: EffectLevitation, Name: "levitation", Description: "potion.levitation", Harmful: true, Color: 0xCEFFFF},
		{ID: EffectFatalPoison, Name: "fatal_poison", Description: "potion.poison", Harmful: true, Color: 0x4E9331, Tick: poisonTick(true)},
		{ID: EffectConduitPower, Name: "conduit_power", Description: "potion.conduitPower", Color: 0x1DC2D1, BreakSpeed: hasteSpeed(EffectConduitPower), BreakRate: hasteRate(EffectConduitPower)},
		{ID: EffectSlowFalling, Name: "slow_falling", Description: "potion.slowFalling", Color: 0xF3CFB9},
		// The omens and Hero of the Village wait for raids, trial spawners and trading.
		{ID: EffectBadOmen, Name: "bad_omen", Description: "effect.badOmen", Color: 0x0B6138},
		{ID: EffectVillageHero, Name: "village_hero", Description: "effect.villageHero", Color: 0x44FF44},
		{ID: EffectDarkness, Name: "darkness", Description: "effect.darkness", Harmful: true, Color: 0x44FF44},
		{ID: EffectTrialOmen, Name: "trial_omen", Description: "effect.trial_omen", Color: 0x16A6A6},
		{ID: EffectWindCharged, Name: "wind_charged", Description: "effect.wind_charged", Harmful: true, Color: 0xBDC9FF, Died: windChargedDied},
		{ID: EffectWeaving, Name: "weaving", Description: "effect.weaving", Harmful: true, Color: 0x78695A, Died: weavingDied},
		{ID: EffectOozing, Name: "oozing", Description: "effect.oozing", Harmful: true, Color: 0x99FFA3, Died: oozingDied},
		{ID: EffectInfested, Name: "infested", Description: "effect.infested", Harmful: true, Color: 0x8C9B8C, Hurt: infestedHurt},
		{ID: EffectRaidOmen, Name: "raid_omen", Description: "effect.raid_omen", Color: 0xDE4058},
		{ID: EffectBreathOfTheNautilus, Name: "breath_of_the_nautilus", Description: "effect.customNautilus", Color: 0x00FFEE},
		// Freezing makes freezing hurt more (player/freeze.go).
		{ID: EffectFreezing, Name: "freezing", Description: "effect.freezing", Color: 0x9FFFFF},
	}
}

// spaced is BDS's spaced amplifier: an effect acts when the ticks left divide by base
// halved once per level, at least 1.
func spaced(base, amplifier, left int32) bool {
	every := max(base>>min(amplifier, 31), 1)
	return left%every == 0
}

// regenerationTick is BDS's Regeneration: a heal at each of its spaced ticks.
func regenerationTick(p *Player, amplifier, left int32) {
	if spaced(regenerationBase, amplifier, left) && !p.dead() {
		p.SetHealth(p.Health() + effectHealthStep)
	}
}

// poisonTick is BDS's Poison ("Poison" buff) and Fatal Poison ("FatalPoison"): magic
// damage at each spaced tick. Poison stops at poisonFloor health.
func poisonTick(fatal bool) func(p *Player, amplifier, left int32) {
	return func(p *Player, amplifier, left int32) {
		if !spaced(poisonBase, amplifier, left) || p.dead() || !fatal && p.Health() <= poisonFloor {
			return
		}
		p.TakeDamage(effectHealthStep, DamageSource{Cause: DamageMagic})
	}
}

// witherTick is BDS's Wither: wither damage at each spaced tick.
func witherTick(p *Player, amplifier, left int32) {
	if spaced(witherBase, amplifier, left) && !p.dead() {
		p.TakeDamage(effectHealthStep, DamageSource{Cause: DamageWither})
	}
}

// hungerTick is BDS's Hunger: exhaustion every tick (its duration amplifier is 1).
func hungerTick(p *Player, amplifier, _ int32) {
	p.s.game.sys.Exhaust(p, hungerExhaustion*float32(amplifier+1))
}

// instantAmount is BDS's instantaneous amplifier: base shifted up once per level, scaled
// and rounded.
func instantAmount(base, amplifier int32, scale float32) float32 {
	return float32(math.Floor(float64(scale*float32(int64(base)<<min(amplifier, 32))) + 0.5))
}

// instantHealth is BDS's Instant Health ("Heal" buff).
func instantHealth(p *Player, amplifier int32, scale float32) {
	if !p.dead() {
		p.SetHealth(p.Health() + instantAmount(instantHealthBase, amplifier, scale))
	}
}

// instantDamage is BDS's Instant Damage ("Harm" buff): magic damage.
func instantDamage(p *Player, amplifier int32, scale float32) {
	p.TakeDamage(instantAmount(instantDamageBase, amplifier, scale), DamageSource{Cause: DamageMagic})
}

// saturationApply is BDS's Saturation: the level's worth of food, at modifier 1.
func saturationApply(p *Player, amplifier int32, _ float32) {
	p.feed(amplifier+1, saturationModifier)
}

// absorptionAdded is BDS's Absorption: what the player has of it starts over at the
// effect's amount.
func absorptionAdded(p *Player, amplifier int32) {
	p.setAttributes(attrAbsorption, float32(amplifier+1)*absorptionPerLevel)
}

// absorptionRemoved takes what is left of Absorption away.
func absorptionRemoved(p *Player) { p.setAttributes(attrAbsorption, float32(0)) }

// flagsChanged and flagsGone show Invisibility to those who see the player.
func flagsChanged(p *Player, _ int32) { p.sendActorFlags() }
func flagsGone(p *Player)             { p.sendActorFlags() }

// windChargedDied is BDS's Wind Charged: a wind burst halfway up the dead, of power 3 to
// 5, that hurts nothing, toggles blocks and takes away fall damage.
func windChargedDied(p *Player, _ int32) {
	r := p.entity.Region()
	if r == nil {
		return
	}
	at := p.feet().Add(mgl32.Vec3{0, playerHeight / 2, 0})
	p.s.Explode(r, &Explosion{
		Pos: at, Radius: windChargedRadius + p.rng.Float32()*windChargedSpread, Kind: ExplosionWindCharge,
		Source: p.entity, AllowUnderwater: true, MaxResistance: noResistance, KnockbackScale: windChargedKnockback,
		Cause: DamageEntityExplosion, Particle: packet.LevelEventParticlesWindExplosion, Sound: packet.SoundEventWindChargeBurst,
		ToggleBlocks: true, NegatesFallDamage: true,
	})
}

// weavingDied is BDS's Weaving: with mobgriefing on, 2 or 3 cobwebs in the blocks around
// the dead, each in a block that can be built over, above a block whose top holds things,
// each with the cobweb animation.
func weavingDied(p *Player, _ int32) {
	r := p.entity.Region()
	web, ok := p.s.reg.Blocks.Block(nameCobweb)
	if r == nil || !ok || !p.s.level.MobGriefing {
		return
	}
	base := floorPos(p.feet())
	want := weavingMin + p.rng.IntN(weavingExtra)
	placed := 0
	for range weavingTries {
		if placed >= want {
			break
		}
		pos := base.Add(cube.Pos{p.rng.IntN(3) - 1, p.rng.IntN(3) - 1, p.rng.IntN(3) - 1})
		b, ok := r.Block(pos, 0)
		if !ok || b == web {
			continue
		}
		below, ok := r.Block(pos.Side(cube.FaceDown), 0)
		if !ok || !p.s.attach.Supports(below, cube.FaceUp) || !p.s.game.replaceable(p, r, pos, b, web) {
			continue
		}
		if r.SetBlock(pos, 0, web) {
			levelEvent(r, pos, mgl32.Vec3{float32(pos[0]), float32(pos[1]), float32(pos[2])}, packet.LevelEventAnimationSpawnCobweb, 0)
			placed++
		}
	}
}

// oozingDied is BDS's Oozing: two medium slimes half a block above the dead, each facing
// a random way.
func oozingDied(p *Player, _ int32) {
	d := p.dim
	if d == nil {
		return
	}
	at := p.feet().Add(mgl32.Vec3{0, oozingLift, 0})
	for range oozingSlimes {
		if m, ok := p.s.SpawnMob(d, nameSlime, at, p.rng.Float32()*360); ok {
			m.m.RunEvent(oozingEvent)
		}
	}
}

// infestedHurt is BDS's Infested: one hurt in ten lets out one or two silverfish halfway
// up the player, thrown out in front of it.
func infestedHurt(p *Player, _ int32, _ DamageSource) {
	d := p.dim
	if d == nil || p.rng.Float32() > infestedChance {
		return
	}
	n := 1 + p.rng.IntN(infestedExtra)
	at := p.feet().Add(mgl32.Vec3{0, playerHeight / 2, 0})
	for range n {
		yaw := p.rng.Float32() * 360
		turn := float64(p.rng.Float32())*math.Pi - math.Pi/2
		view := p.viewVector()
		m, ok := p.s.SpawnMob(d, nameSilverfish, at, yaw)
		if !ok {
			continue
		}
		cos, sin := float32(math.Cos(turn)), float32(math.Sin(turn))
		x, z := view[0]*infestedSpeed, view[2]*infestedSpeed
		m.SetVelocity(mgl32.Vec3{x*cos + z*sin, view[1] * infestedSpeed * infestedLift, z*cos - x*sin})
	}
}

// viewVector is the way the player looks, BDS's getViewVector.
func (p *Player) viewVector() mgl32.Vec3 {
	rot := p.entity.Rotation()
	yaw, pitch := float64(mgl32.DegToRad(rot.Yaw)), float64(mgl32.DegToRad(rot.Pitch))
	return mgl32.Vec3{float32(-math.Sin(yaw) * math.Cos(pitch)), float32(-math.Sin(pitch)), float32(math.Cos(yaw) * math.Cos(pitch))}
}
