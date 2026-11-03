package player

import (
	"maps"
	"math"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/attach"
	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// The placement rules of BDS 1.26.60's getPlacementBlock: which rule and
// traits each block uses comes from helix-data's bds/placement.json; what
// each rule does is below. Research notes are private.

// cardinalNames is direction's state values, for placeOrders.
var cardinalNames = direction.Names()

// The value order BDS uses for the string states placement sets.
var placeOrders = map[string][]string{
	direction.CardinalState:      cardinalNames[:],
	"minecraft:block_face":       {"down", "up", "north", "south", "west", "east"},
	"minecraft:facing_direction": {"down", "up", "north", "south", "west", "east"},
	"minecraft:vertical_half":    {"bottom", "top"},
	"pillar_axis":                {"y", "x", "z"},
	"attachment":                 {"standing", "hanging", "side", "multiple"},
	"lever_direction":            {"down_east_west", "east", "west", "south", "north", "up_north_south", "up_east_west", "down_north_south"},
}

// Attachments of bells and grindstones.
const (
	attachStanding = iota
	attachHanging
	attachSide
	attachMultiple
)

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

// lookFacing is the six-way facing of blocks that face the player: up or
// down when the player stands within one block of it horizontally and the
// block is above or below the player's box, else side. aboveIsUp says whether
// a block above the player faces up (the observer) or down (the rest).
func (s *placeState) lookFacing(side cube.Face, aboveIsUp bool) cube.Face {
	pos, at := s.pl.Player.entity.Position(), s.pl.Pos
	near := abs(at[0]-int(math.Floor(float64(pos[0])))) <= 1 && abs(at[2]-int(math.Floor(float64(pos[2])))) <= 1
	y := float32(at[1])
	inBox := s.feet <= y && y <= s.feet+playerHeight
	if !near || inBox {
		return side
	}
	below := s.feet > y
	if below != aboveIsUp {
		return cube.FaceUp
	}
	return cube.FaceDown
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
	f := cube.Face(s.pl.Face)
	if f == cube.FaceDown || f != cube.FaceUp && s.pl.ClickPos[1] > 0.5 {
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
		s.set(direction.CardinalState, int(direction.FromSteps((s.yaw-t.Offset)/90+0.5)))
	case "facing":
		s.set("minecraft:facing_direction", int(s.lookFacing(direction.FromSteps((s.yaw-t.Offset)/90+0.5).Face(), true)))
	case "vertical_half":
		s.set("minecraft:vertical_half", s.top())
	case "block_face":
		s.set("minecraft:block_face", int(s.pl.Face))
	}
}

// rule applies the block's own getPlacementBlock.
func (s *placeState) rule(rule string) {
	face := cube.Face(s.pl.Face)
	look := direction.FromYaw(s.yaw)
	side, onSide := direction.Of(face)
	switch rule {
	case "stairs":
		s.set("weirdo_direction", look.EastFirst())
		s.set("upside_down_bit", s.top())
	case "pillar":
		s.set("pillar_axis", int(direction.AxisOf(face)))
	case "trapdoor":
		s.set("direction", look.Opposite().EastFirst())
		s.set("upside_down_bit", s.top())
	case "glazed_terracotta":
		s.set("facing_direction", int(look.Opposite().Face()))
	case "skull":
		s.set("facing_direction", int(direction.FromSteps(s.yaw*4/360+2.5).Opposite().Face()))
	case "face_facing":
		s.set("facing_direction", int(face))
	case "leaves":
		s.set("persistent_bit", 1)
	case "look_facing":
		s.set("facing_direction", int(s.lookFacing(look.Opposite().Face(), false)))
	case "piston":
		s.set("facing_direction", int(s.lookFacing(direction.FromSteps((s.yaw-180)*4/360+0.5).Opposite().Face(), false)))
	case "yaw_direction":
		s.set("direction", int(look))
	case "yaw_direction_offset":
		s.set("direction", int(look.Opposite()))
	case "vault":
		s.set(direction.CardinalState, int(look.Opposite()))
	case "tripwire_hook":
		if onSide {
			s.set("direction", int(side))
		}
	case "cocoa":
		// BDS treats the top and bottom as north.
		if !onSide {
			side = direction.North
		}
		s.set("direction", side.NorthFirst())
	case "hopper":
		if face != cube.FaceDown {
			s.set("facing_direction", int(face.Opposite()))
		}
	case "vine":
		if onSide {
			s.set("vine_direction_bits", 1<<side.NorthFirst())
		}
	case "lever":
		s.lever(face, look)
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
		s.attached(face, look, rule == "bell")
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
func (s *placeState) lever(face cube.Face, look direction.Cardinal) {
	side, onSide := direction.Of(face)
	odd := !look.FacesZ()
	switch {
	case face == cube.FaceDown && odd:
		s.set("lever_direction", leverDownNorthSouth)
	case face == cube.FaceDown:
		s.set("lever_direction", leverDownEastWest)
	case face == cube.FaceUp && odd:
		s.set("lever_direction", leverUpEastWest)
	case face == cube.FaceUp:
		s.set("lever_direction", leverUpNorthSouth)
	case onSide:
		// The sides come after down_east_west in the east-first numbering.
		s.set("lever_direction", side.EastFirst()+1)
	}
}

// endRod is BDS's end rod: facing the clicked face, flipped when it is
// put on the end of an end rod facing the same way.
func (s *placeState) endRod(face cube.Face) {
	behind, ok := s.r.Block(s.pl.Pos.Side(face.Opposite()), 0)
	blocks := s.pl.Player.s.reg.Blocks
	if ok && blocks.Name(behind) == s.name && s.get(blocks.States(behind), "facing_direction") == int(face) {
		face = face.Opposite()
	}
	s.set("facing_direction", int(face))
}

// holds reports whether side of the block at pos holds what is attached to it.
func (s *placeState) holds(pos cube.Pos, side cube.Face) bool {
	b, ok := s.r.Block(pos, 0)
	return ok && s.pl.Player.s.attach.Supports(b, side)
}

// attached is BDS's bell and grindstone: standing on a floor, hanging
// from a ceiling (both turned by the player's yaw) or on a side; a bell
// between two blocks is "multiple".
func (s *placeState) attached(face cube.Face, look direction.Cardinal, bell bool) {
	switch face {
	case cube.FaceDown:
		s.set("attachment", attachHanging)
		s.set("direction", int(look.Opposite()))
	case cube.FaceUp:
		s.set("attachment", attachStanding)
		s.set("direction", int(look.Opposite()))
	default:
		a := attachSide
		if bell && s.holds(s.pl.Pos.Side(face), face.Opposite()) {
			a = attachMultiple
		}
		s.set("attachment", a)
		side, _ := direction.Of(face)
		s.set("direction", int(side))
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
