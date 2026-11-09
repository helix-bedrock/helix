package farm

import (
	"math/rand/v2"
	"sync/atomic"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Saplings growing into trees, from BDS 1.26.60's SaplingBlock:
//
//   - The random tick (0xD50DF70): with light 9 above, one in 7, a sapling whose age_bit is
//     0 sets it (0xD50E7C0), and one already set grows.
//   - Growing (0xD50E650, 0xD50F010): when the sapling has a 2x2 tree and stands in a 2x2
//     of the same sapling (0xD50E330: the squares to +x+z, +x-z, -x+z, -x-z, in that order),
//     the four become air and the 2x2 tree is placed at the square's lowest corner; else, when
//     it has a beehive tree, 5 in 100 with a flower within 2 blocks on x and z at its own
//     level (0xB656DA0's list), that tree; else its one-block tree. A tree that fails puts the
//     saplings back.
//   - Bone meal (0xD50E140): a sapling with only a 2x2 tree takes no bone meal outside a
//     2x2; a creative player's grows at once; otherwise 45 in 100 it takes the age_bit step
//     or grows, and the bone meal is used either way.
//
// The trees are worldgen's features, placed by the TreeGrower (generator.Vanilla).

// TreeGrower places a tree feature into a running world for a sapling. It reports whether the
// feature grew. Runs: the region's owner.
type TreeGrower interface {
	GrowFeature(r *world.Region, feature string, pos cube.Pos, seed uint32) bool
}

// Sapling is which features a sapling grows into, by worldgen feature ID; empty for none.
type Sapling struct {
	// Tree is the tree grown from one sapling.
	Tree string
	// Beehive is the tree grown, now and then, near flowers.
	Beehive string
	// Mega is the tree grown from a 2x2 of saplings.
	Mega string
}

// The numbers of BDS's saplings.
const (
	saplingOdds    = 7
	saplingLight   = 9
	beehiveChance  = 0.05
	boneMealChance = 0.45
	flowerReach    = 2
)

// saplingNames are the vanilla saplings, in the order their trees are kept.
var saplingNames = [...]string{
	"minecraft:oak_sapling", "minecraft:spruce_sapling", "minecraft:birch_sapling", "minecraft:jungle_sapling",
	"minecraft:acacia_sapling", "minecraft:dark_oak_sapling", "minecraft:cherry_sapling", "minecraft:pale_oak_sapling",
}

// vanillaSaplings are BDS's trees per sapling, as its block registration gives them.
var vanillaSaplings = [len(saplingNames)]Sapling{
	{Tree: "minecraft:random_oak_tree_from_sapling_feature", Beehive: "minecraft:random_oak_tree_with_beehive_from_sapling_feature"},
	{Tree: "minecraft:select_standing_spruce_tree_feature", Mega: "minecraft:mega_spruce_tree_feature"},
	{Tree: "minecraft:birch_tree_feature", Beehive: "minecraft:birch_tree_with_beehive_feature"},
	{Tree: "minecraft:select_undecorated_jungle_tree_feature", Mega: "minecraft:mega_jungle_tree_feature"},
	{Tree: "minecraft:savanna_tree_feature"},
	{Mega: "minecraft:select_roofed_tree_feature"},
	{Tree: "minecraft:cherry_tree_feature", Beehive: "minecraft:cherry_tree_with_beehive_feature"},
	{Mega: "minecraft:pale_oak_tree_feature"},
}

// beeFlowers are the blocks a beehive tree looks for (BDS's list at 0xB656DA0, the tulips by
// its tulip list).
var beeFlowers = [...]string{
	"minecraft:cherry_leaves", "minecraft:pink_petals", "minecraft:poppy", "minecraft:blue_orchid", "minecraft:allium",
	"minecraft:azure_bluet", "minecraft:red_tulip", "minecraft:orange_tulip", "minecraft:white_tulip", "minecraft:pink_tulip",
	"minecraft:oxeye_daisy", "minecraft:cornflower", "minecraft:lily_of_the_valley", "minecraft:dandelion",
	"minecraft:wither_rose", "minecraft:torchflower", "minecraft:open_eyeblossom", "minecraft:closed_eyeblossom",
	"minecraft:sunflower", "minecraft:lilac", "minecraft:rose_bush", "minecraft:peony", "minecraft:pitcher_plant",
	"minecraft:chorus_flower", "minecraft:spore_blossom",
}

// saplingKit holds the saplings' states and trees.
type saplingKit struct {
	// states holds each sapling by age_bit.
	states  [len(saplingNames)][2]uint32
	flowers map[uint32]bool
	trees   atomic.Pointer[[len(saplingNames)]Sapling]
	grower  atomic.Pointer[treeGrower]
}

// treeGrower boxes a TreeGrower for an atomic pointer.
type treeGrower struct{ g TreeGrower }

func (t *Ticker) newSaplingKit() *saplingKit {
	b := t.blocks
	k := &saplingKit{flowers: map[uint32]bool{}}
	for i, name := range saplingNames {
		for ab := range uint8(2) {
			if id, ok := b.StateID(name, map[string]any{stateAgeBit: ab}); ok {
				k.states[i][ab] = id
				t.info[id] = stateInfo{kind: kindSapling, age: int8(ab), crop: uint8(i)} //nolint:gosec // few saplings
			}
		}
	}
	names := map[string]bool{}
	for _, n := range beeFlowers {
		names[n] = true
	}
	for id := range uint32(b.Count()) { //nolint:gosec // block counts fit
		if names[b.Name(id)] {
			k.flowers[id] = true
		}
	}
	trees := vanillaSaplings
	k.trees.Store(&trees)
	return k
}

// SetTrees sets what places the saplings' trees; without one saplings never grow. Runs: any
// goroutine.
func (t *Ticker) SetTrees(g TreeGrower) {
	if g == nil {
		t.saplingKit.grower.Store(nil)
		return
	}
	t.saplingKit.grower.Store(&treeGrower{g})
}

// SaplingTrees returns which trees the sapling block name grows, and false for a block that is
// not one of the vanilla saplings. Runs: any goroutine.
func (t *Ticker) SaplingTrees(name string) (Sapling, bool) {
	for i, n := range saplingNames {
		if n == name {
			return t.saplingKit.trees.Load()[i], true
		}
	}
	return Sapling{}, false
}

// SetSaplingTrees changes which trees a vanilla sapling grows; false for a block that is not
// one. Runs: any goroutine.
func (t *Ticker) SetSaplingTrees(name string, s Sapling) bool {
	for i, n := range saplingNames {
		if n != name {
			continue
		}
		for {
			old := t.saplingKit.trees.Load()
			next := *old
			next[i] = s
			if t.saplingKit.trees.CompareAndSwap(old, &next) {
				return true
			}
		}
	}
	return false
}

// saplingTick is a sapling's random tick.
func (t *Ticker) saplingTick(r *world.Region, pos cube.Pos, s stateInfo) {
	if r.Brightness(pos.Side(cube.FaceUp), t.skyDarken()) < saplingLight {
		return
	}
	rng := t.rng(r)
	if rng.IntN(saplingOdds) != 0 {
		return
	}
	t.saplingStep(r, pos, s, rng)
}

// saplingStep sets the sapling's age_bit, or grows it when it is set.
func (t *Ticker) saplingStep(r *world.Region, pos cube.Pos, s stateInfo, rng *rand.Rand) {
	if s.age == 0 {
		t.quietSet(r, pos, t.saplingKit.states[s.crop][1])
		return
	}
	t.growSapling(r, pos, s, rng)
}

// saplingSquare finds a 2x2 of the same sapling holding pos and returns its lowest corner.
func (t *Ticker) saplingSquare(r *world.Region, pos cube.Pos, s stateInfo) (cube.Pos, bool) {
	same := func(dx, dz int) bool {
		b, ok := r.Block(pos.Add(cube.Pos{dx, 0, dz}), 0)
		o := t.state(b)
		return ok && o.kind == kindSapling && o.crop == s.crop
	}
	for _, d := range [...][2]int{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
		if same(0, 0) && same(d[0], 0) && same(0, d[1]) && same(d[0], d[1]) {
			return pos.Add(cube.Pos{min(d[0], 0), 0, min(d[1], 0)}), true
		}
	}
	return pos, false
}

// growSapling grows the sapling at pos into its tree.
func (t *Ticker) growSapling(r *world.Region, pos cube.Pos, s stateInfo, rng *rand.Rand) bool {
	tree := t.saplingKit.trees.Load()[s.crop]
	if corner, ok := t.saplingSquare(r, pos, s); ok && tree.Mega != "" {
		return t.placeTree(r, corner, true, tree.Mega, rng)
	}
	if tree.Beehive != "" && rng.Float32() < beehiveChance && t.flowerNear(r, pos) {
		return t.placeTree(r, pos, false, tree.Beehive, rng)
	}
	if tree.Tree != "" {
		return t.placeTree(r, pos, false, tree.Tree, rng)
	}
	return false
}

// flowerNear reports whether a flower is within 2 blocks of pos on x and z, at its level.
func (t *Ticker) flowerNear(r *world.Region, pos cube.Pos) bool {
	for dx := -flowerReach; dx <= flowerReach; dx++ {
		for dz := -flowerReach; dz <= flowerReach; dz++ {
			if b, ok := r.Block(pos.Add(cube.Pos{dx, 0, dz}), 0); ok && t.saplingKit.flowers[b] {
				return true
			}
		}
	}
	return false
}

// placeTree clears the sapling at at (the 2x2 from it when square), places feature there and
// puts the saplings back if it fails.
func (t *Ticker) placeTree(r *world.Region, at cube.Pos, square bool, feature string, rng *rand.Rand) bool {
	g := t.saplingKit.grower.Load()
	if g == nil {
		return false
	}
	cells := []cube.Pos{at}
	if square {
		cells = append(cells, at.Add(cube.Pos{1, 0, 0}), at.Add(cube.Pos{0, 0, 1}), at.Add(cube.Pos{1, 0, 1}))
	}
	old, ok := r.Block(at, 0)
	if !ok {
		return false
	}
	air := t.blocks.Air()
	for _, c := range cells {
		r.SetBlock(c, 0, air)
	}
	if g.g.GrowFeature(r, feature, at, rng.Uint32()) {
		return true
	}
	for _, c := range cells {
		r.SetBlock(c, 0, old)
	}
	return false
}

// fertilizeSapling is bone meal on a sapling. True if the bone meal is used.
func (t *Ticker) fertilizeSapling(r *world.Region, pos cube.Pos, s stateInfo, creative, rapid bool) bool {
	tree := t.saplingKit.trees.Load()[s.crop]
	if tree.Tree == "" && tree.Beehive == "" {
		if _, ok := t.saplingSquare(r, pos, s); !ok && tree.Mega != "" {
			return false
		}
	}
	rng := t.rng(r)
	if creative || rapid {
		t.growSapling(r, pos, s, rng)
		return true
	}
	if rng.Float32() < boneMealChance {
		t.saplingStep(r, pos, s, rng)
	}
	return true
}
