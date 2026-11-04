package player

import (
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// blastWorld is a real flat world with TNT's game rules on.
func blastWorld(t *testing.T) (*Player, *world.Dimension) {
	t.Helper()
	p, d := apiWorld(t, func(*Setup) {})
	p.s.level.TNTExplodes, p.s.level.DoTileDrops = true, true
	ticket := d.KeepLoaded(world.Radius(0, 0, 3), true)
	t.Cleanup(ticket.Release)
	return p, d
}

// surface returns the first air above the ground at x, z.
func surface(r *world.Region, air uint32, x, z int) int {
	for y := 100; y > -64; y-- {
		if b, ok := r.Block(cube.Pos{x, y, z}, 0); ok && b != air {
			return y + 1
		}
	}
	return 0
}

func TestTNTBreaksBlocksAndLightsTNT(t *testing.T) {
	p, d := blastWorld(t)
	blocks := p.s.reg.Blocks
	air := blocks.Air()
	block := func(name string) uint32 {
		id, ok := blocks.Block(name)
		if !ok {
			t.Fatalf("no block %s", name)
		}
		return id
	}
	var y int
	onRegion(d, func(r *world.Region) {
		y = surface(r, air, 8, 8)
		for x := 4; x <= 12; x++ {
			for z := 4; z <= 12; z++ {
				r.SetBlock(cube.Pos{x, y, z}, 0, block("minecraft:stone"))
			}
		}
		r.SetBlock(cube.Pos{8, y + 1, 10}, 0, block("minecraft:tnt"))
		r.SetBlock(cube.Pos{8, y + 1, 6}, 0, block("minecraft:obsidian"))
		x := NewExplosion(mgl32.Vec3{8.5, float32(y) + 1, 8.5}, p.s.redstone.Get().TNT.Power)
		if !p.s.Explode(r, x) {
			t.Error("explosion did nothing")
		}
	})
	var lit bool
	onRegion(d, func(r *world.Region) {
		if b, _ := r.Block(cube.Pos{8, y, 8}, 0); b != air {
			t.Errorf("stone under the blast is %s", blocks.Name(b))
		}
		if b, _ := r.Block(cube.Pos{8, y + 1, 6}, 0); blocks.Name(b) != "minecraft:obsidian" {
			t.Errorf("obsidian became %s", blocks.Name(b))
		}
		if b, _ := r.Block(cube.Pos{8, y + 1, 10}, 0); b != air {
			t.Errorf("TNT in the blast is %s", blocks.Name(b))
		}
		for _, e := range r.AppendEntitiesIn(nil, 0, 0, 16, 16) {
			if _, ok := e.Owner().(*PrimedTNT); ok {
				lit = true
			}
		}
	})
	if !lit {
		t.Error("TNT in the blast was not lit")
	}
}

func TestPrimedTNTExplodesOnFuse(t *testing.T) {
	p, d := blastWorld(t)
	air := p.s.reg.Blocks.Air()
	var y int
	var tnt *PrimedTNT
	onRegion(d, func(r *world.Region) {
		y = surface(r, air, 8, 8)
		tnt = p.s.SpawnTNT(d, mgl32.Vec3{8.5, float32(y), 8.5}, 3, nil)
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		var gone bool
		onRegion(d, func(r *world.Region) {
			b, _ := r.Block(cube.Pos{8, y - 1, 8}, 0)
			gone = tnt.gone && b == air
		})
		if gone {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("TNT did not explode")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBlastDestroysItemsButNotNetherStars(t *testing.T) {
	p, d := blastWorld(t)
	air := p.s.reg.Blocks.Air()
	var star, dirt *ItemActor
	onRegion(d, func(r *world.Region) {
		y := float32(surface(r, air, 8, 8))
		star = p.s.spawnItem(d, ItemStack{Name: netherStar, Count: 1}, mgl32.Vec3{9, y + 0.2, 8.5}, mgl32.Vec3{}, world.Rotation{}, 0, 0)
		dirt = p.s.spawnItem(d, ItemStack{Name: "minecraft:dirt", Count: 1}, mgl32.Vec3{8, y + 0.2, 8.5}, mgl32.Vec3{}, world.Rotation{}, 0, 0)
	})
	onRegion(d, func(r *world.Region) {
		x := NewExplosion(star.entity.Position().Sub(mgl32.Vec3{0.5, 0, 0}), p.s.redstone.Get().TNT.Power)
		x.BreaksBlocks = false
		p.s.Explode(r, x)
	})
	if star.taken {
		t.Error("the nether star was destroyed")
	}
	if !dirt.taken {
		t.Error("the dirt survived")
	}
}

func TestRayBlockedByBlock(t *testing.T) {
	p, d := blastWorld(t)
	blocks := p.s.reg.Blocks
	stone, _ := blocks.Block("minecraft:stone")
	onRegion(d, func(r *world.Region) {
		y := surface(r, blocks.Air(), 8, 8) + 2
		from, to := mgl32.Vec3{6.5, float32(y) + 0.5, 8.5}, mgl32.Vec3{10.5, float32(y) + 0.5, 8.5}
		if p.s.rayBlocked(r, from, to) {
			t.Error("air blocked a ray")
		}
		r.SetBlock(cube.Pos{8, y, 8}, 0, stone)
		if !p.s.rayBlocked(r, from, to) {
			t.Error("stone did not block a ray")
		}
		lo, hi := mgl32.Vec3{5.7, float32(y), 8.2}, mgl32.Vec3{6.3, float32(y) + 1.8, 8.8}
		if e := p.s.exposure(r, to, lo, hi); e >= 1 {
			t.Errorf("exposure behind stone is %v", e)
		}
	})
}
