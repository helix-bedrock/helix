package player

import (
	"testing"
	"time"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// runIn runs fn on the region owning the chunk and waits for it.
func runIn(t *testing.T, d *world.Dimension, fn func(r *world.Region)) {
	t.Helper()
	done := make(chan struct{})
	d.RunAt(mcdb.ChunkPos{}, func(r *world.Region) {
		defer close(done)
		fn(r)
	})
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("region never ran")
	}
}

// TestChest runs a real region: two chests pair, keep their items and their pair
// through a save and a load, lose the pair when one breaks, and throw its items.
func TestChest(t *testing.T) {
	p := testPlayer(t)
	reg := p.s.reg
	table, err := newChestTable(reg.Blocks)
	if err != nil {
		t.Fatal(err)
	}
	p.s.chests = table
	db, err := mcdb.Config{Blocks: reg.Blocks}.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rng, _ := mcdb.Overworld.Range()
	flat, err := world.ParseFlat("", reg.Blocks, reg.Biomes, rng)
	if err != nil {
		t.Fatal(err)
	}
	conf := world.DefaultConfig()
	conf.Store, conf.Blocks, conf.Biomes, conf.Generator = db, reg.Blocks, reg.Biomes, flat
	d, err := world.New(conf)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	d.SetBlockEntities(chestEntities{s: p.s})

	south, ok := reg.Blocks.StateID("minecraft:chest", map[string]any{cardinalState: "south"})
	if !ok {
		t.Fatal("no south chest")
	}
	west, _ := reg.Blocks.StateID("minecraft:chest", map[string]any{cardinalState: "west"})
	a, b := cube.Pos{4, 0, 4}, cube.Pos{5, 0, 4}
	inFront, wrongWay := cube.Pos{8, 0, 4}, cube.Pos{8, 0, 5}

	ticket := d.KeepLoaded(world.Chunks(mcdb.ChunkPos{}), true)
	runIn(t, d, func(r *world.Region) {
		r.SetBlock(a, 0, south)
		r.SetBlock(b, 0, south)
		ca, cb := p.s.chestAt(r, a), p.s.chestAt(r, b)
		if ca == nil || cb == nil {
			t.Fatal("no chests made")
		}
		if !ca.paired || !ca.lead || !cb.paired || cb.lead || ca.partner != b || cb.partner != a {
			t.Fatalf("not paired: %+v %+v", ca, cb)
		}
		ca.items[0] = ItemStack{Name: "minecraft:emerald", Count: 64, netID: 1}
		cb.items[26] = ItemStack{Name: "minecraft:stick", Count: 2, netID: 2}
		r.SaveBlockEntity(a)

		// One behind the other facing the way they line up does not pair.
		r.SetBlock(inFront, 0, south)
		r.SetBlock(wrongWay, 0, south)
		if p.s.chestAt(r, inFront).paired {
			t.Error("chests in line paired")
		}
		// Nor do two facing different ways.
		r.SetBlock(cube.Pos{12, 0, 4}, 0, south)
		r.SetBlock(cube.Pos{13, 0, 4}, 0, west)
		if p.s.chestAt(r, cube.Pos{13, 0, 4}).paired {
			t.Error("chests facing different ways paired")
		}
	})
	ticket.Release()

	var col *chunk.Column
	deadline := time.Now().Add(15 * time.Second)
	for {
		col, err = db.LoadColumn(mcdb.ChunkPos{}, mcdb.Overworld)
		if err == nil && len(col.BlockEntities) == 6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("chests not saved: %v, %+v", err, col)
		}
		time.Sleep(20 * time.Millisecond)
	}

	again := d.KeepLoaded(world.Chunks(mcdb.ChunkPos{}), true)
	defer again.Release()
	time.Sleep(200 * time.Millisecond)
	runIn(t, d, func(r *world.Region) {
		ca, cb := p.s.chestAt(r, a), p.s.chestAt(r, b)
		if ca == nil || cb == nil {
			t.Fatal("chests not loaded")
		}
		if !ca.paired || !ca.lead || !cb.paired || cb.lead {
			t.Fatalf("pair not linked again: %+v %+v", ca, cb)
		}
		if ca.items[0].Name != "minecraft:emerald" || ca.items[0].Count != 64 || ca.items[0].netID == 0 {
			t.Fatalf("items not loaded: %+v", ca.items[0])
		}
		if cb.items[26].Name != "minecraft:stick" {
			t.Fatalf("items not loaded: %+v", cb.items[26])
		}
		r.SetBlock(a, 0, reg.Blocks.Air())
		if _, ok := r.BlockEntity(a); ok {
			t.Error("block entity kept after its block went")
		}
		if cb.paired || !cb.lead {
			t.Errorf("partner kept the pair: %+v", cb)
		}
	})
	time.Sleep(200 * time.Millisecond)
	runIn(t, d, func(r *world.Region) {
		total := 0
		for _, e := range r.AppendEntitiesIn(nil, 0, 0, 15, 15) {
			if it, ok := e.Owner().(*ItemActor); ok {
				if it.stack.Count > chestPileMin+chestPileExtra-1 {
					t.Errorf("pile of %d", it.stack.Count)
				}
				total += int(it.stack.Count)
			}
		}
		if total != 64 {
			t.Errorf("thrown %d items, want the broken half's 64", total)
		}
	})
}

// TestChestObstruction checks BDS's list: glass never stops the lid, stone does
// through its box.
func TestChestObstruction(t *testing.T) {
	p := testPlayer(t)
	table, err := newChestTable(p.s.reg.Blocks)
	if err != nil {
		t.Fatal(err)
	}
	blocks := p.s.reg.Blocks
	glass, _ := blocks.Block("minecraft:glass")
	stone, _ := blocks.Block("minecraft:stone")
	if !table.clear[glass] {
		t.Error("glass stops a chest")
	}
	if table.clear[stone] {
		t.Error("stone does not stop a chest")
	}
	if !chestLidHits(blocks.Properties(stone).Collision[0]) {
		t.Error("a full block misses the lid")
	}
}
