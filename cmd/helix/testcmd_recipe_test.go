package main

import (
	"testing"

	"github.com/helix-bedrock/helix/registry"
)

// TestRecipeTestRemove checks the command removes the item it names, crafting and
// smithing alike, and that reset brings it back.
func TestRecipeTestRemove(t *testing.T) {
	setup := registry.NewSetup(nil, "minecraft")
	if err := registry.Vanilla(setup); err != nil {
		t.Fatal(err)
	}
	reg, err := setup.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	start := reg.Crafting.Packet()
	count := func(item string) int {
		n := 0
		if err := reg.EditRecipes(func(s *registry.Recipes) error { n += len(s.For(item)); return nil }); err != nil {
			t.Fatal(err)
		}
		if err := reg.EditSmithing(func(s *registry.Smithing) error { n += len(s.For(item)); return nil }); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, c := range []struct {
		args []string
		item string
	}{
		{nil, recipeTestStick},
		{[]string{"iron_boots"}, "minecraft:iron_boots"},
		{[]string{"minecraft:netherite_sword"}, "minecraft:netherite_sword"},
	} {
		item := recipeTestItem(c.args)
		if item != c.item {
			t.Fatalf("%v names %s", c.args, item)
		}
		if count(item) == 0 {
			t.Fatalf("%s has no recipes to start with", item)
		}
		recipeTest(reg, start, "remove", item)
		if n := count(item); n != 0 {
			t.Errorf("%s still has %d recipes", item, n)
		}
		recipeTest(reg, start, "reset", item)
		if count(item) == 0 {
			t.Errorf("reset did not bring %s back", item)
		}
	}
}
