package generator

import (
	"fmt"
	"sync"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
	"github.com/helix-bedrock/worldgen/end"
	"github.com/helix-bedrock/worldgen/nether"
	"github.com/helix-bedrock/worldgen/structure"
)

// Nether is BDS 1.26.60's Nether from the worldgen library: terrain, biomes,
// features and structures (fortresses and bastions keep their chests' loot table
// and seed). It is safe for use by many goroutines at once.
type Nether struct {
	cache *nether.Cache
	air   uint32
	pool  sync.Pool
	// mobs are the structure mobs of generated chunks, until their first tick.
	mobs mobStash
}

var (
	_ world.ColumnGenerator = (*Nether)(nil)
	_ world.MobGenerator    = (*Nether)(nil)
)

// TakeMobs is world.MobGenerator's: the mobs the chunk's structures placed.
func (n *Nether) TakeMobs(pos mcdb.ChunkPos) []world.GeneratedMob { return n.mobs.take(pos) }

// NewNether builds the vanilla Nether for seed over the server's own blocks and biomes.
func NewNether(reg *registry.Registry, seed int64) (*Nether, error) {
	list, err := blockList(reg.Blocks)
	if err != nil {
		return nil, err
	}
	gen, err := nether.New(seed, list, biomeIDs(reg.Biomes))
	if err != nil {
		return nil, fmt.Errorf("generator: nether: %w", err)
	}
	n := &Nether{cache: nether.NewCache(gen, cacheChunks), air: reg.Blocks.Air()}
	n.pool.New = func() any { return new(nether.Chunk) }
	return n, nil
}

// Generate fills c with the chunk at pos, without block entities.
func (n *Nether) Generate(pos mcdb.ChunkPos, c *chunk.Chunk) error {
	n.fill(pos, c, nil)
	return nil
}

// GenerateColumn fills col's chunk and adds its structures' block entities.
func (n *Nether) GenerateColumn(pos mcdb.ChunkPos, col *chunk.Column) error {
	n.fill(pos, col.Chunk, col)
	return nil
}

func (n *Nether) fill(pos mcdb.ChunkPos, c *chunk.Chunk, col *chunk.Column) {
	wc, ok := n.pool.Get().(*nether.Chunk)
	if !ok {
		wc = new(nether.Chunk)
	}
	defer n.pool.Put(wc)
	n.cache.Generate(pos[0], pos[1], wc)
	copyColumn(c, n.air, nether.Height, wc.Block, wc.Biome)
	if col != nil {
		col.BlockEntities = appendBlockEntities(col.BlockEntities, wc.BlockEntities)
		n.mobs.put(pos, wc.Entities)
	}
	wc.BlockEntities, wc.Entities = wc.BlockEntities[:0], wc.Entities[:0]
}

// End is BDS 1.26.60's End from the worldgen library: the main island with its
// obsidian pillars, the outer islands, chorus, gateways and End cities. It is safe
// for use by many goroutines at once.
//
// The End crystals worldgen asks to spawn on the pillars are not spawned yet:
// Helix has no End crystal entity.
type End struct {
	gen   *end.End
	cache *end.Cache
	air   uint32
	pool  sync.Pool
}

var _ world.ColumnGenerator = (*End)(nil)

// NewEnd builds the vanilla End for seed over the server's own blocks and biomes.
func NewEnd(reg *registry.Registry, seed int64) (*End, error) {
	list, err := blockList(reg.Blocks)
	if err != nil {
		return nil, err
	}
	gen, err := end.New(seed, list, biomeIDs(reg.Biomes))
	if err != nil {
		return nil, fmt.Errorf("generator: end: %w", err)
	}
	e := &End{gen: gen, cache: end.NewCache(gen, cacheChunks), air: reg.Blocks.Air()}
	e.pool.New = func() any { return new(end.Chunk) }
	return e, nil
}

// ExitPortal is the End's exit portal (the podium), closed or open, in world
// positions and the server's block IDs. The engine builds it: generation does not.
func (e *End) ExitPortal(open bool) []end.PodiumBlock { return e.gen.ExitPortal(open) }

// Generate fills c with the chunk at pos, without block entities.
func (e *End) Generate(pos mcdb.ChunkPos, c *chunk.Chunk) error {
	e.fill(pos, c, nil)
	return nil
}

// GenerateColumn fills col's chunk and adds its structures' block entities.
func (e *End) GenerateColumn(pos mcdb.ChunkPos, col *chunk.Column) error {
	e.fill(pos, col.Chunk, col)
	return nil
}

func (e *End) fill(pos mcdb.ChunkPos, c *chunk.Chunk, col *chunk.Column) {
	wc, ok := e.pool.Get().(*end.Chunk)
	if !ok {
		wc = new(end.Chunk)
	}
	defer e.pool.Put(wc)
	e.cache.Generate(pos[0], pos[1], wc)
	copyColumn(c, e.air, end.Height, wc.Block, wc.Biome)
	if col != nil {
		col.BlockEntities = appendBlockEntities(col.BlockEntities, wc.BlockEntities)
	}
	wc.BlockEntities, wc.Entities = wc.BlockEntities[:0], wc.Entities[:0]
}

// copyColumn copies a worldgen column of the given height into c: its blocks, and its
// one biome per column at every height of c.
func copyColumn(c *chunk.Chunk, air uint32, height int, block func(x, y, z int) uint32, biome func(x, z int) uint32) {
	r := c.Range()
	lo, hi := max(0, r[0]), min(height-1, r[1])
	for z := range 16 {
		for x := range 16 {
			bx, bz := uint8(x), uint8(z) //nolint:gosec // local coordinates fit
			for y := lo; y <= hi; y++ {
				if b := block(x, y, z); b != air {
					c.SetBlock(bx, int16(y), bz, 0, b) //nolint:gosec // heights fit
				}
			}
			id := biome(x, z)
			for y := r[0]; y <= r[1]; y++ {
				c.SetBiome(bx, int16(y), bz, id) //nolint:gosec // heights fit
			}
		}
	}
}

// appendBlockEntities adds worldgen's block entities in their saved form.
func appendBlockEntities(dst []chunk.BlockEntity, src []structure.BlockEntity) []chunk.BlockEntity {
	for _, be := range src {
		dst = append(dst, chunk.BlockEntity{Pos: cube.Pos{int(be.X), int(be.Y), int(be.Z)}, Data: be.Data})
	}
	return dst
}
