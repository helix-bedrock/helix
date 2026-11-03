package player

import (
	"slices"
	"testing"

	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
)

// recipeID finds a vanilla recipe's network ID by its name.
func recipeID(t *testing.T, p *Player, name string) uint32 {
	t.Helper()
	pk := p.s.reg.Crafting.Packet()
	for _, r := range pk.ShapedRecipes {
		if r.RecipeID == name {
			return r.RecipeNetworkID
		}
	}
	for _, r := range pk.ShapelessRecipes {
		if r.RecipeID == name {
			return r.RecipeNetworkID
		}
	}
	t.Fatalf("no recipe %s", name)
	return 0
}

// craftGame gives a test player its events.
func craftGame(t *testing.T, p *Player, register func(*Setup)) {
	t.Helper()
	setup, err := NewSetup(p.s.log)
	if err != nil {
		t.Fatal(err)
	}
	if register != nil {
		register(setup)
	}
	if p.s.game, err = setup.freezeGameplay(); err != nil {
		t.Fatal(err)
	}
}

// craftTestPlayer has the inventory window open and items in its small grid.
func craftTestPlayer(t *testing.T, grid map[int]string) *Player {
	t.Helper()
	p := testPlayer(t)
	craftGame(t, p, nil)
	p.window.open = true
	for slot, name := range grid {
		p.inventory.ui[slot] = p.s.freshStack(ItemStack{Name: name, Count: 4})
	}
	return p
}

func consume(p *Player, slot byte) *protocol.ConsumeStackRequestAction {
	a := &protocol.ConsumeStackRequestAction{}
	a.Count, a.Source = 1, slotInfo(protocol.ContainerCraftingInput, slot, p.inventory.ui[slot].netID)
	return a
}

// TestCraftByHand runs a whole craft as a client asks for it: sticks from two planks.
func TestCraftByHand(t *testing.T) {
	p := craftTestPlayer(t, map[int]string{28: "minecraft:oak_planks", 30: "minecraft:oak_planks"})
	id := recipeID(t, p, "minecraft:stick")
	req := protocol.ItemStackRequest{RequestID: 1, Actions: []protocol.StackRequestAction{
		&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: id, NumberOfCrafts: 1},
		&protocol.CraftResultsDeprecatedStackRequestAction{TimesCrafted: 1},
		consume(p, 28), consume(p, 30),
		take(4, slotInfo(containerCreatedOutput, createdOutputSlot, 0), slotInfo(containerCombined, 1, 0)),
	}}
	if r := p.stackRequest(req); r.Status != protocol.ItemStackResponseStatusOK {
		t.Fatalf("craft refused: %d", r.Status)
	}
	if s := p.inventory.main[1]; s.Name != "minecraft:stick" || s.Count != 4 {
		t.Errorf("result %+v", s)
	}
	if p.inventory.ui[28].Count != 3 || p.inventory.ui[30].Count != 3 || !p.inventory.ui[createdOutputSlot].Empty() {
		t.Errorf("grid %d %d, output %+v", p.inventory.ui[28].Count, p.inventory.ui[30].Count, p.inventory.ui[createdOutputSlot])
	}
}

// TestCraftRefused checks a request changes nothing unless the craft is whole and fits.
func TestCraftRefused(t *testing.T) {
	p := craftTestPlayer(t, map[int]string{28: "minecraft:oak_planks", 30: "minecraft:oak_planks"})
	stick := recipeID(t, p, "minecraft:stick")
	out := take(4, slotInfo(containerCreatedOutput, createdOutputSlot, 0), slotInfo(containerCombined, 1, 0))
	for name, c := range map[string]struct {
		actions []protocol.StackRequestAction
		want    uint8
	}{
		"item left unused": {[]protocol.StackRequestAction{&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: stick, NumberOfCrafts: 1}, consume(p, 28), out}, protocol.ItemStackResponseStatusExpectedItemSlotNotFullyConsumed},
		"unknown recipe":   {[]protocol.StackRequestAction{&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: 1 << 30, NumberOfCrafts: 1}}, protocol.ItemStackResponseStatusFailedToFindRecipeByNetId},
		"too big":          {[]protocol.StackRequestAction{&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: recipeID(t, p, "minecraft:furnace"), NumberOfCrafts: 1}}, protocol.ItemStackResponseStatusMismatchedCraftingSize},
		"wrong grid":       {[]protocol.StackRequestAction{&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: recipeID(t, p, "minecraft:torch"), NumberOfCrafts: 1}}, protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems},
		"no craft running": {[]protocol.StackRequestAction{consume(p, 28)}, protocol.ItemStackResponseStatusInvalidCraftRequest},
	} {
		if r := p.stackRequest(protocol.ItemStackRequest{RequestID: 2, Actions: c.actions}); r.Status != c.want {
			t.Errorf("%s: status %d, want %d", name, r.Status, c.want)
		}
		if p.inventory.ui[28].Count != 4 || !p.inventory.main[1].Empty() {
			t.Fatalf("%s changed items", name)
		}
	}
}

