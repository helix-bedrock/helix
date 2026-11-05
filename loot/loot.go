// Package loot works out what blocks drop, from BDS 1.26.60's drop rules and
// block loot tables (helix-data). It knows nothing of players or the world: the
// caller says what the tool and block are in a Context and turns the Stacks into
// items.
//
// Enchantment loot functions and chest loot tables are not done yet.
package loot

import (
	"encoding/json"
	"fmt"
	"math/bits"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/helix-bedrock/helix-data/data"
	files "github.com/helix-bedrock/helix-data/data/1.26.60"
)

// BDS's fixed numbers in its drop rules.
const (
	// A grown crop's seeds: fortune + cropBonusRolls tries, each failing at
	// cropMiss in cropSpan.
	cropBonusRolls = 3
	cropMiss       = 7
	cropSpan       = 15
	// A stem gets stemRolls tries, each growth+1 in stemSpan.
	stemRolls = 3
	stemSpan  = 15
	// Huge mushroom blocks drop rand(mushroomSpan)-mushroomOff, at least 0.
	mushroomSpan = 9
	mushroomOff  = 6
	// snowFull is the snow height that drops one snow block with Silk Touch.
	snowFull = 8
)

// Context is what a drop depends on. Silk, Fortune and the tool come from the item
// that broke the block; a block broken with no tool leaves them empty.
type Context struct {
	// Silk is whether the tool has Silk Touch, Fortune its Fortune level.
	Silk    bool
	Fortune int32
	// Tool is the tool's item name, ToolTags its tags.
	Tool     string
	ToolTags []string
	// Explosion is whether an explosion broke the block, Radius its radius:
	// drops survive it at one in Radius.
	Explosion bool
	Radius    float32
	// State returns the broken block's state value, as a number.
	State func(name string) (int32, bool)
	// CustomName is the name of the block's block entity, if any.
	CustomName string
}

// Stack is an item that dropped.
type Stack struct {
	// Name is the item's name.
	Name string
	// Count is how many are in the stack.
	Count int32
	// Self means the item is the broken block's own, in its default state
	// but for KeepStates, which keep the broken block's values.
	Self       bool
	KeepStates []string
	// CustomName is the item's name, if it takes the block entity's.
	CustomName string
}

// Result is what a block dropped.
type Result struct {
	Stacks []Stack
	// XP is the experience it gave.
	XP int32
}

// Rule returns what a block drops for one break. Runs: on the region tick.
type Rule func(c *Context) Result

// Table holds each block's drop rule. Rules are set before the server starts and
// must not change while it runs.
type Table struct {
	rules map[string]Rule
}

// New returns BDS 1.26.60's drop rules: its drop strategies, and the block loot
// tables of the blocks that have none. A block in neither drops nothing.
func New() (*Table, error) {
	drops, err := data.BlockDrops()
	if err != nil {
		return nil, fmt.Errorf("loot: %w", err)
	}
	t := &Table{rules: make(map[string]Rule, len(drops))}
	for name, d := range drops {
		if d.Drops != nil {
			t.rules[name] = strategyRule(name, d)
			continue
		}
		raw, err := files.Files().ReadFile("vanilla/loot_tables/blocks/" + strings.TrimPrefix(name, "minecraft:") + ".json")
		if err != nil {
			continue
		}
		rule, err := tableRule(raw)
		if err != nil {
			return nil, fmt.Errorf("loot: table of %s: %w", name, err)
		}
		t.rules[name] = rule
	}
	return t, nil
}

// Set replaces a block's rule; nil means it drops nothing.
func (t *Table) Set(block string, rule Rule) {
	if rule == nil {
		delete(t.rules, block)
		return
	}
	t.rules[block] = rule
}

// Roll returns what the block drops for one break. Runs: on the region tick.
func (t *Table) Roll(block string, c *Context) Result {
	rule, ok := t.rules[block]
	if !ok {
		return Result{}
	}
	return rule(c)
}

