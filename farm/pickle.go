package farm

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Sea pickles, from BDS 1.26.60's SeaPickle (vt 0xFC1C4A8; the research note is private). Their
// footing is foliage.go's rule. Their tick 0xD51CBF0 (after any neighbour change, 0xD51CB50)
// breaks them when they cannot stay, else 0xD51CC30 sets dead_bit by whether water is in the
// block, as onPlace does. Bone meal (canBeFertilized 0xD51D770, onFertilized 0xD51D0F0) works
// on pickles in water on a coral block: across a diamond 5 wide and from one below to their
// level, each water block over a coral block gets, one time in 3, 1 to 3 pickles, and the
// pickles fertilized become 4.

// Numbers and states of sea pickles in BDS.
const (
	seaPickle         = "minecraft:sea_pickle"
	statePickles      = "cluster_count"
	stateDead         = "dead_bit"
	pickleMaxCluster  = 3
	pickleSpreadOdds  = 3
	pickleSpreadWidth = 5
)

// pickleKit is the states of sea pickles, made once by NewTicker.
type pickleKit struct {
	// pickle is each state by cluster_count and dead_bit.
	pickle [pickleMaxCluster + 1][2]uint32
	// of is each pickle state's cluster_count, and whether it is dead.
	of    map[uint32][2]int
	coral map[uint32]bool
}

// newPickleKit finds the sea pickle states and the coral blocks.
func (t *Ticker) newPickleKit() *pickleKit {
	b := t.blocks
	k := &pickleKit{of: map[uint32][2]int{}, coral: map[uint32]bool{}}
	for n := range int32(pickleMaxCluster + 1) {
		for dead := range int32(2) {
			if id, ok := b.StateID(seaPickle, map[string]any{statePickles: n, stateDead: uint8(dead)}); ok { //nolint:gosec // 0 or 1
				k.pickle[n][dead] = id
				k.of[id] = [2]int{int(n), int(dead)}
			}
		}
	}
	for _, c := range coralColours {
		if id, ok := b.Block("minecraft:" + c + "_coral_block"); ok {
			k.coral[id] = true
		}
	}
	return k
}

// isPickle reports whether block is a sea pickle.
func (t *Ticker) isPickle(block uint32) bool {
	_, ok := t.pickleKit.of[block]
	return ok
}

// pickleLive is BDS's 0xD51CC30: dead_bit set by whether water is in the block.
func (t *Ticker) pickleLive(r *world.Region, pos cube.Pos, block uint32) {
	k := t.pickleKit
	of, ok := k.of[block]
	if !ok {
		return
	}
	dead := 1
	if t.water(r, pos) {
		dead = 0
	}
	if dead != of[1] {
		r.SetBlock(pos, 0, k.pickle[of[0]][dead])
	}
}

// pickleFertilizable is canBeFertilized: water in the block and a coral block under it.
func (t *Ticker) pickleFertilizable(r *world.Region, pos cube.Pos) bool {
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	return ok && t.pickleKit.coral[below] && t.water(r, pos)
}

// fertilizePickle is bone meal on sea pickles.
func (t *Ticker) fertilizePickle(r *world.Region, pos cube.Pos) bool {
	if !t.pickleFertilizable(r, pos) {
		return false
	}
	k, rng := t.pickleKit, t.rng(r)
	width, back := 1, 0
	for col := range pickleSpreadWidth {
		for dz := range width {
			for y := pos[1] - 1; y <= pos[1]; y++ {
				at := cube.Pos{pos[0] - 2 + col, y, pos[2] - back + dz}
				if at == pos || rng.IntN(pickleSpreadOdds) != 0 || !t.waterBlock(r, at) {
					continue
				}
				if below, ok := r.Block(at.Side(cube.FaceDown), 0); !ok || !k.coral[below] {
					continue
				}
				r.SetBlock(at, 0, k.pickle[rng.IntN(pickleMaxCluster)][0])
				r.UpdateNeighbours(at)
			}
		}
		if col < 2 {
			width, back = width+2, back+1
		} else {
			width, back = width-2, back-1
		}
	}
	t.set(r, pos, k.pickle[pickleMaxCluster][0])
	return true
}

// waterBlock reports whether the block itself at pos is water.
func (t *Ticker) waterBlock(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && t.blocks.Properties(b).Material == materialWater
}

// pickleTick is SeaPickle's tick: broken when it cannot stay, else dead_bit by its water.
func (t *Ticker) pickleTick(r *world.Region, pos cube.Pos) {
	now, ok := r.Block(pos, 0)
	if !ok || !t.isPickle(now) {
		return
	}
	if !t.plantSurvives(r, pos, t.state(now)) {
		if t.fall != nil {
			t.fall(r, pos, now)
		}
		return
	}
	t.pickleLive(r, pos, now)
}