// TestCraftTableAndMirror checks the large grid and a recipe at an offset.
func TestCraftTableAndMirror(t *testing.T) {
	p := testPlayer(t)
	craftGame(t, p, nil)
	p.window.open, p.window.workbench = true, true
	planks, sticks := "minecraft:oak_planks", "minecraft:stick"
	for slot, name := range map[int]string{33: planks, 34: planks, 36: planks, 37: sticks, 40: sticks} {
		p.inventory.ui[slot] = p.s.freshStack(ItemStack{Name: name, Count: 1})
	}
	actions := make([]protocol.StackRequestAction, 0, 7)
	actions = append(actions, &protocol.CraftRecipeStackRequestAction{RecipeNetworkID: recipeID(t, p, "minecraft:wooden_axe"), NumberOfCrafts: 1})
	for _, slot := range []byte{33, 34, 36, 37, 40} {
		actions = append(actions, consume(p, slot))
	}
	actions = append(actions, take(1, slotInfo(containerCreatedOutput, createdOutputSlot, 0), slotInfo(containerCombined, 1, 0)))
	if r := p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: actions}); r.Status != protocol.ItemStackResponseStatusOK {
		t.Fatalf("craft refused: %d", r.Status)
	}
	if p.inventory.main[1].Name != "minecraft:wooden_axe" {
		t.Errorf("result %+v", p.inventory.main[1])
	}
}

// TestCraftAuto is the recipe book: items come from the inventory, the results follow.
func TestCraftAuto(t *testing.T) {
	p := testPlayer(t)
	craftGame(t, p, nil)
	p.window.open = true
	p.inventory.main[1] = p.s.freshStack(ItemStack{Name: "minecraft:oak_planks", Count: 4})
	used := &protocol.ConsumeStackRequestAction{}
	used.Count, used.Source = 2, slotInfo(containerCombined, 1, p.inventory.main[1].netID)
	r := p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: []protocol.StackRequestAction{
		&protocol.AutoCraftRecipeStackRequestAction{RecipeNetworkID: recipeID(t, p, "minecraft:stick"), NumberOfCrafts: 1},
		used,
		take(4, slotInfo(containerCreatedOutput, createdOutputSlot, 0), slotInfo(containerCombined, 3, 0)),
	}})
	if r.Status != protocol.ItemStackResponseStatusOK {
		t.Fatalf("auto craft refused: %d", r.Status)
	}
	if p.inventory.main[1].Count != 2 || p.inventory.main[3].Name != "minecraft:stick" {
		t.Errorf("planks %d, result %+v", p.inventory.main[1].Count, p.inventory.main[3])
	}
}

// TestCraftSeveralResults is a cake: the client names each result, and the buckets
// come back.
func TestCraftSeveralResults(t *testing.T) {
	p := testPlayer(t)
	craftGame(t, p, nil)
	p.window.open, p.window.workbench = true, true
	for slot, name := range map[int]string{32: "minecraft:milk_bucket", 33: "minecraft:milk_bucket", 34: "minecraft:milk_bucket", 35: "minecraft:sugar", 36: "minecraft:egg", 37: "minecraft:sugar", 38: "minecraft:wheat", 39: "minecraft:wheat", 40: "minecraft:wheat"} {
		p.inventory.ui[slot] = p.s.freshStack(ItemStack{Name: name, Count: 1})
	}
	actions := []protocol.StackRequestAction{&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: recipeID(t, p, "minecraft:cake"), NumberOfCrafts: 1}}
	for slot := byte(32); slot <= 40; slot++ {
		actions = append(actions, consume(p, slot))
	}
	output := slotInfo(containerCreatedOutput, createdOutputSlot, 0)
	actions = append(actions,
		&protocol.CreateStackRequestAction{ResultsSlot: 0}, take(1, output, slotInfo(containerCombined, 10, 0)),
		&protocol.CreateStackRequestAction{ResultsSlot: 1}, take(3, output, slotInfo(containerCombined, 11, 0)),
	)
	if r := p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: actions}); r.Status != protocol.ItemStackResponseStatusOK {
		t.Fatalf("craft refused: %d", r.Status)
	}
	if p.inventory.main[10].Name != "minecraft:cake" || p.inventory.main[11].Name != "minecraft:bucket" || p.inventory.main[11].Count != 3 {
		t.Errorf("results %+v %+v", p.inventory.main[10], p.inventory.main[11])
	}
}

