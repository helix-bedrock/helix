package generator

import (
	"log/slog"
	"sync/atomic"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	wgrand "github.com/helix-bedrock/worldgen/1.26.60/rand"
)

// GrowFeature places worldgen's feature (a tree, for a growing sapling) at pos into the running
// region, as BDS places a feature into the level: the feature reads the region's blocks, biomes
// and height map, then its blocks are set with neighbour updates and its block ticks queued.
// Block entities it makes (a bee nest's) are not placed yet. It reports whether the feature
// says it grew; a failed one's blocks stay, as in BDS. Worldgen's block IDs are Helix's (see
// blockList). Runs: the region's owner.
func (v *Vanilla) GrowFeature(r *world.Region, feature string, pos cube.Pos, seed uint32) bool {
	placed, ok := v.gen.PlaceFeature(feature, int32(pos[0]), int32(pos[1]), int32(pos[2]), wgrand.NewMT(seed), liveRegion{r}) //nolint:gosec // block positions fit int32
	changed := make([]cube.Pos, 0, len(placed.Blocks))
	for _, b := range placed.Blocks {
		at := cube.Pos{int(b.X), int(b.Y), int(b.Z)}
		if r.SetBlock(at, 0, b.Block) {
			changed = append(changed, at)
		}
	}
	for _, at := range changed {
		r.UpdateNeighbours(at)
	}
	for _, tk := range placed.Ticks {
		r.ScheduleTick(cube.Pos{int(tk.X), int(tk.Y), int(tk.Z)}, tk.Block, tk.Delay, 0)
	}
	return ok
}

// liveRegion is a region as worldgen's LiveWorld.
type liveRegion struct{ r *world.Region }

func (l liveRegion) Block(x, y, z int32) (uint32, bool) {
	return l.r.Block(cube.Pos{int(x), int(y), int(z)}, 0)
}

func (l liveRegion) Biome(x, y, z int32) (uint32, bool) {
	id, ok := l.r.Biome(cube.Pos{int(x), int(y), int(z)})
	return uint32(id), ok && id >= 0 //nolint:gosec // checked not negative
}

func (l liveRegion) Height(x, z int32) int32 {
	h, ok := l.r.Height(int(x), int(z))
	if !ok {
		return int32(l.r.Dimension().Range()[0])
	}
	return int32(h) //nolint:gosec // heights fit int32
}

// BackgroundTrees grows trees with a vanilla overworld built on another goroutine, for worlds
// whose own generator is not [Vanilla] (flat, void): BDS grows the same trees there. Until the
// overworld is built (a few seconds after start) saplings do not grow; no tick waits for it.
// Helix's choice: BDS has its feature registry from the start.
type BackgroundTrees struct {
	v atomic.Pointer[Vanilla]
}

// NewBackgroundTrees starts building the overworld for trees from reg and seed. A failure is
// logged and saplings then never grow.
func NewBackgroundTrees(reg *registry.Registry, seed int64, log *slog.Logger) *BackgroundTrees {
	b := &BackgroundTrees{}
	go func() {
		v, err := NewVanilla(reg, seed, nil)
		if err != nil {
			if log != nil {
				log.Error("generator: overworld for sapling trees", "err", err)
			}
			return
		}
		b.v.Store(v)
	}()
	return b
}

// GrowFeature is [Vanilla.GrowFeature] once the overworld is built, else false.
func (b *BackgroundTrees) GrowFeature(r *world.Region, feature string, pos cube.Pos, seed uint32) bool {
	v := b.v.Load()
	return v != nil && v.GrowFeature(r, feature, pos, seed)
}
