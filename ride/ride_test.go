package ride

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/world/cube"
)

type testRider struct {
	id   int64
	body Body
}

func (r *testRider) RideID() int64  { return r.id }
func (r *testRider) RideBody() Body { return r.body }

func player(id int64) *testRider {
	return &testRider{id: id, body: Body{HeightOffset: 1.62, Width: 0.6, Height: 1.8, Families: []string{"player"}}}
}

// testBlocks is flat ground below y=0 and whatever else a test puts down.
type testBlocks struct {
	solid map[cube.Pos]bool
	open  map[cube.Pos]bool
}

var fullBox = []registry.Box{{0, 0, 0, 1, 1, 1}}

func (b *testBlocks) Boxes(pos cube.Pos) []registry.Box {
	if pos[1] < 0 || b.solid[pos] {
		return fullBox
	}
	return nil
}

func (b *testBlocks) PassThrough(pos cube.Pos) bool { return b.open[pos] }

func TestSeatPutsAPlayerInTheCart(t *testing.T) {
	v := New(&Rules{SeatCount: 1, Seats: []Seat{{Position: mgl32.Vec3{0, -0.2, 0}}}})
	p := player(1)
	if !v.Add(p) {
		t.Fatal("no seat")
	}
	got := v.SeatOffset(p)
	want := mgl32.Vec3{0, 1.42, 0}
	if !got.ApproxEqual(want) {
		t.Fatalf("seat offset %v, want %v", got, want)
	}
	if m := v.Metadata(p)[protocol.EntityDataKeySeatOffset]; m != got {
		t.Fatalf("metadata seat %v", m)
	}
}

func TestSeatsChangeWithRiders(t *testing.T) {
	v := New(&Rules{SeatCount: 2, Seats: []Seat{
		{Position: mgl32.Vec3{0, 1, 0}, MinRiders: 1, MaxRiders: 1},
		{Position: mgl32.Vec3{0, 1, 0.5}, MinRiders: 2, MaxRiders: 2},
		{Position: mgl32.Vec3{0, 1, -0.5}, MinRiders: 2, MaxRiders: 2},
	}})
	a, b := player(1), player(2)
	v.Add(a)
	if s := v.Seat(a); s.Position[2] != 0 {
		t.Fatalf("alone in %v", s.Position)
	}
	v.Add(b)
	if s := v.Seat(a); s.Position[2] != 0.5 {
		t.Fatalf("first of two in %v", s.Position)
	}
	if s := v.Seat(b); s.Position[2] != -0.5 {
		t.Fatalf("second of two in %v", s.Position)
	}
	v.Remove(a)
	if s := v.Seat(b); s.Position[2] != 0 {
		t.Fatalf("left alone in %v", s.Position)
	}
}

func TestWhoMayRide(t *testing.T) {
	v := New(&Rules{SeatCount: 1, MaxWidth: 1, Families: []string{"player"}})
	if v.CanAdd(&testRider{id: 1, body: Body{Width: 0.6, Families: []string{"zombie"}}}) {
		t.Fatal("wrong family got on")
	}
	if v.CanAdd(&testRider{id: 2, body: Body{Width: 1, Families: []string{"player"}}}) {
		t.Fatal("too wide got on")
	}
	p := player(3)
	if !v.Add(p) || v.Add(p) || v.CanAdd(player(4)) {
		t.Fatal("seat count")
	}
}

func TestPositionTurnsWithTheVehicle(t *testing.T) {
	pos := mgl32.Vec3{10, 5, 20}
	if got := Position(pos, 0, mgl32.Vec3{0, 1, 1}); !got.ApproxEqual(mgl32.Vec3{10, 6, 21}) {
		t.Fatalf("yaw 0: %v", got)
	}
	// BDS turns by -yaw: x' = x cos + z sin, z' = z cos - x sin.
	if got := Position(pos, 90, mgl32.Vec3{0, 1, 1}); !got.ApproxEqualThreshold(mgl32.Vec3{9, 6, 20}, 1e-4) {
		t.Fatalf("yaw 90: %v", got)
	}
}

func TestExitLandsBesideAtRest(t *testing.T) {
	b := &testBlocks{}
	got := Exit(b, player(1).body, mgl32.Vec3{4.5, 0.35, 4.5}, mgl32.Vec3{}, mgl32.Vec3{4.5, 1.77, 4.5}, 0.7)
	want := mgl32.Vec3{4.5, 1.621, 3.5}
	if !got.ApproxEqualThreshold(want, 1e-4) {
		t.Fatalf("landed at %v, want %v", got, want)
	}
}

func TestExitTriesTheOtherSide(t *testing.T) {
	b := &testBlocks{solid: map[cube.Pos]bool{{4, 0, 3}: true, {4, 1, 3}: true}}
	got := Exit(b, player(1).body, mgl32.Vec3{4.5, 0.35, 4.5}, mgl32.Vec3{}, mgl32.Vec3{4.5, 1.77, 4.5}, 0.7)
	if !got.ApproxEqualThreshold(mgl32.Vec3{4.5, 1.621, 5.5}, 1e-4) {
		t.Fatalf("landed at %v", got)
	}
}

func TestExitFollowsTheWayItMoves(t *testing.T) {
	b := &testBlocks{}
	// Moving east, its right is south.
	got := Exit(b, player(1).body, mgl32.Vec3{4.5, 0.35, 4.5}, mgl32.Vec3{0.4, 0, 0}, mgl32.Vec3{4.5, 1.77, 4.5}, 0.7)
	if !got.ApproxEqualThreshold(mgl32.Vec3{4.5, 1.621, 5.5}, 1e-4) {
		t.Fatalf("landed at %v", got)
	}
}

func TestExitWalledInLandsOnTop(t *testing.T) {
	solid := map[cube.Pos]bool{}
	for x := 2; x <= 6; x++ {
		for z := 2; z <= 6; z++ {
			if x == 4 && z == 4 {
				continue
			}
			for y := -1; y <= 3; y++ {
				solid[cube.Pos{x, y, z}] = true
			}
		}
	}
	got := Exit(&testBlocks{solid: solid}, player(1).body, mgl32.Vec3{4.5, 0.35, 4.5}, mgl32.Vec3{}, mgl32.Vec3{4.5, 1.77, 4.5}, 0.7)
	if !got.ApproxEqualThreshold(mgl32.Vec3{4.5, 2.321, 4.5}, 1e-4) {
		t.Fatalf("landed at %v", got)
	}
}

func TestExitThroughAnOpenBlock(t *testing.T) {
	at := cube.Pos{4, 0, 3}
	b := &testBlocks{solid: map[cube.Pos]bool{at: true}, open: map[cube.Pos]bool{at: true}}
	got := Exit(b, player(1).body, mgl32.Vec3{4.5, 0.35, 4.5}, mgl32.Vec3{}, mgl32.Vec3{4.5, 1.77, 4.5}, 0.7)
	if !got.ApproxEqualThreshold(mgl32.Vec3{4.5, 1.621, 3.5}, 1e-4) {
		t.Fatalf("landed at %v", got)
	}
}
