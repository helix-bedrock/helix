package player

import (
	"testing"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// A closed door has its panel for whatever is thrown at it, and an open one moves it.
func TestDoorStopsWhatMoves(t *testing.T) {
	p, d, y := cartWorld(t, "minecraft:air", nil)
	blocks := p.s.reg.Blocks
	door, ok := blocks.StateID("minecraft:wooden_door", map[string]any{
		"minecraft:cardinal_direction": "west", "open_bit": uint8(0), "upper_block_bit": uint8(0), "door_hinge_bit": uint8(0),
	})
	if !ok {
		t.Fatal("no door state")
	}
	at := cube.Pos{4, y, 4}
	onRegion(d, func(r *world.Region) {
		if !blockuse.PlaceDoor(blocks, r, at, door) {
			t.Fatal("door not placed")
		}
		lower, _ := r.Block(at, 0)
		upper, _ := r.Block(at.Side(cube.FaceUp), 0)
		// A door turned west closes along the north side, for both its halves.
		want := registry.Box{0, 0, 0, 1, 1, 0.1825}
		for _, half := range []struct {
			pos cube.Pos
			b   uint32
		}{{at, lower}, {at.Side(cube.FaceUp), upper}} {
			got := p.s.boxesAt(r, half.pos, half.b)
			if len(got) != 1 || got[0] != want {
				t.Errorf("closed door at %v: %v", half.pos, got)
			}
		}
		// From the north, moving south, the panel stops the box.
		lo, hi := mgl64.Vec3{4.4, float64(y) + 0.3, 3.8}, mgl64.Vec3{4.6, float64(y) + 0.5, 3.95}
		if got := p.s.clipSweep(r, lo, hi, 2, 0.5); got > 0.051 {
			t.Errorf("box went %v through the closed door", got)
		}
		// Once open the panel is out of its way.
		opened, _ := blocks.StateID("minecraft:wooden_door", map[string]any{
			"minecraft:cardinal_direction": "west", "open_bit": uint8(1), "upper_block_bit": uint8(0), "door_hinge_bit": uint8(0),
		})
		r.SetBlock(at, 0, opened)
		if got := p.s.clipSweep(r, lo, hi, 2, 0.5); got != 0.5 {
			t.Errorf("open door stopped the box at %v", got)
		}
	})
}

// A door half left alone reads as BDS's door with everything at zero.
func TestLoneDoorHalf(t *testing.T) {
	p, d, y := cartWorld(t, "minecraft:air", nil)
	blocks := p.s.reg.Blocks
	lone, _ := blocks.StateID("minecraft:wooden_door", map[string]any{
		"minecraft:cardinal_direction": "north", "open_bit": uint8(1), "upper_block_bit": uint8(1), "door_hinge_bit": uint8(1),
	})
	at := cube.Pos{4, y + 2, 4}
	onRegion(d, func(r *world.Region) {
		r.SetBlock(at, 0, lone)
		got := p.s.boxesAt(r, at, lone)
		if len(got) != 1 || got[0] != (registry.Box{0, 0, 0, 0.1825, 1, 1}) {
			t.Errorf("lone half: %v", got)
		}
	})
}

// An item really stops at a closed door, and does so without going through.
func TestItemStopsAtClosedDoor(t *testing.T) {
	p, d, y := cartWorld(t, "minecraft:air", nil)
	blocks := p.s.reg.Blocks
	door, _ := blocks.StateID("minecraft:wooden_door", map[string]any{
		"minecraft:cardinal_direction": "west", "open_bit": uint8(0), "upper_block_bit": uint8(0), "door_hinge_bit": uint8(0),
	})
	at := cube.Pos{4, y, 4}
	var i *ItemActor
	onRegion(d, func(r *world.Region) {
		blockuse.PlaceDoor(blocks, r, at, door)
		i = p.s.spawnItem(r.Dimension(), ItemStack{Name: "minecraft:emerald", Count: 1},
			mgl64.Vec3{4.5, float64(y) + 0.6, 3.5}, mgl64.Vec3{0, 0.1, 0.6}, world.Rotation{}, 0, 0)
	})
	waitRegion(t, d, func(*world.Region) bool { return i.entity.Region() != nil && i.onGround })
	onRegion(d, func(*world.Region) {
		if z := i.entity.Position()[2]; z > 4 {
			t.Errorf("item went through the closed door, to z %v", z)
		}
	})
}

// Scaffolding is solid only to a box wholly above it.
func TestScaffoldingOnlyHoldsFromAbove(t *testing.T) {
	p, d, y := cartWorld(t, "minecraft:air", nil)
	blocks := p.s.reg.Blocks
	held, _ := blocks.StateID(nameScaffolding, map[string]any{"stability": int32(0), "stability_check": uint8(0)})
	loose, _ := blocks.StateID(nameScaffolding, map[string]any{"stability": int32(scaffoldingLoose), "stability_check": uint8(0)})
	at := cube.Pos{4, y, 4}
	top := float32(y) + 1
	onRegion(d, func(r *world.Region) {
		above := p.s.boxesFor(r, at, held, mgl64.Vec3{4.4, float64(top), 4.4}, mgl64.Vec3{4.6, float64(top + 0.25), 4.6})
		if len(above) != 1 {
			t.Errorf("above: %v", above)
		}
		if in := p.s.boxesFor(r, at, held, mgl64.Vec3{4.4, float64(top - 0.5), 4.4}, mgl64.Vec3{4.6, float64(top - 0.25), 4.6}); len(in) != 0 {
			t.Errorf("inside: %v", in)
		}
		if side := p.s.boxesFor(r, at, held, mgl64.Vec3{6, float64(top), 4.4}, mgl64.Vec3{6.2, float64(top + 0.25), 4.6}); len(side) != 0 {
			t.Errorf("to the side: %v", side)
		}
		if l := p.s.boxesFor(r, at, loose, mgl64.Vec3{4.4, float64(top), 4.4}, mgl64.Vec3{4.6, float64(top + 0.25), 4.6}); len(l) != 0 {
			t.Errorf("loose: %v", l)
		}
		if ray := p.s.boxesAt(r, at, held); len(ray) != 0 {
			t.Errorf("no entity: %v", ray)
		}
	})
}

// A shulker box is a whole block now, so an item stops on it.
func TestShulkerBoxIsSolid(t *testing.T) {
	p, _, _ := cartWorld(t, "minecraft:air", nil)
	box, _ := p.s.reg.Blocks.Block("minecraft:undyed_shulker_box")
	got := p.s.reg.Blocks.Properties(box)
	if got.CollisionComputed || len(got.Collision) != 1 || got.Collision[0] != (registry.Box{0, 0, 0, 1, 1, 1}) {
		t.Errorf("shulker box: %+v", got.Collision)
	}
}
