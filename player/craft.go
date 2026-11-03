package player

import (
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// The crafting grids, as slots of the UI container in BDS 1.26.60.
const (
	smallGridStart = 28
	smallGridSize  = 4
	smallGridWidth = 2
	largeGridStart = 32
	largeGridSize  = 9
	largeGridWidth = 3
	// craftingTable is the block that opens the large grid.
	craftingTable = "minecraft:crafting_table"
	// craftingTableRecipes is the block tag of the recipes the grids make.
	craftingTableRecipes = "crafting_table"
	// craftViewRange is how far a player may be from an open crafting table: the
	// range chests use, as the same check keeps both windows open.
	craftViewRange = chestViewRange
)

// craftState is what the running request's craft still has to do. It lasts one
// request, as BDS's craft handler state does.
type craftState struct {
	active bool
	// auto is a craft from the recipe book, which takes items from anywhere.
	auto bool
	// waiting holds an auto craft's recipe until its items have been consumed.
	waiting *crafting.Recipe
	times   uint8
	// slots are the grid slots a craft by hand must consume.
	slots []expectedSlot
	// anywhere are the ingredients an auto craft must consume.
	anywhere []expectedAnywhere
	consumed bool
	// results are the items the craft makes.
	results []craftResult
}

// expectedSlot is a grid slot and how many of its items must still be consumed.
type expectedSlot struct {
	container byte
	slot      byte
	item      ItemStack
	left      uint8
}

// expectedAnywhere is an ingredient and how many items must still be consumed for it.
type expectedAnywhere struct {
	ingredient crafting.Ingredient
	left       int32
}

// craftResult is a result and how many of it have yet to reach the created output.
type craftResult struct {
	stack ItemStack
	left  int32
}

// CraftEvent fires once a request's recipe has passed BDS's checks, before any item
// is used. Cancel refuses the whole request. Results are what one craft makes;
// changing them changes what the player gets, and the player is sent its items after
// the response, because the client had worked out the recipe's own results. Runs:
// owner of the player's entity.
type CraftEvent struct {
	Cancellable
	Player *Player
	// Recipe is shared: read it, do not change it.
	Recipe *crafting.Recipe
	// Times is how many times the recipe is being made.
	Times uint8
	// Auto says the player used the recipe book rather than filling the grid.
	Auto    bool
	Results []ItemStack
}

// OnCraft listens to crafts before they use any items.
func OnCraft(s *Setup, fn func(e *CraftEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.craftEv.add(s.owner, p, fn)
	return nil
}

// Recipes returns the recipes now in force. Use Registry.Crafting to change them.
// Runs: any goroutine.
func (s *Host) Recipes() *crafting.Table {
	return s.recipes.Table(s.reg.Crafting.Packet())
}

// craftGrid is the grid of the open window: the crafting table's large one, or the
// small one in the player's own inventory. Other windows have no grid.
func (p *Player) craftGrid() (start, size, width int, ok bool) {
	w := &p.window
	switch {
	case w.workbench:
		return largeGridStart, largeGridSize, largeGridWidth, true
	case w.open && !w.chest && !w.smithing:
		return smallGridStart, smallGridSize, smallGridWidth, true
	}
	return 0, 0, 0, false
}

// useCraftingTable is what clicking a crafting table does: it is taken whether or
// not it opens, as with a chest.
func (p *Player) useCraftingTable(r *world.Region, u *BlockUse) bool {
	if p.openStation(r, u, byte(protocol.ContainerTypeWorkbench)) {
		p.window.workbench = true
	}
	return true
}

// useSmithingTable is the same for a smithing table.
func (p *Player) useSmithingTable(r *world.Region, u *BlockUse) bool {
	if p.openStation(r, u, byte(protocol.ContainerTypeSmithingTable)) {
		p.window.smithing = true
	}
	return true
}

// openStation opens the window of a block with no items of its own, if the player
// may and none is open.
func (p *Player) openStation(r *world.Region, u *BlockUse, kind byte) bool {
	if !p.abilities.Flag(AbilityOpenContainers) || p.window.open {
		return false
	}
	w := &p.window
	w.nextID()
	w.open, w.workbenchPos, w.workbenchDim = true, u.Pos, r.Dimension()
	p.send(&packet.ContainerOpen{
		WindowID: w.id, ContainerType: kind,
		ContainerPosition:       protocol.BlockPos{int32(u.Pos[0]), int32(u.Pos[1]), int32(u.Pos[2])}, //nolint:gosec // block coordinates fit int32
		ContainerEntityUniqueID: -1,
	})
	p.flush()
	return true
}

// tickWorkbench closes a crafting or smithing window when its table is gone or too far.
func (p *Player) tickWorkbench() {
	if !p.window.workbench && !p.window.smithing {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	pos := p.window.workbenchPos
	if r.Dimension() == p.window.workbenchDim && p.workbenchThere(r, pos) {
		return
	}
	p.closeWindow(true)
	p.flush()
}

// workbenchThere reports whether the table still stands within range. An unloaded
// chunk counts as gone, as for a chest.
func (p *Player) workbenchThere(r *world.Region, pos cube.Pos) bool {
	block, ok := r.Block(pos, 0)
	want := craftingTable
	if p.window.smithing {
		want = smithingTable
	}
	if !ok || p.s.reg.Blocks.Name(block) != want {
		return false
	}
	d := p.entity.Position().Sub(blockVec(pos).Add(mgl32.Vec3{0.5, 0.5, 0.5}))
	return d.Dot(d) <= craftViewRange*craftViewRange
}

// recipeFor finds a request's recipe, as BDS's checks start. The client names the
// recipe, so one tagged for another block counts as not found: stricter than BDS as
// read, or a grid would smelt ore and cut stone for anyone who asked.
func (p *Player) recipeFor(id uint32) (*crafting.Recipe, uint8) {
	r, ok := p.s.Recipes().Recipe(id)
	if !ok || r.Block != craftingTableRecipes {
		return nil, protocol.ItemStackResponseStatusFailedToFindRecipeByNetId
	}
	if _, size, _, _ := p.craftGrid(); r.Large() && size != largeGridSize {
		return nil, protocol.ItemStackResponseStatusMismatchedCraftingSize
	}
	return r, protocol.ItemStackResponseStatusOK
}

// craftRecipe is a craft by hand: the grid must fit the recipe, and each item in it
// must be consumed once per craft by the actions that follow.
func (p *Player) craftRecipe(a *protocol.CraftRecipeStackRequestAction) (uint8, []slotRef) {
	if p.window.smithing {
		return p.craftSmithing(a)
	}
	c := &p.requests.craft
	start, size, width, ok := p.craftGrid()
	if !ok {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	if c.active {
		return protocol.ItemStackResponseStatusRequestAlreadyInProgress, nil
	}
	if a.NumberOfCrafts == 0 {
		return protocol.ItemStackResponseStatusInvalidNumCrafts, nil
	}
	r, status := p.recipeFor(a.RecipeNetworkID)
	if r == nil {
		return status, nil
	}
	work := &p.requests.work
	grid := crafting.Grid{Items: make([]crafting.Item, size), Width: width}
	for i := range size {
		grid.Items[i] = p.craftItem(work.ui[start+i])
	}
	if !r.Fits(grid) {
		return protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems, nil
	}
	c.active, c.times = true, a.NumberOfCrafts
	for i := range size {
		if s := work.ui[start+i]; !s.Empty() {
			c.slots = append(c.slots, expectedSlot{container: protocol.ContainerCraftingInput, slot: byte(start + i), item: s, left: a.NumberOfCrafts}) //nolint:gosec // slots are small
		}
	}
	return p.startResults(r, false), nil
}

// craftAuto is a craft from the recipe book: the recipe's ingredients must be
// consumed from the grid or the inventory, and the results come after that.
func (p *Player) craftAuto(a *protocol.AutoCraftRecipeStackRequestAction) (uint8, []slotRef) {
	c := &p.requests.craft
	if _, _, _, ok := p.craftGrid(); !ok {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	if c.active {
		return protocol.ItemStackResponseStatusRequestAlreadyInProgress, nil
	}
	if a.NumberOfCrafts == 0 {
		return protocol.ItemStackResponseStatusInvalidNumCrafts, nil
	}
	r, status := p.recipeFor(a.RecipeNetworkID)
	if r == nil {
		return status, nil
	}
	c.active, c.auto, c.times, c.waiting = true, true, a.NumberOfCrafts, r
	for _, in := range r.Ingredients() {
		if i := slices.IndexFunc(c.anywhere, func(e expectedAnywhere) bool { return e.ingredient == in }); i >= 0 {
			c.anywhere[i].left += int32(a.NumberOfCrafts)
			continue
		}
		c.anywhere = append(c.anywhere, expectedAnywhere{ingredient: in, left: int32(a.NumberOfCrafts)})
	}
	return protocol.ItemStackResponseStatusOK, nil
}

// craftItem is what a recipe reads of a stack.
func (p *Player) craftItem(s ItemStack) crafting.Item {
	if s.Empty() {
		return crafting.Item{}
	}
	t, _ := p.s.reg.Items.ByName(s.Name)
	return crafting.Item{Name: s.Name, Meta: s.Meta, Tags: t.Tags}
}

// startResults makes the recipe's results, after listeners had their say.
func (p *Player) startResults(r *crafting.Recipe, auto bool) uint8 {
	st := &p.requests
	c := &st.craft
	if len(c.results) != 0 {
		return protocol.ItemStackResponseStatusRequestAlreadyInProgress
	}
	results := make([]ItemStack, 0, len(r.Output))
	for _, out := range r.Output {
		results = append(results, itemFromNetwork(p.s.reg, out))
	}
	if g := p.s.game; g.craftEv.Has() {
		before := slices.Clone(results)
		e := CraftEvent{Player: p, Recipe: r, Times: c.times, Auto: auto, Results: results}
		g.craftEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return protocol.ItemStackResponseStatusInvalidCraftRequest
		}
		results = e.Results
		if !slices.EqualFunc(before, results, sameStack) {
			st.refresh = true
		}
	}
	if len(results) == 0 {
		return protocol.ItemStackResponseStatusEmptyCraftResults
	}
	for _, s := range results {
		if s.Empty() {
			return protocol.ItemStackResponseStatusInvalidCraftResultItem
		}
		c.results = append(c.results, craftResult{stack: s, left: int32(s.Count) * int32(c.times)})
	}
	return protocol.ItemStackResponseStatusOK
}

// preCraftAction runs before each action, as BDS's craft handler does: an auto
// craft's results follow its last consume, and a single result goes to the created
// output by itself. A recipe with several results waits for the client to name each.
func (p *Player) preCraftAction(a protocol.StackRequestAction) uint8 {
	c := &p.requests.craft
	if !c.active {
		return protocol.ItemStackResponseStatusOK
	}
	if _, consuming := a.(*protocol.ConsumeStackRequestAction); c.waiting != nil && c.consumed && !consuming {
		r := c.waiting
		c.waiting = nil
		if status := p.startResults(r, true); status != protocol.ItemStackResponseStatusOK {
			return status
		}
	}
	if len(c.results) == 1 && c.results[0].left > 0 {
		return p.createResult(0)
	}
	return protocol.ItemStackResponseStatusOK
}

// createResult puts as much of a result as one slot holds in the created output.
func (p *Player) createResult(i int) uint8 {
	c := &p.requests.craft
	if i >= len(c.results) {
		return protocol.ItemStackResponseStatusInvalidCraftResultIndex
	}
	res := &c.results[i]
	if res.left <= 0 {
		return protocol.ItemStackResponseStatusInvalidCraftResultItem
	}
	out := &p.requests.work.ui[createdOutputSlot]
	if !out.Empty() {
		return protocol.ItemStackResponseStatusFailedToSetCreatedItemOutputSlot
	}
	stackMax := res.stack.maxStack(p.s.reg)
	if res.stack.Count > stackMax {
		return protocol.ItemStackResponseStatusInvalidCraftResultStackSize
	}
	s := res.stack
	s.Count = uint8(min(res.left, int32(stackMax))) //nolint:gosec // at most a stack
	res.left -= int32(s.Count)
	*out = p.s.freshStack(s)
	return protocol.ItemStackResponseStatusOK
}

// createAction is the client naming a result of a recipe with several.
func (p *Player) createAction(a *protocol.CreateStackRequestAction) (uint8, []slotRef) {
	if !p.requests.craft.active {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	return p.createResult(int(a.ResultsSlot)), nil
}

// consumeAction takes items out for the craft, which must expect them.
func (p *Player) consumeAction(a *protocol.ConsumeStackRequestAction) (uint8, []slotRef) {
	if !p.requests.craft.active {
		return protocol.ItemStackResponseStatusInvalidCraftRequest, nil
	}
	src, ok := p.validate(a.Source, true)
	if !ok {
		return protocol.ItemStackResponseStatusFailedToValidateSrcSlot, nil
	}
	if a.Count == 0 || a.Count > src.stack.Count {
		return protocol.ItemStackResponseStatusInvalidRemovedAmount, nil
	}
	if !p.mayRemove(src, *src.stack) {
		return protocol.ItemStackResponseStatusCannotConsumeItem, nil
	}
	used := *src.stack
	used.Count = a.Count
	if status := p.consumed(src, used); status != protocol.ItemStackResponseStatusOK {
		return status, nil
	}
	src.stack.Count -= a.Count
	if src.stack.Count == 0 {
		*src.stack = ItemStack{}
	}
	return protocol.ItemStackResponseStatusOK, []slotRef{src}
}

// consumed checks items taken out against what the craft expects.
func (p *Player) consumed(src slotRef, used ItemStack) uint8 {
	c := &p.requests.craft
	if c.auto {
		return p.consumedAnywhere(src, used)
	}
	i := slices.IndexFunc(c.slots, func(e expectedSlot) bool { return e.container == src.kind && e.slot == src.slot })
	if i < 0 {
		return protocol.ItemStackResponseStatusFailedToMatchExpectedSlotConsumedItem
	}
	e := &c.slots[i]
	switch {
	case e.left == 0:
		return protocol.ItemStackResponseStatusItemAlreadyConsumedFromSlot
	case used.Count > e.left:
		return protocol.ItemStackResponseStatusConsumedTooMuchFromSlot
	case !e.item.stacksWith(used):
		return protocol.ItemStackResponseStatusMismatchSlotExpectedConsumedItem
	}
	e.left -= used.Count
	return protocol.ItemStackResponseStatusOK
}

// consumedAnywhere is the same for an auto craft: the grid or the inventory, and
// never an enchanted or worn item, as the recipe book leaves those alone.
func (p *Player) consumedAnywhere(src slotRef, used ItemStack) uint8 {
	c := &p.requests.craft
	if src.kind != protocol.ContainerCraftingInput && src.kind != containerCombined {
		return protocol.ItemStackResponseStatusConsumedItemNotAllowed
	}
	found := false
	item := p.craftItem(used)
	if _, enchanted := used.Tag["ench"]; !enchanted && itemDamage(used) == 0 {
		for i := range c.anywhere {
			e := &c.anywhere[i]
			if !e.ingredient.Matches(item) {
				continue
			}
			found = true
			if e.left >= int32(used.Count) {
				e.left -= int32(used.Count)
				c.consumed = true
				return protocol.ItemStackResponseStatusOK
			}
		}
	}
	if found {
		return protocol.ItemStackResponseStatusConsumedTooMuchFromSlot
	}
	return protocol.ItemStackResponseStatusFailedToMatchExpectedAllowedAnywhereConsumedItem
}

// finishCraft is BDS's end of a craft: every result must have been made and every
// expected item used, or the request is refused.
func (p *Player) finishCraft() uint8 {
	c := &p.requests.craft
	if !c.active {
		return protocol.ItemStackResponseStatusOK
	}
	for _, r := range c.results {
		if r.left > 0 {
			return protocol.ItemStackResponseStatusResultTransferFailed
		}
	}
	for _, e := range c.slots {
		if e.left != 0 {
			return protocol.ItemStackResponseStatusExpectedItemSlotNotFullyConsumed
		}
	}
	for _, e := range c.anywhere {
		if e.left != 0 {
			return protocol.ItemStackResponseStatusExpectedAnywhereItemNotFullyConsumed
		}
	}
	return protocol.ItemStackResponseStatusOK
}
