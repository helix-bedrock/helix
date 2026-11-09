package farm

import (
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Grass blocks dying and spreading, from BDS 1.26.60's GrassBlockBase random tick (0xCAE28F0)
// and its survival check (0xCAE2D40):
//
//   - Grass survives when the light above it (with the sky's darkening) is under 4 and the
//     block above is not one of dark_dies, or when that light is 4 or more and the block above
//     is not water or lava, a stair that is not upside down, a bottom slab, or one of smother
//     (helix-data's bds/grass_cover.json, read at runtime).
//   - On its random tick grass that cannot survive turns to dirt. Grass that can, with light 9
//     or more above it, tries 4 times to spread to a dirt block within 1 on x and z and from 3
//     below to 1 above (BDS draws z, then y, then x), which must itself be able to survive.
//
// So water flowing over grass turns it to dirt, and once the water is gone grass nearby
// spreads back onto it.

// The light levels grass reads.
const (
	grassDarkLight   = 4
	grassSpreadLight = 9
	grassSpreadTries = 4
)

// grassBlockKit holds the grass block, dirt and the blocks that kill grass.
type grassBlockKit struct {
	grass, dirt                      uint32
	smother, darkDies, stairs, slabs map[uint32]bool
}

func (t *Ticker) newGrassBlockKit() *grassBlockKit {
	b := t.blocks
	k := &grassBlockKit{smother: map[uint32]bool{}, darkDies: map[uint32]bool{}, stairs: map[uint32]bool{}, slabs: map[uint32]bool{}}
	k.grass = t.grassKit.grass
	k.dirt, _ = b.Block(dirt)
	cover, err := data.GrassCovers()
	if err != nil {
		return k
	}
	for list, m := range map[*[]string]map[uint32]bool{&cover.Smother: k.smother, &cover.DarkDies: k.darkDies, &cover.Stairs: k.stairs, &cover.Slabs: k.slabs} {
		set := map[string]bool{}
		for _, n := range *list {
			set[n] = true
		}
		for id := range uint32(len(t.info)) { //nolint:gosec // the registry's block count fits uint32
			if set[b.Name(id)] {
				m[id] = true
			}
		}
	}
	if id, ok := b.Block(grassBlock); ok {
		for s := range uint32(len(t.info)) { //nolint:gosec // the registry's block count fits uint32
			if b.Name(s) == b.Name(id) {
				t.info[s].kind = kindGrassBlock
			}
		}
	}
	return k
}

// grassSurvives is BDS's check of whether a grass block may stay at pos.
func (t *Ticker) grassSurvives(r *world.Region, pos cube.Pos) bool {
	k := t.grassBlockKit
	up := pos.Side(cube.FaceUp)
	above, ok := r.Block(up, 0)
	if !ok {
		return true
	}
	if r.Brightness(up, t.skyDarken()) < grassDarkLight {
		return !k.darkDies[above]
	}
	if m := t.blocks.Properties(above).Material; m == registry.MaterialWater || m == registry.MaterialLava {
		return false
	}
	// The smother list was read from each block's default state, a right-way-up stair or a
	// bottom slab, so an upside-down stair or a top slab is taken as letting grass live
	// (Helix's reading; BDS reads the state's own flag).
	states := t.blocks.States(above)
	if k.stairs[above] {
		v, _ := states["upside_down_bit"].(uint8)
		return v != 0
	}
	if k.slabs[above] {
		v, _ := states["minecraft:vertical_half"].(string)
		return v != "bottom"
	}
	return !k.smother[above]
}

// grassTick is a grass block's random tick: it dies without what it needs, else spreads.
func (t *Ticker) grassTick(r *world.Region, pos cube.Pos) {
	k := t.grassBlockKit
	if !t.grassSurvives(r, pos) {
		t.set(r, pos, k.dirt)
		return
	}
	if r.Brightness(pos.Side(cube.FaceUp), t.skyDarken()) < grassSpreadLight {
		return
	}
	rng := t.rng(r)
	for range grassSpreadTries {
		dz := rng.IntN(3) - 1
		dy := rng.IntN(5) - 3
		dx := rng.IntN(3) - 1
		at := pos.Add(cube.Pos{dx, dy, dz})
		if b, ok := r.Block(at, 0); ok && t.blocks.Name(b) == t.blocks.Name(k.dirt) && t.grassSurvives(r, at) {
			t.set(r, at, k.grass)
		}
	}
}
