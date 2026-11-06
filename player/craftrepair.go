package player

import (
	"slices"

	"github.com/google/uuid"
	"github.com/helix-bedrock/protocol"
)

// BDS 1.26.60's repair item recipe (RepairItemRecipe: matches 0xC4948A0, assemble
// 0xC493530, recipe ID repair_item_recipeId), two worn items of one kind crafted into one.
// The research note is private.

// repairItemRecipe is the UUID BDS sends the recipe under in CraftingData.
var repairItemRecipe = uuid.UUID{15: 0x01}

// craftRepairShare is what a crafted repair gives back on top of the two items'
// durability: a twentieth of the most.
const craftRepairShare = 20

// CraftRepairFunc is what two worn items crafted together make; no item refuses the
// craft. The grid's items are read from the server's slots.
type CraftRepairFunc func(p *Player, grid []ItemStack) ItemStack

// CraftRepairEvent fires when a repair in a crafting grid has passed the CraftRepair
// system. Cancel refuses it; changing Result changes what the player gets. Runs: owner of
// the player's entity.
type CraftRepairEvent struct {
	Cancellable
	Player        *Player
	First, Second ItemStack
	Result        ItemStack
}

// OnCraftRepair listens to players repairing items in a crafting grid.
func OnCraftRepair(s *Setup, fn func(e *CraftRepairEvent), p Priority) error {
	return addListener(s, &s.h.game.craftRepairEv, fn, p)
}

// repairPair finds the two items of a grid as BDS's matches does: exactly two, of one
// kind, one of each, and the kind has durability.
func (p *Player) repairPair(grid []ItemStack) (first, second ItemStack, ok bool) {
	var items []ItemStack
	for _, s := range grid {
		if !s.Empty() {
			items = append(items, s)
		}
	}
	if len(items) != 2 {
		return ItemStack{}, ItemStack{}, false
	}
	first, second = items[0], items[1]
	if first.Name != second.Name || first.Count != 1 || second.Count != 1 || p.maxDamage(first.Name) <= 0 {
		return ItemStack{}, ItemStack{}, false
	}
	return first, second, true
}

// vanillaCraftRepair is BDS's assemble. An item whose repairable component takes its own
// kind (spears in vanilla) is repaired through its rule both ways round, the less damaged
// kept with all its data. Any other comes out as a new item of the kind with the two
// durabilities and a twentieth, and nothing else. Either way the curses of both items
// are put on it.
func vanillaCraftRepair(p *Player, grid []ItemStack) ItemStack {
	first, second, ok := p.repairPair(grid)
	if !ok {
		return ItemStack{}
	}
	var result ItemStack
	if rule := p.repairRuleFor(first.Name, second); rule != nil {
		a, _ := p.componentRepair(first, rule, second)
		b, _ := p.componentRepair(second, rule, first)
		result = a
		if itemDamage(a) > itemDamage(b) {
			result = b
		}
	} else {
		limit := p.maxDamage(first.Name)
		damage := max(itemDamage(first)+itemDamage(second)-limit-limit/craftRepairShare, 0)
		result = withDamage(ItemStack{Name: first.Name, Count: 1}, damage)
	}
	return p.s.game.addCurses(result, first, second)
}

// addCurses puts the curses of inputs on result, each enchantment and level once, through
// the usual adding rule.
func (g *gameplay) addCurses(result ItemStack, inputs ...ItemStack) ItemStack {
	var curses []EnchantLevel
	for _, in := range inputs {
		enchantLevels(in, func(id, level int16) {
			v := EnchantLevel{ID: id, Level: level}
			if e, ok := g.enchant(id); ok && e.Curse && !slices.Contains(curses, v) {
				curses = append(curses, v)
			}
		})
	}
	if len(curses) == 0 {
		return result
	}
	l := g.itemEnchants(result)
	added := false
	for _, v := range curses {
		added = g.add(&l, v) || added
	}
	if added {
		writeEnchants(&result, l)
	}
	return result
}

// craftRepairResult runs the CraftRepair system and its event for a grid, with the stack
// request status.
func (p *Player) craftRepairResult(grid []ItemStack) (ItemStack, uint8) {
	g := p.s.game
	result := g.sys.CraftRepair(p, grid)
	if result.Empty() {
		return result, protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems
	}
	if !g.craftRepairEv.Has() {
		return result, protocol.ItemStackResponseStatusOK
	}
	first, second, _ := p.repairPair(grid)
	e := CraftRepairEvent{Player: p, First: first, Second: second, Result: result}
	g.craftRepairEv.fire(p.s.log, &e)
	return specialEventResult(e.Cancelled(), e.Result)
}
