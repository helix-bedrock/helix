package player

import (
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/attach"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// machineWorld is an apiWorld where blocks make their block entities.
func machineWorld(t *testing.T) (*Player, *world.Dimension) {
	t.Helper()
	p, d := apiWorld(t, func(*Setup) {})
	var err error
	if p.s.attach, err = attach.New(p.s.reg.Blocks); err != nil {
		t.Fatal(err)
	}
	if p.s.furnaces, err = loadFurnaceFacts(); err != nil {
		t.Fatal(err)
	}
	d.SetBlockEntities(chestEntities{s: p.s})
	return p, d
}

// onRegion runs fn on the region owning chunk 0,0 and waits for it.
func onRegion(d *world.Dimension, fn func(r *world.Region)) {
	done := make(chan struct{})
	d.RunAt(mcdb.ChunkPos{}, func(r *world.Region) {
		fn(r)
		close(done)
	})
	<-done
}

// waitRegion polls cond on the region until it holds.
func waitRegion(t *testing.T, d *world.Dimension, cond func(r *world.Region) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var ok bool
		onRegion(d, func(r *world.Region) { ok = cond(r) })
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// setNamed places the block with these states.
func setNamed(t *testing.T, p *Player, r *world.Region, pos cube.Pos, name string, states map[string]any) {
	t.Helper()
	blocks := p.s.reg.Blocks
	id, ok := blocks.Block(name)
	if !ok {
		t.Fatalf("no block %s", name)
	}
	if states != nil {
		all := map[string]any{}
		for k, v := range blocks.States(id) {
			all[k] = v
		}
		for k, v := range states {
			all[k] = v
		}
		if id, ok = blocks.StateID(name, all); !ok {
			t.Fatalf("no state of %s: %v", name, states)
		}
	}
	if !r.SetBlock(pos, 0, id) {
		t.Fatalf("could not place %s", name)
	}
}

func TestHopperMovesChestToChest(t *testing.T) {
	p, d := machineWorld(t)
	top, mid, low := cube.Pos{1, 70, 1}, cube.Pos{1, 69, 1}, cube.Pos{1, 68, 1}
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, top, "minecraft:chest", nil)
		setNamed(t, p, r, mid, "minecraft:hopper", map[string]any{"facing_direction": int32(0)})
		setNamed(t, p, r, low, "minecraft:chest", nil)
		if !p.s.chestAt(r, top).Inventory(r).SetItem(3, ItemStack{Name: "minecraft:emerald", Count: 2}) {
			t.Fatal("no slot")
		}
	})
	waitRegion(t, d, func(r *world.Region) bool {
		c := p.s.chestAt(r, low)
		return c != nil && c.Inventory(r).Item(0).Count == 2 && p.s.chestAt(r, top).Inventory(r).Item(3).Empty()
	})
}

func TestHopperCollectsItemsAbove(t *testing.T) {
	p, d := machineWorld(t)
	at := cube.Pos{1, 69, 1}
	var c *Chest
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, at, "minecraft:hopper", nil)
		c = p.s.chestAt(r, at)
	})
	// One with a name tag, as -testcmds gives every dropped item, is still just an item.
	i := p.s.spawnItem(d, ItemStack{Name: "minecraft:emerald", Count: 3}, mgl64.Vec3{1.5, 70, 1.5}, mgl64.Vec3{}, world.Rotation{}, 0, 0)
	onRegion(d, func(*world.Region) { i.SetView(func(*Player, *ItemView) {}) })
	waitRegion(t, d, func(r *world.Region) bool { return c.Inventory(r).Item(0).Count == 3 })
}

// An item on a slab or stair above a hopper is in reach of it.
func TestHopperCollectsOnPartBlockAbove(t *testing.T) {
	for _, name := range []string{"minecraft:smooth_stone_slab", "minecraft:oak_stairs"} {
		t.Run(name, func(t *testing.T) {
			p, d := machineWorld(t)
			at := cube.Pos{1, 69, 1}
			var c *Chest
			onRegion(d, func(r *world.Region) {
				setNamed(t, p, r, at, "minecraft:hopper", nil)
				setNamed(t, p, r, at.Side(cube.FaceUp), name, nil)
				c = p.s.chestAt(r, at)
			})
			// From above the box, over the low half of the stair: its default rises to the east.
			p.s.spawnItem(d, ItemStack{Name: "minecraft:emerald", Count: 2}, mgl64.Vec3{1.25, 71.4, 1.5}, mgl64.Vec3{}, world.Rotation{}, 0, 0)
			waitRegion(t, d, func(r *world.Region) bool { return c.Inventory(r).Item(0).Count == 2 })
		})
	}
}

// An item resting on a full block above a hopper is just out of reach of it.
func TestHopperLeavesItemOnFullBlockAbove(t *testing.T) {
	p, d := machineWorld(t)
	at := cube.Pos{1, 69, 1}
	var c *Chest
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, at, "minecraft:hopper", nil)
		setNamed(t, p, r, at.Side(cube.FaceUp), "minecraft:stone", nil)
		c = p.s.chestAt(r, at)
	})
	i := p.s.spawnItem(d, ItemStack{Name: "minecraft:emerald", Count: 2}, mgl64.Vec3{1.5, 71.4, 1.5}, mgl64.Vec3{}, world.Rotation{}, 0, 0)
	waitRegion(t, d, func(*world.Region) bool { return i.onGround })
	time.Sleep(300 * time.Millisecond)
	onRegion(d, func(r *world.Region) {
		if !c.Inventory(r).Item(0).Empty() {
			t.Error("the hopper took an item through a full block")
		}
	})
}

