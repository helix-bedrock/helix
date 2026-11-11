package loot

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/helix-bedrock/helix-data/data"
)

// Entity loot tables: what mobs drop when they die and what they spawn wearing, as BDS
// 1.26.60 rolls them (LootTable::getRandomItems; pool roll 0xD6B3690). The table is the
// path a definition names, such as "loot_tables/entities/zombie.json".
//
// A pool runs when all its conditions pass. A pool with tiers picks one entry by index:
// a random index below initial_range, raised by one for each of bonus_rolls draws under
// bonus_chance. Other pools draw rolls (min plus a random part of the range) plus
// bonus_rolls times luck, and each draw picks one entry by weight (weight plus quality
// times luck, rounded down) among the entries whose conditions pass. An entry's nested
// pools run when it gave something. Conditions and functions are BDS's (vtables of
// LootItem*Condition and the *Function classes); see each case.

// Random is what a roll draws from: the mob's own random, as BDS's Actor::getRandom.
type Random interface {
	// Intn returns 0 to n-1; Float returns 0 to 1.
	Intn(n int32) int32
	Float() float32
}

// EntityContext is what an entity roll depends on (BDS's LootTableContext).
type EntityContext struct {
	Random Random
	// Difficulty is the game difficulty, 0 peaceful to 3 hard; Special is the level's
	// special multiplier, the clamped regional difficulty (0 to 1).
	Difficulty int32
	Special    float32
	// Luck is the killer player's luck; Looting the Looting level of the killer's item.
	Luck    float32
	Looting int32
	// KillerPlayer is set when a player killed the entity (its last attacker was one);
	// KillerPet when a tamed mob a player owns did.
	KillerPlayer, KillerPet bool
	// Killer is the entity type that killed it, Damager the one that dealt the blow
	// (an arrow's, for a shot), Killed the type the entity itself killed, Vehicle the
	// type it rides; empty for none.
	Killer, Damager, Killed, Vehicle string
	// OnFire, Baby and Variant are about the entity itself; Property answers its bool
	// properties.
	OnFire  bool
	Baby    bool
	Variant int32
	Color   int32
	// BoolProperty answers the entity's bool property by name; nil answers none.
	BoolProperty func(name string) (bool, bool)
}

// Drop is one stack a roll gave, for the engine to turn into an item.
type Drop struct {
	Name  string
	Count int32
	// Data is the aux value, set by set_data and the like; -1 leaves the item's own.
	Data int32
	// Damage is the share of durability used (set_damage), -1 for none.
	Damage float32
	// EnchantLevel enchants the item at that level (enchant_random_gear,
	// enchant_with_levels), Treasure letting treasure enchantments in; 0 for none.
	EnchantLevel int32
	Treasure     bool
	// Enchants are specific enchantments by name and level.
	Enchants []Enchant
	// Smelt is set when furnace_smelt applied: the engine smelts the item.
	Smelt bool
	// Potion names the potion of set_potion; Banner is set_banner_details's type;
	// Ominous is set_ominous_bottle_amplifier's amplifier, -1 for none.
	Potion  string
	Banner  int32
	Ominous int32
}

// Enchant is one enchantment of specific_enchants.
type Enchant struct {
	ID    string
	Level int32
}

// EntityTables holds the entity loot tables by path. Read-only once made.
type EntityTables struct {
	tables map[string]*table
}

// NewEntityTables reads BDS 1.26.60's entity loot tables from helix-data.
func NewEntityTables() (*EntityTables, error) {
	raw, err := data.EntityLoot()
	if err != nil {
		return nil, fmt.Errorf("loot: %w", err)
	}
	t := &EntityTables{tables: make(map[string]*table, len(raw))}
	for name, b := range raw {
		var tb table
		if err := json.Unmarshal(b, &tb); err != nil {
			return nil, fmt.Errorf("loot: table %s: %w", name, err)
		}
		t.tables[name] = &tb
	}
	return t, nil
}

// Has reports a table by path.
func (t *EntityTables) Has(name string) bool {
	_, ok := t.tables[name]
	return ok
}

// Roll rolls a table and appends what it gave to dst. An unknown table gives nothing.
// Runs: anywhere; it only reads the tables.
func (t *EntityTables) Roll(name string, c *EntityContext, dst []Drop) []Drop {
	return t.roll(name, c, dst, 0)
}

// maxDepth stops tables that name each other.
const maxDepth = 16

