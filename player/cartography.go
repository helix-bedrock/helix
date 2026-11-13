package player

import (
	"math/rand/v2"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
)

// The cartography table and BDS 1.26.60's map recipes: MapExtendingRecipe (assemble 0xC4DABC0,
// matches 0xC4DA5B0), MapCloningRecipe, MapLockingRecipe (0xC4DBAD0, 0xC4DB640) and
// MapUpgradingRecipe, each with a crafting table and a cartography table UUID (locking only at
// the cartography table). The research note is private.
const (
	cartographyBlock   = "minecraft:cartography_table"
	cartographyRecipes = "cartography_table"
	// The table's slots of the UI container: the map and what is added to it.
	cartographyInputSlot      = 12
	cartographyAdditionalSlot = 13

	itemPaper     = "minecraft:paper"
	itemCompass   = "minecraft:compass"
	itemGlassPane = "minecraft:glass_pane"

	// mapExtendPaper is the paper a crafting table's map extending takes, around the map in
	// the middle slot.
	mapExtendPaper   = 8
	mapExtendMiddle  = 4
	mapLockedIDRange = 1 << 32
)

// The map recipes' UUIDs.
var (
	mapExtendRecipe       = uuid.UUID{0xd3, 0x92, 0xb0, 0x75, 0x4b, 0xa1, 0x40, 0xae, 0x87, 0x89, 0xaf, 0x86, 0x8d, 0x56, 0xf6, 0xce} // d392b075-4ba1-40ae-8789-af868d56f6ce
	mapExtendCartography  = uuid.UUID{0x8b, 0x36, 0x26, 0x8c, 0x18, 0x29, 0x48, 0x3c, 0xa0, 0xf1, 0x99, 0x3b, 0x71, 0x56, 0xa8, 0xf2} // 8b36268c-1829-483c-a0f1-993b7156a8f2
	mapCloneRecipe        = uuid.UUID{0x85, 0x93, 0x97, 0x55, 0xba, 0x10, 0x4d, 0x9d, 0xa4, 0xcc, 0xef, 0xb7, 0xa8, 0xe9, 0x43, 0xc4} // 85939755-ba10-4d9d-a4cc-efb7a8e943c4
	mapCloneCartography   = uuid.UUID{0x44, 0x2d, 0x85, 0xed, 0x82, 0x72, 0x45, 0x43, 0xa6, 0xf1, 0x41, 0x8f, 0x90, 0xde, 0xd0, 0x5d} // 442d85ed-8272-4543-a6f1-418f90ded05d
	mapUpgradeRecipe      = uuid.UUID{0xae, 0xcd, 0x22, 0x94, 0x4b, 0x94, 0x43, 0x4b, 0x86, 0x67, 0x44, 0x99, 0xbb, 0x2c, 0x93, 0x27} // aecd2294-4b94-434b-8667-4499bb2c9327
	mapUpgradeCartography = uuid.UUID{0x98, 0xc8, 0x4b, 0x38, 0x10, 0x85, 0x46, 0xbd, 0xb1, 0xce, 0xdd, 0x38, 0xc1, 0x59, 0xe6, 0xcc} // 98c84b38-1085-46bd-b1ce-dd38c159e6cc
	mapLockCartography    = uuid.UUID{0x60, 0x22, 0x34, 0xe4, 0xca, 0xc1, 0x43, 0x53, 0x8b, 0xb7, 0xb1, 0xeb, 0xff, 0x70, 0x02, 0x4b} // 602234e4-cac1-4353-8bb7-b1ebff70024b
)

// useCartography is what clicking a cartography table does.
func (p *Player) useCartography(r *world.Region, u *BlockUse) bool {
	p.openStation(r, u, ContainerCartography, byte(protocol.ContainerTypeCartography))
	return true
}

// mapRecipeResult is the result of one of the map recipes from the items it is given, nil
// for a UUID that is none of them.
func (p *Player) mapRecipeResult(recipe uuid.UUID) func([]ItemStack) (ItemStack, uint8) {
	switch recipe {
	case mapExtendRecipe:
		return func(g []ItemStack) (ItemStack, uint8) { return p.mapExtend(g, false) }
	case mapExtendCartography:
		return func(g []ItemStack) (ItemStack, uint8) { return p.mapExtend(g, true) }
	case mapCloneRecipe, mapCloneCartography:
		return p.mapClone
	case mapUpgradeRecipe, mapUpgradeCartography:
		return p.mapUpgrade
	case mapLockCartography:
		return p.mapLock
	}
	return nil
}

