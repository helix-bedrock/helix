package player

import (
	"maps"

	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
)

// A smithing table's slots of the UI container in BDS 1.26.60.
const (
	smithBaseSlot     = 51
	smithAdditionSlot = 52
	smithTemplateSlot = 53
	// smithingTable is the block that opens the screen.
	smithingTable = "minecraft:smithing_table"
)

// SmithEvent fires once a smithing request has passed BDS's checks, before any item
// is used. Cancel refuses the whole request. Changing Result changes what the player
// gets, and the player is sent its items after the response. Runs: owner of the
// player's entity.
type SmithEvent struct {
	Cancellable
	Player *Player
	// Recipe is shared: read it, do not change it.
	Recipe                   *crafting.SmithingRecipe
	Template, Base, Addition ItemStack
	Result                   ItemStack
}

// OnSmith listens to smithing before it uses any items.
func OnSmith(s *Setup, fn func(e *SmithEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.smithEv.add(s.owner, p, fn)
	return nil
}

// smithSlot is which slot of a smithing table a container is.
func smithSlot(container byte) (crafting.SmithingSlot, int) {
	switch container {
	case protocol.ContainerSmithingTableTemplate:
		return crafting.SmithingTemplate, smithTemplateSlot
	case protocol.ContainerSmithingTableInput:
		return crafting.SmithingBase, smithBaseSlot
	}
	return crafting.SmithingAddition, smithAdditionSlot
}

// craftSmithing is BDS's smithing action: base and addition must be there, the slots
// must fit the recipe, and one of each is consumed by the actions that follow. The
// result carries the base's data.
func (p *Player) craftSmithing(a *protocol.CraftRecipeStackRequestAction) (uint8, []slotRef) {
	st := &p.requests
	c := &st.craft
	if c.active {
		return protocol.ItemStackResponseStatusRequestAlreadyInProgress, nil
	}
	r, ok := p.s.Recipes().Smithing(a.RecipeNetworkID)
	if !ok {
		return protocol.ItemStackResponseStatusFailedToFindRecipeByNetId, nil
	}
	ui := &st.work.ui
	template, base, addition := ui[smithTemplateSlot], ui[smithBaseSlot], ui[smithAdditionSlot]
	if base.Empty() || addition.Empty() {
		return protocol.ItemStackResponseStatusMissingInputItem, nil
	}
	if !r.Fits(p.craftItem(template), p.craftItem(base), p.craftItem(addition)) {
		return protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems, nil
	}
	result := itemFromNetwork(p.s.reg, r.Result)
	if result.Empty() {
		return protocol.ItemStackResponseStatusInvalidCraftResultItem, nil
	}
	if base.Tag != nil {
		result.Tag = maps.Clone(base.Tag)
	}
	if g := p.s.game; g.smithEv.Has() {
		e := SmithEvent{Player: p, Recipe: r, Template: template, Base: base, Addition: addition, Result: result}
		g.smithEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
		}
		if !sameStack(e.Result, result) {
			st.refresh = true
		}
		if result = e.Result; result.Empty() {
			return protocol.ItemStackResponseStatusInvalidCraftResultItem, nil
		}
	}
	c.active, c.times = true, 1
	if !template.Empty() {
		c.slots = append(c.slots, expectedSlot{container: protocol.ContainerSmithingTableTemplate, slot: smithTemplateSlot, item: template, left: 1})
	}
	c.slots = append(c.slots,
		expectedSlot{container: protocol.ContainerSmithingTableInput, slot: smithBaseSlot, item: base, left: 1},
		expectedSlot{container: protocol.ContainerSmithingTableMaterial, slot: smithAdditionSlot, item: addition, left: 1},
	)
	c.results = append(c.results, craftResult{stack: result, left: int32(result.Count)})
	return protocol.ItemStackResponseStatusOK, nil
}