// TestCraftEvent checks a plugin can change the result or refuse the craft.
func TestCraftEvent(t *testing.T) {
	refuse := false
	p := testPlayer(t)
	craftGame(t, p, func(s *Setup) {
		if err := OnCraft(s, func(e *CraftEvent) {
			if refuse {
				e.Cancel()
				return
			}
			e.Results[0].Name = "minecraft:apple"
		}, Normal); err != nil {
			t.Fatal(err)
		}
	})
	p.window.open = true
	for _, slot := range []int{28, 30} {
		p.inventory.ui[slot] = p.s.freshStack(ItemStack{Name: "minecraft:oak_planks", Count: 4})
	}
	id := recipeID(t, p, "minecraft:stick")
	craft := func() protocol.ItemStackResponse {
		return p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: []protocol.StackRequestAction{
			&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: id, NumberOfCrafts: 1},
			consume(p, 28), consume(p, 30),
			take(4, slotInfo(containerCreatedOutput, createdOutputSlot, 0), slotInfo(containerCombined, 1, 0)),
		}})
	}
	if r := craft(); r.Status != protocol.ItemStackResponseStatusOK || p.inventory.main[1].Name != "minecraft:apple" || !p.requests.refresh {
		t.Errorf("changed result: status %d, item %+v, refresh %v", r.Status, p.inventory.main[1], p.requests.refresh)
	}
	refuse = true
	if r := craft(); r.Status != protocol.ItemStackResponseStatusInvalidCraftRequest || p.inventory.ui[28].Count != 3 {
		t.Errorf("refused: status %d, grid %d", r.Status, p.inventory.ui[28].Count)
	}
}

// TestRecipesReadable checks every ingredient of BDS's recipes names a real item or
// a tag some item has, and every result is a real item.
func TestRecipesReadable(t *testing.T) {
	p := testPlayer(t)
	tags := map[string]bool{}
	for _, entry := range p.s.reg.Items.RegistryPacket().Items {
		it, _ := p.s.reg.Items.ByName(entry.Name)
		for _, tag := range it.Tags {
			tags[tag] = true
		}
	}
	pk := p.s.reg.Crafting.Packet()
	ids := make([]uint32, 0, len(pk.ShapedRecipes)+len(pk.ShapelessRecipes))
	for _, r := range pk.ShapedRecipes {
		ids = append(ids, r.RecipeNetworkID)
	}
	for _, r := range pk.ShapelessRecipes {
		ids = append(ids, r.RecipeNetworkID)
	}
	bad := map[string]bool{}
	for _, id := range ids {
		r, ok := p.s.Recipes().Recipe(id)
		if !ok {
			t.Fatalf("recipe %d missing", id)
		}
		for _, in := range r.Ingredients() {
			if in.Tag != "" && !tags[in.Tag] {
				bad["tag "+in.Tag] = true
			}
			if in.Tag == "" {
				if _, ok := p.s.reg.Items.ByName(in.Name); !ok {
					bad[in.Name] = true
				}
			}
		}
		for _, out := range r.Output {
			if itemFromNetwork(p.s.reg, out).Empty() {
				bad["result of "+r.ID] = true
			}
		}
	}
	for b := range bad {
		t.Errorf("unreadable: %s", b)
	}
}

