package player

import (
	"math"
	"slices"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/world/cube"
)

// BreakInfo is what the break speed of a block is worked out from. The
// tool rule fills Harvest and Enchantable; the other hooks read it.
type BreakInfo struct {
	Player *Player
	Pos    cube.Pos
	// Block is the block state, Name its block type's name.
	Block uint32
	Name  string
	// Item is the held item.
	Item ItemStack
	// DestroyTime is the block state's hardness; negative never breaks.
	DestroyTime float32
	// Harvest means the item is the right tool (or none is needed): full
	// speed (/30 instead of /100) and drops.
	Harvest bool
	// Enchantable means the tool's own speed applied, so Efficiency adds
	// to it (BDS: a digger on its block tag, shears on their lists).
	Enchantable bool
}

// Enchantment is an enchantment's entry. Vanilla's carry BDS 1.26.60's
// numbers; plugins may change them or add their own.
type Enchantment struct {
	// ID is the ID saved in an item's "ench" list.
	ID   int16
	Name string
	// ToolSpeed changes the held tool's speed when the enchantment is on it
	// at level. Nil changes nothing.
	ToolSpeed func(b *BreakInfo, level int16, speed float32) float32
	// Underwater, on the worn helmet, lifts the under-water slowdown.
	Underwater bool
	// DamageChance is the percent chance, at level, that an item with the
	// enchantment takes damage when it would (Unbreaking). Nil always
	// takes it.
	DamageChance func(level int16) int32
	// Melee is the damage the enchantment adds to a hit on target. Nil adds none.
	Melee func(level int16, target *Player) float32
	// Protection is what the enchantment, on worn armour, counts for against
	// damage of cause. Nil counts for nothing.
	Protection func(level int16, cause DamageCause) int32
}

// Effect is a status effect's entry. Vanilla's carry BDS's numbers.
type Effect struct {
	// ID is BDS's effect ID, sent in MobEffect.
	ID   int32
	Name string
	// BreakSpeed changes the break speed while a player has the effect at
	// amplifier (0 is level I). Nil changes nothing.
	BreakSpeed func(b *BreakInfo, amplifier int32, speed float32) float32
	// BreakRate changes the progress a tick, applied after BreakSpeed. BDS
	// applies Haste and Mining Fatigue at both stages.
	BreakRate func(b *BreakInfo, amplifier int32, rate float32) float32
	// Attack changes the damage of the player's hits.
	Attack func(amplifier int32, damage float32) float32
	// Resist changes the damage the player takes, for causes it applies to.
	Resist func(amplifier int32, damage float32) float32
	// Added runs when the player gets the effect, Removed when it loses it, and Tick
	// each tick it has it, with the ticks left before this one counts.
	Added   func(p *Player, amplifier int32)
	Removed func(p *Player)
	Tick    func(p *Player, amplifier, left int32)
}

// Vanilla enchantment and effect IDs used by the break speed (BDS 1.26.60:
// read in its destroy speed and digger code).
const (
	EnchantAquaAffinity int16 = 8
	EnchantEfficiency   int16 = 15

	EffectHaste         int32 = 3
	EffectMiningFatigue int32 = 4
	EffectConduitPower  int32 = 26
)

// ActiveEffect is an effect a player has; Duration is the ticks left.
type ActiveEffect struct {
	ID        int32
	Amplifier int32
	Duration  int32
}

// breakEnd is the progress at which BDS counts a block as broken.
const breakEnd = 0.99999

// vanillaEnchantments are BDS's enchantments that change the break speed.
func vanillaEnchantments() []Enchantment {
	return []Enchantment{
		{ID: EnchantAquaAffinity, Name: "minecraft:aqua_affinity", Underwater: true},
		{ID: EnchantEfficiency, Name: "minecraft:efficiency", ToolSpeed: efficiencySpeed},
		{ID: EnchantUnbreaking, Name: "minecraft:unbreaking", DamageChance: unbreakingChance},
		{ID: EnchantSharpness, Name: "minecraft:sharpness", Melee: sharpnessDamage},
		{ID: EnchantKnockback, Name: "minecraft:knockback"},
		{ID: EnchantProtection, Name: "minecraft:protection", Protection: protectionKind(protectAll)},
		{ID: EnchantFireProtection, Name: "minecraft:fire_protection", Protection: protectionKind(protectFire)},
		{ID: EnchantFeatherFalling, Name: "minecraft:feather_falling", Protection: protectionKind(protectFall)},
		{ID: EnchantBlastProtection, Name: "minecraft:blast_protection", Protection: protectionKind(protectBlast)},
		{ID: EnchantProjectileProtection, Name: "minecraft:projectile_protection", Protection: protectionKind(protectProjectile)},
	}
}