func strategyRule(name string, d data.BlockDrop) Rule {
	return func(c *Context) Result {
		w := walker{block: name, xp: d.XP, c: c}
		w.drops(d.Drops)
		return w.res
	}
}

// walker runs one drop rule.
type walker struct {
	block string
	xp    [2]int32
	c     *Context
	res   Result
}

func randInt(n int32) int32 {
	if n <= 0 {
		return 0
	}
	return rand.Int32N(n) //nolint:gosec // a drop, not a secret
}

// inChance is BDS's chance test: a in b.
func inChance(a, b int32) bool {
	if b <= 0 {
		return false
	}
	return a >= b || randInt(b) < a
}

func (w *walker) copies(s Stack, n int32) {
	s.Count = 1
	for range max(n, 0) {
		w.res.Stacks = append(w.res.Stacks, s)
	}
}

func (w *walker) self() Stack { return Stack{Name: w.block, Self: true} }

func (w *walker) state(name string) int32 {
	if w.c.State == nil {
		return 0
	}
	v, _ := w.c.State(name)
	return v
}

// drops adds what node n drops.
func (w *walker) drops(n *data.DropNode) {
	if n == nil {
		return
	}
	switch n.Kind {
	case "xp_range":
		w.res.XP += w.xp[0] + randInt(w.xp[1]-w.xp[0]+1)
	case "self":
		w.copies(w.self(), w.count(n.Count))
	case "self_states":
		s := w.self()
		s.KeepStates = n.States
		w.copies(s, w.count(n.Count))
	case "item":
		w.copies(Stack{Name: n.Name}, w.count(n.Count))
	case "explosion_decay":
		from := len(w.res.Stacks)
		w.drops(n.Of)
		w.res.Stacks = decay(w.res.Stacks, from, w.c)
	case "name_tag":
		from := len(w.res.Stacks)
		w.drops(n.Of)
		for i := from; i < len(w.res.Stacks); i++ {
			w.res.Stacks[i].CustomName = w.c.CustomName
		}
	case "combine":
		w.drops(n.A)
		w.drops(n.B)
	case "when":
		if w.test(n.If) {
			w.drops(n.Then)
		} else {
			w.drops(n.Else)
		}
	case "crop":
		w.copies(Stack{Name: n.Product}, w.count(n.Count))
		for range max(w.c.Fortune+cropBonusRolls, 1) {
			if !inChance(cropMiss, cropSpan) {
				w.copies(Stack{Name: n.Seeds}, 1)
			}
		}
	case "item_nine_minus_six":
		w.copies(Stack{Name: n.Name}, max(randInt(mushroomSpan), mushroomOff)-mushroomOff)
	case "popcount_multiface":
		if c := bits.OnesCount32(uint32(w.state(n.State))); c > 0 { //nolint:gosec // state bits
			s := w.self()
			s.Count = int32(c) //nolint:gosec // at most 32
			w.res.Stacks = append(w.res.Stacks, s)
		}
	case "state_half_rounded":
		w.copies(Stack{Name: n.Name}, max((w.state(n.State)+1)/2, 1))
	case "snow_height":
		if h := w.state(n.State) + 1; h != snowFull {
			w.copies(w.self(), h)
		} else {
			w.copies(Stack{Name: n.Name}, 1)
		}
	case "actor_item":
		// BDS builds these from the block entity (a banner's pattern, a
		// shulker box's items, a bed's colour). The engine adds what it runs;
		// here the plain item drops.
		if n.Name != "" {
			w.copies(Stack{Name: n.Name}, 1)
		} else {
			w.copies(w.self(), 1)
		}
	}
}

// decay removes the stacks from index from on that an explosion destroys.
func decay(stacks []Stack, from int, c *Context) []Stack {
	if !c.Explosion || c.Radius <= 1 {
		return stacks
	}
	kept := stacks[:from]
	for _, s := range stacks[from:] {
		if rand.Float32() < 1/c.Radius { //nolint:gosec // a drop, not a secret
			kept = append(kept, s)
		}
	}
	return kept
}

