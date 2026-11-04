package circuit_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
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
	reg      *registry.Registry
	circuits *circuit.Ticker
	d        *world.Dimension
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
	w.d, w.circuits = d, circuits
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

// name is the block at pos's name.
func (w *testWorld) name(r *world.Region, pos cube.Pos) string {
	b, _ := r.Block(pos, 0)
	return w.reg.Blocks.Name(b)
}

// Facing state 3 of a piston points north, so it pushes towards smaller z.
func TestPistonPushesAndComesBack(t *testing.T) {
	w := openWorld(t)
	piston := w.state(t, "minecraft:piston", map[string]any{"facing_direction": int32(3)})
	stone := w.state(t, "minecraft:stone", nil)
	block := w.state(t, "minecraft:redstone_block", nil)
	at := cube.Pos{1, surfaceY, 8}
	arm := cube.Pos{1, surfaceY, 7}
	pushed := cube.Pos{1, surfaceY, 7}
	moved := cube.Pos{1, surfaceY, 6}
	power := cube.Pos{1, surfaceY, 9}
	w.run(func(r *world.Region) {
		place(r, at, piston)
		place(r, pushed, stone)
		place(r, power, block)
	})
	w.wait(t, func(r *world.Region) bool {
		return w.name(r, arm) == "minecraft:piston_arm_collision" && w.name(r, moved) == "minecraft:stone"
	})
	w.run(func(r *world.Region) { place(r, power, w.reg.Blocks.Air()) })
	w.wait(t, func(r *world.Region) bool {
		return w.name(r, arm) == "minecraft:air" && w.name(r, moved) == "minecraft:stone"
	})
}

func TestStickyPistonPullsBack(t *testing.T) {
	w := openWorld(t)
	piston := w.state(t, "minecraft:sticky_piston", map[string]any{"facing_direction": int32(3)})
	stone := w.state(t, "minecraft:stone", nil)
	block := w.state(t, "minecraft:redstone_block", nil)
	at := cube.Pos{1, surfaceY, 8}
	first := cube.Pos{1, surfaceY, 7}
	moved := cube.Pos{1, surfaceY, 6}
	power := cube.Pos{1, surfaceY, 9}
	w.run(func(r *world.Region) {
		place(r, at, piston)
		place(r, first, stone)
		place(r, power, block)
	})
	w.wait(t, func(r *world.Region) bool { return w.name(r, moved) == "minecraft:stone" })
	w.run(func(r *world.Region) { place(r, power, w.reg.Blocks.Air()) })
	w.wait(t, func(r *world.Region) bool {
		return w.name(r, first) == "minecraft:stone" && w.name(r, moved) == "minecraft:air"
	})
}

func TestPistonStoppedByObsidian(t *testing.T) {
	w := openWorld(t)
	piston := w.state(t, "minecraft:piston", map[string]any{"facing_direction": int32(3)})
	obsidian := w.state(t, "minecraft:obsidian", nil)
	block := w.state(t, "minecraft:redstone_block", nil)
	at := cube.Pos{1, surfaceY, 8}
	front := cube.Pos{1, surfaceY, 7}
	w.run(func(r *world.Region) {
		place(r, at, piston)
		place(r, front, obsidian)
		place(r, cube.Pos{1, surfaceY, 9}, block)
	})
	time.Sleep(400 * time.Millisecond)
	w.run(func(r *world.Region) {
		if n := w.name(r, front); n != "minecraft:obsidian" {
			t.Errorf("obsidian became %s", n)
		}
	})
}

func TestPistonBreaksTorchInFront(t *testing.T) {
	w := openWorld(t)
	piston := w.state(t, "minecraft:piston", map[string]any{"facing_direction": int32(3)})
	torch := w.state(t, "minecraft:torch", nil)
	block := w.state(t, "minecraft:redstone_block", nil)
	at := cube.Pos{1, surfaceY, 8}
	front := cube.Pos{1, surfaceY, 7}
	w.run(func(r *world.Region) {
		place(r, at, piston)
		r.SetBlock(front, 0, torch)
		place(r, cube.Pos{1, surfaceY, 9}, block)
	})
	w.wait(t, func(r *world.Region) bool { return w.name(r, front) == "minecraft:piston_arm_collision" })
}

