package connect

import (
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Since 1.26.50 a stair's corner is its own block state (minecraft:corner), so the
// server works it out on placing and when a neighbour changes, as it does fence arms.
// BDS 1.26.60's rule: the stair in front (on the tall side) can turn this one into
// an outer corner, the stair behind into an inner one, each only when it faces
// across and sits on the same half, and only when the block on the other side is
// not this very stair. Research notes are private.

// The corner values, in BDS's order.
const (
	cornerNone = iota
	cornerInnerLeft
	cornerInnerRight
	cornerOuterLeft
	cornerOuterRight
	cornerCount
)

var cornerNames = [cornerCount]string{"none", "inner_left", "inner_right", "outer_left", "outer_right"}

// cornerState is the stair corner's state name.
const cornerState = "minecraft:corner"

// The horizontal directions in the order BDS's stair code numbers them.
const (
	stairSouth = iota
	stairWest
	stairNorth
	stairEast
)

// weirdoDirs maps weirdo_direction (east, west, south, north) to those numbers.
var weirdoDirs = [4]uint8{stairEast, stairWest, stairSouth, stairNorth}

// stairSides is each direction's side and the direction turned left of it (BDS's
// table per direction: front, then left and right with the blocks beside them).
var stairSides = [4]struct {
	face cube.Face
	left uint8
}{
	stairSouth: {cube.FaceSouth, stairEast},
	stairWest:  {cube.FaceWest, stairSouth},
	stairNorth: {cube.FaceNorth, stairWest},
	stairEast:  {cube.FaceEast, stairNorth},
}

// stairFacts is what the corner rule reads about one stair state.
type stairFacts struct {
	dir uint8
	top bool
	// corners holds the same stair with each corner; corners[cornerNone] is how two
	// stairs are compared with their corners left out.
	corners [cornerCount]uint32
}

// stairsOf indexes every stair state with a corner. index holds 1 + its place in
// facts, 0 for anything else.
func stairsOf(blocks *registry.Blocks) (index []uint16, facts []stairFacts) {
	index = make([]uint16, blocks.BlockCount())
	for id := range index {
		state := uint32(id) //nolint:gosec // state IDs fit uint32
		s := blocks.States(state)
		if _, ok := s[cornerState].(string); !ok {
			continue
		}
		w, ok := s["weirdo_direction"].(int32)
		if !ok || w < 0 || w > 3 {
			continue
		}
		up, _ := s["upside_down_bit"].(uint8)
		f := stairFacts{dir: weirdoDirs[w], top: up != 0}
		name := blocks.Name(state)
		next := make(map[string]any, len(s))
		for k, v := range s {
			next[k] = v
		}
		complete := true
		for c, cn := range cornerNames {
			next[cornerState] = cn
			cid, ok := blocks.StateID(name, next)
			if !ok {
				complete = false
				break
			}
			f.corners[c] = cid
		}
		if !complete || len(facts) >= 0xFFFF {
			continue
		}
		facts = append(facts, f)
		index[id] = uint16(len(facts)) //nolint:gosec // bounded above
	}
	return index, facts
}

// stair returns a state's stair facts.
func (t *Table) stair(block uint32) (stairFacts, bool) {
	if int(block) >= len(t.stairIndex) || t.stairIndex[block] == 0 {
		return stairFacts{}, false
	}
	return t.stairs[t.stairIndex[block]-1], true
}

// opposite returns the direction behind d.
func opposite(d uint8) uint8 { return (d + 2) & 3 }

// StairCorner returns the stair at pos with its corner worked out from its
// neighbours, or block itself if it is not a stair with a corner. A neighbour in an
// unloaded chunk counts as air. Runs: owner of the region.
func (t *Table) StairCorner(r *world.Region, pos cube.Pos, block uint32) uint32 {
	self, ok := t.stair(block)
	if !ok {
		return block
	}
	sides := stairSides[self.dir]
	left := sides.left
	right := opposite(left)
	// The stair in front can make an outer corner, toward the way it faces.
	if f, ok := t.neighbourStair(r, pos.Side(sides.face), self.top); ok {
		switch {
		case f.dir == left && !t.sameStair(r, pos.Side(stairSides[right].face), self):
			return self.corners[cornerOuterLeft]
		case f.dir == right && !t.sameStair(r, pos.Side(stairSides[left].face), self):
			return self.corners[cornerOuterRight]
		}
	}
	// The stair behind can make an inner corner, toward the way it faces.
	if b, ok := t.neighbourStair(r, pos.Side(stairSides[opposite(self.dir)].face), self.top); ok {
		switch {
		case b.dir == left && !t.sameStair(r, pos.Side(stairSides[left].face), self):
			return self.corners[cornerInnerLeft]
		case b.dir == right && !t.sameStair(r, pos.Side(stairSides[right].face), self):
			return self.corners[cornerInnerRight]
		}
	}
	return self.corners[cornerNone]
}

// neighbourStair returns the stair at pos when it sits on the same half.
func (t *Table) neighbourStair(r *world.Region, pos cube.Pos, top bool) (stairFacts, bool) {
	n, ok := r.Block(pos, 0)
	if !ok {
		return stairFacts{}, false
	}
	f, ok := t.stair(n)
	if !ok || f.top != top {
		return stairFacts{}, false
	}
	return f, true
}

// sameStair reports whether the block at pos is the same stair as self, the same
// kind facing the same way on the same half, whatever its corner.
func (t *Table) sameStair(r *world.Region, pos cube.Pos, self stairFacts) bool {
	n, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	f, ok := t.stair(n)
	return ok && f.corners[cornerNone] == self.corners[cornerNone]
}