// efficiencySpeed is BDS's Efficiency: level² + 1 on a tool whose own speed
// applied.
func efficiencySpeed(b *BreakInfo, level int16, speed float32) float32 {
	if !b.Enchantable || level <= 0 {
		return speed
	}
	l := float32(level)
	return speed + l*l + 1
}

// vanillaEffects are BDS's effects that change the break speed. Haste and
// Conduit Power do not add up: the higher one counts (Haste on a tie).
func vanillaEffects() []Effect {
	return []Effect{
		{ID: EffectHaste, Name: "minecraft:haste", BreakSpeed: hasteSpeed(EffectHaste), BreakRate: hasteRate(EffectHaste)},
		{ID: EffectConduitPower, Name: "minecraft:conduit_power", BreakSpeed: hasteSpeed(EffectConduitPower), BreakRate: hasteRate(EffectConduitPower)},
		{ID: EffectMiningFatigue, Name: "minecraft:mining_fatigue", BreakSpeed: fatigueSpeed, BreakRate: fatigueRate},
		{ID: EffectStrength, Name: "minecraft:strength", Attack: strengthAttack},
		{ID: EffectWeakness, Name: "minecraft:weakness", Attack: weaknessAttack},
		{ID: EffectResistance, Name: "minecraft:resistance", Resist: resistanceDamage},
		{ID: EffectRegeneration, Name: "minecraft:regeneration", Tick: regenerationTick},
		{ID: EffectAbsorption, Name: "minecraft:absorption", Added: absorptionAdded, Removed: absorptionRemoved},
		{ID: EffectFireResistance, Name: "minecraft:fire_resistance"},
	}
}

// hasteCounts reports whether effect id is the haste that counts: the
// higher of Haste and Conduit Power, Haste on a tie.
func hasteCounts(p *Player, id int32, amplifier int32) bool {
	other := EffectConduitPower
	if id == EffectConduitPower {
		other = EffectHaste
	}
	o, ok := p.Effect(other)
	if !ok {
		return true
	}
	if id == EffectHaste {
		return amplifier >= o
	}
	return amplifier > o
}

func hasteSpeed(id int32) func(b *BreakInfo, amplifier int32, speed float32) float32 {
	return func(b *BreakInfo, amplifier int32, speed float32) float32 {
		if !hasteCounts(b.Player, id, amplifier) {
			return speed
		}
		return speed * (float32(amplifier+1)*0.2 + 1)
	}
}

func hasteRate(id int32) func(b *BreakInfo, amplifier int32, rate float32) float32 {
	return func(b *BreakInfo, amplifier int32, rate float32) float32 {
		if !hasteCounts(b.Player, id, amplifier) {
			return rate
		}
		return float32(float64(rate) * math.Pow(1.2000000476837158, float64(amplifier+1)))
	}
}

func fatigueSpeed(_ *BreakInfo, amplifier int32, speed float32) float32 {
	return float32(float64(speed) * math.Pow(0.30000001192092896, float64(amplifier+1)))
}

func fatigueRate(_ *BreakInfo, amplifier int32, rate float32) float32 {
	return float32(float64(rate) * math.Pow(0.699999988079071, float64(amplifier+1)))
}

// mining is BDS's mining table (helix-data bds/mining.json) in the form
// the tool rule reads.
type mining struct {
	tierTags []string
	items    map[string]data.MiningItem
	blocks   map[string]data.MiningBlock
	sword    swordLists
	shears   shearsLists
	maceTag  string
}

type swordLists struct {
	tag                  string
	speed15, speed30, s1 map[string]bool
}

type shearsLists struct {
	tag                  string
	canDestroy           map[string]bool
	plus15, plus5, plus2 map[string]bool
}

