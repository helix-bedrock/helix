package player

import (
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
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
	setup, err := NewSetup(p.s.log)
	if err != nil {
		t.Fatal(err)
	}
	if p.s.game, err = setup.freezeGameplay(); err != nil {
		t.Fatal(err)
	}
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

// TestEnderChest checks that ender chests are known, come back from a save as ender
// chests, and that their items are the player's, through a save and a load.
func TestEnderChest(t *testing.T) {
	p := testPlayer(t)
	table, err := newChestTable(p.s.reg.Blocks)
	if err != nil {
		t.Fatal(err)
	}
	if ender, _ := p.s.reg.Blocks.Block("minecraft:ender_chest"); table.kind[ender] != chestEnder {
		t.Fatal("ender chest not known")
	}
	saved := (&Chest{ender: true, lead: true}).SaveData(cube.Pos{1, 2, 3})
	if saved["id"] != enderChestEntityID {
		t.Fatalf("saved %+v", saved)
	}
	be, ok := chestEntities{s: p.s}.Load(cube.Pos{1, 2, 3}, saved)
	if c, isChest := be.(*Chest); !ok || !isChest || !c.ender {
		t.Fatalf("loaded %+v", be)
	}
	var inv, back playerItems
	inv.ender[5] = ItemStack{Name: "minecraft:emerald", Count: 7}
	data := map[string]any{}
	inv.save(data)
	back.load(data, func() int32 { return 1 })
	if s := back.ender[5]; s.Name != "minecraft:emerald" || s.Count != 7 {
		t.Fatalf("loaded %+v", s)
	}
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

// apiWorld gives a test player a real region and the listeners register adds.
func apiWorld(t *testing.T, register func(*Setup)) (*Player, *world.Dimension) {
	t.Helper()
	p := testPlayer(t)
	reg := p.s.reg
	var err error
	if p.s.chests, err = newChestTable(reg.Blocks); err != nil {
		t.Fatal(err)
	}
	if p.s.drops, err = loot.New(); err != nil {
		t.Fatal(err)
	}
	setup, err := NewSetup(p.s.log)
	if err != nil {
		t.Fatal(err)
	}
	register(setup)
	if p.s.game, err = setup.freezeGameplay(); err != nil {
		t.Fatal(err)
	}
	db, err := mcdb.Config{Blocks: reg.Blocks}.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
	t.Cleanup(func() { _ = d.Close() })
	d.SetBlockEntities(chestEntities{s: p.s})
	ticket := d.KeepLoaded(world.Chunks(mcdb.ChunkPos{}), true)
	t.Cleanup(ticket.Release)
	p.entity = d.AddEntity(world.EntityConfig{UniqueID: 1, RuntimeID: 1, Pos: mgl32.Vec3{4, 0, 5}})
	// An entity joins its region on that region's tick.
	for deadline := time.Now().Add(10 * time.Second); p.entity.Region() == nil; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("player never reached a region")
		}
	}
	return p, d
}

// TestChestAPI drives the chest methods and events through a player's real requests.
func TestChestAPI(t *testing.T) {
	refuse, rewrite := false, false
	var spilled, closed int
	p, d := apiWorld(t, func(s *Setup) {
		_ = OnContainerItem(s, func(e *ContainerItemEvent) {
			if refuse {
				e.Cancel()
			}
			if rewrite {
				e.New.Name = "minecraft:stick"
				for _, slot := range e.InventorySlots {
					s := e.InventoryItem(slot)
					s.Name = "minecraft:apple"
					e.SetInventoryItem(slot, s)
				}
			}
		}, Normal)
		_ = OnContainerOpen(s, func(e *ContainerOpenEvent) {
			if e.Blocked {
				e.Cancel()
			}
		}, Normal)
		_ = OnContainerClose(s, func(*ContainerCloseEvent) { closed++ }, Normal)
		_ = OnChestSpill(s, func(e *ChestSpillEvent) {
			spilled = len(e.Items)
			e.Cancel()
		}, Normal)
	})
	blocks := p.s.reg.Blocks
	chest, _ := blocks.StateID("minecraft:chest", map[string]any{cardinalState: "south"})
	at := cube.Pos{4, 0, 4}
	runIn(t, d, func(r *world.Region) {
		r.SetBlock(at, 0, chest)
		c := p.s.ChestAt(r, at)
		if c == nil {
			t.Error("no chest")
			return
		}
		if left := c.Inventory(r).AddItem(ItemStack{Name: "minecraft:emerald", Count: 70}); !left.Empty() {
			t.Errorf("left over %+v", left)
		}
		if c.Inventory(r).Item(0).Count != 64 || c.Inventory(r).Item(1).Count != 6 {
			t.Errorf("added %+v %+v", c.Inventory(r).Item(0), c.Inventory(r).Item(1))
		}
		if n := c.Inventory(r).RemoveItem("minecraft:emerald", 66); n != 66 || !c.Inventory(r).Item(0).Empty() || c.Inventory(r).Item(1).Count != 4 {
			t.Errorf("removed %d, left %+v", n, c.Inventory(r).Items()[:2])
		}
		c.SetName(r, "loot")
		if c.Name() != "loot" {
			t.Errorf("name %q", c.Name())
		}
		// As OpenChest leaves it; the test player has no connection to send on.
		p.window.open, p.window.inv, p.window.kind, p.window.pos = true, c.Inventory(r), c.Kind(), at
		c.addViewer(r, p)
		if len(c.Viewers(r)) != 1 {
			t.Error("no viewer")
		}
		take := func() protocol.ItemStackResponse {
			a := &protocol.TakeStackRequestAction{}
			a.Count, a.Source, a.Destination = 1, slotInfo(containerLevelEntity, 1, c.Inventory(r).Item(1).netID), slotInfo(containerCursor, cursorSlot, 0)
			return p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: []protocol.StackRequestAction{a}})
		}
		refuse = true
		if resp := take(); resp.Status == protocol.ItemStackResponseStatusOK || c.Inventory(r).Item(1).Count != 4 || !p.inventory.ui[cursorSlot].Empty() {
			t.Errorf("refused take: status %d, chest %+v, cursor %+v", resp.Status, c.Inventory(r).Item(1), p.inventory.ui[cursorSlot])
		}
		refuse, rewrite = false, true
		if resp := take(); resp.Status != protocol.ItemStackResponseStatusOK || c.Inventory(r).Item(1).Name != "minecraft:stick" || !p.requests.refresh {
			t.Errorf("rewritten take: status %d, chest %+v", resp.Status, c.Inventory(r).Item(1))
		}
		moved := &protocol.TakeStackRequestAction{}
		moved.Count, moved.Source, moved.Destination = 1, slotInfo(containerLevelEntity, 1, c.Inventory(r).Item(1).netID), slotInfo(containerCombined, 1, 0)
		if resp := p.stackRequest(protocol.ItemStackRequest{RequestID: 2, Actions: []protocol.StackRequestAction{moved}}); resp.Status != protocol.ItemStackResponseStatusOK || p.inventory.main[1].Name != "minecraft:apple" {
			t.Errorf("shift-click: status %d, slot %+v", resp.Status, p.inventory.main[1])
		}
		p.leaveChest(r, false)
		if closed != 1 || len(c.Viewers(r)) != 0 {
			t.Errorf("close events %d, viewers %d", closed, len(c.Viewers(r)))
		}
		r.SetBlock(at, 0, blocks.Air())
		if spilled != chestSize {
			t.Errorf("spill event saw %d slots", spilled)
		}
	})
}

