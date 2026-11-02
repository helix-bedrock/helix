package loot

import "testing"

func TestDrops(t *testing.T) {
	table, err := New()
	if err != nil {
		t.Fatal(err)
	}
	one := func(block string, c Context) Stack {
		t.Helper()
		r := table.Roll(block, &c)
		if len(r.Stacks) != 1 {
			t.Fatalf("%s dropped %+v", block, r)
		}
		return r.Stacks[0]
	}
	if s := one("minecraft:stone", Context{}); s.Name != "minecraft:cobblestone" {
		t.Errorf("stone dropped %+v", s)
	}
	if s := one("minecraft:stone", Context{Silk: true}); !s.Self || s.Name != "minecraft:stone" {
		t.Errorf("stone with Silk Touch dropped %+v", s)
	}
	if s := one("minecraft:chest", Context{CustomName: "loot"}); !s.Self || s.CustomName != "loot" {
		t.Errorf("chest dropped %+v", s)
	}
	if s := one("minecraft:black_concrete_double_slab", Context{}); s.Count != 2 {
		t.Errorf("slab table dropped %+v", s)
	}
	r := table.Roll("minecraft:diamond_ore", &Context{})
	if len(r.Stacks) != 1 || r.Stacks[0].Name != "minecraft:diamond" || r.XP < 3 || r.XP > 7 {
		t.Errorf("diamond ore gave %+v", r)
	}
	height := func(string) (int32, bool) { return 7, true }
	if s := one("minecraft:snow_layer", Context{Silk: true, State: height}); s.Name != "minecraft:snow" {
		t.Errorf("full snow dropped %+v", s)
	}
	if r := table.Roll("minecraft:snow_layer", &Context{State: height}); len(r.Stacks) != 4 || r.Stacks[0].Name != "minecraft:snowball" {
		t.Errorf("snow dropped %+v", r)
	}
	if r := table.Roll("minecraft:stone", &Context{Explosion: true, Radius: 1e9}); len(r.Stacks) != 0 {
		t.Errorf("explosion left %+v", r)
	}
}
