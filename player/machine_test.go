package player

import (
	"testing"
	"time"

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
		if !p.s.chestAt(r, top).SetItem(r, 3, ItemStack{Name: "minecraft:emerald", Count: 2}) {
			t.Fatal("no slot")
		}
	})
	waitRegion(t, d, func(r *world.Region) bool {
		c := p.s.chestAt(r, low)
		return c != nil && c.Item(r, 0).Count == 2 && p.s.chestAt(r, top).Item(r, 3).Empty()
	})
}

func TestHopperTurnedOffHolds(t *testing.T) {
	p, d := machineWorld(t)
	mid, low := cube.Pos{1, 69, 1}, cube.Pos{1, 68, 1}
	onRegion(d, func(r *world.Region) {
		setNamed(t, p, r, mid, "minecraft:hopper", map[string]any{"facing_direction": int32(0), "toggle_bit": uint8(1)})
		setNamed(t, p, r, low, "minecraft:chest", nil)
		p.s.chestAt(r, mid).SetItem(r, 0, ItemStack{Name: "minecraft:emerald", Count: 1})
	})
	time.Sleep(600 * time.Millisecond)
	onRegion(d, func(r *world.Region) {
		if !p.s.chestAt(r, low).Item(r, 0).Empty() {
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
		c.SetItem(r, 4, ItemStack{Name: "minecraft:emerald", Count: 3})
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
		if n := back.Size(r); n != 5 {
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
		c.SetItem(r, 2, ItemStack{Name: "minecraft:emerald", Count: 2})
		block, _ := r.Block(at, 0)
		p.s.Dispense(r, at, block)
		if n := c.Item(r, 2).Count; n != 1 {
			t.Errorf("%d left", n)
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
		c.SetItem(r, 0, ItemStack{Name: "minecraft:emerald", Count: 2})
		block, _ := r.Block(at, 0)
		p.s.Dispense(r, at, block)
		if n := p.s.chestAt(r, front).Item(r, 0).Count; n != 1 {
			t.Errorf("chest got %d", n)
		}
		if n := c.Item(r, 0).Count; n != 1 {
			t.Errorf("%d left", n)
		}
	})
}
