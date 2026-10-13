package mob

import (
	"testing"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/world"
)

type moveCounter struct {
	world.NopViewer
	n int
}

func (c *moveCounter) ViewEntityMovement(world.Entity, mgl64.Vec3, cube.Rotation, bool) { c.n++ }

// TestWalkingSentFarFromOrigin checks that every walking step reaches
// viewers far from the origin, where a relative comparison would hide it.
func TestWalkingSentFarFromOrigin(t *testing.T) {
	w := mobWorld(t)
	v := &moveCounter{}
	l := world.NewLoader(2, w, v)
	w.Do(func(tx *world.Tx) {
		for x := 990; x <= 1010; x++ {
			for z := -10; z <= 10; z++ {
				tx.SetBlock(cube.Pos{x, 0, z}, block.Stone{}, nil)
			}
		}
		l.Move(tx, mgl64.Vec3{1000, 1, 0})
		l.Load(tx, 100)
	})
	h := spawn(t, w, "minecraft:zombie", mgl64.Vec3{1000.5, 1, 0.5})
	withMob(t, w, h, func(m *Mob) {
		// One walking step with no turn, as a sheep strolling straight.
		mv := &moved{m: m, from: m.data.Pos.Sub(mgl64.Vec3{0.1, 0, 0}), fromRot: m.data.Rot, fromHead: m.s.headYaw}
		mv.Send()
	})
	if v.n != 1 {
		t.Fatalf("a 0.1 block step at x=1000 was sent %d times, want 1", v.n)
	}
}
