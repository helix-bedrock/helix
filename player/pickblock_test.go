package player

import "testing"

func pickTestStack(name string) ItemStack { return ItemStack{Name: name, Count: 1} }

// Survival only moves what the inventory holds; creative makes what it does not.
func TestPick(t *testing.T) {
	stone, dirt, sand := pickTestStack("minecraft:stone"), pickTestStack("minecraft:dirt"), pickTestStack("minecraft:sand")

	var inv Inventory
	inv.main[4] = stone
	if !inv.pick(stone, false) || inv.held != 4 {
		t.Errorf("in hotbar: held %d, want 4", inv.held)
	}

	inv = Inventory{}
	if inv.pick(stone, false) {
		t.Error("survival made an item")
	}

	inv = Inventory{held: 1}
	inv.main[0] = dirt
	inv.main[20] = stone
	if !inv.pick(stone, false) || inv.held != 1 || inv.main[1].Name != stone.Name || !inv.main[20].Empty() {
		t.Errorf("to free slot: held %d, slot 1 %q, slot 20 %q", inv.held, inv.main[1].Name, inv.main[20].Name)
	}

	inv = Inventory{held: 2}
	for i := range hotbarSize {
		inv.main[i] = dirt
	}
	inv.main[20] = stone
	if !inv.pick(stone, false) || inv.held != 2 || inv.main[2].Name != stone.Name || inv.main[20].Name != dirt.Name {
		t.Errorf("swap: held %d, slot 2 %q, slot 20 %q", inv.held, inv.main[2].Name, inv.main[20].Name)
	}

	inv = Inventory{held: 3}
	for i := range hotbarSize {
		inv.main[i] = dirt
	}
	if !inv.pick(sand, true) || inv.main[3].Name != sand.Name || inv.main[hotbarSize].Name != dirt.Name {
		t.Errorf("creative full hotbar: slot 3 %q, slot %d %q", inv.main[3].Name, hotbarSize, inv.main[hotbarSize].Name)
	}

	inv = Inventory{held: 3}
	for i := range hotbarSize {
		inv.main[i] = dirt
	}
	inv.main[3].Tag = map[string]any{"minecraft:item_lock": uint8(itemLockInSlot)}
	if inv.pick(sand, true) {
		t.Error("replaced an item locked in its slot")
	}
}

// Blocks give their item, or nothing when they have none.
func TestPickedItem(t *testing.T) {
	p := testPlayer(t)
	want := map[string]string{
		"minecraft:stone":            "minecraft:stone",
		"minecraft:wheat":            "minecraft:wheat_seeds",
		"minecraft:redstone_wire":    "minecraft:redstone",
		"minecraft:powered_repeater": "minecraft:repeater",
		"minecraft:lit_furnace":      "minecraft:furnace",
		"minecraft:oak_double_slab":  "minecraft:oak_slab",
		"minecraft:air":              "",
	}
	for block, item := range want {
		id, ok := p.s.reg.Blocks.Block(block)
		if !ok {
			t.Errorf("no block %s", block)
			continue
		}
		got, _ := p.s.pickedItem(id)
		if got.Name != item {
			t.Errorf("%s gives %q, want %q", block, got.Name, item)
		}
	}
	for block, item := range pickedAs {
		if _, ok := p.s.reg.Blocks.Block(block); !ok {
			t.Errorf("no block %s", block)
		}
		if _, ok := p.s.reg.Items.ByName(item); !ok {
			t.Errorf("no item %s", item)
		}
	}
}
