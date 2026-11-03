package player

import (
	"testing"

	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
)

// smithPlayer has a smithing table open, and an enchanted diamond sword, a netherite
// ingot and a template in its slots.
func smithPlayer(t *testing.T, register func(*Setup)) *Player {
	t.Helper()
	p := testPlayer(t)
	craftGame(t, p, register)
	p.window.open, p.window.smithing = true, true
	p.inventory.ui[smithTemplateSlot] = p.s.freshStack(ItemStack{Name: "minecraft:netherite_upgrade_smithing_template", Count: 2})
	p.inventory.ui[smithBaseSlot] = p.s.freshStack(ItemStack{Name: "minecraft:diamond_sword", Count: 1, Tag: map[string]any{"Damage": int32(7)}})
	p.inventory.ui[smithAdditionSlot] = p.s.freshStack(ItemStack{Name: "minecraft:netherite_ingot", Count: 3})
	return p
}

func smithRecipeID(t *testing.T, p *Player, id string) uint32 {
	t.Helper()
	for _, r := range p.s.reg.Crafting.Packet().SmithingTransformRecipes {
		if r.RecipeID == id {
			return r.RecipeNetworkID
		}
	}
	t.Fatalf("no smithing recipe %s", id)
	return 0
}

func smithConsume(p *Player, container, slot byte) *protocol.ConsumeStackRequestAction {
	a := &protocol.ConsumeStackRequestAction{}
	a.Count, a.Source = 1, slotInfo(container, slot, p.inventory.ui[slot].netID)
	return a
}

// smithActions are what a client asks for to use the table.
func smithActions(p *Player, id uint32) []protocol.StackRequestAction {
	return []protocol.StackRequestAction{
		&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: id, NumberOfCrafts: 1},
		smithConsume(p, protocol.ContainerSmithingTableTemplate, smithTemplateSlot),
		smithConsume(p, protocol.ContainerSmithingTableInput, smithBaseSlot),
		smithConsume(p, protocol.ContainerSmithingTableMaterial, smithAdditionSlot),
		take(1, slotInfo(containerCreatedOutput, createdOutputSlot, 0), slotInfo(containerCombined, 1, 0)),
	}
}

// TestSmithing runs an upgrade: the result keeps the base's data, one of each is used.
func TestSmithing(t *testing.T) {
	p := smithPlayer(t, nil)
	id := smithRecipeID(t, p, "minecraft:smithing_netherite_sword")
	if r := p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: smithActions(p, id)}); r.Status != protocol.ItemStackResponseStatusOK {
		t.Fatalf("refused: %d", r.Status)
	}
	s := p.inventory.main[1]
	if s.Name != "minecraft:netherite_sword" || itemDamage(s) != 7 {
		t.Errorf("result %+v", s)
	}
	ui := p.inventory.ui
	if ui[smithTemplateSlot].Count != 1 || !ui[smithBaseSlot].Empty() || ui[smithAdditionSlot].Count != 2 {
		t.Errorf("slots %d %d %d", ui[smithTemplateSlot].Count, ui[smithBaseSlot].Count, ui[smithAdditionSlot].Count)
	}
}