func set(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func loadMining() (*mining, error) {
	d, err := data.MiningData()
	if err != nil {
		return nil, err
	}
	return &mining{
		tierTags: d.TierTags, items: d.Items, blocks: d.Blocks, maceTag: d.Mace.Tag,
		sword: swordLists{tag: d.Sword.Tag, speed15: set(d.Sword.Speed15), speed30: set(d.Sword.Speed30), s1: set(d.Sword.Speed1_5)},
		shears: shearsLists{tag: d.Shears.Tag, canDestroy: set(d.Shears.CanDestroy),
			plus15: set(d.Shears.Plus15), plus5: set(d.Shears.Plus5), plus2: set(d.Shears.Plus2)},
	}, nil
}

func (m *mining) hasTag(block, tag string) bool {
	return tag != "" && slices.Contains(m.blocks[block].Tags, tag)
}

// toolSpeed is BDS 1.26.60's item destroy speed and right-tool rules
// (DiggerItem, ShearsItem, WeaponItem, the mace), from the mining table.
// It sets b.Harvest and b.Enchantable. Efficiency is added after it by
// the enchantment's own hook.
func (m *mining) toolSpeed(b *BreakInfo) float32 {
	needs := m.blocks[b.Name].NeedsTool
	b.Harvest, b.Enchantable = !needs, false
	if b.Item.Empty() {
		return 1
	}
	it, ok := m.items[b.Item.Name]
	if !ok {
		return 1
	}
	switch it.Rule {
	case "digger":
		if !m.hasTag(b.Name, it.Tag) {
			return 1
		}
		b.Enchantable = true
		b.Harvest = b.Harvest || m.tierAllows(b.Name, it.Tier)
		return it.Speed
	case "sword":
		b.Harvest = b.Harvest || b.Name == "minecraft:web" || m.hasTag(b.Name, m.sword.tag)
		switch {
		case m.sword.speed15[b.Name]:
			return 15
		case m.sword.speed30[b.Name]:
			return 30
		case m.sword.s1[b.Name]:
			return 1.5
		}
		return 1
	case "shears":
		b.Harvest = b.Harvest || m.shears.canDestroy[b.Name] || m.hasTag(b.Name, m.shears.tag)
		switch {
		case m.shears.plus15[b.Name]:
			b.Enchantable = true
			return 15
		case m.shears.plus5[b.Name] || m.hasTag(b.Name, m.shears.tag):
			b.Enchantable = true
			return 5
		case m.shears.plus2[b.Name]:
			b.Enchantable = true
			return 2
		}
		return 1
	case "mace":
		b.Harvest = b.Harvest || m.hasTag(b.Name, m.maceTag)
	}
	return 1
}

// tierAllows is BDS's digger check: each tier tag on the block asks for a
// tool tier above its index.
func (m *mining) tierAllows(block string, tier int) bool {
	for i, tag := range m.tierTags {
		if m.hasTag(block, tag) && tier <= i {
			return false
		}
	}
	return true
}

// enchantLevels calls fn for each enchantment saved on s.
func enchantLevels(s ItemStack, fn func(id, level int16)) {
	for _, e := range compounds(s.Tag["ench"]) {
		id, _ := e["id"].(int16)
		lvl, _ := e["lvl"].(int16)
		fn(id, lvl)
	}
}

// vanillaBreakTime is BDS 1.26.60's destroy rate: the progress a tick of
// breaking adds, 1 meaning one tick. Every number comes from a hook: the
// tool rule, each enchantment on the item, each effect on the player, and
// the player's own multiplier.
func (g *gameplay) vanillaBreakTime(b *BreakInfo) float32 {
	if b.DestroyTime < 0 {
		return 0
	}
	p := b.Player
	rate := float32(1)
	if b.DestroyTime > 0 {
		speed := g.sys.ToolSpeed(b)
		enchantLevels(b.Item, func(id, lvl int16) {
			if e, ok := g.enchants[id]; ok && e.ToolSpeed != nil {
				speed = e.ToolSpeed(b, lvl, speed)
			}
		})
		for _, a := range p.effects {
			if e, ok := g.effects[a.ID]; ok && e.BreakSpeed != nil {
				speed = e.BreakSpeed(b, a.Amplifier, speed)
			}
		}
		if !p.entity.OnGround() && !p.abilities.Flag(AbilityFlying) {
			speed /= 5
		}
		if p.underWater() && !g.underwaterHelmet(p) {
			speed /= 5
		}
		speed *= p.breakSpeed
		div := float32(100)
		if b.Harvest {
			div = 30
		}
		rate = speed / b.DestroyTime / div
	}
	for _, a := range p.effects {
		if e, ok := g.effects[a.ID]; ok && e.BreakRate != nil {
			rate = e.BreakRate(b, a.Amplifier, rate)
		}
	}
	return rate
}

// underwaterHelmet reports whether the worn helmet has an enchantment that
// lifts the under-water slowdown.
func (g *gameplay) underwaterHelmet(p *Player) bool {
	found := false
	enchantLevels(p.inventory.armor[0], func(id, lvl int16) {
		if e, ok := g.enchants[id]; ok && e.Underwater && lvl > 0 {
			found = true
		}
	})
	return found
}