func TestPistonIgnoresPowerAtItsFront(t *testing.T) {
	w := openWorld(t)
	piston := w.state(t, "minecraft:piston", map[string]any{"facing_direction": int32(3)})
	block := w.state(t, "minecraft:redstone_block", nil)
	at := cube.Pos{1, surfaceY, 8}
	front := cube.Pos{1, surfaceY, 7}
	w.run(func(r *world.Region) {
		place(r, at, piston)
		place(r, front, block)
	})
	time.Sleep(400 * time.Millisecond)
	w.run(func(r *world.Region) {
		if n := w.name(r, cube.Pos{1, surfaceY, 6}); n == "minecraft:redstone_block" {
			t.Error("piston was powered through its front")
		}
	})
}

func TestObserverPulses(t *testing.T) {
	w := openWorld(t)
	at := cube.Pos{4, surfaceY, 4}
	front := cube.Pos{5, surfaceY, 4}
	lamp := cube.Pos{3, surfaceY, 4}
	observer := w.state(t, "minecraft:observer", map[string]any{"minecraft:facing_direction": "east"})
	var sawLit, sawPowered bool
	w.run(func(r *world.Region) {
		place(r, lamp, w.state(t, "minecraft:redstone_lamp", nil))
		place(r, at, observer)
	})
	w.wait(t, func(r *world.Region) bool {
		b, _ := r.Block(at, 0)
		return b == observer
	})
	w.run(func(r *world.Region) { place(r, front, w.state(t, "minecraft:stone", nil)) })
	w.wait(t, func(r *world.Region) bool {
		b, _ := r.Block(at, 0)
		powered, _ := w.reg.Blocks.States(b)["powered_bit"].(uint8)
		sawPowered = sawPowered || powered == 1
		sawLit = sawLit || w.name(r, lamp) == "minecraft:lit_redstone_lamp"
		return sawPowered && sawLit && powered == 0
	})
}

func TestObserverIgnoresItsBack(t *testing.T) {
	w := openWorld(t)
	at := cube.Pos{4, surfaceY, 4}
	observer := w.state(t, "minecraft:observer", map[string]any{"minecraft:facing_direction": "east"})
	w.run(func(r *world.Region) {
		place(r, at, observer)
		place(r, cube.Pos{3, surfaceY, 4}, w.state(t, "minecraft:stone", nil))
	})
	for range 10 {
		w.run(func(r *world.Region) {
			if b, _ := r.Block(at, 0); b != observer {
				t.Error("an observer fired for its back")
			}
		})
		time.Sleep(world.TickDuration)
	}
}

func TestPlateGoesDownAndComesUp(t *testing.T) {
	w := openWorld(t)
	at := cube.Pos{4, surfaceY, 4}
	on := atomic.Bool{}
	on.Store(true)
	w.circuits.SetEntityBox(func(e *world.Entity) (lo, hi mgl32.Vec3, mob, ok bool) {
		if !on.Load() {
			return lo, hi, false, false
		}
		return mgl32.Vec3{4.2, surfaceY, 4.2}, mgl32.Vec3{4.8, surfaceY + 2, 4.8}, true, true
	})
	id := w.d.AddEntity(world.EntityConfig{UniqueID: 900, RuntimeID: 900, Pos: mgl32.Vec3{4.5, surfaceY, 4.5}})
	t.Cleanup(id.Remove)
	lamp := cube.Pos{4, surfaceY - 1, 5}
	w.run(func(r *world.Region) {
		place(r, cube.Pos{4, surfaceY - 1, 4}, w.state(t, "minecraft:stone", nil))
		place(r, lamp, w.state(t, "minecraft:redstone_lamp", nil))
		place(r, at, w.state(t, "minecraft:stone_pressure_plate", nil))
	})
	w.wait(t, func(r *world.Region) bool {
		b, _ := r.Block(at, 0)
		return w.reg.Blocks.States(b)["redstone_signal"] == int32(1) && w.name(r, lamp) == "minecraft:lit_redstone_lamp"
	})
	on.Store(false)
	w.wait(t, func(r *world.Region) bool {
		b, _ := r.Block(at, 0)
		return w.reg.Blocks.States(b)["redstone_signal"] == int32(0)
	})
}

