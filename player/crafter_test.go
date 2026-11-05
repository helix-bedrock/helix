package player

import (
	"testing"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// crafterAt places a crafter facing down onto a chest and waits for both block entities.
func crafterAt(t *testing.T, p *Player, d *world.Dimension) (*Chest, *Chest, cube.Pos) {
	t.Helper()
	var at cube.Pos
	onRegion(d, func(r *world.Region) {
		y := surface(r, p.s.reg.Blocks.Air(), 3, 3)
		at = cube.Pos{3, y + 1, 3}
		setNamed(t, p, r, cube.Pos{3, y, 3}, "minecraft:chest", nil)
		setNamed(t, p, r, at, crafterBlock, map[string]any{stateOrientation: "down_east"})
	})
	var c, chest *Chest
	waitRegion(t, d, func(r *world.Region) bool {
		c, chest = p.s.chestAt(r, at), p.s.chestAt(r, at.Side(cube.FaceDown))
		return c != nil && chest != nil
	})
	return c, chest, at
}

func TestCrafterMakesRecipeIntoChest(t *testing.T) {
	p, d := machineWorld(t)
	c, chest, at := crafterAt(t, p, d)
	if c.machine.window != 36 {
		t.Fatalf("window %d", c.machine.window)
	}
	onRegion(d, func(r *world.Region) {
		block, _ := r.Block(at, 0)
		if p.s.crafterFace(block) != cube.FaceDown {
			t.Fatalf("face %v", p.s.crafterFace(block))
		}
		c.Inventory(r).SetItem(0, ItemStack{Name: "minecraft:oak_log", Count: 2})
		p.s.Dispense(r, at, block)
		if c.items[0].Count != 1 {
			t.Errorf("log left %+v", c.items[0])
		}
		if got := chest.Inventory(r).Item(0); got.Name != "minecraft:oak_planks" || got.Count != 4 {
			t.Errorf("chest holds %+v", got)
		}
		block, _ = r.Block(at, 0)
		if v, _ := p.s.reg.Blocks.States(block)[stateCrafting].(uint8); v != 1 || c.crafting != p.s.redstone.Get().Crafter.ShowTicks {
			t.Errorf("crafting state %d, timer %d", v, c.crafting)
		}
	})
	waitRegion(t, d, func(r *world.Region) bool {
		block, _ := r.Block(at, 0)
		v, _ := p.s.reg.Blocks.States(block)[stateCrafting].(uint8)
		return v == 0 && c.crafting == 0
	})
}

func TestCrafterNoRecipeKeepsItems(t *testing.T) {
	p, d := machineWorld(t)
	c, chest, at := crafterAt(t, p, d)
	onRegion(d, func(r *world.Region) {
		block, _ := r.Block(at, 0)
		c.Inventory(r).SetItem(4, ItemStack{Name: "minecraft:bedrock", Count: 1})
		p.s.Dispense(r, at, block)
		if c.items[4].Count != 1 || !chest.Inventory(r).Item(0).Empty() {
			t.Errorf("slot %+v chest %+v", c.items[4], chest.Inventory(r).Item(0))
		}
	})
}

func TestCrafterSpreadsItemsAndSkipsDisabledSlots(t *testing.T) {
	p, d := machineWorld(t)
	c, _, _ := crafterAt(t, p, d)
	onRegion(d, func(r *world.Region) {
		c.disabled = 1 << 1
		iron := ItemStack{Name: "minecraft:iron_ingot", Count: 1}
		for range crafterSize {
			if !c.takeOne(r, iron) {
				t.Fatal("no room")
			}
		}
		if !c.items[1].Empty() {
			t.Errorf("disabled slot holds %+v", c.items[1])
		}
		if c.items[0].Count != 2 {
			t.Errorf("slot 0 holds %d after a full round", c.items[0].Count)
		}
		if got := c.ComparatorSignal(r, c.pos); got != 9 {
			t.Errorf("signal %d", got)
		}
		saved := c.SaveData(c.pos)
		if saved[disabledSlotsTag] != int16(2) {
			t.Errorf("saved %v", saved[disabledSlotsTag])
		}
		if back := p.s.loadMachine(c.pos, saved, c.machine); back.disabled != 2 {
			t.Errorf("loaded %d", back.disabled)
		}
	})
}