// mapInputs splits a recipe's items into the one filled map and the rest, false unless there
// is exactly one filled map. at is the map's place among the items.
func mapInputs(items []ItemStack) (m ItemStack, at int, rest []ItemStack, ok bool) {
	at = -1
	for i, s := range items {
		switch {
		case s.Empty():
		case s.Name == itemFilledMapName:
			if at >= 0 {
				return ItemStack{}, -1, nil, false
			}
			m, at = s, i
		default:
			rest = append(rest, s)
		}
	}
	return m, at, rest, at >= 0
}

// onlyOf reports whether every stack is of one item, and how many stacks there are.
func onlyOf(items []ItemStack, name string) (int, bool) {
	for _, s := range items {
		if s.Name != name {
			return 0, false
		}
	}
	return len(items), true
}

// mapResult is a copy of a map item for a recipe's result, one of it.
func mapResult(m ItemStack) ItemStack {
	m.Count = 1
	m.Tag = cloneTag(m.Tag)
	return m
}

// mapExtend is BDS's map extending: one map that is not locked nor an explorer map, under
// the largest scale, with paper (one at the cartography table, eight around it at the crafting
// table) becomes its parent map, centred on it the first time.
func (p *Player) mapExtend(items []ItemStack, cartography bool) (ItemStack, uint8) {
	m, at, rest, ok := mapInputs(items)
	n, paper := onlyOf(rest, itemPaper)
	want := mapExtendPaper
	if cartography {
		want = 1
	}
	if !ok || !paper || n < want || (!cartography && at != mapExtendMiddle) || m.Meta > mapMetaLocator {
		return ItemStack{}, protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems
	}
	d := p.s.mapByID(mapID(m))
	if d == nil {
		return ItemStack{}, protocol.ItemStackResponseStatusInvalidCraftRequest
	}
	d.mu.Lock()
	locked, scale, parentID, x, z := d.locked, d.scale, d.parent, d.x, d.z
	d.mu.Unlock()
	parent := p.s.mapByID(parentID)
	if locked || scale >= mapMaxScale || parent == nil {
		return ItemStack{}, protocol.ItemStackResponseStatusInvalidCraftRequest
	}
	if !mapFlag(m, mapTagInit) {
		parent.mu.Lock()
		parent.setOrigin(scale+1, x, z)
		parent.mu.Unlock()
	}
	out := withTag(mapResult(m), mapTagUUID, parent.id)
	return withTag(out, mapTagInit, uint8(1)), protocol.ItemStackResponseStatusOK
}

// mapClone is map cloning: a map and empty maps make that many more of the map.
func (p *Player) mapClone(items []ItemStack) (ItemStack, uint8) {
	m, _, rest, ok := mapInputs(items)
	n, empty := onlyOf(rest, itemEmptyMapName)
	if !ok || !empty || n == 0 || p.s.mapByID(mapID(m)) == nil {
		return ItemStack{}, protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems
	}
	out := mapResult(m)
	out.Count = uint8(n + 1) //nolint:gosec // at most nine slots
	return out, protocol.ItemStackResponseStatusOK
}

// mapUpgrade is map upgrading: a map that is not locked, with a compass, shows players.
func (p *Player) mapUpgrade(items []ItemStack) (ItemStack, uint8) {
	m, _, rest, ok := mapInputs(items)
	n, compass := onlyOf(rest, itemCompass)
	if !ok || !compass || n != 1 || mapFlag(m, mapTagPlayers) {
		return ItemStack{}, protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems
	}
	d := p.s.mapByID(mapID(m))
	if d == nil {
		return ItemStack{}, protocol.ItemStackResponseStatusInvalidCraftRequest
	}
	d.mu.Lock()
	locked := d.locked
	d.mu.Unlock()
	if locked {
		return ItemStack{}, protocol.ItemStackResponseStatusInvalidCraftRequest
	}
	return withTag(mapResult(m), mapTagPlayers, uint8(1)), protocol.ItemStackResponseStatusOK
}

