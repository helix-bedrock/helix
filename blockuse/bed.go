package blockuse

import (
	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// The beds, and the states BDS 1.26.60 keeps their halves in.
const (
	Bed      = "minecraft:bed"
	StrawBed = "minecraft:straw_bed"
	// BedHead is set on the head half, BedOccupied while someone sleeps in it.
	BedHead     = "head_piece_bit"
	BedOccupied = "occupied_bit"
	// bedDirection is the bed's facing, numbered as a Cardinal (BDS's direction state);
	// a straw bed keeps it in the cardinal state instead.
	bedDirection = "direction"
)

// IsBed reports whether block is a bed or a straw bed.
func IsBed(blocks *registry.Blocks, block uint32) bool {
	n := blocks.Name(block)
	return n == Bed || n == StrawBed
}

// BedFacing is the way from a bed's foot to its head (BDS's sub_CCD1570).
func BedFacing(blocks *registry.Blocks, block uint32) direction.Cardinal {
	states := blocks.States(block)
	if v, ok := states[bedDirection].(int32); ok {
		return direction.Cardinal(v & 3) //nolint:gosec // masked to a direction
	}
	if s, ok := states[stateCardinal].(string); ok {
		c, _ := direction.Parse(s)
		return c
	}
	return direction.South
}

// BedWithFacing is block turned to face c, in whichever state its kind of bed keeps it.
func BedWithFacing(blocks *registry.Blocks, block uint32, c direction.Cardinal) uint32 {
	if _, ok := blocks.States(block)[bedDirection]; ok {
		return with(blocks, block, bedDirection, int32(c))
	}
	return with(blocks, block, stateCardinal, c.String())
}

// BedSet is block with a bit state changed (BedHead, BedOccupied).
func BedSet(blocks *registry.Blocks, block uint32, key string, on bool) uint32 {
	return with(blocks, block, key, flag(on))
}

// BedIsHead reports whether block is the head half of its bed.
func BedIsHead(blocks *registry.Blocks, block uint32) bool { return bit(blocks, block, BedHead) }

// BedIsOccupied reports whether the bed's occupied bit is set.
func BedIsOccupied(blocks *registry.Blocks, block uint32) bool {
	return bit(blocks, block, BedOccupied)
}

// BedOther is where the other half of the bed at pos is (BDS's sub_CCD21E0): the head is
// one step along its facing from the foot.
func BedOther(blocks *registry.Blocks, pos cube.Pos, block uint32) cube.Pos {
	f := BedFacing(blocks, block).Face()
	if BedIsHead(blocks, block) {
		return pos.Side(f.Opposite())
	}
	return pos.Side(f)
}

// BedHeadPos is the head of the bed at pos, false if its other half is missing (BDS's
// sub_CCD0D70, which refuses when the other block is not the same kind of bed).
func BedHeadPos(blocks *registry.Blocks, r *world.Region, pos cube.Pos, block uint32) (cube.Pos, bool) {
	if BedIsHead(blocks, block) {
		return pos, true
	}
	other := BedOther(blocks, pos, block)
	b, ok := r.Block(other, 0)
	return other, ok && blocks.Name(b) == blocks.Name(block)
}

// bedWhole reports whether the bed at pos still has its other half. A half whose other
// half went is removed with no drop: the half that was broken dropped the bed.
func bedWhole(blocks *registry.Blocks, r *world.Region, pos cube.Pos, block uint32) bool {
	other := BedOther(blocks, pos, block)
	b, ok := r.Block(other, 0)
	if !ok {
		return true
	}
	return blocks.Name(b) == blocks.Name(block) && BedIsHead(blocks, b) != BedIsHead(blocks, block)
}
