package farm

import (
	"math"
	"math/rand/v2"
	"slices"

	"github.com/helix-bedrock/helix/attach"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/worldgen/noise"
	wgrand "github.com/helix-bedrock/worldgen/rand"
)

// Bone meal on grass blocks, short grass and ferns, from BDS 1.26.60:
//
//   - GrassBlock's onFertilized (0xCAE3040): with air above, 48 tries, each a walk of 1, 2 or 3
//     steps from the block above (x and z each -1 to 1, y down one in 9, else up one in 8),
//     stopping where the block below is not a grass block or the block is solid
//     (0xCAE1E90); a try that was not stopped grows something there (0xCAE1F00).
//   - What grows (0xCAE1F00): in air, a flower one in 8 (0xD901320, by the biome's tags),
//     else a fern one in 7, else short grass, if it may stand there; on short grass with air
//     above, one in 10, the short grass grows tall.
//   - TallGrassBlock's onFertilized (0xD4E1710): short grass becomes tall grass and a fern a
//     large fern, with air above and one of BDS's soils below (0xCB03DE0).
//   - The flowers (0xD901320, 0xD9017A0, 0xD901980, 0xD901C60): flower forests pick from
//     BDS's red flower list by a simplex noise (Random 2345, one octave) at x/48, z/48, a blue
//     orchid becoming a poppy; plains pick tulips where the same noise at x/200, z/200 is
//     under -0.8, else a dandelion one in 4, else a poppy, azure bluet, oxeye daisy or
//     cornflower; swamps a blue orchid; pale gardens a closed eyeblossom; meadows and birch
//     forests (not hills) wildflowers of growth 0 to 2; forests (not taiga or extreme hills)
//     a lily of the valley or a dandelion half each; anywhere else a dandelion one in 3, else
//     a poppy.
const (
	grassTries        = 48
	grassTryStep      = 16
	shortGrass        = "minecraft:short_grass"
	fern              = "minecraft:fern"
	tallGrass         = "minecraft:tall_grass"
	largeFern         = "minecraft:large_fern"
	poppy             = "minecraft:poppy"
	dandelion         = "minecraft:dandelion"
	blueOrchid        = "minecraft:blue_orchid"
	closedEye         = "minecraft:closed_eyeblossom"
	lilyOfValley      = "minecraft:lily_of_the_valley"
	cornflower        = "minecraft:cornflower"
	wildflowers       = "minecraft:wildflowers"
	stateUpper        = "upper_block_bit"
	flowerOdds        = 8
	fernOdds          = 7
	tallOdds          = 10
	walkDownOdds      = 9
	walkUpOdds        = 8
	wildflowerKinds   = 3
	plainsTulipBelow  = -0.8
	plainsDandelion   = 4
	plainsKinds       = 4
	forestPoppyOdds   = 3
	flowerForestScale = 48
	plainsScale       = 200
	// noiseTop is BDS's largest flower forest noise, so the index stays in the list.
	noiseTop = 0.9999
	// flowerNoiseSeed is the seed of BDS's GrassBlock flower noise.
	flowerNoiseSeed = 2345
)

// The biome tags BDS's flower choice reads.
const (
	tagFlowerForest = "flower_forest"
	tagPlains       = "plains"
	tagSwamp        = "swamp"
	tagPaleGarden   = "pale_garden"
	tagMeadow       = "meadow"
	tagBirch        = "birch"
	tagHills        = "hills"
	tagForest       = "forest"
	tagForestGen    = "forest_generation"
	tagExtremeHills = "extreme_hills"
	tagTaiga        = "taiga"
)

// redFlowers is BDS's red flower list, which flower forests pick from.
var redFlowers = [...]string{
	poppy, blueOrchid, "minecraft:allium", "minecraft:azure_bluet", "minecraft:red_tulip", "minecraft:orange_tulip",
	"minecraft:white_tulip", "minecraft:pink_tulip", "minecraft:oxeye_daisy", cornflower, lilyOfValley,
}

