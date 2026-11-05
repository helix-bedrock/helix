package player

import (
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// BDS 1.26.60's anvil and grindstone.
const (
	anvilBlock      = "minecraft:anvil"
	anvilChipped    = "minecraft:chipped_anvil"
	anvilDamaged    = "minecraft:damaged_anvil"
	anvilDeprecated = "minecraft:deprecated_anvil"
	grindstone      = "minecraft:grindstone"

	anvilFirstSlot       = 1
	anvilSecondSlot      = 2
	grindstoneFirstSlot  = 16
	grindstoneSecondSlot = 17

	// Maps have a recipe of their own on an anvil, which comes with maps.
	itemFilledMap = "minecraft:filled_map"
	itemEmptyMap  = "minecraft:empty_map"
	// itemGlowStick is never merged with another.
	itemGlowStick = "minecraft:glow_stick"

	// anvilTooExpensive is the cost a player out of creative is refused at, and
	// anvilNameOnly what a rename alone is held to.
	anvilTooExpensive = 40
	// anvilWearChance is the chance a use out of creative wears the anvil a step.
	anvilWearChance = 0.12
	// anvilNameLength is the most characters a name may have, from the text box's
	// max_length in bedrock-samples' anvil_screen.json. BDS's anvil code has no limit, but
	// the name is the client's text kept on the item, so longer ones are refused: the
	// lead's decision, and the game's own screen can never send one.
	anvilNameLength = 30
	anvilNameOnly   = 39
	// anvilStackCost is what merging enchantments onto a stack costs, whatever they are.
	anvilStackCost = 40
	// Merging two damaged items: the bonus on a plain merge, on a merge through the
	// repairable component, and what a material restores at most.
	anvilMergeBonus      = 12
	anvilRepairableBonus = 7
	anvilMaterialShare   = 4
	anvilMergeCost       = 2

	// A grindstone gives a twentieth back on top of the sum of two items' durability.
	grindstoneBonusShare = 20
	// A grindstone's result is priced by how many curses it keeps.
	grindstoneMaxCurses = 3

	repairAmountOther   = "context.other->query.remaining_durability"
	repairAmountQuarter = "query.max_durability * 0.25"
	repairTagPrefix     = "q.all_tags('"
	repairTagSuffix     = "')"
)

// grindstoneCurseCost is the result's RepairCost by how many curses it kept.
var grindstoneCurseCost = [grindstoneMaxCurses + 1]int32{0, 1, 3}

// The curses. BDS's grindstone names one more ID that no enchantment has.
const (
	enchantBinding   int16 = 27
	enchantVanishing int16 = 28
)

// repairRule is an entry of BDS's repairable component.
type repairRule struct {
	names, tags []string
	amount      string
}

// repairRules are an item's rules, with its durability as the component holds it.
type repairRules struct {
	rules []repairRule
	max   int32
}

// loadRepairRules reads the repairable component of each item that has one.
func loadRepairRules() (map[string]*repairRules, error) {
	entries, err := data.ItemRegistry()
	if err != nil {
		return nil, err
	}
	out := map[string]*repairRules{}
	for _, it := range entries {
		c, _ := it.Data["components"].(map[string]any)
		r, ok := c["minecraft:repairable"].(map[string]any)
		if !ok {
			continue
		}
		rules := &repairRules{}
		if d, ok := c["minecraft:durability"].(map[string]any); ok {
			rules.max, _ = d["max_durability"].(int32)
		}
		for _, entry := range anyList(r["repair_items"]) {
			m, _ := entry.(map[string]any)
			rule := repairRule{}
			rule.amount, _ = m["repair_amount"].(string)
			for _, x := range anyList(m["items"]) {
				d, _ := x.(map[string]any)
				if name, ok := d["name"].(string); ok {
					rule.names = append(rule.names, name)
				}
				if tag, ok := d["tags"].(string); ok {
					rule.tags = append(rule.tags, strings.TrimSuffix(strings.TrimPrefix(tag, repairTagPrefix), repairTagSuffix))
				}
			}
			rules.rules = append(rules.rules, rule)
		}
		out[it.Name] = rules
	}
	return out, nil
}

// anyList reads an NBT list of any kind as a slice.
func anyList(v any) []any {
	switch l := v.(type) {
	case []any:
		return l
	case []map[string]any:
		out := make([]any, len(l))
		for i, m := range l {
			out[i] = m
		}
		return out
	}
	return nil
}

// AnvilResult is what an anvil makes.
type AnvilResult struct {
	// Result is the item made; no item means nothing is made.
	Result ItemStack
	// Cost is the levels taken.
	Cost int32
	// SecondUsed is how many of the second slot's items go. The first slot is used whole.
	SecondUsed uint8
	// MinLevel is the level the player must have.
	MinLevel int32
	// TooExpensive refuses it however many levels the player has.
	TooExpensive bool
}

// AnvilEvent fires when an anvil's result has passed BDS's checks. Cancel refuses it.
// Runs: owner of the player's entity.
type AnvilEvent struct {
	Cancellable
	Player        *Player
	First, Second ItemStack
	Name          string
	Out           AnvilResult
}

// OnAnvil listens to players using anvils.
func OnAnvil(s *Setup, fn func(e *AnvilEvent), p Priority) error {
	return addListener(s, &s.h.game.anvilEv, fn, p)
}

// GrindstoneResult is what a grindstone makes.
type GrindstoneResult struct {
	// Result is the item made; no item means nothing is made.
	Result ItemStack
	// Experience is what the inputs pay back, before BDS's random share is added.
	Experience int32
}

// GrindstoneEvent fires when a grindstone's result has passed BDS's checks. Cancel
// refuses it. Runs: owner of the player's entity.
type GrindstoneEvent struct {
	Cancellable
	Player        *Player
	First, Second ItemStack
	Out           GrindstoneResult
}

// OnGrindstone listens to players using grindstones.
func OnGrindstone(s *Setup, fn func(e *GrindstoneEvent), p Priority) error {
	return addListener(s, &s.h.game.grindstoneEv, fn, p)
}

// useAnvil is what clicking an anvil does.
func (p *Player) useAnvil(r *world.Region, u *BlockUse) bool {
	p.openStation(r, u, ContainerAnvil, byte(protocol.ContainerTypeAnvil))
	return true
}

// useGrindstone is what clicking a grindstone does.
func (p *Player) useGrindstone(r *world.Region, u *BlockUse) bool {
	p.openStation(r, u, ContainerGrindstone, byte(protocol.ContainerTypeGrindstone))
	return true
}

// repairCost is what anvils have added to an item's price.
func repairCost(s ItemStack) int32 {
	v, _ := s.Tag[repairCostTag].(int32)
	return v
}

// withTag sets one tag of s, on a Tag of its own. A nil value removes it.
func withTag(s ItemStack, key string, value any) ItemStack {
	tag := maps.Clone(s.Tag)
	if tag == nil {
		tag = map[string]any{}
	}
	if value == nil {
		delete(tag, key)
	} else {
		tag[key] = value
	}
	s.Tag = tag
	return s
}

// withDamage is s with its damage set.
func withDamage(s ItemStack, damage int32) ItemStack { return withTag(s, "Damage", damage) }

// damageable reports whether an item has durability and is not marked unbreakable.
func (p *Player) damageable(s ItemStack) bool {
	if s.Empty() || p.maxDamage(s.Name) <= 0 {
		return false
	}
	u, _ := s.Tag["Unbreakable"].(uint8)
	return u == 0
}

// maxDamage is the item's durability.
func (p *Player) maxDamage(name string) int32 {
	g := p.s.game
	if d, ok := g.durability[name]; ok && d.MaxDamage > 0 {
		return int32(d.MaxDamage)
	}
	if r, ok := g.rules().repairable[name]; ok {
		return r.max
	}
	return 0
}

// repairRuleFor is the component rule that takes other as a repair of item.
func (p *Player) repairRuleFor(item string, other ItemStack) *repairRule {
	r, ok := p.s.game.rules().repairable[item]
	if !ok {
		return nil
	}
	var tags []string
	if t, ok := p.s.reg.Items.ByName(other.Name); ok {
		tags = t.Tags
	}
	for i := range r.rules {
		rule := &r.rules[i]
		if slices.Contains(rule.names, other.Name) || slices.ContainsFunc(rule.tags, func(t string) bool { return slices.Contains(tags, t) }) {
			return rule
		}
	}
	return nil
}

// repairAmount is the amount a rule restores, by BDS's two expressions.
func (p *Player) repairAmount(rule *repairRule, item string, other ItemStack) int32 {
	switch rule.amount {
	case repairAmountOther:
		return p.maxDamage(other.Name) - itemDamage(other)
	case repairAmountQuarter:
		return p.maxDamage(item) / anvilMaterialShare
	}
	return 0
}

// componentRepair is BDS's repair through the component: damage falls by the amount, up
// to the damage left, once per item of other. It reports how many of other went.
func (p *Player) componentRepair(s ItemStack, rule *repairRule, other ItemStack) (ItemStack, int32) {
	amount, damage := p.repairAmount(rule, s.Name, other), itemDamage(s)
	var used int32
	for step := min(amount, damage); step > 0 && used < int32(other.Count); step = min(amount, damage) {
		damage -= step
		used++
	}
	return withDamage(s, damage), used
}

// anvilMerge is BDS's merging of second's enchantments onto result: it returns the merged
// item and what that adds to the cost. A book's price counts half, and a stack pays one
// fixed price.
func (p *Player) anvilMerge(first, second, result ItemStack, book bool) (ItemStack, int32) {
	if second.Tag[enchantTag] == nil {
		return result, 0
	}
	g := p.s.game
	merged := result
	g.applyEnchants(&merged, g.itemEnchants(second))
	before := g.enchantWeight(g.itemEnchants(first), book)
	after := g.enchantWeight(g.itemEnchants(merged), book)
	cost := after - before
	if cost > 0 && first.Count >= 2 {
		cost = int32(p.Tuning(TuneAnvilStackCost))
	}
	return merged, cost
}

// vanillaAnvil is BDS's anvil. Paths in its order: the repairable component, then a
// repair material, then two of one item or an enchanted book. After that a new name, the
// price of earlier work, and the player's level.
func vanillaAnvil(p *Player, first, second ItemStack, name string) AnvilResult {
	if first.Empty() {
		return AnvilResult{}
	}
	result := first
	book := second.Name == itemEnchantedBook && second.Tag[enchantTag] != nil
	prior := repairCost(first) + repairCost(second)
	var cost, used int32
	if !second.Empty() {
		if first.Name == itemFilledMap || first.Name == itemEmptyMap {
			return AnvilResult{}
		}
		var ok bool
		if result, cost, used, ok = p.anvilCombine(first, second, book); !ok {
			return AnvilResult{}
		}
	}
	var renamed int32
	if cost > 0 || second.Empty() {
		switch old := customName(first); {
		case name != "" && name != old:
			result = withName(result, name)
			cost++
			renamed = 1
		case name == "" && old != "":
			result = withName(result, "")
			cost++
			renamed = 1
		}
	}
	if cost <= 0 {
		return AnvilResult{}
	}
	total := cost + prior
	nameOnly := renamed == cost && renamed != 0
	creative := p.creative()
	tooExpensive := false
	switch {
	case nameOnly:
		total = min(total, int32(p.Tuning(TuneAnvilNameLimit)))
	case total >= int32(p.Tuning(TuneAnvilTooExpensive)) && !creative:
		tooExpensive = true
	}
	price := max(repairCost(result), repairCost(second))
	if !nameOnly {
		price = 2*price + 1
	}
	out := AnvilResult{Result: withTag(result, repairCostTag, price), Cost: total, SecondUsed: uint8(max(min(used, 255), 0)), MinLevel: total, TooExpensive: tooExpensive} //nolint:gosec // clamped
	if creative {
		out.Cost, out.MinLevel = 0, 0
	}
	return out
}

// anvilCombine is the part of the anvil that uses the second slot. ok false refuses the
// pair.
func (p *Player) anvilCombine(first, second ItemStack, book bool) (result ItemStack, cost, used int32, ok bool) {
	result = first
	if rule := p.repairRuleFor(first.Name, second); rule != nil {
		result, used = p.componentRepair(result, rule, second)
		same := first.Name == second.Name
		cost = used
		if same {
			cost = used << 1
		}
		if same && p.maxDamage(result.Name) > 0 {
			bonus := min(int32(p.Tuning(TuneAnvilRepairableBonus))*p.maxDamage(result.Name)/100, itemDamage(result))
			if d := itemDamage(result); d > bonus {
				result = withDamage(result, d-bonus)
			}
			used = int32(result.Count)
		}
		merged, extra := p.anvilMerge(first, second, result, book)
		if extra > 0 {
			result = merged
			cost += extra
		}
		return result, cost, used, true
	}
	if p.damageable(result) && p.s.game.rules().repair[result.Name][second.Name] {
		share := max(int32(p.Tuning(TuneAnvilMaterialShare)), 1)
		step := func(damage int32) int32 { return min(p.maxDamage(result.Name)/share, damage) }
		damage := itemDamage(result)
		if step(damage) <= 0 {
			return result, 0, 0, false
		}
		for s := step(damage); used < int32(second.Count) && s > 0; s = step(damage) {
			damage -= s
			used++
		}
		return withDamage(result, damage), used, used, true
	}
	if !book && (result.Name != second.Name || second.Name == itemGlowStick || !p.damageable(result)) {
		return result, 0, 0, false
	}
	var repaired int32
	if !book && p.damageable(result) {
		limit := p.maxDamage(result.Name)
		damage := max(itemDamage(first)+itemDamage(second)-limit-int32(p.Tuning(TuneAnvilMergeBonus))*limit/100, 0)
		if damage < itemDamage(result) {
			result = withDamage(result, damage)
			repaired = int32(p.Tuning(TuneAnvilMergeCost))
		}
	}
	merged, extra := p.anvilMerge(first, second, result, book)
	return merged, extra + repaired, int32(result.Count), true
}

// withName is s with an anvil's name, or none.
func withName(s ItemStack, name string) ItemStack {
	display, _ := s.Tag["display"].(map[string]any)
	display = maps.Clone(display)
	if display == nil {
		display = map[string]any{}
	}
	if name == "" {
		delete(display, "Name")
	} else {
		display["Name"] = name
	}
	if len(display) == 0 {
		return withTag(s, "display", nil)
	}
	return withTag(s, "display", display)
}

// craftAnvil is the anvil's action: the server works out the result by itself, and the
// actions that follow must use up what it says.
func (p *Player) craftAnvil(a *protocol.CraftRecipeOptionalStackRequestAction) (uint8, []slotRef) {
	st := &p.requests
	c := &st.craft
	if p.window.kind != ContainerAnvil {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	if c.active {
		return protocol.ItemStackResponseStatusRequestAlreadyInProgress, nil
	}
	first, second := st.work.ui[anvilFirstSlot], st.work.ui[anvilSecondSlot]
	var name string
	if i := int(a.FilterStringIndex); i >= 0 && i < len(st.filter) {
		name = st.filter[i]
	}
	if utf8.RuneCountInString(name) > int(p.Tuning(TuneAnvilNameLength)) {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	g := p.s.game
	out := g.sys.Anvil(p, first, second, name)
	if out.Result.Empty() {
		return protocol.ItemStackResponseStatusInvalidCraftResultItem, nil
	}
	if g.anvilEv.Has() {
		e := AnvilEvent{Player: p, First: first, Second: second, Name: name, Out: out}
		g.anvilEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
		}
		if !sameStack(e.Out.Result, out.Result) || e.Out.SecondUsed != out.SecondUsed {
			st.refresh = true
		}
		if out = e.Out; out.Result.Empty() {
			return protocol.ItemStackResponseStatusInvalidCraftResultItem, nil
		}
	}
	if out.TooExpensive || out.MinLevel > p.Level() {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	c.active, c.times = true, 1
	c.after.levels, c.after.anvilWear = out.Cost, true
	c.slots = append(c.slots, expectedSlot{container: protocol.ContainerAnvilInput, slot: anvilFirstSlot, item: first, left: first.Count})
	if out.SecondUsed > 0 && !second.Empty() {
		c.slots = append(c.slots, expectedSlot{container: protocol.ContainerAnvilMaterial, slot: anvilSecondSlot, item: second, left: out.SecondUsed})
	}
	c.results = append(c.results, craftResult{stack: out.Result, left: int32(out.Result.Count)})
	// As at a table: the client is sent the item the server made.
	st.refresh = true
	return protocol.ItemStackResponseStatusOK, nil
}

// vanillaAnvilWear is BDS's wear after an anvil is used: out of creative, a chance to go a
// step down (a deprecated anvil counts as a whole one), a damaged one breaking with no
// drop. Whatever happens, those nearby hear it. A block no longer an anvil does nothing.
func vanillaAnvilWear(p *Player, r *world.Region, pos cube.Pos) {
	b, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	blocks := p.s.reg.Blocks
	var next string
	switch blocks.Name(b) {
	case anvilBlock, anvilDeprecated:
		next = anvilChipped
	case anvilChipped:
		next = anvilDamaged
	case anvilDamaged:
	default:
		return
	}
	event := int32(packet.LevelEventSoundAnvilUsed)
	if !p.creative() && p.rng.Float32() < p.Tuning(TuneAnvilWearChance) {
		becomes := blocks.Air()
		if next == "" {
			event = packet.LevelEventSoundAnvilBroken
		} else if becomes, ok = blocks.StateID(next, blocks.States(b)); !ok {
			becomes = b
		}
		if becomes != b && r.SetBlock(pos, 0, becomes) {
			r.UpdateNeighbours(pos)
		}
	}
	levelEvent(r, pos, blockVec(pos), event, 0)
}

// keepCurses takes every enchantment but the curses off result, keeps the curses of
// inputs, and prices it by how many. ok false means too many for BDS's price table.
func (g *gameplay) keepCurses(result ItemStack, inputs ...ItemStack) (out ItemStack, kept, ok bool) {
	l := enchantList{slot: g.rules().items[result.Name].Slot}
	count := 0
	for _, in := range inputs {
		enchantLevels(in, func(id, level int16) {
			if e, ok := g.enchant(id); ok && e.Curse {
				g.add(&l, EnchantLevel{ID: id, Level: level})
				kept = true
				count++
			}
		})
	}
	if count > grindstoneMaxCurses {
		return result, kept, false
	}
	writeEnchants(&result, l)
	return withTag(result, repairCostTag, grindstoneCurseCost[count]), kept, true
}

// grindstoneChanges reports whether a grindstone did anything to an item: its
// enchantments or price differ.
func (g *gameplay) grindstoneChanges(input, result ItemStack) bool {
	return repairCost(input) != repairCost(result) || !slices.EqualFunc(flatEnchants(input), flatEnchants(result), func(a, b EnchantLevel) bool { return a == b })
}

// flatEnchants is an item's enchantments in the order it saves them.
func flatEnchants(s ItemStack) []EnchantLevel {
	var out []EnchantLevel
	enchantLevels(s, func(id, level int16) { out = append(out, EnchantLevel{ID: id, Level: level}) })
	return out
}

// grindstoneExperience is what an item's enchantments pay back: each one's minimum cost
// at its level, curses paying nothing.
func (g *gameplay) grindstoneExperience(s ItemStack) int32 {
	var sum int32
	enchantLevels(s, func(id, level int16) {
		if e, ok := g.enchant(id); ok && !e.Curse && e.MinCost != nil {
			sum += e.MinCost(int32(level))
		}
	})
	return sum
}

// vanillaGrindstone is BDS's grindstone. Each slot takes one item. Two of one damageable
// item merge their durability; either way enchantments come off but curses, a book with
// none left goes back to a plain book, and a single item it would not change, or a
// trimmed item alone in the first slot, makes nothing.
func vanillaGrindstone(p *Player, first, second ItemStack) GrindstoneResult {
	g := p.s.game
	if first.Count >= 2 || second.Count >= 2 || first.Empty() && second.Empty() {
		return GrindstoneResult{}
	}
	var result ItemStack
	if !first.Empty() && !second.Empty() {
		if first.Name != second.Name || !p.damageable(first) || !p.damageable(second) {
			return GrindstoneResult{}
		}
		result = p.grindstoneMerged(first, second)
		var ok bool
		if result, _, ok = g.keepCurses(result, first, second); !ok {
			return GrindstoneResult{}
		}
	} else {
		input := first
		if input.Empty() {
			input = second
		} else if first.Tag[trimTag] != nil {
			return GrindstoneResult{}
		}
		var kept, ok bool
		if result, kept, ok = g.keepCurses(input, input); !ok || !g.grindstoneChanges(input, result) {
			return GrindstoneResult{}
		}
		if result.Name == itemEnchantedBook && !kept {
			result.Name = itemBook
		}
	}
	return GrindstoneResult{Result: result, Experience: g.grindstoneExperience(first) + g.grindstoneExperience(second)}
}

// grindstoneMerged is two damaged items as one. Items with the repairable component work
// it out through their rule and keep the less damaged way; others add their durability
// and a twentieth, keeping the data of the first that has any.
func (p *Player) grindstoneMerged(first, second ItemStack) ItemStack {
	if rule := p.repairRuleFor(first.Name, second); rule != nil {
		a, _ := p.componentRepair(first, rule, second)
		b, _ := p.componentRepair(second, rule, first)
		if itemDamage(a) > itemDamage(b) {
			return b
		}
		return a
	}
	base := first
	if base.Tag == nil {
		base = second
	}
	limit := p.maxDamage(first.Name)
	return withDamage(base, max(itemDamage(first)+itemDamage(second)-limit-limit/max(int32(p.Tuning(TuneGrindstoneBonusShare)), 1), 0))
}

// craftGrindstone is the grindstone's action. The client names the price it expects,
// which must be the result's, and both inputs must be used up.
func (p *Player) craftGrindstone(a *protocol.CraftGrindstoneRecipeStackRequestAction) (uint8, []slotRef) {
	st := &p.requests
	c := &st.craft
	if p.window.kind != ContainerGrindstone {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	if c.active {
		return protocol.ItemStackResponseStatusRequestAlreadyInProgress, nil
	}
	first, second := st.work.ui[grindstoneFirstSlot], st.work.ui[grindstoneSecondSlot]
	g := p.s.game
	out := g.sys.Grindstone(p, first, second)
	if out.Result.Empty() || repairCost(out.Result) != a.Cost {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	if g.grindstoneEv.Has() {
		e := GrindstoneEvent{Player: p, First: first, Second: second, Out: out}
		g.grindstoneEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
		}
		if !sameStack(e.Out.Result, out.Result) {
			st.refresh = true
		}
		if out = e.Out; out.Result.Empty() {
			return protocol.ItemStackResponseStatusInvalidCraftResultItem, nil
		}
	}
	c.active, c.times = true, 1
	if half := (int32(float32(out.Experience)*p.Tuning(TuneGrindstoneXPScale)) + 1) / 2; half > 0 {
		c.after.experience = half + p.rng.Int32N(half)
	}
	if !first.Empty() {
		c.slots = append(c.slots, expectedSlot{container: protocol.ContainerGrindstoneInput, slot: grindstoneFirstSlot, item: first, left: first.Count})
	}
	if !second.Empty() {
		c.slots = append(c.slots, expectedSlot{container: protocol.ContainerGrindstoneAdditional, slot: grindstoneSecondSlot, item: second, left: second.Count})
	}
	c.results = append(c.results, craftResult{stack: out.Result, left: int32(out.Result.Count)})
	// As at a table: the client is sent the item the server made.
	st.refresh = true
	return protocol.ItemStackResponseStatusOK, nil
}

// mendable is the player's held and worn items that can drink experience.
func (p *Player) mendable() []*ItemStack {
	g := p.s.game
	inv := &p.inventory
	var picks []*ItemStack
	consider := func(s *ItemStack) {
		if s.Empty() || itemDamage(*s) <= 0 || !p.damageable(*s) {
			return
		}
		mends := false
		enchantLevels(*s, func(id, level int16) {
			if e, ok := g.enchant(id); ok && e.Mends && level > 0 {
				mends = true
			}
		})
		if mends {
			picks = append(picks, s)
		}
	}
	consider(&inv.main[inv.held])
	consider(&inv.offhand[0])
	for i := range inv.armor {
		consider(&inv.armor[i])
	}
	return picks
}

// mendStack is BDS's sum: the item takes up to twice the value in repair, and half of
// what it took comes off the value. It returns what is left.
func mendStack(s *ItemStack, value int32) int32 {
	repair := min(value*2, itemDamage(*s))
	*s = withDamage(*s, itemDamage(*s)-repair)
	return value - repair/2
}

// mend is BDS's Mending as a player takes an orb: one mendable item, picked at random,
// drinks the orb's value. It returns what is left for the player.
func (p *Player) mend(value int32) int32 {
	picks := p.mendable()
	if len(picks) == 0 {
		return value
	}
	s := picks[0]
	if len(picks) > 1 && p.rng != nil {
		s = picks[p.rng.IntN(len(picks))]
	}
	value = mendStack(s, value)
	p.refreshItems()
	p.sendEquipment()
	return value
}
