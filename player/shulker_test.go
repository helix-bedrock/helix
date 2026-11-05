package player

import (
	"testing"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// TestShulkerBox runs a real region: a box opens only with room for its lid, refuses boxes,
// keeps its facing through a save, and its items leave with the dropped box into the next one.
func TestShulkerBox(t *testing.T) {
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
	entities := chestEntities{s: p.s}
	d.SetBlockEntities(entities)

	red, ok := reg.Blocks.Block("minecraft:red_shulker_box")
	if !ok {
		t.Fatal("no red shulker box")
	}
	stone, _ := reg.Blocks.Block("minecraft:stone")
	at, next := cube.Pos{4, 0, 4}, cube.Pos{8, 0, 4}
	runIn(t, d, func(r *world.Region) {
		if !r.SetBlock(at, 0, red) || !r.SetBlock(next, 0, red) {
			t.Fatal("box not set")
		}
		c := p.s.chestAt(r, at)
		if c == nil || !c.shulker || c.facing != uint8(cube.FaceUp) {
			t.Fatalf("new box: %+v", c)
		}
		if !c.canOpen(r, at) {
			t.Error("box with air above does not open")
		}
		r.SetBlock(at.Side(cube.FaceUp), 0, stone)
		if c.canOpen(r, at) {
			t.Error("box under stone opens")
		}
		// Opening sideways, only that side counts.
		c.placedFrom(r, &Placement{Face: int32(cube.FaceSouth), Pos: at})
		if !c.canOpen(r, at) {
			t.Error("box opening south is stopped by the block above")
		}

		if c.takeOne(r, ItemStack{Name: "minecraft:white_shulker_box", Count: 1}) {
			t.Error("a box took a box")
		}
		if !c.takeOne(r, ItemStack{Name: "minecraft:stone", Count: 1}) {
			t.Error("a box refused stone")
		}

		saved := c.SaveData(at)
		if saved["id"] != shulkerEntityID || saved[shulkerFacingTag] != uint8(cube.FaceSouth) {
			t.Errorf("saved %v", saved)
		}
		loaded, ok := entities.Load(at, saved)
		if l, isChest := loaded.(*Chest); !ok || !isChest || !l.shulker || l.facing != uint8(cube.FaceSouth) || l.items[0].Name != "minecraft:stone" {
			t.Errorf("loaded %+v", loaded)
		}

		// Its items go with the drop, and into the box placed from it.
		drops := p.s.shulkerDrops(r, at, []ItemStack{{Name: "minecraft:red_shulker_box", Count: 1}})
		r.SetBlock(at, 0, reg.Blocks.Air())
		o := p.s.chestAt(r, next)
		o.placedFrom(r, &Placement{Face: int32(cube.FaceWest), Pos: next, Item: drops[0]})
		if o.items[0].Name != "minecraft:stone" || o.items[0].Count != 1 || o.facing != uint8(cube.FaceWest) {
			t.Errorf("placed from the drop: facing %d, %+v", o.facing, o.items[0])
		}
	})
}
