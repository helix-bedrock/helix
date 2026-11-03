package direction

import (
	"testing"

	"github.com/helix-bedrock/world/cube"
)

// TestNumberings checks each numbering against the tables read from BDS.
func TestNumberings(t *testing.T) {
	stairsByYaw := [4]int{2, 1, 3, 0}
	trapdoorByYaw := [4]int{3, 0, 2, 1}
	facingByYaw := [4]cube.Face{2, 5, 3, 4}
	directionByYaw := [4]Cardinal{2, 3, 0, 1}
	lookFacing := [4]cube.Face{2, 5, 3, 4}
	traitFacing := [4]cube.Face{3, 4, 2, 5}
	for i := range Cardinal(4) {
		if got := i.EastFirst(); got != stairsByYaw[i] {
			t.Errorf("%v EastFirst = %d", i, got)
		}
		if got := i.Opposite().EastFirst(); got != trapdoorByYaw[i] {
			t.Errorf("%v trapdoor = %d", i, got)
		}
		if got := i.Opposite().Face(); got != facingByYaw[i] || got != lookFacing[i] {
			t.Errorf("%v facing = %v", i, got)
		}
		if got := i.Face(); got != traitFacing[i] {
			t.Errorf("%v trait facing = %v", i, got)
		}
		if got := i.Opposite(); got != directionByYaw[i] {
			t.Errorf("%v opposite = %v", i, got)
		}
		if back, ok := FromEastFirst(i.EastFirst()); !ok || back != i {
			t.Errorf("%v FromEastFirst = %v %v", i, back, ok)
		}
		if back, ok := Of(i.Face()); !ok || back != i {
			t.Errorf("%v Of = %v %v", i, back, ok)
		}
		if back, ok := Parse(i.String()); !ok || back != i {
			t.Errorf("%v Parse = %v %v", i, back, ok)
		}
	}
	byFace := map[cube.Face]int{cube.FaceNorth: 1, cube.FaceEast: 2, cube.FaceSouth: 4, cube.FaceWest: 8}
	cocoa := map[cube.Face]int{cube.FaceNorth: 0, cube.FaceSouth: 2, cube.FaceWest: 3, cube.FaceEast: 1}
	for f, bit := range byFace {
		c, _ := Of(f)
		if 1<<c.NorthFirst() != bit || c.NorthFirst() != cocoa[f] {
			t.Errorf("%v NorthFirst = %d", f, c.NorthFirst())
		}
	}
}

func TestFromYaw(t *testing.T) {
	for _, c := range []struct {
		yaw  float32
		want Cardinal
	}{{0, South}, {44, South}, {46, West}, {90, West}, {180, North}, {-180, North}, {-90, East}, {270, East}, {-46, East}, {-44, South}} {
		if got := FromYaw(c.yaw); got != c.want {
			t.Errorf("FromYaw(%v) = %v, want %v", c.yaw, got, c.want)
		}
	}
}

// TestOrders pins the orders BDS walks neighbours in: changing one changes what
// happens in the world.
func TestOrders(t *testing.T) {
	updates := [6]cube.Pos{{-1, 0, 0}, {1, 0, 0}, {0, 0, 1}, {0, 0, -1}, {0, 1, 0}, {0, -1, 0}}
	for i, f := range Updates() {
		if got := (cube.Pos{}).Side(f); got != updates[i] {
			t.Errorf("Updates[%d] = %v", i, got)
		}
	}
	spread := [4]cube.Pos{{-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 0, 1}}
	sides := Spread()
	for i, f := range sides {
		if got := (cube.Pos{}).Side(f); got != spread[i] {
			t.Errorf("Spread[%d] = %v", i, got)
		}
		if sides[i^1] != f.Opposite() {
			t.Errorf("Spread[%d] opposite = %v", i, sides[i^1])
		}
	}
	clockwise := [4]cube.Pos{{0, 0, -1}, {1, 0, 0}, {0, 0, 1}, {-1, 0, 0}}
	for i, f := range Clockwise() {
		if got := (cube.Pos{}).Side(f); got != clockwise[i] {
			t.Errorf("Clockwise[%d] = %v", i, got)
		}
	}
}

// TestBadValues checks nothing out of range panics or passes as valid.
func TestBadValues(t *testing.T) {
	if _, ok := Of(cube.Face(9)); ok {
		t.Error("Of accepted a bad face")
	}
	if _, ok := Of(cube.FaceUp); ok {
		t.Error("Of accepted the top")
	}
	if _, ok := FromEastFirst(4); ok {
		t.Error("FromEastFirst accepted 4")
	}
	if _, ok := FromEastFirst(-1); ok {
		t.Error("FromEastFirst accepted -1")
	}
	if _, ok := Parse("up"); ok {
		t.Error("Parse accepted up")
	}
	_ = Cardinal(200).Face()
	_ = Cardinal(200).String()
	_ = Cardinal(200).EastFirst()
	_ = Cardinal(200).NorthFirst()
}
