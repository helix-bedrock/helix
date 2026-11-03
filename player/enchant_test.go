package player

import (
	"math/rand/v2"
	"testing"

	"github.com/helix-bedrock/protocol"
)

// enchantPlayer is a survival player on a host with vanilla's gameplay set up.
func enchantPlayer(t *testing.T) *Player {
	t.Helper()
	p := testPlayer(t)
	craftGame(t, p, nil)
	p.rng = rand.New(rand.NewPCG(1, 2))
	return p
}

func setupForTest(t *testing.T) *Setup {
	t.Helper()
	s, err := NewSetup(nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func stack(name string, damage int32, enchants ...EnchantLevel) ItemStack {
	s := ItemStack{Name: name, Count: 1}
	if damage != 0 {
		s = withDamage(s, damage)
	}
	if len(enchants) > 0 {
		var l enchantList
		l.lists[0] = enchants
		writeEnchants(&s, l)
	}
	return s
}

// TestMT pins BDS's generator to the standard one: the first result for seed 5489.
func TestMT(t *testing.T) {
	if got := newMT(5489).u32(); got != 3499211612 {
		t.Fatalf("first result %d", got)
	}
}

// TestDraw checks a draw is fixed by its seed, stays on the item's slot and has no
// two enchantments that exclude each other.
func TestDraw(t *testing.T) {
	g := enchantPlayer(t).s.game
	for seed := range uint32(200) {
		l := g.drawEnchants(newMT(seed), "minecraft:diamond_sword", 30, 0, false)
		again := g.drawEnchants(newMT(seed), "minecraft:diamond_sword", 30, 0, false)
		seen := map[int32]bool{}
		n := 0
		for i, list := range l.lists {
			for j, v := range list {
				n++
				if again.lists[i][j] != v {
					t.Fatalf("seed %d: draw changed", seed)
				}
				e, _ := g.enchant(v.ID)
				if e.Slots&16 == 0 && e.SecondarySlots&16 == 0 {
					t.Fatalf("seed %d: %s on a sword", seed, e.Name)
				}
				if e.Treasure {
					t.Fatalf("seed %d: treasure %s", seed, e.Name)
				}
				if e.Group != 0 && seen[e.Group] {
					t.Fatalf("seed %d: two of group %d", seed, e.Group)
				}
				seen[e.Group] = true
			}
		}
		if n == 0 {
			t.Fatalf("seed %d: nothing drawn", seed)
		}
	}
}

// TestOptionCosts pins BDS's three costs with 15 shelves: the last is always 30.
func TestOptionCosts(t *testing.T) {
	for seed := range uint32(50) {
		c := optionCosts(newMT(seed), 40, enchantMaxShelves)
		if c[2] != 30 || c[0] < 1 || c[1] <= c[0] {
			t.Fatalf("seed %d: costs %v", seed, c)
		}
	}
}

// TestAnvil runs BDS's paths: a material, two of one item, a book, a rename, and the
// price of earlier work.
func TestAnvil(t *testing.T) {
	p := enchantPlayer(t)
	p.attributes.set(attrLevel, 30)

	sword := stack("minecraft:diamond_sword", 1000)
	out := vanillaAnvil(p, sword, ItemStack{Name: "minecraft:diamond", Count: 5}, "")
	// 1561/4 = 390 per diamond; 1000 takes three steps at 390, 390, 220.
	if d := itemDamage(out.Result); d != 0 || out.Cost != 3 || out.SecondUsed != 3 {
		t.Fatalf("material: damage %d cost %d used %d", d, out.Cost, out.SecondUsed)
	}
	if repairCost(out.Result) != 1 {
		t.Fatalf("material: price %d", repairCost(out.Result))
	}

	a, b := stack("minecraft:iron_sword", 200), stack("minecraft:iron_sword", 200)
	out = vanillaAnvil(p, a, b, "")
	// 200+200-250-30 = 120; repairing alone costs 2.
	if itemDamage(out.Result) != 120 || out.Cost != 2 {
		t.Fatalf("merge: damage %d cost %d", itemDamage(out.Result), out.Cost)
	}

	book := stack(itemEnchantedBook, 0, EnchantLevel{ID: EnchantSharpness, Level: 3})
	out = vanillaAnvil(p, stack("minecraft:iron_sword", 0), book, "")
	if !hasEnchant(out.Result, EnchantSharpness) || out.Cost != 3 {
		t.Fatalf("book: cost %d", out.Cost)
	}

	out = vanillaAnvil(p, stack("minecraft:stick", 0), ItemStack{}, "Wand")
	if customName(out.Result) != "Wand" || out.Cost != 1 || repairCost(out.Result) != 0 {
		t.Fatalf("name: %q cost %d price %d", customName(out.Result), out.Cost, repairCost(out.Result))
	}

	if out = vanillaAnvil(p, stack("minecraft:stick", 0), ItemStack{}, ""); !out.Result.Empty() {
		t.Fatal("nothing to do made a result")
	}

	dear := withTag(stack("minecraft:iron_sword", 200), repairCostTag, int32(40))
	if out = vanillaAnvil(p, dear, stack("minecraft:iron_sword", 200), ""); !out.TooExpensive {
		t.Fatal("not too expensive")
	}
	p.SetTuning(TuneAnvilTooExpensive, 1000)
	if out = vanillaAnvil(p, dear, stack("minecraft:iron_sword", 200), ""); out.TooExpensive {
		t.Fatal("the limit did not change")
	}
	p.ClearAllTuning()
	if out = vanillaAnvil(p, dear, ItemStack{}, "Old"); out.Cost != anvilNameOnly {
		t.Fatalf("name only held at %d", out.Cost)
	}
}

// TestGrindstone runs BDS's paths: enchantments off, curses kept and priced, a book back
// to a book, two items merged, and nothing to do.
func TestGrindstone(t *testing.T) {
	p := enchantPlayer(t)
	out := vanillaGrindstone(p, stack("minecraft:iron_sword", 0, EnchantLevel{ID: EnchantSharpness, Level: 2}, EnchantLevel{ID: enchantVanishing, Level: 1}), ItemStack{})
	if hasEnchant(out.Result, EnchantSharpness) || !hasEnchant(out.Result, enchantVanishing) || repairCost(out.Result) != 1 || out.Experience != 12 {
		t.Fatalf("curse: %+v", out)
	}
	out = vanillaGrindstone(p, stack(itemEnchantedBook, 0, EnchantLevel{ID: EnchantSharpness, Level: 1}), ItemStack{})
	if out.Result.Name != itemBook {
		t.Fatalf("book: %s", out.Result.Name)
	}
	out = vanillaGrindstone(p, stack("minecraft:iron_sword", 200), stack("minecraft:iron_sword", 100))
	// 200+100-250-12 = 38.
	if itemDamage(out.Result) != 38 {
		t.Fatalf("merge: %d", itemDamage(out.Result))
	}
	if out = vanillaGrindstone(p, stack("minecraft:iron_sword", 5), ItemStack{}); !out.Result.Empty() {
		t.Fatal("plain sword made a result")
	}
}

// TestAddRules pins the list rules: the same level goes one up, the maximum stays, and a
// group refuses.
func TestAddRules(t *testing.T) {
	g := enchantPlayer(t).s.game
	l := enchantList{slot: allSlots}
	for range 2 {
		if !g.add(&l, EnchantLevel{ID: EnchantSharpness, Level: 3}) {
			t.Fatal("refused")
		}
	}
	if got := l.lists[g.activationOf(EnchantSharpness)][0].Level; got != 4 {
		t.Fatalf("level %d", got)
	}
	if g.add(&l, EnchantLevel{ID: 10, Level: 1}) {
		t.Fatal("smite went on with sharpness")
	}
	l = enchantList{slot: allSlots}
	g.add(&l, EnchantLevel{ID: EnchantRiptide, Level: 1})
	if g.add(&l, EnchantLevel{ID: EnchantChanneling, Level: 1}) {
		t.Fatal("channeling went on with riptide")
	}
}

// TestEditEnchantment changes a default while running.
func TestEditEnchantment(t *testing.T) {
	p := enchantPlayer(t)
	if !p.s.EditEnchantment(EnchantSharpness, func(e *Enchantment) { e.MaxLevel = 9 }) {
		t.Fatal("not found")
	}
	if e, _ := p.s.Enchantment(EnchantSharpness); e.MaxLevel != 9 {
		t.Fatalf("max %d", e.MaxLevel)
	}
}

// TestEnchantAction checks what a table's option asks for, and its level check.
func TestEnchantAction(t *testing.T) {
	p, _ := apiWorld(t, func(*Setup) {})
	p.rng = rand.New(rand.NewPCG(1, 2))
	p.attributes.set(attrLevel, 30)
	p.window.enchanting, p.window.open = true, true
	sword := p.s.freshStack(stack("minecraft:iron_sword", 0))
	lapis := p.s.freshStack(ItemStack{Name: itemLapis, Count: 5})
	p.inventory.ui[enchantInputSlot], p.inventory.ui[enchantLapisSlot] = sword, lapis
	p.enchant.seeded = true
	p.enchant.options = vanillaEnchantOptions(p, sword, 15)
	if len(p.enchant.options) != enchantOptionCount {
		t.Fatalf("%d options", len(p.enchant.options))
	}
	p.requests.work = p.inventory
	status, _ := p.craftEnchant(&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: p.enchant.options[2].NetworkID, NumberOfCrafts: 1})
	c := &p.requests.craft
	if status != protocol.ItemStackResponseStatusOK || c.after.levels != 3 || !c.after.newSeed {
		t.Fatalf("status %d levels %d", status, c.after.levels)
	}
	if len(c.slots) != 2 || c.slots[0].left != 1 || c.slots[1].left != 3 {
		t.Fatalf("slots %+v", c.slots)
	}
	if res := c.results[0].stack; res.Name != "minecraft:iron_sword" || res.Tag[enchantTag] == nil {
		t.Fatalf("result %+v", res)
	}
	p.requests.craft = craftState{}
	p.attributes.set(attrLevel, 1)
	if status, _ = p.craftEnchant(&protocol.CraftRecipeStackRequestAction{RecipeNetworkID: p.enchant.options[2].NetworkID, NumberOfCrafts: 1}); status != protocol.ItemStackResponseStatusInsufficientPlayerLevelToEnchant {
		t.Fatalf("level 1 got status %d", status)
	}
}

// TestMending pins BDS's sum: a value of 5 repairs up to 10, and half of it is used.
func TestMending(t *testing.T) {
	p := enchantPlayer(t)
	inv := &p.inventory
	inv.held = 3
	inv.main[3] = stack("minecraft:iron_sword", 7, EnchantLevel{ID: EnchantMending, Level: 1})
	picks := p.mendable()
	if len(picks) != 1 || picks[0] != &inv.main[3] {
		t.Fatalf("picks %v", picks)
	}
	if rest := mendStack(picks[0], 5); rest != 2 || itemDamage(inv.main[3]) != 0 {
		t.Fatalf("rest %d damage %d", rest, itemDamage(inv.main[3]))
	}
	if picks = p.mendable(); len(picks) != 0 {
		t.Fatalf("a whole item is still mendable: %v", picks)
	}
}

// TestRemoved checks a removed enchantment stops counting on items and in draws.
func TestRemoved(t *testing.T) {
	p := enchantPlayer(t)
	g := p.s.game
	cursed := stack("minecraft:iron_helmet", 0, EnchantLevel{ID: enchantBinding, Level: 1})
	worn := func(e *Enchantment) bool { return e.StaysWorn }
	if !g.itemHas(cursed, worn) {
		t.Fatal("binding does nothing")
	}
	p.s.RemoveEnchantment(enchantBinding)
	if g.itemHas(cursed, worn) {
		t.Fatal("removed binding still counts")
	}
	p.s.RemoveEnchantment(EnchantSharpness)
	for seed := range uint32(100) {
		l := g.drawEnchants(newMT(seed), "minecraft:diamond_sword", 30, 0, false)
		for _, list := range l.lists {
			for _, v := range list {
				if v.ID == EnchantSharpness {
					t.Fatal("removed sharpness drawn")
				}
			}
		}
	}
	s := setupForTest(t)
	if err := s.RemoveEnchantment(EnchantSharpness); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Enchantment(EnchantSharpness); ok {
		t.Fatal("still registered")
	}
}

// TestRequire lets a plugin ask for nothing, with no lapis there, and for less than the
// client will use, which is settled after the request.
func TestRequire(t *testing.T) {
	p, _ := apiWorld(t, func(s *Setup) {
		s.Systems().EnchantRequire = func(*Player, EnchantOption, int32) EnchantRequirement { return EnchantRequirement{} }
	})
	p.rng = rand.New(rand.NewPCG(1, 2))
	p.window.enchanting, p.window.open = true, true
	sword := p.s.freshStack(stack("minecraft:iron_sword", 0))
	p.inventory.ui[enchantInputSlot] = sword
	p.enchant.seeded = true
	p.enchant.options = vanillaEnchantOptions(p, sword, 15)
	id := p.enchant.options[2].NetworkID
	action := &protocol.CraftRecipeStackRequestAction{RecipeNetworkID: id, NumberOfCrafts: 1}
	p.requests.work = p.inventory
	if status, _ := p.craftEnchant(action); status != protocol.ItemStackResponseStatusOK {
		t.Fatalf("no lapis, level 0: status %d", status)
	}
	c := &p.requests.craft
	if len(c.slots) != 1 || c.after.levels != 0 {
		t.Fatalf("slots %+v levels %d", c.slots, c.after.levels)
	}

	p.requests.craft = craftState{}
	p.inventory.ui[enchantLapisSlot] = p.s.freshStack(ItemStack{Name: itemLapis, Count: 5})
	p.requests.work = p.inventory
	if status, _ := p.craftEnchant(action); status != protocol.ItemStackResponseStatusOK {
		t.Fatalf("with lapis: status %d", status)
	}
	if !c.slots[1].upTo || c.slots[1].left != 5 || !c.after.settle || c.after.materialLeft != 5 {
		t.Fatalf("not settled: %+v %+v", c.slots[1], c.after)
	}
	p.inventory.ui[enchantLapisSlot].Count = 2
	p.afterCraft(craftAfter{settle: true, materialLeft: 5})
	if p.inventory.ui[enchantLapisSlot].Count != 5 {
		t.Fatalf("lapis %d", p.inventory.ui[enchantLapisSlot].Count)
	}
}

// TestLiveRules changes an item's enchant rule while running.
func TestLiveRules(t *testing.T) {
	p := enchantPlayer(t)
	g := p.s.game
	p.s.SetItemEnchantable("minecraft:stick", 16, 10)
	if g.rules().items["minecraft:stick"].Value != 10 {
		t.Fatal("not set")
	}
	if l := g.drawEnchants(newMT(1), "minecraft:stick", 30, 0, false); len(l.lists[0])+len(l.lists[1])+len(l.lists[2]) == 0 {
		t.Fatal("a stick draws nothing")
	}
	p.s.SetEnchantMaterial("minecraft:emerald")
	if !p.mayPlace(slotRef{kind: protocol.ContainerEnchantingMaterial}, ItemStack{Name: "minecraft:emerald", Count: 1}) {
		t.Fatal("emerald refused")
	}
	it := ItemStack{Name: "minecraft:stick", Count: 1}.WithEnchant(EnchantSharpness, 99)
	if it.Enchants()[0].Level != 99 || len(it.WithoutEnchant(EnchantSharpness).Enchants()) != 0 {
		t.Fatalf("item api: %+v", it.Enchants())
	}
}
