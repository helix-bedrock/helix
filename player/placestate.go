package player

import (
	"maps"
	"math"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/attach"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// The placement rules of BDS 1.26.60's getPlacementBlock: which rule and
// traits each block uses comes from helix-data's bds/placement.json; what
// each rule does is below. Research notes are private.

// The value order BDS uses for the string states placement sets.
var placeOrders = map[string][]string{
	"minecraft:cardinal_direction": {"south", "west", "north", "east"},
	"minecraft:block_face":         {"down", "up", "north", "south", "west", "east"},
	"minecraft:facing_direction":   {"down", "up", "north", "south", "west", "east"},
	"minecraft:vertical_half":      {"bottom", "top"},
	"pillar_axis":                  {"y", "x", "z"},
	"attachment":                   {"standing", "hanging", "side", "multiple"},
	"lever_direction":              {"down_east_west", "east", "west", "south", "north", "up_north_south", "up_east_west", "down_north_south"},
}

// Byte tables BDS shifts by eight times a yaw index: the horizontal
// facing of blocks that face the player (dispensers, pistons, barrels)
// and of the observer's facing trait.
const (
	lookFacingTable  = 0x04030502
	traitFacingTable = 0x05020403
)

// The faces' numbers, as the client sends them.
const (
	faceDown = iota
	faceUp
	faceNorth
	faceSouth
	faceWest
	faceEast
)

// Attachments of bells and grindstones.
const (
	attachStanding = iota
	attachHanging
	attachSide
	attachMultiple
)

// oppositeFace is each face's opposite.
var oppositeFace = [6]int{faceUp, faceDown, faceSouth, faceNorth, faceEast, faceWest}

// placeState is the state being built for a placement.
type placeState struct {
	pl     *Placement
	r      *world.Region
	name   string
	states map[string]any
	yaw    float32
	// feet is the placing player's feet height, for its box.
	feet float32
}

// set sets state key to value index v, in the state's own type: a name
// for string states, a byte for bits, an int otherwise. A block without
// the state is left as it is.
func (s *placeState) set(key string, v int) {
	switch s.states[key].(type) {
	case string:
		if order := placeOrders[key]; v >= 0 && v < len(order) {
			s.states[key] = order[v]
		}
	case uint8:
		s.states[key] = uint8(v) //nolint:gosec // state values are small
	case int32:
		s.states[key] = int32(v) //nolint:gosec // state values are small
	}
}

// get returns state key's value index, -1 if the block has none.
func (s *placeState) get(states map[string]any, key string) int {
	switch v := states[key].(type) {
	case string:
		for i, o := range placeOrders[key] {
			if o == v {
				return i
			}
		}
	case uint8:
		return int(v)
	case int32:
		return int(v)
	}
	return -1
}

// yawIndex is BDS's floor(yaw * 4 / 360 + add) as an index 0 to 3.
func (s *placeState) yawIndex(add float32) int {
	return int(math.Floor(float64(s.yaw*4/360+add))) & 3
}

// tableIndex is the byte of table BDS's shift by eight times the yaw
// index picks.
func tableIndex(table uint32, f float32) int {
	i := int(math.Floor(float64(f)))
	return int(table >> (uint(8*i) & 31) & 0xff) //nolint:gosec // the shift is masked as x86 masks it
}

// lookFacing is the six-way facing of blocks that face the player: up or
// down when the player stands within one block of it horizontally and the
// block is above or below the player's box, else the byte of table that
// yaw (the yaw index before flooring) picks. aboveIsUp says whether a
// block above the player faces up (the observer) or down (the rest).
func (s *placeState) lookFacing(table uint32, yaw float32, aboveIsUp bool) int {
	pos, at := s.pl.Player.entity.Position(), s.pl.Pos
	near := abs(at[0]-int(math.Floor(float64(pos[0])))) <= 1 && abs(at[2]-int(math.Floor(float64(pos[2])))) <= 1
	y := float32(at[1])
	inBox := s.feet <= y && y <= s.feet+playerHeight
	if !near || inBox {
		return tableIndex(table, yaw)
	}
	below := s.feet > y
	if below != aboveIsUp {
		return faceUp
	}
	return faceDown
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// top is BDS's vertical half rule: the top half when the bottom face of a
// block was clicked, or the upper half of a side.
func (s *placeState) top() int {
	f := s.pl.Face
	if f == faceDown || f != faceUp && s.pl.ClickPos[1] > 0.5 {
		return 1
	}
	return 0
}

// vanillaPlaceState is BDS's getPlacementBlock: a block behaviour's rule
// first, else the block's rule and traits from BDS's table.
func vanillaPlaceState(pl *Placement) (uint32, bool) {
	p := pl.Player
	blocks := p.s.reg.Blocks
	name := blocks.Name(pl.Block)
	if s, ok := p.s.game.blocks[name].(PlacementStater); ok {
		return s.PlacementState(pl)
	}
	how, ok := p.s.game.placement[name]
	r := p.entity.Region()
	if !ok || r == nil {
		return pl.Block, true
	}
	s := &placeState{pl: pl, r: r, name: name, states: maps.Clone(blocks.States(pl.Block)),
		yaw: p.entity.Rotation().Yaw, feet: p.entity.Position()[1] - eyeHeight}
	for _, t := range how.Traits {
		s.trait(t)
	}
	s.rule(how.Rule)
	if id, ok := blocks.StateID(name, s.states); ok {
		return id, true
	}
	return pl.Block, true
}

// trait applies one of BDS's placement traits.
func (s *placeState) trait(t data.PlacementTrait) {
	switch t.Kind {
	case "cardinal":
		s.set("minecraft:cardinal_direction", int(math.Floor(float64((s.yaw-t.Offset)/90+0.5)))&3)
	case "facing":
		s.set("minecraft:facing_direction", s.lookFacing(traitFacingTable, (s.yaw-t.Offset)/90+0.5, true))
	case "vertical_half":
		s.set("minecraft:vertical_half", s.top())
	case "block_face":
		s.set("minecraft:block_face", int(s.pl.Face))
	}
}

// Direction tables by yaw index or face.
var (
	stairsByYaw    = [4]int{2, 1, 3, 0}
	trapdoorByYaw  = [4]int{3, 0, 2, 1}
	facingByYaw    = [4]int{2, 5, 3, 4}
	directionByYaw = [4]int{2, 3, 0, 1}
	// directionByFace is a horizontal face as a "direction" state.
	directionByFace = [6]int{0, 0, 2, 0, 1, 3}
	tripwireByFace  = [6]int{-1, -1, 2, 0, 1, 3}
	cocoaByFace     = [6]int{0, 0, 0, 2, 3, 1}
	vineByFace      = [6]int{0, 0, 1, 4, 8, 2}
)

// rule applies the block's own getPlacementBlock.
func (s *placeState) rule(rule string) {
	face := int(s.pl.Face)
	switch rule {
	case "stairs":
		s.set("weirdo_direction", stairsByYaw[s.yawIndex(0.5)])
		s.set("upside_down_bit", s.top())
	case "pillar":
		axis := 0
		switch face {
		case faceNorth, faceSouth:
			axis = 2
		case faceWest, faceEast:
			axis = 1
		}
		s.set("pillar_axis", axis)
	case "trapdoor":
		s.set("direction", trapdoorByYaw[s.yawIndex(0.5)])
		s.set("upside_down_bit", s.top())
	case "glazed_terracotta":
		s.set("facing_direction", facingByYaw[s.yawIndex(0.5)])
	case "skull":
		s.set("facing_direction", facingByYaw[s.yawIndex(2.5)])
	case "face_facing":
		s.set("facing_direction", face)
	case "leaves":
		s.set("persistent_bit", 1)
	case "look_facing":
		s.set("facing_direction", s.lookFacing(lookFacingTable, s.yaw*4/360+0.5, false))
	case "piston":
		s.set("facing_direction", s.lookFacing(lookFacingTable, (s.yaw-180)*4/360+0.5, false))
	case "yaw_direction":
		s.set("direction", s.yawIndex(0.5))
	case "yaw_direction_offset":
		s.set("direction", directionByYaw[s.yawIndex(0.5)])
	case "vault":
		s.set("minecraft:cardinal_direction", directionByYaw[s.yawIndex(0.5)])
	case "tripwire_hook":
		if d := tripwireByFace[face]; d >= 0 {
			s.set("direction", d)
		}
	case "cocoa":
		if face < faceSouth {
			face = faceNorth
		}
		s.set("direction", cocoaByFace[face])
	case "hopper":
		if face != faceDown {
			s.set("facing_direction", oppositeFace[face])
		}
	case "vine":
		if v := vineByFace[face]; v != 0 {
			s.set("vine_direction_bits", v)
		}
	case "lever":
		s.lever(face)
	case "lantern":
		s.set("hanging", b2i(s.pl.Player.s.attach.LanternHangs(s.r, s.pl.Pos, cube.Face(face))))
	case "torch":
		s.states["torch_facing_direction"] = attach.TorchState(s.pl.Player.s.attach.TorchFace(s.r, s.pl.Pos, cube.Face(face)))
	case "ladder":
		if f, ok := s.pl.Player.s.attach.LadderFace(s.r, s.pl.Pos, cube.Face(face)); ok {
			s.set("facing_direction", int(f))
		}
	case "end_rod":
		s.endRod(face)
	case "bell", "grindstone":
		s.attached(face, rule == "bell")
	}
}

// Lever directions on floors and ceilings.
const (
	leverDownEastWest   = 0
	leverUpNorthSouth   = 5
	leverUpEastWest     = 6
	leverDownNorthSouth = 7
)

// lever is BDS's lever: the face clicked, and on a floor or ceiling the
// axis the player looks along.
func (s *placeState) lever(face int) {
	odd := int(math.Floor(float64(s.yaw*4/360+0.5)))&1 == 1
	switch {
	case face == faceDown && odd:
		s.set("lever_direction", leverDownNorthSouth)
	case face == faceDown:
		s.set("lever_direction", leverDownEastWest)
	case face == faceUp && odd:
		s.set("lever_direction", leverUpEastWest)
	case face == faceUp:
		s.set("lever_direction", leverUpNorthSouth)
	default:
		s.set("lever_direction", 6-face)
	}
}

// endRod is BDS's end rod: facing the clicked face, flipped when it is
// put on the end of an end rod facing the same way.
func (s *placeState) endRod(face int) {
	behind, ok := s.r.Block(s.pl.Pos.Side(cube.Face(oppositeFace[face])), 0)
	blocks := s.pl.Player.s.reg.Blocks
	if ok && blocks.Name(behind) == s.name && s.get(blocks.States(behind), "facing_direction") == face {
		face = oppositeFace[face]
	}
	s.set("facing_direction", face)
}

// holds reports whether side of the block at pos holds what is attached to it.
func (s *placeState) holds(pos cube.Pos, side int) bool {
	b, ok := s.r.Block(pos, 0)
	return ok && s.pl.Player.s.attach.Supports(b, cube.Face(side))
}

// attached is BDS's bell and grindstone: standing on a floor, hanging
// from a ceiling (both turned by the player's yaw) or on a side; a bell
// between two blocks is "multiple".
func (s *placeState) attached(face int, bell bool) {
	switch face {
	case faceDown:
		s.set("attachment", attachHanging)
		s.set("direction", directionByYaw[s.yawIndex(0.5)])
	case faceUp:
		s.set("attachment", attachStanding)
		s.set("direction", directionByYaw[s.yawIndex(0.5)])
	default:
		a := attachSide
		if bell && s.holds(s.pl.Pos.Side(cube.Face(face)), oppositeFace[face]) {
			a = attachMultiple
		}
		s.set("attachment", a)
		s.set("direction", directionByFace[face])
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