// test runs a condition node.
func (w *walker) test(n *data.DropNode) bool {
	if n == nil {
		return false
	}
	switch n.Kind {
	case "silk_touch":
		return w.c.Silk
	case "tool_is":
		return w.c.Tool == n.Name
	case "tool_has_tag":
		return slices.Contains(w.c.ToolTags, n.Name)
	case "or":
		return w.test(n.A) || w.test(n.B)
	case "one_in":
		return inChance(1, n.N)
	case "one_in_by_fortune":
		return inChance(1, int32(byFortune(n.Table, w.c.Fortune)))
	case "chance_by_fortune":
		chance := float32(byFortune(n.Table, w.c.Fortune))
		return chance >= 1 || (chance > 0 && rand.Float32() < chance) //nolint:gosec // a drop, not a secret
	case "not_explosion":
		return !w.c.Explosion
	case "state_is_bool", "state_is":
		return w.state(n.State) == n.Value
	case "state_at_least":
		return w.state(n.State) >= n.Value
	}
	// config_toggle (TNT's) reads a server setting not traced yet.
	return false
}

func byFortune(table []float64, fortune int32) float64 {
	if len(table) == 0 {
		return 0
	}
	return table[min(int(max(fortune, 0)), len(table)-1)]
}

// count runs a count node.
func (w *walker) count(n *data.DropNode) int32 {
	if n == nil {
		return 0
	}
	switch n.Kind {
	case "constant":
		return n.N
	case "uniform":
		return n.Min + randInt(n.Max-n.Min+1)
	case "fortune_multiplier":
		c := w.count(n.Of)
		if w.c.Fortune > 0 {
			c *= max(randInt(w.c.Fortune+2), 1)
		}
		return c
	case "fortune_uniform_bonus":
		c := w.count(n.Of)
		if w.c.Fortune > 0 {
			c += randInt(w.c.Fortune + 1)
		}
		return c
	case "limit":
		return min(n.Max, w.count(n.Of))
	case "state_plus":
		return w.state(n.State) + n.Add
	case "stem_seeds":
		var c int32
		for range stemRolls {
			if inChance(w.state(n.State)+1, stemSpan) {
				c++
			}
		}
		return c
	}
	return 0
}

// tableFile is a block loot table file, as far as block tables use one.
type tableFile struct {
	Pools []struct {
		Rolls   int32 `json:"rolls"`
		Entries []struct {
			Name      string `json:"name"`
			Weight    *int32 `json:"weight"`
			Functions []struct {
				Function string `json:"function"`
				Count    int32  `json:"count"`
			} `json:"functions"`
		} `json:"entries"`
	} `json:"pools"`
}

// tableRule is a block loot table run as BDS's pool roll: each roll picks one
// entry by weight. Only set_count and explosion_decay occur in block tables.
func tableRule(raw []byte) (Rule, error) {
	var f tableFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	return func(c *Context) Result {
		var res Result
		for _, p := range f.Pools {
			var total int32
			for _, e := range p.Entries {
				total += weightOf(e.Weight)
			}
			for range p.Rolls {
				pick := randInt(total)
				for _, e := range p.Entries {
					if pick -= weightOf(e.Weight); pick >= 0 {
						continue
					}
					from := len(res.Stacks)
					res.Stacks = append(res.Stacks, Stack{Name: e.Name, Count: 1})
					for _, fn := range e.Functions {
						switch strings.TrimPrefix(fn.Function, "minecraft:") {
						case "set_count":
							if len(res.Stacks) > from {
								res.Stacks[from].Count = fn.Count
							}
						case "explosion_decay":
							res.Stacks = decay(res.Stacks, from, c)
						}
					}
					break
				}
			}
		}
		return res
	}, nil
}

func weightOf(w *int32) int32 {
	if w == nil {
		return 1
	}
	return *w
}
