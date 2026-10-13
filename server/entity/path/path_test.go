package path_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/entity/path"
	"github.com/helix-bedrock/helix/server/world"
)

// floorWorld returns a world with a stone floor at y=0 from -20 to 20.
func floorWorld(t *testing.T) *world.World {
	t.Helper()
	w := world.Config{Synchronous: true}.New()
	t.Cleanup(func() { _ = w.Close() })
	w.Do(func(tx *world.Tx) {
		for x := -20; x <= 20; x++ {
			for z := -20; z <= 20; z++ {
				tx.SetBlock(cube.Pos{x, 0, z}, block.Stone{}, nil)
			}
		}
	})
	return w
}

func zombie(pos mgl64.Vec3) *path.Mob {
	return &path.Mob{Position: pos, Width: 0.6, Height: 1.9, OnGround: true, MaxFall: 3, MinY: -64, CanPassDoors: true}
}

func TestFindsStraightPath(t *testing.T) {
	w := floorWorld(t)
	w.Do(func(tx *world.Tx) {
		p := path.Find(tx, zombie(mgl64.Vec3{0.5, 1, 0.5}), mgl64.Vec3{8.5, 1, 0.5}, 1, 16)
		if p == nil || !p.Reaches() {
			t.Fatalf("no path on a flat floor: %+v", p)
		}
		if n := len(p.Nodes()); n < 8 || n > 10 {
			t.Fatalf("path of %d nodes for 8 blocks", n)
		}
	})
}

func TestPathGoesAroundWall(t *testing.T) {
	w := floorWorld(t)
	w.Do(func(tx *world.Tx) {
		// A wall three high across z=-5..5 at x=4.
		for z := -5; z <= 5; z++ {
			for y := 1; y <= 3; y++ {
				tx.SetBlock(cube.Pos{4, y, z}, block.Stone{}, nil)
			}
		}
		p := path.Find(tx, zombie(mgl64.Vec3{0.5, 1, 0.5}), mgl64.Vec3{8.5, 1, 0.5}, 1, 32)
		if p == nil || !p.Reaches() {
			t.Fatal("no path around the wall")
		}
		for _, n := range p.Nodes() {
			if n.Pos[0] == 4 && n.Pos[2] >= -5 && n.Pos[2] <= 5 {
				t.Fatalf("path goes through the wall at %v", n.Pos)
			}
		}
	})
}

func TestPathClimbsOneBlock(t *testing.T) {
	w := floorWorld(t)
	w.Do(func(tx *world.Tx) {
		for x := 3; x <= 8; x++ {
			for z := -20; z <= 20; z++ {
				tx.SetBlock(cube.Pos{x, 1, z}, block.Stone{}, nil)
			}
		}
		p := path.Find(tx, zombie(mgl64.Vec3{0.5, 1, 0.5}), mgl64.Vec3{6.5, 2, 0.5}, 1, 16)
		if p == nil || !p.Reaches() {
			t.Fatal("no path up a one-block step")
		}
		if last := p.Nodes()[len(p.Nodes())-1]; last.Pos[1] != 2 {
			t.Fatalf("path ends at y=%d, want 2", last.Pos[1])
		}
	})
}

func TestNoPathOverTwoBlockWall(t *testing.T) {
	w := floorWorld(t)
	w.Do(func(tx *world.Tx) {
		// Enclose the target in a two-high ring.
		for x := 5; x <= 9; x++ {
			for z := -2; z <= 2; z++ {
				if x == 5 || x == 9 || z == -2 || z == 2 {
					tx.SetBlock(cube.Pos{x, 1, z}, block.Stone{}, nil)
					tx.SetBlock(cube.Pos{x, 2, z}, block.Stone{}, nil)
				}
			}
		}
		p := path.Find(tx, zombie(mgl64.Vec3{0.5, 1, 0.5}), mgl64.Vec3{7.5, 1, 0.5}, 0, 16)
		if p != nil && p.Reaches() {
			t.Fatal("path into a ring two blocks high")
		}
	})
}

func TestLavaIsAvoided(t *testing.T) {
	w := floorWorld(t)
	w.Do(func(tx *world.Tx) {
		for z := -1; z <= 1; z++ {
			tx.SetBlock(cube.Pos{4, 0, z}, block.Lava{Still: true, Depth: 8}, nil)
		}
		mob := zombie(mgl64.Vec3{0.5, 1, 0.5})
		mob.SetMalus(path.Lava, -1)
		p := path.Find(tx, mob, mgl64.Vec3{8.5, 1, 0.5}, 1, 32)
		if p == nil || !p.Reaches() {
			t.Fatal("no path around lava")
		}
		for _, n := range p.Nodes() {
			if n.Pos[0] == 4 && n.Pos[2] >= -1 && n.Pos[2] <= 1 {
				t.Fatalf("path crosses lava at %v", n.Pos)
			}
		}
	})
}

func BenchmarkFind(b *testing.B) {
	w := world.Config{Synchronous: true}.New()
	defer w.Close()
	w.Do(func(tx *world.Tx) {
		for x := -20; x <= 20; x++ {
			for z := -20; z <= 20; z++ {
				tx.SetBlock(cube.Pos{x, 0, z}, block.Stone{}, nil)
			}
		}
		for z := -5; z <= 5; z++ {
			tx.SetBlock(cube.Pos{4, 1, z}, block.Stone{}, nil)
			tx.SetBlock(cube.Pos{4, 2, z}, block.Stone{}, nil)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			path.Find(tx, zombie(mgl64.Vec3{0.5, 1, 0.5}), mgl64.Vec3{8.5, 1, 0.5}, 1, 32)
		}
	})
}
