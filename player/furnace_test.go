package player

import (
	"testing"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// BDS's burn times at a furnace's 200 ticks a cook, and a blast furnace burning twice as
// fast.
func TestFurnaceFuel(t *testing.T) {
	p, _ := machineWorld(t)
	f := &Furnace{kind: furnaceKindFurnace}
	f.c = &Chest{s: p.s, furnace: f}
	blast := &Furnace{kind: furnaceKindBlast, c: f.c}
	for name, want := range map[string]int32{
		"minecraft:coal": 1600, "minecraft:charcoal": 1600, "minecraft:oak_planks": 300, "minecraft:oak_log": 300,
		"minecraft:stick": 100, "minecraft:coal_block": 16000, "minecraft:lava_bucket": 20000,
		"minecraft:crimson_planks": 0, "minecraft:stone": 0, "minecraft:bamboo": 50, "minecraft:blaze_rod": 2400,
	} {
		if got := vanillaFurnaceFuel(FurnaceInfo{Furnace: f}, ItemStack{Name: name, Count: 1}); got != want {
			t.Errorf("%s burns %d, want %d", name, got, want)
		}
	}
	if got := vanillaFurnaceFuel(FurnaceInfo{Furnace: blast}, ItemStack{Name: "minecraft:coal", Count: 1}); got != 800 {
		t.Errorf("coal in a blast furnace burns %d, want 800", got)
	}
}

// A furnace sped up through the API smelts both items with one coal, lights while it
// burns, and a hopper above feeds it.
func TestFurnaceSmelts(t *testing.T) {
	p, d := machineWorld(t)
	at := cube.Pos{1, 69, 1}
	var c *Chest
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, at, "minecraft:furnace", nil)
		setNamed(t, p, r, at.Side(cube.FaceUp), "minecraft:hopper", map[string]any{"facing_direction": int32(0)})
		c = p.s.chestAt(r, at)
		if c == nil || c.Furnace() == nil {
			t.Fatal("no furnace block entity")
		}
		c.Furnace().SetCookTicks(4)
		c.Inventory(r).SetItem(furnaceInput, ItemStack{Name: "minecraft:raw_iron", Count: 1})
		c.Inventory(r).SetItem(furnaceFuel, ItemStack{Name: "minecraft:coal", Count: 1})
		p.s.chestAt(r, at.Side(cube.FaceUp)).Inventory(r).SetItem(0, ItemStack{Name: "minecraft:raw_iron", Count: 1})
	})
	waitRegion(t, d, func(r *world.Region) bool {
		b, _ := r.Block(at, 0)
		return p.s.reg.Blocks.Name(b) == "minecraft:lit_furnace" && p.s.chestAt(r, at) == c
	})
	waitRegion(t, d, func(r *world.Region) bool { return c.Inventory(r).Item(furnaceResult).Count == 2 })
	onRegion(d, func(r *world.Region) {
		if out := c.Inventory(r).Item(furnaceResult); out.Name != "minecraft:iron_ingot" {
			t.Errorf("made %s", out.Name)
		}
		if !c.Inventory(r).Item(furnaceFuel).Empty() {
			t.Error("the coal was not used")
		}
	})
}
