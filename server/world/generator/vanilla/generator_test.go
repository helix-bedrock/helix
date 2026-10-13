package vanilla

import (
	"testing"
	"time"

	"github.com/helix-bedrock/helix/server/world"
	"github.com/helix-bedrock/helix/server/world/chunk"
)

func newGen(t testing.TB) *Generator {
	t.Helper()
	world.DefaultBlockRegistry.Finalize()
	g, err := New(0, world.DefaultBlockRegistry)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGeneratesTerrain(t *testing.T) {
	g := newGen(t)
	br := world.DefaultBlockRegistry
	counts := map[string]int{}
	start := time.Now()
	n := 0
	for cx := int32(-3); cx < 3; cx++ {
		for cz := int32(-3); cz < 3; cz++ {
			c := chunk.New(br, world.Overworld.Range())
			g.GenerateChunk(world.ChunkPos{cx * 7, cz * 7}, c)
			n++
			for x := range uint8(16) {
				for z := range uint8(16) {
					for y := int16(-64); y < 320; y++ {
						b, _ := br.BlockByRuntimeID(c.Block(x, y, z, 0))
						name, _ := b.EncodeBlock()
						counts[name]++
					}
				}
			}
		}
	}
	t.Logf("%d chunks in %v (%v each)", n, time.Since(start), time.Since(start)/time.Duration(n))
	t.Logf("blocks: %v", counts)
	for _, want := range []string{"minecraft:stone", "minecraft:bedrock", "minecraft:water", "minecraft:grass_block", "minecraft:dirt", "minecraft:deepslate"} {
		if counts[want] == 0 {
			t.Errorf("no %s in 36 chunks", want)
		}
	}
}

func TestDefaultSpawnIsOnLand(t *testing.T) {
	g := newGen(t)
	p := g.DefaultSpawn(world.Overworld)
	if p[1] <= 63 {
		t.Fatalf("spawn %v is not above the sea", p)
	}
}

func BenchmarkGenerateChunk(b *testing.B) {
	g := newGen(b)
	b.ReportAllocs()
	for i := range b.N {
		c := chunk.New(world.DefaultBlockRegistry, world.Overworld.Range())
		g.GenerateChunk(world.ChunkPos{int32(i), 0}, c)
	}
}

// TestCavesAreMostlyDry checks the aquifers: well below the sea, most empty
// space in the ground is air, not water.
func TestCavesAreMostlyDry(t *testing.T) {
	g := newGen(t)
	br := world.DefaultBlockRegistry
	air, water := 0, 0
	for cx := int32(0); cx < 4; cx++ {
		for cz := int32(0); cz < 4; cz++ {
			c := chunk.New(br, world.Overworld.Range())
			g.GenerateChunk(world.ChunkPos{cx * 5, cz * 5}, c)
			for x := range uint8(16) {
				for z := range uint8(16) {
					for y := int16(-50); y < 30; y++ {
						b, _ := br.BlockByRuntimeID(c.Block(x, y, z, 0))
						switch name, _ := b.EncodeBlock(); name {
						case "minecraft:air":
							air++
						case "minecraft:water":
							water++
						}
					}
				}
			}
		}
	}
	t.Logf("between y=-50 and 30: %d air, %d water", air, water)
	if air <= water {
		t.Fatalf("caves are flooded: %d air, %d water", air, water)
	}
}
