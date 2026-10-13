package mob

import (
	"math/rand/v2"
	"testing"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/entity"
	"github.com/helix-bedrock/helix/server/world"
	"github.com/helix-bedrock/helix/server/world/biome"
)

// TestFishSpawnInOcean checks water mobs get spots in open water, and
// structure-only mobs such as guardians do not spawn.
func TestFishSpawnInOcean(t *testing.T) {
	reg, _ := Registry(entity.DefaultRegistry)
	sp, _ := NewSpawner(reg)
	w := world.Config{Synchronous: true, Entities: reg}.New()
	defer w.Close()
	w.SetDifficulty(world.DifficultyNormal)
	w.Do(func(tx *world.Tx) {
		for x := -60; x <= 60; x++ {
			for z := -60; z <= 60; z++ {
				tx.SetBlock(cube.Pos{x, 0, z}, block.Stone{}, nil)
				for y := 1; y <= 20; y++ {
					tx.SetBlock(cube.Pos{x, y, z}, block.Water{Still: true, Depth: 8}, nil)
					tx.SetBiome(cube.Pos{x, y, z}, biome.Ocean{})
				}
			}
		}
		r := rand.New(rand.NewPCG(1, 2))
		for range 400 {
			sp.attempt(tx, mgl64.Vec3{0.5, 21, 0.5}, 2, r)
		}
		n := map[string]int{}
		for e := range tx.Entities() {
			n[e.H().Type().EncodeEntity()]++
		}
		t.Logf("spawned: %v", n)
		if n["minecraft:cod"]+n["minecraft:salmon"]+n["minecraft:squid"] == 0 {
			t.Error("no fish or squid spawned in an ocean")
		}
		if n["minecraft:guardian"] > 0 {
			t.Error("guardians spawned outside a monument")
		}
	})
}