func (t *EntityTables) roll(name string, c *EntityContext, dst []Drop, depth int) []Drop {
	tb, ok := t.tables[name]
	if !ok || depth > maxDepth || c.Random == nil {
		return dst
	}
	for i := range tb.Pools {
		dst = t.pool(&tb.Pools[i], c, dst, depth)
	}
	return dst
}

// pool is BDS's pool roll (0xD6B3690).
func (t *EntityTables) pool(p *pool, c *EntityContext, dst []Drop, depth int) []Drop {
	if !passes(p.Conditions, c) {
		return dst
	}
	if p.Tiers != nil {
		tier := int32(0)
		if p.Tiers.InitialRange > 0 {
			tier = c.Random.Intn(p.Tiers.InitialRange)
		}
		for range max(p.Tiers.BonusRolls, 0) {
			if p.Tiers.BonusChance > c.Random.Float() {
				tier++
			}
		}
		if tier >= 0 && int(tier) < len(p.Entries) {
			dst = t.entry(&p.Entries[tier], c, dst, depth)
		}
		return dst
	}
	n := p.Rolls.Min
	if p.Rolls.Max >= p.Rolls.Min {
		n += c.Random.Intn(p.Rolls.Max - p.Rolls.Min + 1)
	}
	// The bonus draw is taken even with no luck, as BDS takes it.
	f := c.Random.Float()
	n += int32(math.Floor(float64(((p.Bonus.Max-p.Bonus.Min)*f + p.Bonus.Min) * c.Luck)))
	for range max(n, 0) {
		total := int32(0)
		for i := range p.Entries {
			if e := &p.Entries[i]; passes(e.Conditions, c) {
				total += e.weight(c.Luck)
			}
		}
		if total <= 0 {
			continue
		}
		left := c.Random.Intn(total)
		for i := range p.Entries {
			e := &p.Entries[i]
			if !passes(e.Conditions, c) {
				continue
			}
			if left -= e.weight(c.Luck); left < 0 {
				dst = t.entry(e, c, dst, depth)
				break
			}
		}
	}
	return dst
}

// entry gives what an entry gives, then runs its nested pools when it gave something.
func (t *EntityTables) entry(e *entry, c *EntityContext, dst []Drop, depth int) []Drop {
	gave := false
	switch e.Type {
	case "item":
		d := Drop{Name: ns(e.Name), Count: 1, Data: -1, Damage: -1, Ominous: -1}
		for i := range e.Functions {
			e.Functions[i].apply(&d, c)
		}
		if d.Count > 0 {
			dst = append(dst, d)
			gave = true
		}
	case "loot_table":
		dst = t.roll(e.Name, c, dst, depth+1)
		gave = true
	}
	if gave {
		for i := range e.Pools {
			dst = t.pool(&e.Pools[i], c, dst, depth+1)
		}
	}
	return dst
}

func ns(name string) string {
	if name == "" || strings.Contains(name, ":") {
		return name
	}
	return "minecraft:" + name
}

// table is a loot table file.
type table struct {
	Pools []pool `json:"pools"`
}

type pool struct {
	Conditions []condition `json:"conditions"`
	Rolls      intRange    `json:"rolls"`
	Bonus      floatRange  `json:"bonus_rolls"`
	Tiers      *tiers      `json:"tiers"`
	Entries    []entry     `json:"entries"`
}

type tiers struct {
	InitialRange int32   `json:"initial_range"`
	BonusRolls   int32   `json:"bonus_rolls"`
	BonusChance  float32 `json:"bonus_chance"`
}

type entry struct {
	Type       string      `json:"type"`
	Name       string      `json:"name"`
	Weight     *int32      `json:"weight"`
	Quality    int32       `json:"quality"`
	Conditions []condition `json:"conditions"`
	Functions  []function  `json:"functions"`
	Pools      []pool      `json:"pools"`
}

// weight is weight + quality * luck, rounded down, at least 0.
func (e *entry) weight(luck float32) int32 {
	w := int32(1)
	if e.Weight != nil {
		w = *e.Weight
	}
	return max(int32(math.Floor(float64(float32(w)+float32(e.Quality)*luck))), 0)
}

// intRange is a number or {min, max}, read as BDS reads rolls: floats rounded down.
type intRange struct{ Min, Max int32 }

func (r *intRange) UnmarshalJSON(b []byte) error {
	var f floatRange
	if err := f.UnmarshalJSON(b); err != nil {
		return err
	}
	r.Min, r.Max = int32(math.Floor(float64(f.Min))), int32(math.Floor(float64(f.Max)))
	return nil
}

