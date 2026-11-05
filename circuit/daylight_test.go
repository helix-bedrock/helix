package circuit_test

import (
	"testing"

	"github.com/helix-bedrock/helix/attach"
	"github.com/helix-bedrock/helix/daytime"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

func stoppedClock(time int32) *daytime.Clock {
	return daytime.NewClock(func() int64 { return 0 }, time, false)
}

func TestDaylightSensorReadsTheSky(t *testing.T) {
	for _, c := range []struct {
		name  string
		block string
		time  int32
		want  int32
	}{
		{"noon", "minecraft:daylight_detector", 6000, 15},
		{"midnight", "minecraft:daylight_detector", 18000, 0},
		{"inverted at midnight", "minecraft:daylight_detector_inverted", 18000, 11},
		{"inverted at noon", "minecraft:daylight_detector_inverted", 6000, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := openWorld(t)
			w.circuits.SetClock(stoppedClock(c.time))
			at := cube.Pos{4, surfaceY, 4}
			lamp := cube.Pos{5, surfaceY, 4}
			w.run(func(r *world.Region) {
				place(r, at, w.state(t, c.block, nil))
				place(r, lamp, w.state(t, "minecraft:redstone_lamp", nil))
			})
			w.wait(t, func(r *world.Region) bool {
				b, _ := r.Block(at, 0)
				return b == w.state(t, c.block, map[string]any{"redstone_signal": c.want}) && (c.want == 0 || w.name(r, lamp) == "minecraft:lit_redstone_lamp")
			})
		})
	}
}

func TestSkyLight(t *testing.T) {
	w := openWorld(t)
	stone := w.state(t, "minecraft:stone", nil)
	open, under := cube.Pos{4, surfaceY, 4}, cube.Pos{8, surfaceY, 8}
	w.run(func(r *world.Region) {
		if got := r.SkyLight(open); got != 15 {
			t.Errorf("open sky: %d", got)
		}
		// A roof reaching two blocks in from the edge on one side: light comes in sideways.
		for x := 7; x <= 12; x++ {
			for z := 3; z <= 13; z++ {
				place(r, cube.Pos{x, surfaceY + 1, z}, stone)
			}
		}
		if got := r.SkyLight(under); got != 13 {
			t.Errorf("two blocks under a roof: %d, want 13", got)
		}
		// Shut in on all sides: nothing.
		shut := cube.Pos{10, surfaceY, 8}
		for f := cube.Face(0); f < 6; f++ {
			place(r, shut.Side(f), stone)
		}
		if got := r.SkyLight(shut); got != 0 {
			t.Errorf("shut in: %d", got)
		}
	})
}

func TestButtonNeedsAHolder(t *testing.T) {
	w := openWorld(t)
	held, err := attach.New(w.reg.Blocks)
	if err != nil {
		t.Fatal(err)
	}
	button := func(face int32) uint32 {
		return w.state(t, "minecraft:stone_button", map[string]any{"facing_direction": face})
	}
	east := int32(cube.FaceEast)
	at := cube.Pos{5, surfaceY, 4}
	holder := cube.Pos{4, surfaceY, 4}
	w.run(func(r *world.Region) {
		place(r, holder, w.state(t, "minecraft:iron_door", nil))
		if held.MayPlace(r, at, button(east), cube.FaceEast) {
			t.Error("a button was allowed on a door")
		}
		place(r, holder, w.state(t, "minecraft:stone", nil))
		if !held.MayPlace(r, at, button(east), cube.FaceEast) {
			t.Error("a button was refused on stone")
		}
		place(r, at, button(east))
		place(r, holder, w.state(t, "minecraft:air", nil))
		r.UpdateNeighbours(holder)
	})
	w.wait(t, func(r *world.Region) bool { return w.name(r, at) == "minecraft:air" })
}