// TestCraftClientCannotLie sends what a dishonest client could: nothing it says about
// its items may change what it gets, and every refused request leaves the items as
// they were. Only the server's copy of the items counts.
func TestCraftClientCannotLie(t *testing.T) {
	p := craftTestPlayer(t, map[int]string{28: "minecraft:oak_planks", 30: "minecraft:oak_planks"})
	stick := recipeID(t, p, "minecraft:stick")
	craft := func(times byte) *protocol.CraftRecipeStackRequestAction {
		return &protocol.CraftRecipeStackRequestAction{RecipeNetworkID: stick, NumberOfCrafts: times}
	}
	output := slotInfo(containerCreatedOutput, createdOutputSlot, 0)
	inv := slotInfo(containerCombined, 1, 0)
	wrongID := consume(p, 28)
	wrongID.Source.StackNetworkID++
	twoFromOne := consume(p, 28)
	twoFromOne.Count = 2
	notGrid := &protocol.ConsumeStackRequestAction{}
	notGrid.Count, notGrid.Source = 1, slotInfo(containerCombined, 2, p.inventory.main[2].netID)
	anyDiamonds := &protocol.CraftResultsDeprecatedStackRequestAction{TimesCrafted: 1, ResultItems: []protocol.StackRequestItem{{}}}
	for name, c := range map[string]struct {
		actions []protocol.StackRequestAction
		want    uint8
	}{
		"takes the result, uses nothing": {[]protocol.StackRequestAction{craft(1), take(4, output, inv)}, protocol.ItemStackResponseStatusExpectedItemSlotNotFullyConsumed},
		"takes more than was made":       {[]protocol.StackRequestAction{craft(1), consume(p, 28), consume(p, 30), take(5, output, inv)}, protocol.ItemStackResponseStatusError},
		"asks for two, uses one":         {[]protocol.StackRequestAction{craft(2), consume(p, 28), consume(p, 30), take(8, output, inv)}, protocol.ItemStackResponseStatusExpectedItemSlotNotFullyConsumed},
		"names a stack it does not have": {[]protocol.StackRequestAction{craft(1), wrongID}, protocol.ItemStackResponseStatusFailedToValidateSrcSlot},
		"uses more than one craft takes": {[]protocol.StackRequestAction{craft(1), twoFromOne}, protocol.ItemStackResponseStatusConsumedTooMuchFromSlot},
		"pays with another item":         {[]protocol.StackRequestAction{craft(1), notGrid}, protocol.ItemStackResponseStatusFailedToMatchExpectedSlotConsumedItem},
		"crafts zero times":              {[]protocol.StackRequestAction{craft(0), take(4, output, inv)}, protocol.ItemStackResponseStatusInvalidNumCrafts},
		"claims its own results":         {[]protocol.StackRequestAction{anyDiamonds, take(4, output, inv)}, protocol.ItemStackResponseStatusFailedToValidateSrcSlot},
		"takes with no craft":            {[]protocol.StackRequestAction{take(4, output, inv)}, protocol.ItemStackResponseStatusFailedToValidateSrcSlot},
	} {
		if r := p.stackRequest(protocol.ItemStackRequest{RequestID: 2, Actions: c.actions}); r.Status != c.want {
			t.Errorf("%s: status %d, want %d", name, r.Status, c.want)
		}
		if p.inventory.ui[28].Count != 4 || p.inventory.ui[30].Count != 4 || !p.inventory.main[1].Empty() || !p.inventory.ui[createdOutputSlot].Empty() {
			t.Fatalf("%s changed items", name)
		}
	}

	// An item put in the grid after the recipe was checked is not what the recipe took.
	p.requests.craft = craftState{active: true, slots: []expectedSlot{{container: protocol.ContainerCraftingInput, slot: 28, item: p.inventory.ui[28], left: 1}}}
	swapped := slotRef{container: protocol.ContainerCraftingInput, slot: 28, kind: protocol.ContainerCraftingInput}
	if got := p.consumed(swapped, ItemStack{Name: "minecraft:emerald", Count: 1}); got != protocol.ItemStackResponseStatusMismatchSlotExpectedConsumedItem {
		t.Errorf("swapped grid: status %d", got)
	}
	p.requests.craft = craftState{}

	// The large grid and the smithing slots do not exist without their tables,
	// whatever the client says is open.
	for name, a := range map[string]*protocol.TakeStackRequestAction{
		"large grid":    take(1, slotInfo(containerCombined, 2, p.inventory.main[2].netID), slotInfo(protocol.ContainerCraftingInput, largeGridStart, 0)),
		"smithing slot": take(1, slotInfo(containerCombined, 2, p.inventory.main[2].netID), slotInfo(protocol.ContainerSmithingTableInput, smithBaseSlot, 0)),
	} {
		if r := p.stackRequest(protocol.ItemStackRequest{RequestID: 4, Actions: []protocol.StackRequestAction{a}}); r.Status != protocol.ItemStackResponseStatusFailedToValidateDstSlot {
			t.Errorf("%s with no table: status %d", name, r.Status)
		}
	}

	// A chest window has no grid at all.
	p.window.chest = true
	if status, _ := p.craftRecipe(craft(1)); status != protocol.ItemStackResponseStatusInvalidCraftRequest {
		t.Errorf("craft in a chest window: status %d", status)
	}
}

