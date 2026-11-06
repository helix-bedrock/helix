package player

import (
	"testing"

	"github.com/helix-bedrock/protocol"
)

// TestCraftRepair repairs two pickaxes in the inventory grid as a client asks for it:
// a twentieth is given back, a curse stays and other enchantments go.
func TestCraftRepair(t *testing.T) {
	p := testPlayer(t)
	craftGame(t, p, nil)
	p.window.open, p.window.kind = true, ContainerInventory
	pick := ItemStack{Name: "minecraft:iron_pickaxe", Count: 1}
	first := withDamage(pick, 200).WithEnchant(enchantVanishing, 1).WithEnchant(15, 3)
	p.inventory.ui[28] = p.s.freshStack(first)
	p.inventory.ui[31] = p.s.freshStack(withDamage(pick, 150))
	req := protocol.ItemStackRequest{RequestID: 1, Actions: []protocol.StackRequestAction{
		&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: specialRecipeID(t, p, repairItemRecipe), NumberOfCrafts: 1},
		&protocol.CraftResultsDeprecatedStackRequestAction{TimesCrafted: 1},
		consume(p, 28), consume(p, 31),
		take(1, slotInfo(containerCreatedOutput, createdOutputSlot, 0), slotInfo(containerCombined, 1, 0)),
	}}
	if r := p.stackRequest(req); r.Status != protocol.ItemStackResponseStatusOK {
		t.Fatalf("refused: %d", r.Status)
	}
	got := p.inventory.main[1]
	// 200 + 150 - 250 - 250/20.
	if got.Name != pick.Name || itemDamage(got) != 88 {
		t.Fatalf("result %+v", got)
	}
	if e := got.Enchants(); len(e) != 1 || e[0].ID != enchantVanishing {
		t.Fatalf("enchants %+v", e)
	}
	if !p.inventory.ui[28].Empty() || !p.inventory.ui[31].Empty() {
		t.Fatal("inputs not used")
	}
}

// TestCraftRepairRules checks what BDS's recipe refuses, and spears, which repair through
// their component and keep their data.
func TestCraftRepairRules(t *testing.T) {
	p := testPlayer(t)
	craftGame(t, p, nil)
	pick := withDamage(ItemStack{Name: "minecraft:iron_pickaxe", Count: 1}, 10)
	cases := map[string][]ItemStack{
		"one item":      {pick},
		"three items":   {pick, pick, pick},
		"two kinds":     {pick, withDamage(ItemStack{Name: "minecraft:iron_axe", Count: 1}, 10)},
		"a stack":       {pick, withTag(pick, "Damage", int32(10))},
		"no durability": {{Name: "minecraft:stick", Count: 1}, {Name: "minecraft:stick", Count: 1}},
	}
	cases["a stack"][1].Count = 2
	for name, grid := range cases {
		if got := vanillaCraftRepair(p, grid); !got.Empty() {
			t.Errorf("%s: made %+v", name, got)
		}
	}
	spear := ItemStack{Name: "minecraft:iron_spear", Count: 1}
	named := withTag(withDamage(spear, 200), "display", map[string]any{"Name": "old"})
	got := vanillaCraftRepair(p, []ItemStack{{}, named, withDamage(spear, 100)})
	// Each way round leaves 50; the first is kept, name and all.
	if itemDamage(got) != 50 || got.Tag["display"] == nil {
		t.Fatalf("spear %+v", got)
	}
}
