package circuit_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// railDirection reads a rail's direction.
func (w *testWorld) railDirection(r *world.Region, pos cube.Pos) int32 {
	b, _ := r.Block(pos, 0)
	v, _ := w.reg.Blocks.States(b)["rail_direction"].(int32)
	return v
}

// railOn reads a rail's bit.
func (w *testWorld) railOn(r *world.Region, pos cube.Pos) bool {
	b, _ := r.Block(pos, 0)
	v, _ := w.reg.Blocks.States(b)["rail_data_bit"].(uint8)
	return v != 0
}

func TestRailsLineUp(t *testing.T) {
	w := openWorld(t)
	rail := w.state(t, "minecraft:rail", nil)
	w.run(func(r *world.Region) {
		place(r, cube.Pos{4, surfaceY, 4}, rail)
		place(r, cube.Pos{5, surfaceY, 4}, rail)
	})
	w.wait(t, func(r *world.Region) bool {
		return w.railDirection(r, cube.Pos{4, surfaceY, 4}) == 1 && w.railDirection(r, cube.Pos{5, surfaceY, 4}) == 1
	})
}

func TestRailCurves(t *testing.T) {
	w := openWorld(t)
	rail := w.state(t, "minecraft:rail", nil)
	w.run(func(r *world.Region) {
		place(r, cube.Pos{4, surfaceY, 4}, rail)
		place(r, cube.Pos{5, surfaceY, 4}, rail)
		place(r, cube.Pos{4, surfaceY, 5}, rail)
	})
	// East and south of the first rail.
	w.wait(t, func(r *world.Region) bool { return w.railDirection(r, cube.Pos{4, surfaceY, 4}) == 6 })
}

func TestGoldenRailDoesNotCurve(t *testing.T) {
	w := openWorld(t)
	golden := w.state(t, "minecraft:golden_rail", nil)
	w.run(func(r *world.Region) {
		place(r, cube.Pos{4, surfaceY, 4}, golden)
		place(r, cube.Pos{5, surfaceY, 4}, golden)
		place(r, cube.Pos{4, surfaceY, 5}, golden)
	})
	w.wait(t, func(r *world.Region) bool {
		d := w.railDirection(r, cube.Pos{4, surfaceY, 4})
		return d < 6 && w.railDirection(r, cube.Pos{5, surfaceY, 4}) == 1
	})
}

func TestRailClimbsToRailAbove(t *testing.T) {
	w := openWorld(t)
	rail := w.state(t, "minecraft:rail", nil)
	w.run(func(r *world.Region) {
		place(r, cube.Pos{5, surfaceY, 4}, w.state(t, "minecraft:stone", nil))
		place(r, cube.Pos{5, surfaceY + 1, 4}, rail)
		place(r, cube.Pos{4, surfaceY, 4}, rail)
	})
	w.wait(t, func(r *world.Region) bool { return w.railDirection(r, cube.Pos{4, surfaceY, 4}) == 2 })
}

func TestRailFallsWithoutFloor(t *testing.T) {
	w := openWorld(t)
	floor := cube.Pos{4, surfaceY - 1, 4}
	at := cube.Pos{4, surfaceY, 4}
	w.run(func(r *world.Region) { place(r, at, w.state(t, "minecraft:rail", nil)) })
	w.run(func(r *world.Region) {
		r.SetBlock(floor, 0, w.reg.Blocks.Air())
		r.UpdateNeighbours(floor)
	})
	w.wait(t, func(r *world.Region) bool { return w.name(r, at) == "minecraft:air" })
}

func TestPoweredRailCarriesNineRails(t *testing.T) {
	w := openWorld(t)
	golden := w.state(t, "minecraft:golden_rail", nil)
	w.run(func(r *world.Region) {
		for x := 1; x <= 12; x++ {
			place(r, cube.Pos{x, surfaceY, 4}, golden)
		}
		// A redstone block at the end of the line.
		place(r, cube.Pos{0, surfaceY, 4}, w.state(t, "minecraft:redstone_block", nil))
	})
	w.wait(t, func(r *world.Region) bool {
		return w.railOn(r, cube.Pos{1, surfaceY, 4}) && w.railOn(r, cube.Pos{9, surfaceY, 4}) && !w.railOn(r, cube.Pos{10, surfaceY, 4})
	})
	w.run(func(r *world.Region) {
		r.SetBlock(cube.Pos{0, surfaceY, 4}, 0, w.reg.Blocks.Air())
		r.UpdateNeighbours(cube.Pos{0, surfaceY, 4})
	})
	w.wait(t, func(r *world.Region) bool { return !w.railOn(r, cube.Pos{1, surfaceY, 4}) })
}

func TestPoweredRailStopsAtActivatorRail(t *testing.T) {
	w := openWorld(t)
	w.run(func(r *world.Region) {
		place(r, cube.Pos{1, surfaceY, 4}, w.state(t, "minecraft:golden_rail", nil))
		place(r, cube.Pos{2, surfaceY, 4}, w.state(t, "minecraft:activator_rail", nil))
		place(r, cube.Pos{0, surfaceY, 4}, w.state(t, "minecraft:redstone_block", nil))
	})
	w.wait(t, func(r *world.Region) bool { return w.railOn(r, cube.Pos{1, surfaceY, 4}) })
	w.run(func(r *world.Region) {
		if w.railOn(r, cube.Pos{2, surfaceY, 4}) {
			t.Error("power crossed to the other kind of rail")
		}
	})
}

func TestDetectorRailPowersForCart(t *testing.T) {
	w := openWorld(t)
	at := cube.Pos{4, surfaceY, 4}
	lamp := cube.Pos{4, surfaceY - 1, 5}
	w.run(func(r *world.Region) {
		place(r, at, w.state(t, "minecraft:detector_rail", nil))
		place(r, lamp, w.state(t, "minecraft:redstone_lamp", nil))
	})
	present := true
	cart := mgl64.Vec3{4.5, surfaceY + 0.35, 4.5}
	w.circuits.SetEntityBox(func(*world.Entity) (lo, hi mgl64.Vec3, mob, ok bool) {
		return cart.Sub(mgl64.Vec3{0.49, 0.35, 0.49}), cart.Add(mgl64.Vec3{0.49, 0.35, 0.49}), false, true
	})
	w.circuits.SetCartBox(func(*world.Entity) (int32, bool) { return -1, present })
	e := w.d.AddEntity(world.EntityConfig{UniqueID: 901, RuntimeID: 901, Pos: cart})
	t.Cleanup(e.Remove)
	w.run(func(r *world.Region) { w.circuits.CartOnRail(r, at) })
	w.wait(t, func(r *world.Region) bool { return w.railOn(r, at) })
	w.run(func(*world.Region) { present = false })
	w.wait(t, func(r *world.Region) bool { return !w.railOn(r, at) })
}