// mapLock is map locking: a map that is not locked, with a glass pane, becomes a locked copy
// under a new random ID, as BDS's assemble draws one.
func (p *Player) mapLock(items []ItemStack) (ItemStack, uint8) {
	m, _, rest, ok := mapInputs(items)
	n, pane := onlyOf(rest, itemGlassPane)
	if !ok || !pane || n != 1 {
		return ItemStack{}, protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems
	}
	d := p.s.mapByID(mapID(m))
	if d == nil {
		return ItemStack{}, protocol.ItemStackResponseStatusInvalidCraftRequest
	}
	d.mu.Lock()
	locked := d.locked
	d.mu.Unlock()
	if locked {
		return ItemStack{}, protocol.ItemStackResponseStatusInvalidCraftRequest
	}
	var copied *mapData
	for copied == nil {
		copied = p.s.lockedCopy(d.id, rand.Int64N(mapLockedIDRange)) //nolint:gosec // an ID, not a secret
	}
	out := withTag(mapResult(m), mapTagUUID, copied.id)
	out = withTag(out, mapTagInit, uint8(1))
	out.Meta = mapMetaLocked
	return out, protocol.ItemStackResponseStatusOK
}

// craftCartography is a craft at the cartography table: one of the map recipes, or a
// cartography table recipe (paper into an empty map, with a compass a locator map). Each
// slot gives one item a craft.
func (p *Player) craftCartography(a *protocol.CraftRecipeStackRequestAction) (uint8, []slotRef) {
	st := &p.requests
	c := &st.craft
	if c.active || len(c.results) != 0 {
		return protocol.ItemStackResponseStatusRequestAlreadyInProgress, nil
	}
	if a.NumberOfCrafts == 0 {
		return protocol.ItemStackResponseStatusInvalidNumCrafts, nil
	}
	in, add := st.work.ui[cartographyInputSlot], st.work.ui[cartographyAdditionalSlot]
	var made ItemStack
	var r *crafting.Recipe
	if u, special := p.s.Recipes().Special(a.RecipeNetworkID); special && p.hasRecipe(a.RecipeNetworkID) {
		result := p.mapRecipeResult(u)
		if result == nil || u == mapExtendRecipe || u == mapCloneRecipe || u == mapUpgradeRecipe {
			return protocol.ItemStackResponseStatusFailedToFindRecipeByNetId, nil
		}
		var status uint8
		if made, status = result([]ItemStack{in, add}); status != protocol.ItemStackResponseStatusOK {
			return status, nil
		}
	} else {
		var ok bool
		r, ok = p.s.Recipes().Recipe(a.RecipeNetworkID)
		if !ok || r.Block != cartographyRecipes || !p.hasRecipe(a.RecipeNetworkID) {
			return protocol.ItemStackResponseStatusFailedToFindRecipeByNetId, nil
		}
		if !r.Fits(crafting.Grid{Items: []crafting.Item{p.craftItem(in), p.craftItem(add)}, Width: 2}) {
			return protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems, nil
		}
	}
	c.active, c.times = true, a.NumberOfCrafts
	if !in.Empty() {
		c.slots = append(c.slots, expectedSlot{container: protocol.ContainerCartographyInput, slot: cartographyInputSlot, item: in, left: a.NumberOfCrafts})
	}
	if !add.Empty() {
		c.slots = append(c.slots, expectedSlot{container: protocol.ContainerCartographyAdditional, slot: cartographyAdditionalSlot, item: add, left: a.NumberOfCrafts})
	}
	if r != nil {
		return p.startResults(r, false), nil
	}
	c.results = append(c.results, craftResult{stack: made, left: int32(made.Count) * int32(a.NumberOfCrafts)})
	// As at a table: the client is sent the item the server made.
	st.refresh = true
	return protocol.ItemStackResponseStatusOK, nil
}

// craftCartographyName is renaming a map at the cartography table: the map takes the name,
// or loses its name for none, and nothing else is used.
func (p *Player) craftCartographyName(a *protocol.CraftRecipeOptionalStackRequestAction) (uint8, []slotRef) {
	st := &p.requests
	c := &st.craft
	if c.active || len(c.results) != 0 {
		return protocol.ItemStackResponseStatusRequestAlreadyInProgress, nil
	}
	in := st.work.ui[cartographyInputSlot]
	if in.Name != itemFilledMapName && in.Name != itemEmptyMapName {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	var name string
	if i := int(a.FilterStringIndex); i >= 0 && i < len(st.filter) {
		name = st.filter[i]
	}
	if utf8.RuneCountInString(name) > int(p.Tuning(TuneAnvilNameLength)) {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	made := withName(mapResult(in), name)
	made.Count = in.Count
	c.active, c.times = true, 1
	c.slots = append(c.slots, expectedSlot{container: protocol.ContainerCartographyInput, slot: cartographyInputSlot, item: in, left: in.Count})
	c.results = append(c.results, craftResult{stack: made, left: int32(made.Count)})
	st.refresh = true
	return protocol.ItemStackResponseStatusOK, nil
}
