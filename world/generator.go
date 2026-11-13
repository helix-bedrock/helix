package world

import (
	"encoding/json"
	"fmt"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Generator makes the chunks a world has not saved yet.
type Generator interface {
	// Generate fills c, a new chunk of air at pos. It is called on worker
	// goroutines, many at once, so it must not keep state between calls
	// that is not safe for concurrent use.
	Generate(pos mcdb.ChunkPos, c *chunk.Chunk) error
}

// ColumnGenerator is a Generator that also makes block entities, such as the
// chests and beds of structures. The dimension calls GenerateColumn instead
// of Generate; the block entities are added to col.BlockEntities in their
// saved form, as if the chunk had been loaded.
type ColumnGenerator interface {
	Generator
	GenerateColumn(pos mcdb.ChunkPos, col *chunk.Column) error
}

// GeneratedMob is a mob generation placed in a chunk (a village's villagers, an
// outpost's pillagers), for the server to spawn on the chunk's first tick.
type GeneratedMob struct {
	// Identifier is the entity type, Event the spawn event that picks its
	// component groups (empty for the definition's own).
	Identifier, Event string
	// X, Y, Z are its feet, Yaw its facing in degrees.
	X, Y, Z float32
	Yaw     float32
	// Persistent marks a mob that never despawns, as BDS sets on every mob a
	// structure places.
	Persistent bool
	// Source names what placed it ("jigsaw" for village pieces, "structure").
	Source string
}

// MobGenerator is a Generator whose chunks hold mobs. TakeMobs returns the
// mobs of the chunk at pos that GenerateColumn made, and forgets them, so each
// is handed out once. It may be called from any goroutine.
type MobGenerator interface {
	TakeMobs(pos mcdb.ChunkPos) []GeneratedMob
}

// GeneratedMobs takes the mobs the dimension's generator placed in the chunk at
// pos (see MobGenerator); nil when it places none. Runs: anywhere.
func (d *Dimension) GeneratedMobs(pos mcdb.ChunkPos) []GeneratedMob {
	if g, ok := d.conf.Generator.(MobGenerator); ok {
		return g.TakeMobs(pos)
	}
	return nil
}

// Spawner is a Generator that knows where a player can stand: the Y of the
// block above the ground of the column at x, z. Joining players start there
// while the world has not placed its spawn. It may be called from any
// goroutine.
type Spawner interface {
	SpawnHeight(x, z int32) int32
}

// Empty generates nothing: every chunk stays air. It is BDS's void world.
type Empty struct{}

// Generate leaves c as it is.
func (Empty) Generate(mcdb.ChunkPos, *chunk.Chunk) error { return nil }

// FlatLayer is a layer of a flat world, from the bottom up.
type FlatLayer struct {
	// Block is the block state ID the layer is made of.
	Block uint32
	// Count is how many blocks thick the layer is.
	Count int
}

// Flat generates BDS's flat world: the same layers in every column from
// Bottom up, in one biome.
type Flat struct {
	Layers []FlatLayer
	// Biome is the biome ID of every block.
	Biome uint32
	// Bottom is the Y of the lowest layer.
	Bottom int16
}

// Generate places the layers.
func (f Flat) Generate(_ mcdb.ChunkPos, c *chunk.Chunk) error {
	top := int16(c.Range()[1]) //nolint:gosec // dimension heights fit int16
	y := f.Bottom
	for _, l := range f.Layers {
		for range l.Count {
			if y > top {
				break
			}
			if y >= int16(c.Range()[0]) { //nolint:gosec // as above
				for x := range uint8(16) {
					for z := range uint8(16) {
						c.SetBlock(x, y, z, 0, l.Block)
					}
				}
			}
			y++
		}
	}
	if f.Biome != 0 {
		c.ReplaceBiomes(func(uint32) uint32 { return f.Biome })
	}
	return nil
}

// DefaultFlatWorldLayers is the flat world BDS 1.26.60 makes when level.dat
// has none (the template in the server binary, with world_version filled
// in as BDS does for new worlds): bedrock, two dirt and grass in plains.
const DefaultFlatWorldLayers = `{"biome_id":1,"block_layers":[{"block_name":"minecraft:bedrock","count":1},` +
	`{"block_name":"minecraft:dirt","count":2},{"block_name":"minecraft:grass_block","count":1}],` +
	`"encoding_version":6,"structure_options":null,"world_version":"version.post_1_18"}`

// flatWorldLayers is level.dat's FlatWorldLayers JSON.
type flatWorldLayers struct {
	BiomeID     int16 `json:"biome_id"`
	BlockLayers []struct {
		BlockName string `json:"block_name"`
		Count     int    `json:"count"`
	} `json:"block_layers"`
	WorldVersion string `json:"world_version"`
}

// ParseFlat builds the flat generator of level.dat's FlatWorldLayers; an
// empty string means BDS's default. Worlds made before 1.18 start their
// layers at Y 0, later ones at the bottom of the dimension.
func ParseFlat(layers string, blocks *registry.Blocks, biomes *registry.Biomes, r cube.Range) (Flat, error) {
	if layers == "" {
		layers = DefaultFlatWorldLayers
	}
	var l flatWorldLayers
	if err := json.Unmarshal([]byte(layers), &l); err != nil {
		return Flat{}, fmt.Errorf("world: flat world layers: %w", err)
	}
	f := Flat{Biome: uint32(biomes.Resolve(l.BiomeID)), Bottom: int16(r[0])} //nolint:gosec // biome IDs are not negative after Resolve; heights fit int16
	if l.WorldVersion != "version.post_1_18" {
		f.Bottom = 0
	}
	for _, bl := range l.BlockLayers {
		id, ok := blocks.Block(bl.BlockName)
		if !ok {
			id = blocks.Unknown()
		}
		if bl.Count > 0 {
			f.Layers = append(f.Layers, FlatLayer{Block: id, Count: bl.Count})
		}
	}
	return f, nil
}
