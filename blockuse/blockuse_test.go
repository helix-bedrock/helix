package blockuse_test

import (
	"testing"
	"time"

	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/liquid"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// surfaceY is the air just above the default flat world.
const surfaceY = -60

// open starts a flat world with the zone around chunk 0,0 loaded and returns a
// way to run code on its region.
func open(t *testing.T) (*registry.Registry, func(func(*world.Region))) {
	t.Helper()
	s := registry.NewSetup(nil, "minecraft")
	if err := registry.Vanilla(s); err != nil {
		t.Fatal(err)
	}
	reg, err := s.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	db, err := mcdb.Config{Blocks: reg.Blocks}.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r, _ := mcdb.Overworld.Range()
	flat, err := world.ParseFlat("", reg.Blocks, reg.Biomes, r)
	if err != nil {
		t.Fatal(err)
	}
	conf := world.DefaultConfig()
	conf.Store, conf.Blocks, conf.Biomes, conf.Generator = db, reg.Blocks, reg.Biomes, flat
	conf.Ticker = blockuse.NewTicker(reg.Blocks, liquid.New(reg.Blocks))
	d, err := world.New(conf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	v := d.AddViewer(func(packet.Packet) {}, func() {})
	t.Cleanup(v.Close)
	v.Move(mcdb.ChunkPos{}, 2)
	select {
	case <-v.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("zone never loaded")
	}
	return reg, func(fn func(*world.Region)) {
		done := make(chan struct{})
		d.RunAt(mcdb.ChunkPos{}, func(r *world.Region) { fn(r); close(done) })
		<-done
	}
}

// bit reads a bit state of the block at pos.
func bit(reg *registry.Registry, r *world.Region, pos cube.Pos, key string) uint8 {
	b, _ := r.Block(pos, 0)
	v, _ := reg.Blocks.States(b)[key].(uint8)
	return v
}

// TestDoor places a door, opens it from its upper half, and breaks its lower
// half to see the upper one go too.
func TestDoor(t *testing.T) {
	reg, run := open(t)
	door, _ := reg.Blocks.Block("minecraft:wooden_door")
	lower := cube.Pos{8, surfaceY, 8}
	upper := lower.Side(cube.FaceUp)
	run(func(r *world.Region) {
		if !blockuse.PlaceDoor(reg.Blocks, r, lower, door) {
			t.Fatal("door not placed")
		}
		if bit(reg, r, upper, "upper_block_bit") != 1 {
			t.Fatal("no upper half")
		}
		b, _ := r.Block(upper, 0)
		if !blockuse.Use(reg.Blocks, r, upper, b, 0) || bit(reg, r, lower, "open_bit") != 1 {
			t.Error("using the upper half did not open the door")
		}
		r.SetBlock(lower, 0, reg.Blocks.Air())
		r.UpdateNeighbours(lower)
		if b, _ := r.Block(upper, 0); b != reg.Blocks.Air() {
			t.Error("upper half stayed without its lower half")
		}
	})
}

// TestIronDoorRefuses checks an iron door leaves the click to the held item.
func TestIronDoorRefuses(t *testing.T) {
	reg, run := open(t)
	door, _ := reg.Blocks.Block("minecraft:iron_door")
	run(func(r *world.Region) {
		if blockuse.Use(reg.Blocks, r, cube.Pos{8, surfaceY, 8}, door, 0) {
			t.Error("an iron door took the click")
		}
	})
}

// TestButtonPopsOut presses a stone button and waits for it to come back.
func TestButtonPopsOut(t *testing.T) {
	reg, run := open(t)
	button, _ := reg.Blocks.Block("minecraft:stone_button")
	at := cube.Pos{8, surfaceY, 8}
	run(func(r *world.Region) {
		r.SetBlock(at, 0, button)
		if !blockuse.Use(reg.Blocks, r, at, button, 0) || bit(reg, r, at, "button_pressed_bit") != 1 {
			t.Error("button not pressed")
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var pressed uint8
		run(func(r *world.Region) { pressed = bit(reg, r, at, "button_pressed_bit") })
		if pressed == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("button never popped out")
}