// TestCraftOtherBlocksRecipe checks a grid refuses a recipe of another block when the
// client names it, by hand or from the recipe book, though the items fit.
func TestCraftOtherBlocksRecipe(t *testing.T) {
	p := testPlayer(t)
	craftGame(t, p, nil)
	p.window.open = true
	seen := map[string]bool{}
	for _, r := range p.s.reg.Crafting.Packet().ShapelessRecipes {
		if r.Block == craftingTableRecipes || r.Block == "deprecated" || seen[r.Block] || len(r.Input) != 1 {
			continue
		}
		in := crafting.ReadIngredient(r.Input[0])
		if in.Name == "" {
			continue
		}
		seen[r.Block] = true
		meta := int16(0)
		if in.Meta != 0x7FFF {
			meta = int16(in.Meta)
		}
		p.inventory.ui[smallGridStart] = p.s.freshStack(ItemStack{Name: in.Name, Meta: meta, Count: 4})
		before := p.inventory
		rec, ok := p.s.Recipes().Recipe(r.RecipeNetworkID)
		if !ok || !rec.Fits(crafting.Grid{Items: []crafting.Item{p.craftItem(before.ui[smallGridStart])}, Width: smallGridWidth}) {
			t.Fatalf("%s: the items should fit", r.RecipeID)
		}
		for name, a := range map[string]protocol.StackRequestAction{
			"by hand": &protocol.CraftRecipeStackRequestAction{RecipeNetworkID: r.RecipeNetworkID, NumberOfCrafts: 1},
			"auto":    &protocol.AutoCraftRecipeStackRequestAction{RecipeNetworkID: r.RecipeNetworkID, NumberOfCrafts: 1},
		} {
			got := p.stackRequest(protocol.ItemStackRequest{RequestID: 2, Actions: []protocol.StackRequestAction{a}})
			if got.Status != protocol.ItemStackResponseStatusFailedToFindRecipeByNetId {
				t.Errorf("%s %s: status %d", r.Block, name, got.Status)
			}
		}
	}
	if len(seen) < 3 {
		t.Fatalf("only tried %v", seen)
	}
	if !seen["furnace"] || !seen["stonecutter"] {
		t.Fatalf("tried %v", seen)
	}
}

