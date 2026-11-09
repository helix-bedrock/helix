package farm

import (
	"maps"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Flowers, grass and the other foliage, from BDS 1.26.60's FoliageBlock (vt 0xFB8C100): when a
// neighbour changes and the block can no longer stay (canSurvive, slot 976), a tick 1 later
// destroys it with its drops (0xCB060E0, 0xCB06340). What each may stand on (mayPlaceOn, slot
// 552), read from the code and its lists at runtime:
//
//   - soil: BDS's plant soil list (0xCB03DE0; dirt, grass, podzol, coarse dirt, mycelium,
//     rooted dirt, moss, pale moss, mud, muddy mangrove roots, farmland). Flowers (0xCB03CA0)
//     too; crimson and warped roots also on soul soil and nylium.
//   - wither rose (0xD42A990): a flower's, or netherrack or soul sand.
//   - dead bush (0xCB0EC30): soil, a block type tagged sand, hardened clay or terracotta.
//   - dry grass (0xD524120): sand, red sand, suspicious sand, hardened clay, terracotta or soil.
//   - azalea (0xCCF5B10): clay or soil.
//   - double plants (0xCB1ACC0): the upper half needs its own block under it and soil two
//     down; the lower half its own block above and soil under it. So breaking either half
//     takes the other a tick later.
//   - flower beds (leaf litter, pink petals, wildflowers; 0xCB03000): no water or lava in the
//     block and a block tagged dirt under it.
//
// Not ported: mushrooms (light), lily pads, propagules, coral, sea pickles, dripleaf, shelf
// mushrooms and cactus flowers.

// foliageRule is what a foliage block stands on.
type foliageRule uint8

const (
	ruleSoil foliageRule = iota
	ruleNetherFlower
	ruleWitherRose
	ruleDeadBush
	ruleDryGrass
	ruleAzalea
	ruleDouble
	ruleBed
)

// foliageRules are the foliage blocks by their rule.
var foliageRules = map[string]foliageRule{
	"minecraft:dandelion": ruleSoil, "minecraft:poppy": ruleSoil, "minecraft:blue_orchid": ruleSoil,
	"minecraft:allium": ruleSoil, "minecraft:azure_bluet": ruleSoil, "minecraft:red_tulip": ruleSoil,
	"minecraft:orange_tulip": ruleSoil, "minecraft:white_tulip": ruleSoil, "minecraft:pink_tulip": ruleSoil,
	"minecraft:oxeye_daisy": ruleSoil, "minecraft:cornflower": ruleSoil, "minecraft:lily_of_the_valley": ruleSoil,
	"minecraft:open_eyeblossom": ruleSoil, "minecraft:closed_eyeblossom": ruleSoil,
	"minecraft:golden_dandelion": ruleSoil, "minecraft:torchflower": ruleSoil,
	"minecraft:short_grass": ruleSoil, "minecraft:fern": ruleSoil, "minecraft:bush": ruleSoil,
	"minecraft:red_shrub": ruleSoil, "minecraft:firefly_bush": ruleSoil,
	"minecraft:crimson_roots": ruleNetherFlower, "minecraft:warped_roots": ruleNetherFlower,
	"minecraft:wither_rose":     ruleWitherRose,
	"minecraft:deadbush":        ruleDeadBush,
	"minecraft:short_dry_grass": ruleDryGrass, "minecraft:tall_dry_grass": ruleDryGrass,
	"minecraft:azalea": ruleAzalea, "minecraft:flowering_azalea": ruleAzalea,
	"minecraft:sunflower": ruleDouble, "minecraft:lilac": ruleDouble, "minecraft:rose_bush": ruleDouble,
	"minecraft:peony": ruleDouble, "minecraft:tall_grass": ruleDouble, "minecraft:large_fern": ruleDouble,
	"minecraft:pitcher_plant": ruleDouble,
	"minecraft:leaf_litter":   ruleBed, "minecraft:pink_petals": ruleBed, "minecraft:wildflowers": ruleBed,
}

// stateUpperBit is a double plant's half.
const stateUpperBit = "upper_block_bit"

// foliageKit holds the blocks foliage rules name besides the soils.
type foliageKit struct {
	nether, wither, dry, terracotta map[uint32]bool
	hardenedClay, clay              uint32
	// upper is each lower double plant half's upper half.
	upper map[uint32]uint32
}

func (t *Ticker) newFoliageKit() *foliageKit {
	b := t.blocks
	k := &foliageKit{nether: map[uint32]bool{}, wither: map[uint32]bool{}, dry: map[uint32]bool{}, terracotta: map[uint32]bool{}, upper: map[uint32]uint32{}}
	names := func(m map[uint32]bool, list ...string) {
		for _, n := range list {
			if id, ok := b.Block(n); ok {
				m[id] = true
			}
		}
	}
	names(k.nether, "minecraft:soul_soil", "minecraft:warped_nylium", "minecraft:crimson_nylium")
	names(k.wither, "minecraft:netherrack", "minecraft:soul_sand")
	names(k.dry, "minecraft:sand", "minecraft:red_sand", "minecraft:suspicious_sand")
	for _, c := range [...]string{"white", "orange", "magenta", "light_blue", "yellow", "lime", "pink", "gray",
		"light_gray", "cyan", "purple", "blue", "brown", "green", "red", "black"} {
		names(k.terracotta, "minecraft:"+c+"_terracotta")
	}
	k.hardenedClay, _ = b.Block("minecraft:hardened_clay")
	k.clay, _ = b.Block("minecraft:clay")
	for id := range uint32(len(t.info)) { //nolint:gosec // the registry's block count fits uint32
		rule, ok := foliageRules[b.Name(id)]
		if !ok {
			continue
		}
		upper := int8(0)
		if v, _ := b.States(id)[stateUpperBit].(uint8); v != 0 {
			upper = 1
		}
		t.info[id].kind, t.info[id].crop, t.info[id].facing = kindFoliage, uint8(rule), upper
		if rule == ruleDouble && upper == 0 {
			states := maps.Clone(b.States(id))
			states[stateUpperBit] = uint8(1)
			if up, ok := b.StateID(b.Name(id), states); ok {
				k.upper[id] = up
			}
		}
	}
	return k
}

// foliageSurvives is a foliage block's canSurvive at pos; below is the block under it.
func (t *Ticker) foliageSurvives(r *world.Region, pos cube.Pos, block, below uint32, s stateInfo) bool {
	k, soils := t.foliageKit, t.grassKit.soils
	switch foliageRule(s.crop) {
	case ruleNetherFlower:
		return k.nether[below] || soils[below]
	case ruleWitherRose:
		return soils[below] || k.wither[below]
	case ruleDeadBush:
		return soils[below] || t.hasTag(below, tagSand) || below == k.hardenedClay || k.terracotta[below]
	case ruleDryGrass:
		return k.dry[below] || below == k.hardenedClay || k.terracotta[below] || soils[below]
	case ruleAzalea:
		return below == k.clay || soils[below]
	case ruleDouble:
		name := t.blocks.Name(block)
		if s.facing == 1 {
			if t.blocks.Name(below) != name {
				return false
			}
			soil, ok := r.Block(pos.Add(cube.Pos{0, -2, 0}), 0)
			return !ok || soils[soil]
		}
		above, ok := r.Block(pos.Side(cube.FaceUp), 0)
		return (!ok || t.blocks.Name(above) == name) && soils[below]
	case ruleBed:
		if liq, ok := r.Block(pos, 1); ok {
			if m := t.blocks.Properties(liq).Material; m == registry.MaterialWater || m == registry.MaterialLava {
				return false
			}
		}
		return t.hasTag(below, tagDirt)
	}
	return soils[below]
}

// hasTag reports whether block carries tag.
func (t *Ticker) hasTag(block uint32, tag string) bool {
	for _, g := range t.blocks.Tags(block) {
		if g == tag {
			return true
		}
	}
	return false
}

// doublePlantMayPlace is BDS's mayPlace for a double plant's lower half (0xCB1AAF0): no water
// or lava where it goes, room for the upper half in the dimension, soil under it, and air or
// water above.
func (t *Ticker) doublePlantMayPlace(r *world.Region, pos cube.Pos) bool {
	for layer := range uint8(2) {
		if b, ok := r.Block(pos, layer); ok {
			if m := t.blocks.Properties(b).Material; m == registry.MaterialWater || m == registry.MaterialLava {
				return false
			}
		}
	}
	if pos[1]+1 > r.Dimension().Range()[1] {
		return false
	}
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok || !t.grassKit.soils[below] {
		return false
	}
	above, ok := r.Block(pos.Side(cube.FaceUp), 0)
	return ok && (above == t.blocks.Air() || t.blocks.Properties(above).Material == registry.MaterialWater)
}

// doublePlantPlaced is a double plant's onPlace (0xCB1A880): a lower half puts its upper half
// above it.
func (t *Ticker) doublePlantPlaced(r *world.Region, pos cube.Pos, block uint32) {
	if up, ok := t.foliageKit.upper[block]; ok {
		t.set(r, pos.Side(cube.FaceUp), up)
	}
}

// DoublePlantUpper reports whether block is the upper half of a double plant (tall grass,
// large fern, sunflower, lilac, rose bush, peony, pitcher plant).
func (t *Ticker) DoublePlantUpper(block uint32) bool {
	s := t.state(block)
	return s.kind == kindFoliage && foliageRule(s.crop) == ruleDouble && s.facing == 1
}
