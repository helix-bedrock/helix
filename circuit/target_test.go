package circuit

import (
	"testing"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/world/cube"
)

// The power of a hit follows BDS's sum: the distance from the centre of the face it
// landed on, across the span to the corner.
func TestTargetSignal(t *testing.T) {
	pos := cube.Pos{10, 20, 30}
	for _, c := range []struct {
		name string
		hit  mgl64.Vec3
		want int32
	}{
		{"centre of the top", mgl64.Vec3{10.5, 21, 30.5}, 15},
		{"centre of a side", mgl64.Vec3{10, 20.5, 30.5}, 15},
		{"halfway along the top", mgl64.Vec3{10.75, 21, 30.5}, 10},
		{"along a side face", mgl64.Vec3{11, 20.5, 30.75}, 10},
		{"near a corner of the top", mgl64.Vec3{10.9, 21, 30.9}, 3},
		{"middle of an edge, where two faces tie", mgl64.Vec3{10.5, 21, 31}, 15},
		{"corner", mgl64.Vec3{11, 21, 31}, 4},
		{"far outside still gives 1", mgl64.Vec3{14, 23, 30.5}, 1},
	} {
		if got := targetSignal(pos, c.hit); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}
