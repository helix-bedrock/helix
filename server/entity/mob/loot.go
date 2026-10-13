package mob

import (
	"encoding/json"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/item"
	"github.com/helix-bedrock/helix/server/world"
)

// Loot tables in BDS's format. Functions and conditions Helix does not
// know yet are skipped: an unknown function leaves the stack as it is, an
// unknown condition fails, so a mob never drops what vanilla would not.
type lootTable struct {
	Pools []lootPool `json:"pools"`
}

type lootPool struct {
	Rolls      json.RawMessage   `json:"rolls"`
	Conditions []json.RawMessage `json:"conditions"`
	Entries    []lootEntry       `json:"entries"`
}

type lootEntry struct {
	Type      string            `json:"type"`
	Name      string            `json:"name"`
	Weight    *int              `json:"weight"`
	Functions []json.RawMessage `json:"functions"`
	Pools     []lootPool        `json:"pools"`
}

type lootCtx struct {
	m        *Mob
	byPlayer bool
	looting  int
	depth    int
}

// dropLoot drops the mob's loot where it died.
func (m *Mob) dropLoot() {
	table := m.s.spec.loot
	if table == "" {
		return
	}
	c := &lootCtx{m: m}
	if k := m.s.killer; k != nil {
		c.byPlayer = isPlayer(k)
	}
	for _, s := range c.roll(table) {
		m.drop(s)
	}
}

func (c *lootCtx) roll(name string) []item.Stack {
	raw, ok := c.m.s.t.def.loot[name]
	if !ok || c.depth > 8 {
		return nil
	}
	var t lootTable
	if json.Unmarshal(raw, &t) != nil {
		return nil
	}
	c.depth++
	defer func() { c.depth-- }()
	var out []item.Stack
	for _, p := range t.Pools {
		out = append(out, c.pool(p)...)
	}
	return out
}

func (c *lootCtx) pool(p lootPool) []item.Stack {
	for _, cond := range p.Conditions {
		if !c.condition(cond) {
			return nil
		}
	}
	rolls := 1
	if lo, hi, ok := number(p.Rolls); ok {
		rolls = c.between(lo, hi)
	}
	var out []item.Stack
	for range rolls {
		var total int
		for _, e := range p.Entries {
			total += weight(e)
		}
		if total <= 0 {
			continue
		}
		pick := c.m.s.rand.IntN(total)
		for _, e := range p.Entries {
			if pick -= weight(e); pick < 0 {
				out = append(out, c.entry(e)...)
				break
			}
		}
	}
	return out
}

func weight(e lootEntry) int {
	if e.Weight == nil {
		return 1
	}
	return *e.Weight
}

func (c *lootCtx) entry(e lootEntry) []item.Stack {
	switch e.Type {
	case "item":
		count, meta := 1, int16(0)
		for _, f := range e.Functions {
			var fn struct {
				Function string          `json:"function"`
				Count    json.RawMessage `json:"count"`
				Data     json.RawMessage `json:"data"`
			}
			if json.Unmarshal(f, &fn) != nil {
				continue
			}
			switch fn.Function {
			case "set_count", "minecraft:set_count":
				if lo, hi, ok := number(fn.Count); ok {
					count = c.between(lo, hi)
				}
			case "looting_enchant", "minecraft:looting_enchant":
				if lo, hi, ok := number(fn.Count); ok && c.looting > 0 {
					for range c.looting {
						count += c.between(lo, hi)
					}
				}
			case "set_data", "minecraft:set_data":
				if lo, _, ok := number(fn.Data); ok {
					meta = int16(lo)
				}
			}
		}
		it, ok := world.ItemByName(e.Name, meta)
		if !ok || count <= 0 {
			return nil
		}
		return []item.Stack{item.NewStack(it, count)}
	case "loot_table":
		return c.roll(e.Name)
	}
	return nil
}

func (c *lootCtx) condition(raw json.RawMessage) bool {
	var cond struct {
		Condition  string  `json:"condition"`
		Chance     float64 `json:"chance"`
		Multiplier float64 `json:"looting_multiplier"`
		Default    float64 `json:"default_chance"`
	}
	if json.Unmarshal(raw, &cond) != nil {
		return false
	}
	switch cond.Condition {
	case "killed_by_player", "killed_by_player_or_pets":
		return c.byPlayer
	case "random_chance":
		return c.m.s.rand.Float64() < cond.Chance
	case "random_chance_with_looting":
		return c.m.s.rand.Float64() < cond.Chance+float64(c.looting)*cond.Multiplier
	case "random_difficulty_chance":
		return c.m.s.rand.Float64() < cond.Default
	}
	return false
}

func (c *lootCtx) between(lo, hi float64) int {
	l, h := int(lo), int(hi)
	if h <= l {
		return l
	}
	return l + c.m.s.rand.IntN(h-l+1)
}

// drop drops a stack where the mob is.
func (m *Mob) drop(s item.Stack) {
	create := m.tx.World().EntityRegistry().Config().Item
	r := m.s.rand
	for !s.Empty() {
		n := min(s.Count(), s.MaxCount())
		opts := world.EntitySpawnOpts{Position: m.data.Pos.Add(mgl64.Vec3{0, 0.5}), Velocity: mgl64.Vec3{r.Float64()*0.2 - 0.1, 0.2, r.Float64()*0.2 - 0.1}}
		m.tx.AddEntity(create(opts, s.Grow(n-s.Count())))
		s = s.Grow(-n)
	}
}

// equip rolls the mob's equipment table and puts the first item it gives
// in the mob's hand, such as a skeleton's bow.
func (m *Mob) equip() {
	if m.s.spec.equipment == "" {
		return
	}
	c := &lootCtx{m: m}
	for _, s := range c.roll(m.s.spec.equipment) {
		m.s.held = s
		for _, v := range m.tx.Viewers(m.data.Pos) {
			v.ViewEntityItems(m)
		}
		return
	}
}

// HeldItems returns the item in the mob's hand. The off hand is empty.
func (m *Mob) HeldItems() (item.Stack, item.Stack) { return m.s.held, item.Stack{} }

// SetHeldItems puts an item in the mob's hand. The off hand is ignored.
func (m *Mob) SetHeldItems(main, _ item.Stack) {
	m.s.held = main
	for _, v := range m.tx.Viewers(m.data.Pos) {
		v.ViewEntityItems(m)
	}
}
