// Package generator holds the world generators a server can pick by name:
// vanilla terrain from the worldgen library, BDS's flat world and the void,
// and the table plugins add their own to. See [Types].
package generator

import (
	"fmt"
	"strings"
	"sync"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
	"github.com/helix-bedrock/worldgen/1.26.60/blocks"
	"github.com/helix-bedrock/worldgen/1.26.60/overworld"
)

// cacheChunks is how many chunks of undecorated terrain and structure
// placements worldgen keeps for the decoration of their neighbours.
const cacheChunks = 1024

// Vanilla is BDS 1.26.60's overworld from the worldgen library: terrain,
// biomes, caves, ores, plants, trees and structures, the same world BDS makes
// from the seed. Structures bring their block entities (chests keep their loot
// table and seed), and springs their pending tick, so they flow. It is safe for use by many goroutines at once.
type Vanilla struct {
	gen   *overworld.Overworld
	cache *overworld.TerrainCache
	// water is the source water state, kept in the second layer of blocks
	// worldgen marks waterlogged.
	water, air uint32
	pool       sync.Pool
}

var (
	_ world.ColumnGenerator = (*Vanilla)(nil)
	_ world.Spawner         = (*Vanilla)(nil)
)

// NewVanilla builds the vanilla overworld for seed over the server's own
// blocks and biomes: worldgen places the registry's state IDs, so a block a
// plugin replaced or deleted follows the registry. conf changes the world
// (biomes, features, structures, terrain; see worldgen's overworld.Config);
// nil is vanilla.
func NewVanilla(reg *registry.Registry, seed int64, conf *overworld.Config) (*Vanilla, error) {
	list, err := blockList(reg.Blocks)
	if err != nil {
		return nil, err
	}
	opts := []overworld.Option{}
	if conf != nil {
		opts = append(opts, overworld.WithConfig(conf))
	}
	gen, err := overworld.NewOverworld(seed, list, biomeIDs(reg.Biomes), opts...)
	if err != nil {
		return nil, fmt.Errorf("generator: vanilla: %w", err)
	}
	water, ok := reg.Blocks.Block("minecraft:water")
	if !ok {
		return nil, fmt.Errorf("generator: vanilla: minecraft:water is not registered")
	}
	v := &Vanilla{gen: gen, cache: overworld.NewTerrainCache(gen, cacheChunks), water: water, air: reg.Blocks.Air()}
	v.pool.New = func() any { return new(overworld.Chunk) }
	return v, nil
}

// blockList hands worldgen the registry's states in ID order, so the indices
// it returns are the server's state IDs.
func blockList(b *registry.Blocks) (*blocks.Registry, error) {
	n := b.Count()
	src := blocks.Source{
		States:       make([]blocks.State, n),
		Defaults:     map[string]uint32{},
		Tags:         map[string][]string{},
		Light:        make([]uint8, n),
		Translucency: make([]float32, n),
	}
	for i := range n {
		id := uint32(i) //nolint:gosec // state counts fit uint32
		name := b.Name(id)
		src.States[i] = blocks.State{Name: name, Props: b.States(id)}
		if _, ok := src.Defaults[name]; !ok {
			src.Defaults[name] = b.Default(id)
			src.Tags[name] = b.Tags(id)
		}
		p := b.Properties(id)
		src.Light[i], src.Translucency[i] = p.LightDampening, p.Translucency
	}
	list, err := blocks.New(src)
	if err != nil {
		return nil, fmt.Errorf("generator: vanilla: %w", err)
	}
	return list, nil
}

// biomeIDs are the registry's biome IDs by name without the minecraft:
// namespace, as worldgen names vanilla biomes.
func biomeIDs(b *registry.Biomes) map[string]uint32 {
	ids := make(map[string]uint32, b.Count())
	for _, bi := range b.All() {
		if bi.ID >= 0 {
			ids[strings.TrimPrefix(bi.Name, "minecraft:")] = uint32(bi.ID)
		}
	}
	return ids
}

// Generate fills c with the chunk at pos, without block entities.
func (v *Vanilla) Generate(pos mcdb.ChunkPos, c *chunk.Chunk) error {
	v.fill(pos, c, nil)
	return nil
}

// GenerateColumn fills col's chunk and adds the block entities of the
// structures in it (chests, beds, bells, spawners), which the client needs
// to draw them.
func (v *Vanilla) GenerateColumn(pos mcdb.ChunkPos, col *chunk.Column) error {
	v.fill(pos, col.Chunk, col)
	return nil
}

// fill copies worldgen's chunk into c; with col set, its block entities too.
func (v *Vanilla) fill(pos mcdb.ChunkPos, c *chunk.Chunk, col *chunk.Column) {
	wc, ok := v.pool.Get().(*overworld.Chunk)
	if !ok {
		wc = new(overworld.Chunk)
	}
	defer v.pool.Put(wc)
	v.cache.Generate(pos[0], pos[1], wc)
	r := c.Range()
	lo, hi := max(overworld.MinY, r[0]), min(overworld.MaxY-1, r[1])
	for y := lo; y <= hi; y++ {
		for z := range 16 {
			for x := range 16 {
				bx, by, bz := uint8(x), int16(y), uint8(z) //nolint:gosec // local coordinates and heights fit
				if b := wc.Block(x, y, z); b != v.air {
					c.SetBlock(bx, by, bz, 0, b)
					if wc.Waterlogged(x, y, z) {
						c.SetBlock(bx, by, bz, 1, v.water)
					}
				}
				c.SetBiome(bx, by, bz, wc.Biome(x, y, z))
			}
		}
	}
	if col == nil {
		return
	}
	for _, be := range wc.BlockEntities {
		col.BlockEntities = append(col.BlockEntities, chunk.BlockEntity{
			Pos: cube.Pos{int(be.X), int(be.Y), int(be.Z)}, Data: be.Data,
		})
	}
	// Ticks generation left pending (springs) go on the chunk's queue, counted on
	// its clock as BDS saves them; the world's liquids run them like any other.
	for _, t := range wc.Ticks {
		col.ScheduledBlocks = append(col.ScheduledBlocks, chunk.ScheduledBlockUpdate{
			Pos: cube.Pos{int(t.X), int(t.Y), int(t.Z)}, Block: t.Block, Tick: col.Tick + int64(t.Delay),
		})
	}
	wc.BlockEntities, wc.Ticks = wc.BlockEntities[:0], wc.Ticks[:0]
}

// SpawnHeight is the Y above the highest block of the column at x, z that
// is not air or water, in the undecorated terrain, never below sea level.
// BDS's own spawn search is not ported; this is a stand-in.
func (v *Vanilla) SpawnHeight(x, z int32) int32 {
	wc, ok := v.pool.Get().(*overworld.Chunk)
	if !ok {
		wc = new(overworld.Chunk)
	}
	defer v.pool.Put(wc)
	v.gen.GenerateChunk(x>>4, z>>4, wc)
	wc.BlockEntities = wc.BlockEntities[:0]
	lx, lz := int(x&15), int(z&15)
	for y := overworld.MaxY - 1; y > overworld.MinY; y-- {
		if b := wc.Block(lx, y, lz); b != v.air && b != v.water {
			return int32(max(y, overworld.SeaLevel) + 1) //nolint:gosec // heights fit
		}
	}
	return overworld.SeaLevel + 1
}

// Temperature is BDS's biome temperature at a block, the same worldgen freezes water and lays
// snow by, for the weather. Runs: any goroutine.
func (v *Vanilla) Temperature(biome uint32, x, y, z int32) float32 {
	return v.gen.Temperature(biome, x, y, z)
}