// TestPlayerRecipes checks recipes taken from one player are gone from its list and
// refused when its client asks anyway, while another player keeps them.
func TestPlayerRecipes(t *testing.T) {
	p := craftTestPlayer(t, map[int]string{28: "minecraft:oak_planks", 30: "minecraft:oak_planks"})
	other := &Player{s: p.s}
	stick := recipeID(t, p, "minecraft:stick")
	craft := func(pl *Player) uint8 {
		_, status := pl.recipeFor(stick)
		return status
	}
	listed := func(pl *Player) bool {
		k := pl.recipesPacket()
		return slices.ContainsFunc(k.ShapedRecipes, func(r protocol.ShapedRecipe) bool { return r.RecipeNetworkID == stick }) ||
			slices.ContainsFunc(k.ShapelessRecipes, func(r protocol.ShapelessRecipe) bool { return r.RecipeNetworkID == stick })
	}
	check := func(name string, has bool) {
		t.Helper()
		want := uint8(protocol.ItemStackResponseStatusFailedToFindRecipeByNetId)
		if has {
			want = protocol.ItemStackResponseStatusOK
		}
		if got := craft(p); got != want || listed(p) != has {
			t.Errorf("%s: status %d, listed %v", name, got, listed(p))
		}
		if craft(other) != protocol.ItemStackResponseStatusOK || !listed(other) {
			t.Errorf("%s: the other player lost the recipe", name)
		}
		if !p.recipesPacket().ClearRecipes {
			t.Errorf("%s: the client would keep its old recipes", name)
		}
	}
	check("at the start", true)
	edits := []struct {
		name string
		edit func(r *PlayerRecipes)
	}{
		{"by ID", func(r *PlayerRecipes) { r.Remove("minecraft:stick") }},
		{"by item", func(r *PlayerRecipes) { r.RemoveFor("minecraft:stick") }},
		{"by block", func(r *PlayerRecipes) { r.ClearBlock(craftingTableRecipes) }},
		{"all", func(r *PlayerRecipes) { r.Clear() }},
	}
	for _, e := range edits {
		p.applyRecipeEdit(e.edit)
		check(e.name, false)
		p.applyRecipeEdit(func(r *PlayerRecipes) { r.Reset() })
		check("reset after "+e.name, true)
	}
	if p.recipesPacket() != p.s.reg.Crafting.Packet() {
		t.Error("a player with no rules should share the server's packet")
	}

	// A refused request changes nothing.
	p.applyRecipeEdit(func(r *PlayerRecipes) { r.RemoveFor("minecraft:stick") })
	req := protocol.ItemStackRequest{RequestID: 2, Actions: []protocol.StackRequestAction{
		&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: stick, NumberOfCrafts: 1}, consume(p, 28), consume(p, 30),
		take(4, slotInfo(containerCreatedOutput, createdOutputSlot, 0), slotInfo(containerCombined, 1, 0)),
	}}
	if r := p.stackRequest(req); r.Status != protocol.ItemStackResponseStatusFailedToFindRecipeByNetId || p.inventory.ui[28].Count != 4 {
		t.Errorf("hidden recipe: status %d, %d planks", r.Status, p.inventory.ui[28].Count)
	}

	// A rule follows the server's recipes: one added later is hidden as well.
	if err := p.s.reg.EditRecipes(func(s *registry.Recipes) error {
		return s.Set(registry.Recipe{
			ID: "test:stick", Input: []crafting.Ingredient{crafting.AnyVariant("minecraft:apple")},
			Output: []registry.RecipeItem{{Name: "minecraft:stick"}},
		})
	}); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(p.recipesPacket().ShapelessRecipes, func(r protocol.ShapelessRecipe) bool { return r.RecipeID == "test:stick" }) {
		t.Error("a recipe added later for a removed item was listed")
	}
	if !slices.ContainsFunc(other.recipesPacket().ShapelessRecipes, func(r protocol.ShapelessRecipe) bool { return r.RecipeID == "test:stick" }) {
		t.Error("the other player should get the new recipe")
	}

	// Smithing follows the same rules.
	sword := p.s.reg.Crafting.Packet().SmithingTransformRecipes[0]
	p.applyRecipeEdit(func(r *PlayerRecipes) { r.Reset(); r.Remove(sword.RecipeID) })
	p.window.open, p.window.smithing = true, true
	if status, _ := p.craftSmithing(&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: sword.RecipeNetworkID, NumberOfCrafts: 1}); status != protocol.ItemStackResponseStatusFailedToFindRecipeByNetId {
		t.Errorf("hidden smithing recipe: status %d", status)
	}
}

