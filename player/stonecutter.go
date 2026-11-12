package player

import (
	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
)

// The stonecutter: a screen with one input slot, UI slot 3 (the offset pmmp's
// StonecutterInventory uses for BDS's container), making the recipes tagged stonecutter.
const (
	stonecutterBlock   = "minecraft:stonecutter_block"
	stonecutterRecipes = "stonecutter"
	stonecutterSlot    = 3
)

// useStonecutter is what clicking a stonecutter does: it is taken whether or not it
// opens, as with a crafting table.
func (p *Player) useStonecutter(r *world.Region, u *BlockUse) bool {
	p.openStation(r, u, ContainerStonecutter, byte(protocol.ContainerTypeStonecutter))
	return true
}

// craftStonecut is a stonecutter's craft: the recipe must be a stonecutter recipe the
// player has, the input slot must fit it and hold one item for each craft, which the
// actions that follow consume.
func (p *Player) craftStonecut(a *protocol.CraftRecipeStackRequestAction) (uint8, []slotRef) {
	c := &p.requests.craft
	if c.active {
		return protocol.ItemStackResponseStatusRequestAlreadyInProgress, nil
	}
	if a.NumberOfCrafts == 0 {
		return protocol.ItemStackResponseStatusInvalidNumCrafts, nil
	}
	r, ok := p.s.Recipes().Recipe(a.RecipeNetworkID)
	if !ok || r.Block != stonecutterRecipes || !p.hasRecipe(a.RecipeNetworkID) {
		return protocol.ItemStackResponseStatusFailedToFindRecipeByNetId, nil
	}
	in := p.requests.work.ui[stonecutterSlot]
	if in.Empty() {
		return protocol.ItemStackResponseStatusMissingInputItem, nil
	}
	if !r.Fits(crafting.Grid{Items: []crafting.Item{p.craftItem(in)}, Width: 1}) {
		return protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems, nil
	}
	if in.Count < a.NumberOfCrafts {
		return protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems, nil
	}
	c.active, c.times = true, a.NumberOfCrafts
	c.slots = append(c.slots, expectedSlot{container: protocol.ContainerStonecutterInput, slot: stonecutterSlot, item: in, left: a.NumberOfCrafts})
	return p.startResults(r, false), nil
}
