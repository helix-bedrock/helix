package player

import (
	"maps"
	"slices"

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
	// The tag BDS saves an armour trim in, and its two fields.
	trimTag      = "Trim"
	trimMaterial = "Material"
	trimPattern  = "Pattern"
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

// trimmed is the base with the trim its template and addition make, as BDS's trim
// recipe: the template must be a known pattern and the addition a known material,
// and a base that already has that exact trim does not fit.
func (p *Player) trimmed(template, base, addition ItemStack) (ItemStack, bool) {
	trims := p.s.reg.Trims.Packet()
	i := slices.IndexFunc(trims.Patterns, func(t protocol.TrimPattern) bool { return t.ItemName == template.Name })
	j := slices.IndexFunc(trims.Materials, func(t protocol.TrimMaterial) bool { return t.ItemName == addition.Name })
	if i < 0 || j < 0 {
		return ItemStack{}, false
	}
	pattern, material := trims.Patterns[i].PatternID, trims.Materials[j].MaterialID
	if old, ok := base.Tag[trimTag].(map[string]any); ok && old[trimMaterial] == material && old[trimPattern] == pattern {
		return ItemStack{}, false
	}
	// One base is consumed, so one comes out however many the slot holds.
	result := base
	result.Count = 1
	result.Tag = maps.Clone(base.Tag)
	if result.Tag == nil {
		result.Tag = map[string]any{}
	}
	result.Tag[trimTag] = map[string]any{trimMaterial: material, trimPattern: pattern}
	return result, true
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
	if !ok || !p.hasRecipe(a.RecipeNetworkID) {
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
	var result ItemStack
	if r.Trim {
		var ok bool
		if result, ok = p.trimmed(template, base, addition); !ok {
			return protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems, nil
		}
	} else {
		result = itemFromNetwork(p.s.reg, r.Result)
		if result.Empty() {
			return protocol.ItemStackResponseStatusInvalidCraftResultItem, nil
		}
		if base.Tag != nil {
			result.Tag = maps.Clone(base.Tag)
		}
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
