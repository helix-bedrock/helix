package attach

import (
	"testing"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/world/cube"
)

func TestSupports(t *testing.T) {
	s := registry.NewSetup(nil, "minecraft")
	if err := registry.Vanilla(s); err != nil {
		t.Fatal(err)
	}
	reg, err := s.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	table, err := New(reg.Blocks)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		face cube.Face
		want bool
	}{
		{"minecraft:stone", cube.FaceNorth, true},
		{"minecraft:oak_fence", cube.FaceUp, true},
		{"minecraft:oak_fence", cube.FaceNorth, false},
		{"minecraft:chest", cube.FaceUp, false},
		{"minecraft:hopper", cube.FaceUp, true},
		{"minecraft:oak_leaves", cube.FaceUp, false},
	} {
		b, ok := reg.Blocks.Block(c.name)
		if !ok {
			t.Fatal(c.name)
		}
		if got := table.Supports(b, c.face); got != c.want {
			t.Errorf("%s %v: got %v", c.name, c.face, got)
		}
	}
	for face, want := range map[cube.Face]string{cube.FaceNorth: "south", cube.FaceSouth: "north", cube.FaceWest: "east", cube.FaceEast: "west", cube.FaceUp: "top"} {
		if got := TorchState(face); got != want {
			t.Errorf("torch on %v: got %s", face, got)
		}
	}
}
