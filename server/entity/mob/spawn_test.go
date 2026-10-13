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
	"github.com/helix-bedrock/helix/server/world/generator"
)

// spawnCounts runs spawn attempts around the origin of a superflat plains
// world at the time given and counts what spawned by family.
func spawnCounts(t *testing.T, time int) (animals, monsters int) {
	t.Helper()
	reg, err := Registry(entity.DefaultRegistry)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := NewSpawner(reg)
	if err != nil {
		t.Fatal(err)
	}
	world.DefaultBlockRegistry.Finalize()
	gen := generator.NewFlat(biome.Plains{}, []world.Block{block.Grass{}, block.Dirt{}, block.Dirt{}, block.Bedrock{}})
	w := world.Config{Synchronous: true, Entities: reg, Generator: gen}.New()
	defer w.Close()
	w.SetTime(time)
	w.StopTime()
	w.SetDifficulty(world.DifficultyNormal)
	w.Do(func(tx *world.Tx) {
		for x := -64; x <= 64; x += 16 {
			for z := -64; z <= 64; z += 16 {
				tx.Block(cube.Pos{x, -61, z})
			}
		}
		r := rand.New(rand.NewPCG(1, 2))
		for range 400 {
			sp.attempt(tx, mgl64.Vec3{0.5, -60, 0.5}, 2, r)
		}
		for e := range tx.Entities() {
			m, ok := e.(*Mob)
			if !ok {
				continue
			}
			if !m.s.natural {
				t.Errorf("%s spawned without the natural mark", m.Def().ID)
			}
			if m.Def().ID == "minecraft:slime" {
				// Superflat ground is below y=40, so slimes spawn in slime
				// chunks there day and night, as in vanilla.
				if !slimeMaySpawn(cube.PosFromVec3(m.Position())) {
					t.Errorf("slime outside a slime chunk at %v", m.Position())
				}
				continue
			}
			switch m.s.category {
			case "animal":
				animals++
			case "monster":
				monsters++
			}
		}
	})
	return animals, monsters
}

func TestAnimalsSpawnByDay(t *testing.T) {
	animals, monsters := spawnCounts(t, 6000)
	if animals == 0 || monsters != 0 {
		t.Fatalf("by day: %d animals, %d monsters", animals, monsters)
	}
}

func TestMonstersSpawnAtNight(t *testing.T) {
	_, monsters := spawnCounts(t, 18000)
	if monsters == 0 {
		t.Fatal("no monsters at night")
	}
}

func TestDespawnFarFromPlayers(t *testing.T) {
	d := parseDespawn([]byte(`{"despawn_from_distance":{}}`))
	if !d.fromDistance || d.min != 32 || d.max != 128 || d.chance != 800 || d.inactivity != 600 {
		t.Fatalf("defaults: %+v", d)
	}
}