func TestNoteBlockTunes(t *testing.T) {
	w := openWorld(t)
	at := cube.Pos{4, surfaceY, 4}
	block := w.state(t, "minecraft:noteblock", nil)
	w.run(func(r *world.Region) {
		place(r, at, block)
		for range 26 {
			if !w.circuits.UseBlock(r, at, block) {
				t.Error("a note block did not take a use")
			}
		}
		be, ok := r.BlockEntity(at)
		if !ok {
			t.Fatal("no block entity")
		}
		saved := be.SaveData(at)
		if saved["id"] != "Music" || saved["note"] != uint8(1) {
			t.Errorf("saved %v, want Music at note 1 after 26 uses", saved)
		}
	})
}

func TestDaylightSensorSwaps(t *testing.T) {
	w := openWorld(t)
	at := cube.Pos{4, surfaceY, 4}
	block := w.state(t, "minecraft:daylight_detector", nil)
	w.run(func(r *world.Region) {
		place(r, at, block)
		w.circuits.UseBlock(r, at, block)
		if got := w.name(r, at); got != "minecraft:daylight_detector_inverted" {
			t.Errorf("after use it is %s", got)
		}
	})
}

func TestWireJoinsObserverBack(t *testing.T) {
	w := openWorld(t)
	wire := cube.Pos{4, surfaceY, 4}
	w.run(func(r *world.Region) {
		place(r, cube.Pos{5, surfaceY, 4}, w.state(t, "minecraft:observer", map[string]any{"minecraft:facing_direction": "east"}))
		place(r, wire, w.state(t, "minecraft:redstone_wire", nil))
	})
	// Joined on one side, wire runs straight through. Joined on none it would show all
	// four sides.
	w.wait(t, func(r *world.Region) bool {
		b, _ := r.Block(wire, 0)
		s := w.reg.Blocks.States(b)
		return s["redstone_east"] == "side" && s["redstone_north"] == "none"
	})
}

// tripwire builds hooks with wire between them along x, each hook held by stone, and
// returns the west hook, the east hook and the middle wire.
func (w *testWorld) tripwire(t *testing.T, r *world.Region) (west, east, mid cube.Pos) {
	t.Helper()
	y := surfaceY
	stone := w.state(t, "minecraft:stone", nil)
	place(r, cube.Pos{1, y, 4}, stone)
	place(r, cube.Pos{6, y, 4}, stone)
	west, east, mid = cube.Pos{2, y, 4}, cube.Pos{5, y, 4}, cube.Pos{3, y, 4}
	place(r, west, w.state(t, "minecraft:tripwire_hook", map[string]any{"direction": int32(3)}))
	place(r, east, w.state(t, "minecraft:tripwire_hook", map[string]any{"direction": int32(1)}))
	place(r, mid, w.state(t, "minecraft:trip_wire", nil))
	place(r, cube.Pos{4, y, 4}, w.state(t, "minecraft:trip_wire", nil))
	return west, east, mid
}

func TestTripwireAttachesAndFires(t *testing.T) {
	w := openWorld(t)
	var west, east, mid cube.Pos
	w.run(func(r *world.Region) { west, east, mid = w.tripwire(t, r) })
	bit := func(r *world.Region, pos cube.Pos, key string) bool {
		b, _ := r.Block(pos, 0)
		v, _ := w.reg.Blocks.States(b)[key].(uint8)
		return v == 1
	}
	w.wait(t, func(r *world.Region) bool {
		return bit(r, west, "attached_bit") && bit(r, east, "attached_bit") && bit(r, mid, "attached_bit")
	})
	on := atomic.Bool{}
	on.Store(true)
	w.circuits.SetEntityBox(func(e *world.Entity) (lo, hi mgl32.Vec3, mob, ok bool) {
		if !on.Load() {
			return lo, hi, false, false
		}
		return mgl32.Vec3{3.2, surfaceY, 4.2}, mgl32.Vec3{3.8, surfaceY + 2, 4.8}, true, true
	})
	e := w.d.AddEntity(world.EntityConfig{UniqueID: 901, RuntimeID: 901, Pos: mgl32.Vec3{3.5, surfaceY, 4.5}})
	t.Cleanup(e.Remove)
	lamp := cube.Pos{2, surfaceY, 5}
	w.run(func(r *world.Region) { place(r, lamp, w.state(t, "minecraft:redstone_lamp", nil)) })
	w.wait(t, func(r *world.Region) bool {
		return bit(r, mid, "powered_bit") && bit(r, west, "powered_bit") && bit(r, east, "powered_bit") &&
			w.name(r, lamp) == "minecraft:lit_redstone_lamp"
	})
	on.Store(false)
	w.wait(t, func(r *world.Region) bool { return !bit(r, mid, "powered_bit") && !bit(r, west, "powered_bit") })
}

