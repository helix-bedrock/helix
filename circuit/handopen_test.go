package circuit_test

import (
	"testing"
	"time"

	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// A door, trapdoor or gate opened by hand with no power near stays open while the
// circuits tick: only a change of power moves it.
func TestHandOpenedStaysOpen(t *testing.T) {
	w := openWorld(t)
	blocks := w.reg.Blocks
	door, gate, trap := cube.Pos{1, surfaceY, 1}, cube.Pos{4, surfaceY, 1}, cube.Pos{7, surfaceY, 1}
	w.run(func(r *world.Region) {
		blockuse.PlaceDoor(blocks, r, door, w.state(t, "minecraft:wooden_door", nil))
		place(r, gate, w.state(t, "minecraft:fence_gate", nil))
		place(r, trap, w.state(t, "minecraft:trapdoor", nil))
	})
	time.Sleep(200 * time.Millisecond)
	w.run(func(r *world.Region) {
		for _, pos := range []cube.Pos{door.Side(cube.FaceUp), gate, trap} {
			b, _ := r.Block(pos, 0)
			blockuse.Use(blocks, r, pos, b, 0)
		}
	})
	time.Sleep(500 * time.Millisecond)
	w.run(func(r *world.Region) {
		for _, pos := range []cube.Pos{door, gate, trap} {
			b, _ := r.Block(pos, 0)
			if v, _ := blocks.States(b)["open_bit"].(uint8); v == 0 {
				t.Errorf("%s at %v shut again", blocks.Name(b), pos)
			}
		}
	})
}

// A lever next to a door still opens it, and shuts it when switched off.
func TestLeverStillWorksDoor(t *testing.T) {
	w := openWorld(t)
	blocks := w.reg.Blocks
	door, lever := cube.Pos{1, surfaceY, 1}, cube.Pos{2, surfaceY, 1}
	w.run(func(r *world.Region) {
		blockuse.PlaceDoor(blocks, r, door, w.state(t, "minecraft:wooden_door", nil))
		place(r, lever, w.state(t, "minecraft:lever", map[string]any{"lever_direction": "up_north_south"}))
	})
	isOpen := func(r *world.Region) bool {
		b, _ := r.Block(door, 0)
		v, _ := blocks.States(b)["open_bit"].(uint8)
		return v != 0
	}
	flip := func() {
		w.run(func(r *world.Region) {
			b, _ := r.Block(lever, 0)
			blockuse.Use(blocks, r, lever, b, 0)
		})
	}
	flip()
	w.wait(t, isOpen)
	flip()
	w.wait(t, func(r *world.Region) bool { return !isOpen(r) })
}