// tulips are the plains' tulips, in BDS's order.
var tulips = [...]string{"minecraft:red_tulip", "minecraft:orange_tulip", "minecraft:white_tulip", "minecraft:pink_tulip"}

// plainsFlowers are the plains' other flowers after a dandelion, in BDS's order.
var plainsFlowers = [plainsKinds]string{poppy, "minecraft:azure_bluet", "minecraft:oxeye_daisy", cornflower}

// tallSoils are what short grass and ferns grow tall on (BDS's list at 0xCB03DE0).
var tallSoils = [...]string{
	dirt, grassBlock, "minecraft:podzol", coarseDirt, "minecraft:mycelium", rootedDirt, "minecraft:moss_block",
	"minecraft:pale_moss_block", "minecraft:mud", "minecraft:muddy_mangrove_roots", Farmland,
}

// grassKit is what bone meal on grass needs, made once by NewTicker.
type grassKit struct {
	biomes *registry.Biomes
	solid  *attach.Table
	noise  *noise.SimplexOctaves
	grass  uint32
	short  uint32
	fern   uint32
	// tall holds the two halves of tall grass and of a large fern, by short grass and fern.
	tall  map[uint32][2]uint32
	soils map[uint32]bool
}

func newGrassKit(reg *registry.Registry) *grassKit {
	b := reg.Blocks
	k := &grassKit{biomes: reg.Biomes, tall: map[uint32][2]uint32{}, soils: map[uint32]bool{}}
	k.solid, _ = attach.New(b)
	k.noise = noise.NewSimplexOctaves(wgrand.NewMT(flowerNoiseSeed), 1, false)
	k.grass, k.short, k.fern = blockOrNone(b, grassBlock), blockOrNone(b, shortGrass), blockOrNone(b, fern)
	for from, to := range map[uint32]string{k.short: tallGrass, k.fern: largeFern} {
		lo, okLo := b.StateID(to, map[string]any{stateUpper: uint8(0)})
		hi, okHi := b.StateID(to, map[string]any{stateUpper: uint8(1)})
		if okLo && okHi {
			k.tall[from] = [2]uint32{lo, hi}
		}
	}
	for _, name := range tallSoils {
		for id := range uint32(b.Count()) { //nolint:gosec // block counts fit
			if b.Name(id) == name {
				k.soils[id] = true
			}
		}
	}
	return k
}

// air reports whether the block at pos is of air material.
func (t *Ticker) air(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && t.blocks.Properties(b).Material == 0
}

// fertilizeGrass is bone meal on the grass block at pos (GrassBlock's onFertilized). True
// if something grew.
func (t *Ticker) fertilizeGrass(r *world.Region, pos cube.Pos) bool {
	if !t.air(r, pos.Side(cube.FaceUp)) {
		return false
	}
	rng := t.rng(r)
	grew := false
	for i := grassTryStep; i < grassTryStep+grassTries; i++ {
		steps := i/grassTryStep - 1
		at := pos.Side(cube.FaceUp)
		stopped := false
		for {
			at[0] += rng.IntN(3) - 1
			switch {
			case rng.IntN(walkDownOdds) == 0:
				at[1]--
			case rng.IntN(walkUpOdds) == 0:
				at[1]++
			}
			at[2] += rng.IntN(3) - 1
			stopped = t.walkStops(r, at)
			if stopped || steps == 0 {
				break
			}
			steps--
		}
		if !stopped && t.growOnGrass(r, at, rng) {
			grew = true
		}
	}
	return grew
}

// walkStops reports whether bone meal's walk over grass stops at pos: the block below is not
// a grass block, or pos is solid.
func (t *Ticker) walkStops(r *world.Region, pos cube.Pos) bool {
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok || below != t.grassKit.grass {
		return true
	}
	b, ok := r.Block(pos, 0)
	return !ok || t.grassKit.solid != nil && t.grassKit.solid.Solid(b)
}