func TestHopperDoesNotCollectThroughContainer(t *testing.T) {
	if !containerBlock("minecraft:trapped_chest") || !containerBlock("minecraft:red_shulker_box") || !containerBlock("minecraft:oxidized_copper_chest") {
		t.Error("a container was not seen as one")
	}
	if containerBlock("minecraft:stone") || containerBlock("minecraft:crafting_table") {
		t.Error("a plain block was seen as a container")
	}
}

func TestHopperTurnedOffHolds(t *testing.T) {
	p, d := machineWorld(t)
	mid, low := cube.Pos{1, 69, 1}, cube.Pos{1, 68, 1}
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, mid, "minecraft:hopper", map[string]any{"facing_direction": int32(0), "toggle_bit": uint8(1)})
		setNamed(t, p, r, low, "minecraft:chest", nil)
		p.s.chestAt(r, mid).Inventory(r).SetItem(0, ItemStack{Name: "minecraft:emerald", Count: 1})
	})
	time.Sleep(600 * time.Millisecond)
	onRegion(d, func(r *world.Region) {
		if !p.s.chestAt(r, low).Inventory(r).Item(0).Empty() {
			t.Error("hopper with power moved an item")
		}
	})
}

func TestHopperSaves(t *testing.T) {
	p, d := machineWorld(t)
	at := cube.Pos{1, 69, 1}
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, at, "minecraft:hopper", nil)
		c := p.s.chestAt(r, at)
		c.Inventory(r).SetItem(4, ItemStack{Name: "minecraft:emerald", Count: 3})
		saved := c.SaveData(at)
		if saved["id"] != "Hopper" {
			t.Errorf("id %v", saved["id"])
		}
		be, ok := chestEntities{s: p.s}.Load(at, saved)
		if !ok {
			t.Fatal("did not load")
		}
		back, ok := be.(*Chest)
		if !ok || back.machine == nil || back.items[4].Count != 3 {
			t.Errorf("came back as %+v", be)
		}
		if n := back.Inventory(r).Size(); n != 5 {
			t.Errorf("size %d", n)
		}
	})
}

func TestDispenserThrowsOneItem(t *testing.T) {
	p, d := machineWorld(t)
	at := cube.Pos{1, 69, 1}
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, at, "minecraft:dispenser", map[string]any{"facing_direction": int32(5)})
		c := p.s.chestAt(r, at)
		c.Inventory(r).SetItem(2, ItemStack{Name: "minecraft:emerald", Count: 2})
		block, _ := r.Block(at, 0)
		p.s.Dispense(r, at, block)
		if n := c.Inventory(r).Item(2).Count; n != 1 {
			t.Errorf("%d left", n)
		}
	})
}

func TestDispenserPoursAndFillsBuckets(t *testing.T) {
	p, d := machineWorld(t)
	at, front := cube.Pos{1, 69, 1}, cube.Pos{2, 69, 1}
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, at, "minecraft:dispenser", map[string]any{"facing_direction": int32(5)})
		c := p.s.chestAt(r, at)
		c.Inventory(r).SetItem(0, ItemStack{Name: "minecraft:water_bucket", Count: 1})
		block, _ := r.Block(at, 0)
		p.s.Dispense(r, at, block)
		b, _ := r.Block(front, 0)
		if got := p.s.reg.Blocks.Name(b); got != "minecraft:water" {
			t.Errorf("front is %s", got)
		}
		if got := c.Inventory(r).Item(0); got.Name != emptyBucket || got.Count != 1 {
			t.Errorf("after pouring the dispenser holds %+v", got)
		}
		p.s.Dispense(r, at, block)
		b, _ = r.Block(front, 0)
		if got := p.s.reg.Blocks.Name(b); got == "minecraft:water" {
			t.Error("the empty bucket left the water")
		}
		if got := c.Inventory(r).Item(0); got.Name != "minecraft:water_bucket" || got.Count != 1 {
			t.Errorf("after filling the dispenser holds %+v", got)
		}
	})
}

func TestDispenserThrowsBucketItCannotPour(t *testing.T) {
	p, d := machineWorld(t)
	at, front := cube.Pos{1, 69, 1}, cube.Pos{2, 69, 1}
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, at, "minecraft:dispenser", map[string]any{"facing_direction": int32(5)})
		setNamed(t, p, r, front, "minecraft:stone", nil)
		c := p.s.chestAt(r, at)
		c.Inventory(r).SetItem(0, ItemStack{Name: "minecraft:water_bucket", Count: 1})
		block, _ := r.Block(at, 0)
		p.s.Dispense(r, at, block)
		if got := c.Inventory(r).Item(0); !got.Empty() {
			t.Errorf("the dispenser still holds %+v", got)
		}
		b, _ := r.Block(front, 0)
		if got := p.s.reg.Blocks.Name(b); got != "minecraft:stone" {
			t.Errorf("front is %s", got)
		}
	})
}

func TestDropperFillsContainerInFront(t *testing.T) {
	p, d := machineWorld(t)
	at, front := cube.Pos{1, 69, 1}, cube.Pos{2, 69, 1}
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, at, "minecraft:dropper", map[string]any{"facing_direction": int32(5)})
		setNamed(t, p, r, front, "minecraft:chest", nil)
		c := p.s.chestAt(r, at)
		c.Inventory(r).SetItem(0, ItemStack{Name: "minecraft:emerald", Count: 2})
		block, _ := r.Block(at, 0)
		p.s.Dispense(r, at, block)
		if n := p.s.chestAt(r, front).Inventory(r).Item(0).Count; n != 1 {
			t.Errorf("chest got %d", n)
		}
		if n := c.Inventory(r).Item(0).Count; n != 1 {
			t.Errorf("%d left", n)
		}
	})
}