// TestSmithingAPI checks a plugin can clear, remove and change recipes while running,
// and that the table follows.
func TestSmithingAPI(t *testing.T) {
	p := smithPlayer(t, nil)
	sword := "minecraft:smithing_netherite_sword"
	id := smithRecipeID(t, p, sword)
	reg := p.s.reg

	if err := reg.EditSmithing(func(s *registry.Smithing) error {
		r, ok := s.Recipe(sword)
		if !ok || r.Result != "minecraft:netherite_sword" || r.Base.Name != "minecraft:diamond_sword" {
			t.Errorf("read %+v", r)
		}
		r.Result = "minecraft:apple"
		return s.Set(r)
	}); err != nil {
		t.Fatal(err)
	}
	if got := smithRecipeID(t, p, sword); got != id {
		t.Errorf("changing a recipe moved its ID from %d to %d", id, got)
	}
	if r := p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: smithActions(p, id)}); r.Status != protocol.ItemStackResponseStatusOK || p.inventory.main[1].Name != "minecraft:apple" {
		t.Fatalf("changed recipe: status %d, item %+v", r.Status, p.inventory.main[1])
	}

	var removed int
	if err := reg.EditSmithing(func(s *registry.Smithing) error {
		removed = s.RemoveFor("minecraft:netherite_axe")
		if !s.Remove("minecraft:smithing_netherite_hoe") || s.Remove("minecraft:nothing") {
			t.Error("Remove by ID")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("RemoveFor removed %d", removed)
	}

	before := len(reg.Crafting.Packet().SmithingTransformRecipes)
	if err := reg.EditSmithing(func(s *registry.Smithing) error {
		s.Clear()
		return s.Set(registry.SmithingRecipe{ID: "x", Base: crafting.Tagged("t"), Addition: crafting.AnyVariant("a"), Result: "minecraft:nothing"})
	}); err == nil {
		t.Error("an unknown result was taken")
	}
	if got := len(reg.Crafting.Packet().SmithingTransformRecipes); got != before {
		t.Errorf("a failed edit changed the list: %d to %d", before, got)
	}

	if err := reg.EditSmithing(func(s *registry.Smithing) error {
		s.Clear()
		return s.Set(registry.SmithingRecipe{
			ID: "test:gold", Base: crafting.AnyVariant("minecraft:stick"), Addition: crafting.AnyVariant("minecraft:gold_ingot"),
			Result: "minecraft:golden_sword",
		})
	}); err != nil {
		t.Fatal(err)
	}
	pk := reg.Crafting.Packet()
	if len(pk.SmithingTransformRecipes) != 1 || len(pk.SmithingTrimRecipes) != 0 || pk.SmithingTransformRecipes[0].RecipeNetworkID == 0 {
		t.Fatalf("after clear: %d transforms, %d trims", len(pk.SmithingTransformRecipes), len(pk.SmithingTrimRecipes))
	}
	if _, ok := p.s.Recipes().Smithing(id); ok {
		t.Error("a cleared recipe still works")
	}
	if !p.s.Recipes().SmithingAllows(crafting.SmithingBase, p.craftItem(ItemStack{Name: "minecraft:stick", Count: 1})) {
		t.Error("a plugin's base is not let into the slot")
	}
	if p.s.Recipes().SmithingAllows(crafting.SmithingBase, p.craftItem(ItemStack{Name: "minecraft:apple", Count: 1})) {
		t.Error("an apple went in the base slot")
	}
}

// TestSmithEvent checks a plugin can refuse or change one use of the table.
func TestSmithEvent(t *testing.T) {
	refuse := false
	p := smithPlayer(t, func(s *Setup) {
		if err := OnSmith(s, func(e *SmithEvent) {
			if refuse {
				e.Cancel()
				return
			}
			e.Result.Name = "minecraft:apple"
		}, Normal); err != nil {
			t.Fatal(err)
		}
	})
	id := smithRecipeID(t, p, "minecraft:smithing_netherite_sword")
	refuse = true
	if r := p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: smithActions(p, id)}); r.Status != protocol.ItemStackResponseStatusInvalidCraftRequest || p.inventory.ui[smithBaseSlot].Empty() {
		t.Errorf("refuse: status %d", r.Status)
	}
	refuse = false
	if r := p.stackRequest(protocol.ItemStackRequest{RequestID: 2, Actions: smithActions(p, id)}); r.Status != protocol.ItemStackResponseStatusOK || p.inventory.main[1].Name != "minecraft:apple" || !p.requests.refresh {
		t.Errorf("change: status %d, item %+v", r.Status, p.inventory.main[1])
	}
}

