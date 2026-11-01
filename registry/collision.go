package registry

import "strings"

// The shape rules of the blocks whose collision BDS 1.26.60 computes in code
// (helix-data's bds/collision.json names each block's rule). Rules that
// read only the block's own state are applied once when the registry is
// built; the others (doors) need the world and leave the state marked
// CollisionComputed. Research notes are private.

// The heights BDS gives fences, fence gates and walls: higher than a block,
// so nothing jumps over them.
const fenceHeight = 1.5

// The value order BDS uses for its string block states: the index BDS's
// shape code reads for each value.
var (
	cardinalOrder = []string{"south", "west", "north", "east"}
	cornerOrder   = []string{"none", "inner_left", "inner_right", "outer_left", "outer_right"}
)

// The stair corners by index.
const (
	cornerInnerLeft  = 1
	cornerInnerRight = 2
	cornerOuterLeft  = 3
	cornerOuterRight = 4
)

// stateRules are the rules that read only the state, by rule name.
func stateRule(rule string) func(name string, s map[string]any) []Box {
	switch rule {
	case "slab":
		return slabShape
	case "stairs":
		return stairsShape
	case "wall":
		return wallShape
	case "pane":
		return paneShape
	case "fence":
		return fenceShape
	case "fence_gate":
		return fenceGateShape
	case "carpet":
		return carpetShape
	}
	return nil
}

// num returns an integer or boolean state's value, 0 when missing.
func num(s map[string]any, key string) int {
	switch v := s[key].(type) {
	case uint8:
		return int(v)
	case int32:
		return int(v)
	}
	return 0
}

// enum returns a string state's index in BDS's value order, 0 when missing.
func enum(s map[string]any, key string, order []string) int {
	v, _ := s[key].(string)
	for i, o := range order {
		if o == v {
			return i
		}
	}
	return 0
}

// slabShape: a double slab is a full block; a single one is the top or
// bottom half by its vertical half. BDS tells double slabs by a flag on
// their block type; every block type with it is a *_double_slab.
func slabShape(name string, s map[string]any) []Box {
	if strings.HasSuffix(name, "double_slab") {
		return []Box{{0, 0, 0, 1, 1, 1}}
	}
	if v, _ := s["minecraft:vertical_half"].(string); v == "top" {
		return []Box{{0, 0.5, 0, 1, 1, 1}}
	}
	return []Box{{0, 0, 0, 1, 0.5, 1}}
}

// stairsShape is BDS's three stair boxes: the half slab, the step and, for
// inner corners, the extra quarter, in that order.
func stairsShape(_ string, s map[string]any) []Box {
	up := num(s, "upside_down_bit") != 0
	dir := num(s, "weirdo_direction")
	corner := enum(s, "minecraft:corner", cornerOrder)
	// The slab half and the step's height.
	slab := Box{0, 0, 0, 1, 0.5, 1}
	var y0, y1 float32 = 0.5, 1
	if up {
		slab = Box{0, 0.5, 0, 1, 1, 1}
		y0, y1 = 0, 0.5
	}
	boxes := []Box{slab}
	// The step: the half toward the stair's direction (min x, min z, max x,
	// max z), or a quarter of it for outer corners, which pull one of its
	// sides to the middle.
	steps := [4][4]float32{{0.5, 0, 1, 1}, {0, 0, 0.5, 1}, {0, 0.5, 1, 1}, {0, 0, 1, 0.5}}
	outerLeft := [4]int{3, 1, 0, 2}
	outerRight := [4]int{1, 3, 2, 0}
	step := steps[dir&3]
	switch corner {
	case cornerOuterLeft:
		step[outerLeft[dir&3]] = 0.5
	case cornerOuterRight:
		step[outerRight[dir&3]] = 0.5
	}
	boxes = append(boxes, Box{step[0], y0, step[1], step[2], y1, step[3]})
	if corner != cornerInnerLeft && corner != cornerInnerRight {
		return boxes
	}
	// The inner corner's extra quarter, beside the step.
	left := corner == cornerInnerLeft
	var q [4]float32
	switch dir {
	case 0:
		q = [4]float32{0, 0.5, 0.5, 1}
		if left {
			q = [4]float32{0, 0, 0.5, 0.5}
		}
	case 1:
		q = [4]float32{0.5, 0, 1, 0.5}
		if left {
			q = [4]float32{0.5, 0.5, 1, 1}
		}
	case 2:
		q = [4]float32{0, 0, 0.5, 0.5}
		if left {
			q = [4]float32{0.5, 0, 1, 0.5}
		}
	default:
		q = [4]float32{0, 0.5, 0.5, 1}
		if !left {
			q = [4]float32{0.5, 0.5, 1, 1}
		}
	}
	return append(boxes, Box{q[0], y0, q[1], q[2], y1, q[3]})
}

