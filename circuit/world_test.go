package circuit_test

import (
	"testing"
	"time"

	"github.com/helix-bedrock/helix/attach"
	"github.com/helix-bedrock/helix/circuit"
	"github.com/helix-bedrock/helix/liquid"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// surfaceY is the air just above the default flat world.
const surfaceY = -60

// testWorld is a flat world with circuits around chunk 0,0.
type testWorld struct {
	reg *registry.Registry
	d   *world.Dimension
	// fell is every block the circuit broke.
	fell chan cube.Pos
}

func openWorld(t *testing.T) *testWorld {
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
	attached, err := attach.New(reg.Blocks)
	if err != nil {
		t.Fatal(err)
	}
	w := &testWorld{reg: reg, fell: make(chan cube.Pos, 16)}
	fall := func(r *world.Region, pos cube.Pos, _ uint32) {
		r.SetBlock(pos, 0, reg.Blocks.Air())
		w.fell <- pos
	}
	circuits, err := circuit.NewTicker(reg, attached, fall, liquid.New(reg.Blocks))
	if err != nil {
		t.Fatal(err)
	}
	conf := world.DefaultConfig()
	conf.Store, conf.Blocks, conf.Biomes, conf.Generator = db, reg.Blocks, reg.Biomes, flat
	conf.Ticker = circuits
	d, err := world.New(conf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	d.SetBlockEntities(circuits.Entities(nil))
	v := d.AddViewer(func(packet.Packet) {}, func() {})
	t.Cleanup(v.Close)
	v.Move(mcdb.ChunkPos{}, 4)
	select {
	case <-v.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("zone never loaded")
	}
	w.d = d
	return w
}

// run runs fn on the region owning chunk 0,0 and waits for it.
func (w *testWorld) run(fn func(r *world.Region)) {
	done := make(chan struct{})
	w.d.RunAt(mcdb.ChunkPos{}, func(r *world.Region) {
		fn(r)
		close(done)
	})
	<-done
}

// wait polls until cond holds on the region, or fails the test.
func (w *testWorld) wait(t *testing.T, cond func(r *world.Region) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var ok bool
		w.run(func(r *world.Region) { ok = cond(r) })
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// state returns the default state of name with the changes.
func (w *testWorld) state(t *testing.T, name string, changes map[string]any) uint32 {
	t.Helper()
	id, ok := w.reg.Blocks.Block(name)
	if !ok {
		t.Fatalf("no block %s", name)
	}
	states := map[string]any{}
	for k, v := range w.reg.Blocks.States(id) {
		states[k] = v
	}
	for k, v := range changes {
		states[k] = v
	}
	id, ok = w.reg.Blocks.StateID(name, states)
	if !ok {
		t.Fatalf("no state of %s with %v", name, changes)
	}
	return id
}

// place sets a block as gameplay does.
func place(r *world.Region, pos cube.Pos, block uint32) {
	if r.SetBlock(pos, 0, block) {
		r.UpdateNeighbours(pos)
	}
}

func TestWireBreaksWithoutSupport(t *testing.T) {
	w := openWorld(t)
	stone := w.state(t, "minecraft:stone", nil)
	wire := w.state(t, "minecraft:redstone_wire", nil)
	under := cube.Pos{1, surfaceY, 1}
	at := cube.Pos{1, surfaceY + 1, 1}
	w.run(func(r *world.Region) {
		place(r, under, stone)
		place(r, at, wire)
	})
	w.run(func(r *world.Region) { place(r, under, w.reg.Blocks.Air()) })
	select {
	case pos := <-w.fell:
		if pos != at {
			t.Fatalf("fell at %v, want %v", pos, at)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wire stayed with nothing under it")
	}
}

func TestWireConnects(t *testing.T) {
	w := openWorld(t)
	stone := w.state(t, "minecraft:stone", nil)
	wire := w.state(t, "minecraft:redstone_wire", nil)
	at := cube.Pos{1, surfaceY, 1}
	east := cube.Pos{2, surfaceY, 1}
	stepBlock := cube.Pos{1, surfaceY, 2}
	stepWire := cube.Pos{1, surfaceY + 1, 2}
	w.run(func(r *world.Region) {
		place(r, at, wire)
		place(r, east, wire)
		place(r, stepBlock, stone)
		place(r, stepWire, wire)
	})
	side := func(r *world.Region, key string) any {
		b, _ := r.Block(at, 0)
		return w.reg.Blocks.States(b)[key]
	}
	w.wait(t, func(r *world.Region) bool {
		return side(r, "redstone_east") == "side" && side(r, "redstone_south") == "up"
	})
	// A conductor above cuts the wire going up.
	w.run(func(r *world.Region) { place(r, cube.Pos{1, surfaceY + 1, 1}, stone) })
	w.wait(t, func(r *world.Region) bool { return side(r, "redstone_south") == "none" })
}

func TestLampStaysLitThroughShortOff(t *testing.T) {
	w := openWorld(t)
	block := w.state(t, "minecraft:redstone_block", nil)
	lamp := w.state(t, "minecraft:redstone_lamp", nil)
	at := cube.Pos{1, surfaceY, 1}
	source := cube.Pos{2, surfaceY, 1}
	lit := func(r *world.Region) bool {
		b, _ := r.Block(at, 0)
		return w.reg.Blocks.Name(b) == "minecraft:lit_redstone_lamp"
	}
	w.run(func(r *world.Region) {
		place(r, at, lamp)
		place(r, source, block)
	})
	w.wait(t, lit)
	w.run(func(r *world.Region) { place(r, source, w.reg.Blocks.Air()) })
	w.wait(t, func(r *world.Region) bool { return !lit(r) })
}

func TestComparatorKeepsSignal(t *testing.T) {
	w := openWorld(t)
	block := w.state(t, "minecraft:redstone_block", nil)
	comp := w.state(t, "minecraft:unpowered_comparator", map[string]any{"minecraft:cardinal_direction": "north"})
	at := cube.Pos{1, surfaceY, 1}
	back := cube.Pos{1, surfaceY, 0}
	w.run(func(r *world.Region) {
		place(r, at, comp)
		place(r, back, block)
	})
	w.wait(t, func(r *world.Region) bool {
		b, _ := r.Block(at, 0)
		if w.reg.Blocks.Name(b) != "minecraft:powered_comparator" {
			return false
		}
		be, ok := r.BlockEntity(at)
		if !ok {
			return false
		}
		data := be.SaveData(at)
		return data["id"] == "Comparator" && data["OutputSignal"] == int32(15)
	})
}

func TestContainerSum(t *testing.T) {
	cases := []struct {
		name  string
		count []int
		size  int
		want  int32
	}{
		{"empty", nil, 27, 0},
		{"one item", []int{1}, 27, 1},
		{"full", []int{64, 64, 64, 64, 64}, 5, 15},
		{"half a hopper", []int{64, 64, 32}, 5, 8},
	}
	for _, c := range cases {
		var sum circuit.ContainerSum
		for _, n := range c.count {
			sum.Add(n, 64)
		}
		if got := sum.Signal(c.size); got != c.want {
			t.Errorf("%s: signal %d, want %d", c.name, got, c.want)
		}
	}
}

func TestComparatorReadsBlocks(t *testing.T) {
	w := openWorld(t)
	stone := w.state(t, "minecraft:stone", nil)
	composter := w.state(t, "minecraft:composter", map[string]any{"composter_fill_level": int32(5)})
	comp := w.state(t, "minecraft:unpowered_comparator", map[string]any{"minecraft:cardinal_direction": "north"})
	at := cube.Pos{1, surfaceY, 3}
	output := func(r *world.Region) any {
		be, ok := r.BlockEntity(at)
		if !ok {
			return nil
		}
		return be.SaveData(at)["OutputSignal"]
	}
	w.run(func(r *world.Region) {
		place(r, at, comp)
		place(r, cube.Pos{1, surfaceY, 2}, composter)
	})
	w.wait(t, func(r *world.Region) bool { return output(r) == int32(5) })
	// Through a solid block, one further on.
	w.run(func(r *world.Region) {
		place(r, cube.Pos{1, surfaceY, 2}, stone)
		place(r, cube.Pos{1, surfaceY, 1}, composter)
	})
	w.wait(t, func(r *world.Region) bool {
		b, _ := r.Block(at, 0)
		return w.reg.Blocks.Name(b) == "minecraft:powered_comparator" && output(r) == int32(5)
	})
	// Nothing to read turns it off.
	w.run(func(r *world.Region) { place(r, cube.Pos{1, surfaceY, 1}, stone) })
	w.wait(t, func(r *world.Region) bool {
		b, _ := r.Block(at, 0)
		return w.reg.Blocks.Name(b) == "minecraft:unpowered_comparator" && output(r) == int32(0)
	})
}

func TestLampPowerCancelsOff(t *testing.T) {
	w := openWorld(t)
	block := w.state(t, "minecraft:redstone_block", nil)
	lamp := w.state(t, "minecraft:lit_redstone_lamp", nil)
	at := cube.Pos{1, surfaceY, 1}
	w.run(func(r *world.Region) {
		place(r, at, lamp)
		r.ScheduleTick(at, lamp, 4, 0)
		place(r, cube.Pos{2, surfaceY, 1}, block)
	})
	w.wait(t, func(r *world.Region) bool { return !r.HasScheduledTick(at, lamp) })
	time.Sleep(300 * time.Millisecond)
	w.run(func(r *world.Region) {
		if b, _ := r.Block(at, 0); w.reg.Blocks.Name(b) != "minecraft:lit_redstone_lamp" {
			t.Errorf("lamp with power went out: %s", w.reg.Blocks.Name(b))
		}
	})
}
