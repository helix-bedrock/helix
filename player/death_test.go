package player

import "testing"

// TestDropSlots checks what a death takes: vanishing items go without a drop,
// items kept on death stay, the rest is thrown.
func TestDropSlots(t *testing.T) {
	p := testPlayer(t)
	var thrown []string
	p.s.game = &gameplay{}
	p.s.game.enchants.Store(newEnchantTable(map[int16]*Enchantment{enchantVanishing: {ID: enchantVanishing, VanishesOnDeath: true}}))
	p.s.game.sys.DropItem = func(_ *Player, s ItemStack, randomly bool) bool {
		if !randomly {
			t.Error("a death throws at random")
		}
		thrown = append(thrown, s.Name)
		return true
	}
	slots := []ItemStack{
		{Name: "minecraft:stone", Count: 1},
		{Name: "minecraft:dirt", Count: 1, Tag: map[string]any{keepOnDeathTag: uint8(1)}},
		{Name: "minecraft:emerald", Count: 1, Tag: map[string]any{"ench": []any{map[string]any{"id": enchantVanishing}}}},
	}
	p.dropSlots(slots, true)
	if !slots[2].Empty() || slots[0].Empty() || len(thrown) != 0 {
		t.Fatalf("vanishing pass: %v, thrown %v", slots, thrown)
	}
	p.dropSlots(slots, false)
	if !slots[0].Empty() || slots[1].Empty() || len(thrown) != 1 || thrown[0] != "minecraft:stone" {
		t.Fatalf("drop pass: %v, thrown %v", slots, thrown)
	}
}