// TestRecipeAPI checks a plugin can clear, remove, change and add crafting recipes
// while running, and that crafting follows.
func TestRecipeAPI(t *testing.T) {
	p := craftTestPlayer(t, map[int]string{28: "minecraft:oak_planks", 30: "minecraft:oak_planks"})
	reg := p.s.reg
	id := recipeID(t, p, "minecraft:stick")
	sticks := func() protocol.ItemStackResponse {
		return p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: []protocol.StackRequestAction{
			&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: id, NumberOfCrafts: 1},
			consume(p, 28), consume(p, 30),
			take(4, slotInfo(containerCreatedOutput, createdOutputSlot, 0), slotInfo(containerCombined, 1, 0)),
		}})
	}

	// Change: the same recipe now makes something else, with the same ID.
	if err := reg.EditRecipes(func(s *registry.Recipes) error {
		r, ok := s.Get("minecraft:stick")
		if !ok || r.Block != "crafting_table" || !r.Shaped || r.Input[0].Tag != "minecraft:planks" {
			t.Errorf("read %+v", r)
		}
		r.Output = []registry.RecipeItem{{Name: "minecraft:apple", Count: 4}}
		return s.Set(r)
	}); err != nil {
		t.Fatal(err)
	}
	if got := recipeID(t, p, "minecraft:stick"); got != id {
		t.Errorf("changing a recipe moved its ID from %d to %d", id, got)
	}
	if r := sticks(); r.Status != protocol.ItemStackResponseStatusOK || p.inventory.main[1].Name != "minecraft:apple" {
		t.Fatalf("changed recipe: status %d, item %+v", r.Status, p.inventory.main[1])
	}
	p.inventory.main[1] = ItemStack{}

	// Add: a new recipe gets its own ID and can be crafted.
	var added uint32
	if err := reg.EditRecipes(func(s *registry.Recipes) error {
		return s.Set(registry.Recipe{
			ID: "test:diamond", Input: []crafting.Ingredient{crafting.Tagged("minecraft:planks")},
			Output: []registry.RecipeItem{{Name: "minecraft:diamond"}},
		})
	}); err != nil {
		t.Fatal(err)
	}
	added = recipeID(t, p, "test:diamond")
	if added == 0 || added == id {
		t.Errorf("new recipe's ID %d", added)
	}
	if r, ok := p.s.Recipes().Recipe(added); !ok || r.Block != "crafting_table" || !r.Fits(crafting.Grid{Width: 2, Items: []crafting.Item{p.craftItem(p.inventory.ui[28])}}) {
		t.Errorf("new recipe %+v", r)
	}

	// A failed edit changes nothing, however much it did first.
	before := len(reg.Crafting.Packet().ShapedRecipes)
	for name, bad := range map[string]registry.Recipe{
		"no ID":          {Input: []crafting.Ingredient{crafting.Tagged("x")}, Output: []registry.RecipeItem{{Name: "minecraft:apple"}}},
		"makes nothing":  {ID: "a", Input: []crafting.Ingredient{crafting.Tagged("x")}},
		"takes nothing":  {ID: "a", Output: []registry.RecipeItem{{Name: "minecraft:apple"}}},
		"unknown result": {ID: "a", Input: []crafting.Ingredient{crafting.Tagged("x")}, Output: []registry.RecipeItem{{Name: "minecraft:nothing"}}},
		"wrong shape":    {ID: "a", Shaped: true, Width: 2, Height: 2, Input: []crafting.Ingredient{crafting.Tagged("x")}, Output: []registry.RecipeItem{{Name: "minecraft:apple"}}},
		"over a stack":   {ID: "a", Input: []crafting.Ingredient{crafting.Tagged("x")}, Output: []registry.RecipeItem{{Name: "minecraft:netherite_sword", Count: 2}}},
	} {
		if err := reg.EditRecipes(func(s *registry.Recipes) error {
			s.Clear()
			return s.Set(bad)
		}); err == nil {
			t.Errorf("%s was taken", name)
		}
		if got := len(reg.Crafting.Packet().ShapedRecipes); got != before {
			t.Fatalf("%s: a failed edit changed the list", name)
		}
	}

	// Remove by ID, by item and by block.
	if err := reg.EditRecipes(func(s *registry.Recipes) error {
		if !s.Remove("test:diamond") || s.Remove("test:diamond") {
			t.Error("Remove by ID")
		}
		if s.RemoveFor("minecraft:apple") == 0 {
			t.Error("RemoveFor")
		}
		if s.ClearBlock("stonecutter") == 0 || len(s.ForBlock("stonecutter")) != 0 {
			t.Error("ClearBlock")
		}
		if len(s.ForBlock("furnace")) == 0 {
			t.Error("clearing one block took another's")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if r := sticks(); r.Status != protocol.ItemStackResponseStatusFailedToFindRecipeByNetId {
		t.Errorf("removed recipe: status %d", r.Status)
	}

	// Clear takes everything, smithing and brewing too.
	if err := reg.EditRecipes(func(s *registry.Recipes) error {
		s.Clear()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	k := reg.Crafting.Packet()
	if n := len(k.ShapedRecipes) + len(k.ShapelessRecipes) + len(k.UserDataShapelessRecipes) + len(k.MultiRecipes) + len(k.SmithingTransformRecipes) + len(k.SmithingTrimRecipes) + len(k.PotionRecipes); n != 0 {
		t.Errorf("%d recipes left after clear", n)
	}
}
