package crafting

import (
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Brewing stand recipes, as BDS 1.26.60's PotionBrewing keeps them: container mixes change
// the kind of bottle (potion to splash to lingering) and keep its potion, potion mixes turn one
// potion into another. Both come from the CraftingData packet, so they are BDS's own list and
// change with it.

// BrewItem is an item a brewing stand reads: its network ID and aux value.
type BrewItem struct {
	ID   int32
	Meta int32
}

// brewKey is an item by network ID and aux value.
type brewKey struct{ id, meta int32 }

// potionMix turns a potion into another with a reagent.
type potionMix struct {
	reagent, out brewKey
}

// brewing is the brewing stand's recipes, indexed.
type brewing struct {
	// containers are the container mixes in packet order.
	containers []protocol.PotionContainerChangeRecipe
	// potions are the potion mixes by input, in packet order.
	potions map[brewKey][]potionMix
	// reagents are every potion mix's reagent; reagentIDs their IDs with any aux allowed.
	reagents   map[brewKey]bool
	reagentIDs map[int32]bool
	// containerIDs are every container mix's input and output, recipeInputs every potion
	// mix's input.
	containerIDs map[int32]bool
	recipeInputs map[brewKey]bool
}

// newBrewing indexes the brewing recipes of a packet.
func newBrewing(pk *packet.CraftingData) *brewing {
	b := &brewing{
		containers: pk.PotionContainerChangeRecipes, potions: map[brewKey][]potionMix{},
		reagents: map[brewKey]bool{}, reagentIDs: map[int32]bool{}, containerIDs: map[int32]bool{}, recipeInputs: map[brewKey]bool{},
	}
	for _, r := range pk.PotionContainerChangeRecipes {
		b.containerIDs[r.InputItemID], b.containerIDs[r.OutputItemID] = true, true
	}
	for _, r := range pk.PotionRecipes {
		in := brewKey{r.InputPotionID, r.InputPotionMetadata}
		reagent := brewKey{r.ReagentItemID, r.ReagentItemMetadata}
		b.potions[in] = append(b.potions[in], potionMix{reagent: reagent, out: brewKey{r.OutputPotionID, r.OutputPotionMetadata}})
		b.recipeInputs[in] = true
		if anyAux(reagent.meta) {
			b.reagentIDs[reagent.id] = true
		} else {
			b.reagents[reagent] = true
		}
	}
	return b
}

// anyAux reports an aux value that takes every variant: -1 or 0x7FFF, as BDS's
// ((aux + 1) & 0xFFFF7FFF) == 0 test.
func anyAux(meta int32) bool { return (meta+1)&^0x8000 == 0 }

// Brew is BDS's PotionBrewing::mix: what the bottle in becomes with reagent, false for
// nothing. A container mix comes first and keeps the bottle's aux value; then a potion mix
// for that exact potion and the reagent (its aux too, unless the recipe takes any).
func (t *Table) Brew(in, reagent BrewItem) (BrewItem, bool) {
	b := t.brew()
	for _, r := range b.containers {
		if r.InputItemID == in.ID && r.ReagentItemID == reagent.ID {
			return BrewItem{ID: r.OutputItemID, Meta: in.Meta}, true
		}
	}
	for _, m := range b.potions[brewKey{in.ID, in.Meta}] {
		if m.reagent.id == reagent.ID && (anyAux(m.reagent.meta) || m.reagent.meta == reagent.Meta) {
			return BrewItem{ID: m.out.id, Meta: m.out.meta}, true
		}
	}
	return BrewItem{}, false
}

// HasBrew is BDS's PotionBrewing::hasMix, what the stand checks before brewing: a container
// mix as in Brew, or a potion mix for the exact potion whose reagent is the same item, any
// aux value.
func (t *Table) HasBrew(in, reagent BrewItem) bool {
	b := t.brew()
	for _, r := range b.containers {
		if r.InputItemID == in.ID && r.ReagentItemID == reagent.ID {
			return true
		}
	}
	for _, m := range b.potions[brewKey{in.ID, in.Meta}] {
		if m.reagent.id == reagent.ID {
			return true
		}
	}
	return false
}

// BrewIngredient is BDS's PotionBrewing::isIngredient as far as the recipes say: the reagent of
// any mix. BDS also takes pufferfish whatever the recipes are; the player package adds that.
func (t *Table) BrewIngredient(it BrewItem) bool {
	b := t.brew()
	for _, r := range b.containers {
		if r.ReagentItemID == it.ID {
			return true
		}
	}
	return b.reagentIDs[it.ID] || b.reagents[brewKey{it.ID, it.Meta}]
}

// BrewBottle is BDS's PotionBrewing::isPotionRecipeInput as far as the recipes say: the
// items a bottle slot takes, any container mix's bottle, or a potion some mix starts from.
func (t *Table) BrewBottle(it BrewItem) bool {
	b := t.brew()
	return b.containerIDs[it.ID] || b.recipeInputs[brewKey{it.ID, it.Meta}]
}

// brew returns the brewing index, built on first use. The packet does not change under a
// table, so building it twice at once only wastes the first.
func (t *Table) brew() *brewing {
	if b := t.brewing.Load(); b != nil {
		return b
	}
	b := newBrewing(t.source)
	t.brewing.Store(b)
	return b
}