func TestTripwireCutWithoutShearsFires(t *testing.T) {
	w := openWorld(t)
	var west, mid cube.Pos
	w.run(func(r *world.Region) { west, _, mid = w.tripwire(t, r) })
	w.run(func(r *world.Region) {
		b, _ := r.Block(mid, 0)
		w.circuits.DestroyBlock(r, mid, b, "minecraft:diamond_sword")
		h, _ := r.Block(west, 0)
		if v, _ := w.reg.Blocks.States(h)["powered_bit"].(uint8); v != 1 {
			t.Error("cutting a wire by hand should set it off")
		}
	})
}

func TestPistonPushesItems(t *testing.T) {
	w := openWorld(t)
	w.circuits.SetEntityBox(func(e *world.Entity) (lo, hi mgl32.Vec3, mob, ok bool) {
		at := e.Position()
		return mgl32.Vec3{at[0] - 0.125, at[1], at[2] - 0.125}, mgl32.Vec3{at[0] + 0.125, at[1] + 0.25, at[2] + 0.125}, false, true
	})
	item := w.d.AddEntity(world.EntityConfig{UniqueID: 902, RuntimeID: 902, Pos: mgl32.Vec3{5.5, surfaceY, 4.5}})
	t.Cleanup(item.Remove)
	piston := w.state(t, "minecraft:piston", map[string]any{"facing_direction": int32(4)})
	w.run(func(r *world.Region) { place(r, cube.Pos{4, surfaceY, 4}, piston) })
	w.run(func(r *world.Region) { place(r, cube.Pos{3, surfaceY, 4}, w.state(t, "minecraft:redstone_block", nil)) })
	w.wait(t, func(r *world.Region) bool { return item.Position()[0] >= 6 })
}

func TestTrappedChestPowersWhileOpen(t *testing.T) {
	w := openWorld(t)
	at := cube.Pos{4, surfaceY, 4}
	lamp := cube.Pos{5, surfaceY, 4}
	w.run(func(r *world.Region) {
		place(r, at, w.state(t, "minecraft:trapped_chest", nil))
		place(r, lamp, w.state(t, "minecraft:redstone_lamp", nil))
	})
	w.wait(t, func(r *world.Region) bool {
		_, ok := r.Block(lamp, 0)
		return ok
	})
	w.run(func(r *world.Region) { w.circuits.SetChestViewers(r, at, 1) })
	w.wait(t, func(r *world.Region) bool { return w.name(r, lamp) == "minecraft:lit_redstone_lamp" })
	w.run(func(r *world.Region) { w.circuits.SetChestViewers(r, at, 0) })
	w.wait(t, func(r *world.Region) bool { return w.name(r, lamp) == "minecraft:redstone_lamp" })
}

func TestPowerLightsTNT(t *testing.T) {
	w := openWorld(t)
	got := make(chan cube.Pos, 1)
	w.circuits.SetPowerTNT(func(r *world.Region, pos cube.Pos) {
		select {
		case got <- pos:
		default:
		}
	})
	at := cube.Pos{4, surfaceY, 4}
	w.run(func(r *world.Region) { place(r, at, w.state(t, "minecraft:tnt", nil)) })
	w.run(func(r *world.Region) { place(r, cube.Pos{5, surfaceY, 4}, w.state(t, "minecraft:redstone_block", nil)) })
	select {
	case pos := <-got:
		if pos != at {
			t.Errorf("lit %v", pos)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("power did not reach the TNT")
	}
}