// floatRange is a number, [min, max] or {min, max}.
type floatRange struct{ Min, Max float32 }

func (r *floatRange) UnmarshalJSON(b []byte) error {
	var n float32
	if json.Unmarshal(b, &n) == nil {
		r.Min, r.Max = n, n
		return nil
	}
	var a []float32
	if json.Unmarshal(b, &a) == nil && len(a) == 2 {
		r.Min, r.Max = a[0], a[1]
		return nil
	}
	var o struct {
		Min *float32 `json:"min"`
		Max *float32 `json:"max"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	if o.Min != nil {
		r.Min = *o.Min
	}
	r.Max = r.Min
	if o.Max != nil {
		r.Max = *o.Max
	}
	return nil
}

// randomInt is min plus a random part of the range, as set_count draws it.
func (r intRange) randomInt(rnd Random) int32 {
	n := r.Min
	if r.Max >= r.Min {
		n += rnd.Intn(r.Max - r.Min + 1)
	}
	return n
}

// condition is one LootItem*Condition.
type condition struct {
	Condition         string          `json:"condition"`
	Chance            float32         `json:"chance"`
	LootingMultiplier float32         `json:"looting_multiplier"`
	MaxChance         float32         `json:"max_chance"`
	DefaultChance     *float32        `json:"default_chance"`
	Peaceful          *float32        `json:"peaceful"`
	Easy              *float32        `json:"easy"`
	Normal            *float32        `json:"normal"`
	Hard              *float32        `json:"hard"`
	EntityType        string          `json:"entity_type"`
	Value             json.RawMessage `json:"value"`
	Domain            string          `json:"domain"`
	Properties        map[string]bool `json:"properties"`
}

func passes(cs []condition, c *EntityContext) bool {
	for i := range cs {
		if !cs[i].applies(c) {
			return false
		}
	}
	return true
}

// applies runs the condition. Draws come in BDS's order: a chance condition draws once.
func (k *condition) applies(c *EntityContext) bool {
	switch strings.TrimPrefix(k.Condition, "minecraft:") {
	case "random_chance":
		return k.Chance > c.Random.Float()
	case "random_chance_with_looting":
		// 0xD695060: chance + looting * multiplier.
		return k.Chance+float32(c.Looting)*k.LootingMultiplier > c.Random.Float()
	case "random_difficulty_chance":
		// 0xD6953B0 reads default_chance, then each difficulty with it as the default;
		// 0xD695370 draws once.
		return k.difficultyChance(c.Difficulty) > c.Random.Float()
	case "random_regional_difficulty_chance":
		// 0xD695280: max_chance times the level's special multiplier.
		return c.Special*k.MaxChance > c.Random.Float()
	case "killed_by_player":
		return c.KillerPlayer
	case "killed_by_player_or_pets":
		return c.KillerPlayer || c.KillerPet
	case "killed_by_entity":
		return c.Killer != "" && c.Killer == ns(k.EntityType)
	case "damaged_by_entity":
		return c.Damager != "" && c.Damager == ns(k.EntityType)
	case "entity_killed":
		return c.Killed != "" && c.Killed == ns(k.EntityType)
	case "passenger_of_entity":
		return c.Vehicle != "" && c.Vehicle == ns(k.EntityType)
	case "is_baby":
		return c.Baby
	case "has_variant":
		var v int32
		_ = json.Unmarshal(k.Value, &v)
		return c.Variant == v
	case "entity_properties":
		for name, want := range k.Properties {
			if name == "on_fire" && c.OnFire != want {
				return false
			}
		}
		return true
	case "bool_property":
		if c.BoolProperty == nil {
			return false
		}
		got, ok := c.BoolProperty(k.Domain)
		var want bool
		_ = json.Unmarshal(k.Value, &want)
		return ok && got == want
	}
	// A condition BDS does not know fails, as its parser drops the entry's pool.
	return false
}

func (k *condition) difficultyChance(d int32) float32 {
	def := float32(0)
	if k.DefaultChance != nil {
		def = *k.DefaultChance
	}
	pick := func(p *float32) float32 {
		if p != nil {
			return *p
		}
		return def
	}
	switch d {
	case 0:
		return pick(k.Peaceful)
	case 1:
		return pick(k.Easy)
	case 2:
		return pick(k.Normal)
	}
	return pick(k.Hard)
}

// function is one loot function.
type function struct {
	Function   string          `json:"function"`
	Conditions []condition     `json:"conditions"`
	Count      json.RawMessage `json:"count"`
	Limit      int32           `json:"limit"`
	Chance     float32         `json:"chance"`
	Damage     floatRange      `json:"damage"`
	Data       floatRange      `json:"data"`
	Values     floatRange      `json:"values"`
	Levels     floatRange      `json:"levels"`
	Treasure   bool            `json:"treasure"`
	ID         string          `json:"id"`
	Type       int32           `json:"type"`
	Amplifier  floatRange      `json:"amplifier"`
	Enchants   []struct {
		ID    string     `json:"id"`
		Level floatRange `json:"level"`
	} `json:"enchants"`
}

// apply runs the function on d when its conditions pass.
func (f *function) apply(d *Drop, c *EntityContext) {
	if !passes(f.Conditions, c) {
		return
	}
	switch strings.TrimPrefix(f.Function, "minecraft:") {
	case "set_count":
		// 0xD6A86E0: min plus a random part of the range.
		var r intRange
		if json.Unmarshal(f.Count, &r) == nil {
			d.Count = r.randomInt(c.Random)
		}
	case "looting_enchant":
		// 0xD6A31D0: with Looting, count + round((min + draw * (max - min)) * looting).
		if c.Looting <= 0 {
			return
		}
		var r floatRange
		if json.Unmarshal(f.Count, &r) != nil {
			return
		}
		draw := c.Random.Float()
		d.Count += int32(math.Round(float64((draw*(r.Max-r.Min) + r.Min) * float32(c.Looting))))
		if f.Limit > 0 {
			d.Count = min(d.Count, f.Limit)
		}
	case "enchant_random_gear":
		// 0xD6BD8E0: when chance * special beats the draw, enchant at level
		// special * random(18) + 5.
		if f.Chance*c.Special > c.Random.Float() {
			d.EnchantLevel = int32(c.Special*float32(c.Random.Intn(18)) + 5)
		}
	case "enchant_with_levels":
		d.EnchantLevel = randomFloatInt(f.Levels, c.Random)
		d.Treasure = f.Treasure
	case "specific_enchants":
		for _, e := range f.Enchants {
			d.Enchants = append(d.Enchants, Enchant{ID: e.ID, Level: randomFloatInt(e.Level, c.Random)})
		}
	case "furnace_smelt":
		d.Smelt = true
	case "set_damage":
		d.Damage = f.Damage.Min + c.Random.Float()*(f.Damage.Max-f.Damage.Min)
	case "set_data":
		d.Data = randomFloatInt(f.Data, c.Random)
	case "random_aux_value":
		d.Data = randomFloatInt(f.Values, c.Random)
	case "set_data_from_color_index":
		d.Data = c.Color
	case "set_potion":
		d.Potion = f.ID
	case "set_banner_details":
		d.Banner = f.Type
	case "set_ominous_bottle_amplifier":
		d.Ominous = randomFloatInt(f.Amplifier, c.Random)
	}
}

// randomFloatInt is a whole number from a range: min plus a random part of it.
func randomFloatInt(r floatRange, rnd Random) int32 {
	lo, hi := int32(math.Floor(float64(r.Min))), int32(math.Floor(float64(r.Max)))
	if hi > lo {
		return lo + rnd.Intn(hi-lo+1)
	}
	return lo
}

// SpecialMultiplier is BDS's level special multiplier (ServerLevel 0xCE38310), the clamped
// regional difficulty: difficulty * (0.75 + a time part + a moon part + 0.375, 0.5 on hard,
// the moon and fixed parts halved on easy), then 2 to 4 mapped to 0 to 1. The time part
// is 0.25 once the world tick passes 1.5; the moon part is a quarter of the moon's
// brightness, at most the time part. Peaceful is 0.
func SpecialMultiplier(difficulty int32, worldTick int64, dayTime int64) float32 {
	if difficulty <= 0 {
		return 0
	}
	moon := [8]float32{1, 0.75, 0.5, 0.25, 0, 0.25, 0.5, 0.75}[((dayTime/24000)%8+8)%8]
	t := float32(worldTick) - 0.5
	timePart := max(t, 0) * 0.25
	if t > 1 {
		timePart = 0.25
	}
	extra := min(timePart, max(moon*0.25, 0))
	if difficulty == 3 {
		extra += 0.5
	} else {
		extra += 0.375
	}
	if difficulty == 1 {
		extra *= 0.5
	}
	r := float32(difficulty) * (timePart + 0.75 + extra)
	switch {
	case r < 2:
		return 0
	case r > 4:
		return 1
	}
	return (r - 2) * 0.5
}
