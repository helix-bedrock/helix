package player

import (
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

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
		_ = OnChestItem(s, func(e *ChestItemEvent) {
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
		_ = OnChestOpen(s, func(e *ChestOpenEvent) {
			if e.Blocked {
				e.Cancel()
			}
		}, Normal)
		_ = OnChestClose(s, func(*ChestCloseEvent) { closed++ }, Normal)
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
		if left := c.AddItem(r, ItemStack{Name: "minecraft:emerald", Count: 70}); !left.Empty() {
			t.Errorf("left over %+v", left)
		}
		if c.Item(r, 0).Count != 64 || c.Item(r, 1).Count != 6 {
			t.Errorf("added %+v %+v", c.Item(r, 0), c.Item(r, 1))
		}
		if n := c.RemoveItem(r, "minecraft:emerald", 66); n != 66 || !c.Item(r, 0).Empty() || c.Item(r, 1).Count != 4 {
			t.Errorf("removed %d, left %+v", n, c.Items(r)[:2])
		}
		c.SetName(r, "loot")
		if c.Name() != "loot" {
			t.Errorf("name %q", c.Name())
		}
		// As OpenChest leaves it; the test player has no connection to send on.
		p.window.open, p.window.chest, p.window.chestPos = true, true, at
		c.addViewer(r, p)
		if len(c.Viewers(r)) != 1 {
			t.Error("no viewer")
		}
		take := func() protocol.ItemStackResponse {
			a := &protocol.TakeStackRequestAction{}
			a.Count, a.Source, a.Destination = 1, slotInfo(containerLevelEntity, 1, c.Item(r, 1).netID), slotInfo(containerCursor, cursorSlot, 0)
			return p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: []protocol.StackRequestAction{a}})
		}
		refuse = true
		if resp := take(); resp.Status == protocol.ItemStackResponseStatusOK || c.Item(r, 1).Count != 4 || !p.inventory.ui[cursorSlot].Empty() {
			t.Errorf("refused take: status %d, chest %+v, cursor %+v", resp.Status, c.Item(r, 1), p.inventory.ui[cursorSlot])
		}
		refuse, rewrite = false, true
		if resp := take(); resp.Status != protocol.ItemStackResponseStatusOK || c.Item(r, 1).Name != "minecraft:stick" || !p.requests.refresh {
			t.Errorf("rewritten take: status %d, chest %+v", resp.Status, c.Item(r, 1))
		}
		moved := &protocol.TakeStackRequestAction{}
		moved.Count, moved.Source, moved.Destination = 1, slotInfo(containerLevelEntity, 1, c.Item(r, 1).netID), slotInfo(containerCombined, 1, 0)
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