// wallShape is one box 1.5 high: the post, widened to each connected side;
// a wall without a post running straight north-south or east-west is
// thinner.
func wallShape(_ string, s map[string]any) []Box {
	n := s["wall_connection_type_north"] != "none"
	e := s["wall_connection_type_east"] != "none"
	so := s["wall_connection_type_south"] != "none"
	w := s["wall_connection_type_west"] != "none"
	b := Box{0.25, 0, 0.25, 0.75, fenceHeight, 0.75}
	if n {
		b[2] = 0
	}
	if e {
		b[3] = 1
	}
	if so {
		b[5] = 1
	}
	if w {
		b[0] = 0
	}
	if num(s, "wall_post_bit") == 0 {
		switch {
		case n && so && !e && !w:
			b[0], b[3] = 0.3125, 0.6875
		case e && w && !n && !so:
			b[2], b[5] = 0.3125, 0.6875
		}
	}
	return []Box{b}
}

// paneShape is one box a block high: the middle, widened to each connected
// side.
func paneShape(_ string, s map[string]any) []Box {
	b := Box{0.4375, 0, 0.4375, 0.5625, 1, 0.5625}
	if num(s, "minecraft:connection_north") != 0 {
		b[2] = 0
	}
	if num(s, "minecraft:connection_east") != 0 {
		b[3] = 1
	}
	if num(s, "minecraft:connection_south") != 0 {
		b[5] = 1
	}
	if num(s, "minecraft:connection_west") != 0 {
		b[0] = 0
	}
	return []Box{b}
}

// fenceShape is the north-south arm, then the east-west arm, each when it
// connects, or the post alone; all 1.5 high.
func fenceShape(_ string, s map[string]any) []Box {
	n := num(s, "minecraft:connection_north") != 0
	e := num(s, "minecraft:connection_east") != 0
	so := num(s, "minecraft:connection_south") != 0
	w := num(s, "minecraft:connection_west") != 0
	var boxes []Box
	if n || so {
		b := Box{0.375, 0, 0.375, 0.625, fenceHeight, 0.625}
		if n {
			b[2] = 0
		}
		if so {
			b[5] = 1
		}
		boxes = append(boxes, b)
	}
	if e || w {
		b := Box{0.375, 0, 0.375, 0.625, fenceHeight, 0.625}
		if w {
			b[0] = 0
		}
		if e {
			b[3] = 1
		}
		boxes = append(boxes, b)
	}
	if !n && !e && !so && !w {
		boxes = append(boxes, Box{0.375, 0, 0.375, 0.625, fenceHeight, 0.625})
	}
	return boxes
}

// fenceGateShape: nothing when open, else a bar across the gate's facing.
func fenceGateShape(_ string, s map[string]any) []Box {
	if num(s, "open_bit") != 0 {
		return nil
	}
	if d := enum(s, "minecraft:cardinal_direction", cardinalOrder); d == 0 || d == 2 {
		return []Box{{0, 0, 0.375, 1, fenceHeight, 0.625}}
	}
	return []Box{{0.375, 0, 0, 0.625, fenceHeight, 1}}
}

// carpetShape is a sixteenth of a block high. BDS gives no box instead to
// one kind of shape query (kind 83, not yet identified); players get the
// box.
func carpetShape(string, map[string]any) []Box {
	return []Box{{0, 0, 0, 1, 0.0625, 1}}
}

// Door returns a door's box from its lower and upper half: a thin panel at
// the side its direction, open state and hinge put it. The lower half
// holds the direction and open state, the upper the hinge.
func Door(lower, upper map[string]any) Box {
	const thin, far = 0.1825, 0.8175
	panels := [4]Box{
		{0, 0, 0, 1, 1, thin},
		{far, 0, 0, 1, 1, 1},
		{0, 0, far, 1, 1, 1},
		{0, 0, 0, thin, 1, 1},
	}
	dir := enum(lower, "minecraft:cardinal_direction", cardinalOrder)
	hinge := num(upper, "door_hinge_bit")
	if num(lower, "open_bit") == 0 {
		return panels[(dir+3)%4]
	}
	if dir >= 2 {
		hinge ^= 1
	}
	return panels[2*hinge+dir%2]
}