// TestSmithingClientCannotLie is the same for the smithing table.
func TestSmithingClientCannotLie(t *testing.T) {
	p := smithPlayer(t, nil)
	sword := smithRecipeID(t, p, "minecraft:smithing_netherite_sword")
	axe := smithRecipeID(t, p, "minecraft:smithing_netherite_axe")
	output := slotInfo(containerCreatedOutput, createdOutputSlot, 0)
	inv := slotInfo(containerCombined, 1, 0)
	craft := &protocol.CraftRecipeStackRequestAction{RecipeNetworkID: sword, NumberOfCrafts: 1}
	for name, c := range map[string]struct {
		actions []protocol.StackRequestAction
		want    uint8
	}{
		"takes the result, uses nothing":      {[]protocol.StackRequestAction{craft, take(1, output, inv)}, protocol.ItemStackResponseStatusExpectedItemSlotNotFullyConsumed},
		"keeps the ingot":                     {[]protocol.StackRequestAction{craft, smithConsume(p, protocol.ContainerSmithingTableTemplate, smithTemplateSlot), smithConsume(p, protocol.ContainerSmithingTableInput, smithBaseSlot), take(1, output, inv)}, protocol.ItemStackResponseStatusExpectedItemSlotNotFullyConsumed},
		"names a recipe it has no items for":  {[]protocol.StackRequestAction{&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: axe, NumberOfCrafts: 1}}, protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems},
		"names a recipe that is not smithing": {[]protocol.StackRequestAction{&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: recipeID(t, p, "minecraft:stick"), NumberOfCrafts: 1}}, protocol.ItemStackResponseStatusFailedToFindRecipeByNetId},
	} {
		if r := p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: c.actions}); r.Status != c.want {
			t.Errorf("%s: status %d, want %d", name, r.Status, c.want)
		}
		if p.inventory.ui[smithBaseSlot].Name != "minecraft:diamond_sword" || p.inventory.ui[smithAdditionSlot].Count != 3 || !p.inventory.main[1].Empty() {
			t.Fatalf("%s changed items", name)
		}
	}

	// Each slot takes only what the table allows, and without the template no upgrade.
	apple := p.s.freshStack(ItemStack{Name: "minecraft:apple", Count: 1})
	p.inventory.main[7] = apple
	into := take(1, slotInfo(containerCombined, 7, apple.netID), slotInfo(protocol.ContainerSmithingTableTemplate, smithTemplateSlot, 0))
	p.inventory.ui[smithTemplateSlot] = ItemStack{}
	if r := p.stackRequest(protocol.ItemStackRequest{RequestID: 2, Actions: []protocol.StackRequestAction{into}}); r.Status != protocol.ItemStackResponseStatusCannotPlaceItem {
		t.Errorf("apple as a template: status %d", r.Status)
	}
	if r := p.stackRequest(protocol.ItemStackRequest{RequestID: 3, Actions: []protocol.StackRequestAction{craft}}); r.Status != protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems {
		t.Errorf("no template: status %d", r.Status)
	}
}