// growOnGrass grows a flower, fern or short grass in air at pos, or short grass there tall.
func (t *Ticker) growOnGrass(r *world.Region, pos cube.Pos, rng *rand.Rand) bool {
	k := t.grassKit
	b, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	if t.blocks.Properties(b).Material == 0 {
		place := k.short
		switch {
		case rng.IntN(flowerOdds) == 0:
			place = t.flower(r, pos, rng)
		case rng.IntN(fernOdds) == 0:
			place = k.fern
		}
		if place != 0 {
			t.set(r, pos, place)
			return true
		}
		return false
	}
	if b == k.short && t.air(r, pos.Side(cube.FaceUp)) && rng.IntN(tallOdds) == 0 {
		return t.growTall(r, pos, b)
	}
	return false
}

// growTall is TallGrassBlock's onFertilized: short grass or a fern at pos grows two blocks
// tall, with air above and a soil below. True if it grew.
func (t *Ticker) growTall(r *world.Region, pos cube.Pos, b uint32) bool {
	halves, ok := t.grassKit.tall[b]
	if !ok || !t.air(r, pos.Side(cube.FaceUp)) {
		return false
	}
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok || !t.grassKit.soils[below] {
		return false
	}
	if !r.SetBlock(pos, 0, halves[0]) || !r.SetBlock(pos.Side(cube.FaceUp), 0, halves[1]) {
		return false
	}
	r.UpdateNeighbours(pos)
	r.UpdateNeighbours(pos.Side(cube.FaceUp))
	return true
}

// flower is the flower BDS grows at pos for its biome; 0 if the block is missing.
func (t *Ticker) flower(r *world.Region, pos cube.Pos, rng *rand.Rand) uint32 {
	var tags []string
	if id, ok := r.Biome(pos); ok {
		if bio, ok := t.grassKit.biomes.ByID(id); ok {
			tags = bio.Tags
		}
	}
	has := func(tag string) bool { return slices.Contains(tags, tag) }
	name := ""
	switch {
	case has(tagFlowerForest):
		v := (t.grassKit.noise.Sum2(float32(pos[0])/flowerForestScale, float32(pos[2])/flowerForestScale) + 1) * 0.5
		v = min(max(v, 0), noiseTop)
		name = redFlowers[int(v*float32(len(redFlowers)))]
		if name == blueOrchid {
			name = poppy
		}
	case has(tagPlains):
		v := t.grassKit.noise.Sum2(float32(pos[0])/plainsScale, float32(pos[2])/plainsScale)
		switch {
		case v < plainsTulipBelow:
			name = tulips[rng.IntN(len(tulips))]
		case rng.IntN(plainsDandelion) == 0:
			name = dandelion
		default:
			name = plainsFlowers[rng.IntN(plainsKinds)]
		}
	case has(tagSwamp):
		name = blueOrchid
	case has(tagPaleGarden):
		name = closedEye
	case !has(tagMeadow) && !has(tagBirch) || has(tagHills):
		switch {
		case (has(tagForest) || has(tagForestGen)) && !has(tagExtremeHills) && !has(tagTaiga):
			name = dandelion
			if rng.IntN(2) == 0 {
				name = lilyOfValley
			}
		case rng.IntN(forestPoppyOdds) == 0:
			name = dandelion
		default:
			name = poppy
		}
	default:
		growth := int32(rng.IntN(wildflowerKinds)) //nolint:gosec // 0 to 2
		if id, ok := t.blocks.StateID(wildflowers, map[string]any{stateGrowth: growth}); ok {
			return id
		}
		name = wildflowers
	}
	id, _ := t.blocks.Block(name)
	return id
}

// blockOrNone is the block's ID, or one no block has when it is missing, so it never matches.
func blockOrNone(b *registry.Blocks, name string) uint32 {
	if id, ok := b.Block(name); ok {
		return id
	}
	return math.MaxUint32
}