// TestBlockDropsEvent breaks real blocks and checks what enters the world.
func TestBlockDropsEvent(t *testing.T) {
	var harvest bool
	var wearSeen int32
	p, d := apiWorld(t, func(s *Setup) {
		_ = OnBlockBreak(s, func(e *BlockBreakEvent) {
			wearSeen = e.Wear
			e.Wear = 0
		}, Normal)
		_ = OnBlockDrops(s, func(e *BlockDropsEvent) {
			harvest = e.Harvest
			if e.Player.s.reg.Blocks.Name(e.Block) == "minecraft:glass" {
				e.Drops = append(e.Drops, NewBlockDrop(e.Pos, ItemStack{Name: "minecraft:diamond", Count: 3}))
				e.XP = 11
			}
		}, Normal)
	})
	// The test player has no connection to send its hunger on.
	p.s.game.sys.Exhaust = func(*Player, float32) {}
	blocks := p.s.reg.Blocks
	dirt, _ := blocks.Block("minecraft:dirt")
	stone, _ := blocks.Block("minecraft:stone")
	glass, ok := blocks.Block("minecraft:glass")
	if !ok {
		t.Fatal("no glass")
	}
	broken := func(r *world.Region, at cube.Pos, block uint32) {
		t.Helper()
		r.SetBlock(at, 0, block)
		p.brk.progress = 1
		if !vanillaBreakFinish(p, at) {
			t.Error("block did not break")
		}
	}
	count := func(r *world.Region) map[string]int {
		got := map[string]int{}
		for _, e := range r.AppendEntitiesIn(nil, 0, 0, 15, 15) {
			if o, ok := e.Owner().(*XPOrb); ok {
				got["xp"] += int(o.value)
			}
			if it, ok := e.Owner().(*ItemActor); ok {
				got[it.stack.Name] += int(it.stack.Count)
				it.taken = true
				e.Remove()
			}
		}
		return got
	}
	runIn(t, d, func(r *world.Region) {
		// The test player holds a sword: dirt needs no tool, stone does.
		broken(r, cube.Pos{4, 0, 4}, dirt)
		if !harvest {
			t.Error("dirt not harvested")
		}
		if wearSeen == 0 {
			t.Error("break event saw no wear")
		}
		broken(r, cube.Pos{5, 0, 4}, stone)
		if harvest {
			t.Error("stone harvested by a sword")
		}
		broken(r, cube.Pos{6, 0, 4}, glass)
	})
	var got map[string]int
	runIn(t, d, func(r *world.Region) { got = count(r) })
	if got["minecraft:dirt"] != 1 || got["minecraft:cobblestone"] != 0 || got["minecraft:diamond"] != 3 || got["xp"] != 11 {
		t.Errorf("in the world: %v", got)
	}
}

// TestExperience checks BDS's orb sizes and level sums.
func TestExperience(t *testing.T) {
	for amount, want := range map[int32]int32{1: 1, 2: 1, 3: 3, 6: 3, 7: 7, 16: 7, 17: 17, 2476: 1237, 2477: 2477} {
		if got := xpOrbSize(amount); got != want {
			t.Errorf("orb of %d = %d, want %d", amount, got, want)
		}
	}
	for level, want := range map[int32]int32{0: 7, 14: 35, 15: 37, 29: 107, 30: 112} {
		if got := xpForLevel(level); got != want {
			t.Errorf("level %d costs %d, want %d", level, got, want)
		}
	}
	var reached []int32
	level, progress := gainExperience(0, 0, 16, func(l int32) { reached = append(reached, l) })
	if level != 2 || progress > 1e-3 || len(reached) != 2 {
		t.Errorf("16 from nothing: level %d progress %v reached %v", level, progress, reached)
	}
}