// TestArmourTrim runs every pattern and material through the trim recipe, keeps the
// armour's own data, and refuses what BDS refuses.
func TestArmourTrim(t *testing.T) {
	p := testPlayer(t)
	craftGame(t, p, nil)
	p.window.open, p.window.smithing = true, true
	trims := p.s.reg.Trims.Packet()
	if len(trims.Patterns) != 18 || len(trims.Materials) != 11 {
		t.Fatalf("%d patterns, %d materials", len(trims.Patterns), len(trims.Materials))
	}
	pk := p.s.reg.Crafting.Packet()
	if len(pk.SmithingTrimRecipes) != 1 {
		t.Fatalf("%d trim recipes", len(pk.SmithingTrimRecipes))
	}
	id := pk.SmithingTrimRecipes[0].RecipeNetworkID

	fill := func(template, base, addition string, tag map[string]any) {
		p.inventory.ui[smithTemplateSlot] = p.s.freshStack(ItemStack{Name: template, Count: 1})
		p.inventory.ui[smithBaseSlot] = p.s.freshStack(ItemStack{Name: base, Count: 1, Tag: tag})
		p.inventory.ui[smithAdditionSlot] = p.s.freshStack(ItemStack{Name: addition, Count: 1})
		p.inventory.main[1] = ItemStack{}
	}
	request := func() protocol.ItemStackResponse {
		return p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: smithActions(p, id)})
	}

	for _, pattern := range trims.Patterns {
		for _, material := range trims.Materials {
			fill(pattern.ItemName, "minecraft:iron_chestplate", material.ItemName, map[string]any{"Damage": int32(9)})
			if r := request(); r.Status != protocol.ItemStackResponseStatusOK {
				t.Fatalf("%s + %s refused: %d", pattern.PatternID, material.MaterialID, r.Status)
			}
			s := p.inventory.main[1]
			trim, _ := s.Tag[trimTag].(map[string]any)
			if s.Name != "minecraft:iron_chestplate" || itemDamage(s) != 9 || trim[trimPattern] != pattern.PatternID || trim[trimMaterial] != material.MaterialID {
				t.Fatalf("%s + %s made %+v", pattern.PatternID, material.MaterialID, s)
			}
		}
	}

	tag := func(pattern, material string) map[string]any {
		return map[string]any{trimTag: map[string]any{trimMaterial: material, trimPattern: pattern}}
	}
	wild := "minecraft:wild_armor_trim_smithing_template"
	fill(wild, "minecraft:iron_chestplate", "minecraft:diamond", tag("wild", "diamond"))
	if r := request(); r.Status != protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems {
		t.Errorf("the same trim again: status %d", r.Status)
	}
	fill(wild, "minecraft:iron_chestplate", "minecraft:gold_ingot", tag("wild", "diamond"))
	if r := request(); r.Status != protocol.ItemStackResponseStatusOK {
		t.Errorf("a new material over an old trim: status %d", r.Status)
	}
	fill(wild, "minecraft:iron_sword", "minecraft:gold_ingot", nil)
	if r := request(); r.Status != protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems {
		t.Errorf("trimming a sword: status %d", r.Status)
	}
	fill("minecraft:netherite_upgrade_smithing_template", "minecraft:iron_chestplate", "minecraft:gold_ingot", nil)
	if r := request(); r.Status != protocol.ItemStackResponseStatusMismatchedRecipeForInputGridItems {
		t.Errorf("a template that is no pattern: status %d", r.Status)
	}
}

// TestSmithingEveryUpgrade runs every vanilla upgrade from the server's own items.
func TestSmithingEveryUpgrade(t *testing.T) {
	p := testPlayer(t)
	craftGame(t, p, nil)
	p.window.open, p.window.smithing = true, true
	for _, r := range p.s.reg.Crafting.Packet().SmithingTransformRecipes {
		recipe, ok := p.s.Recipes().Smithing(r.RecipeNetworkID)
		if !ok {
			t.Fatalf("%s is not in the table", r.RecipeID)
		}
		p.inventory.ui[smithTemplateSlot] = p.s.freshStack(ItemStack{Name: recipe.Template.Name, Count: 1})
		p.inventory.ui[smithBaseSlot] = p.s.freshStack(ItemStack{Name: recipe.Base.Name, Count: 1})
		p.inventory.ui[smithAdditionSlot] = p.s.freshStack(ItemStack{Name: recipe.Addition.Name, Count: 1})
		p.inventory.main[1] = ItemStack{}
		if resp := p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: smithActions(p, r.RecipeNetworkID)}); resp.Status != protocol.ItemStackResponseStatusOK {
			t.Errorf("%s (%s, %s, %s) refused: %d", r.RecipeID, recipe.Template.Name, recipe.Base.Name, recipe.Addition.Name, resp.Status)
			continue
		}
		if want := itemFromNetwork(p.s.reg, r.Result); p.inventory.main[1].Name != want.Name {
			t.Errorf("%s made %s, want %s", r.RecipeID, p.inventory.main[1].Name, want.Name)
		}
	}
}
