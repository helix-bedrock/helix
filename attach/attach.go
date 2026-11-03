// Package attach is what torches, lanterns and ladders need from the blocks
// around them, as BDS 1.26.60 does it: which faces of a block hold something,
// where such a block may be placed, how it turns to what holds it, and when it
// falls off.
package attach

import (
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// The block kinds this package places, from BDS's placement rules.
const (
	kindTorch   = "torch"
	kindLantern = "lantern"
	kindLadder  = "ladder"
)

// Blocks these rules name.
const (
	mobSpawner   = "minecraft:mob_spawner"
	hopper       = "minecraft:hopper"
	underwater   = "minecraft:underwater_torch"
	water        = "minecraft:water"
	flowingWater = "minecraft:flowing_water"
	lava         = "minecraft:lava"
	flowingLava  = "minecraft:flowing_lava"
)

// snowFull is the height state of a full block of snow layers.
const snowFull = 7

// torchSides is the order a torch tries the blocks around it: the side of each
// neighbour that faces it.
var torchSides = [...]cube.Face{cube.FaceNorth, cube.FaceEast, cube.FaceSouth, cube.FaceWest}

// ladderSides is the order a ladder tries the blocks around it.
var ladderSides = [...]cube.Face{cube.FaceNorth, cube.FaceSouth, cube.FaceWest, cube.FaceEast}

// trapdoorSides is the face an open trapdoor holds things on, by its direction.
var trapdoorSides = [4]cube.Face{cube.FaceWest, cube.FaceEast, cube.FaceNorth, cube.FaceSouth}

// shelfSide is the face a shelf holds things on, by its direction.
func shelfSide(direction string) (cube.Face, bool) {
	switch direction {
	case "south":
		return cube.FaceNorth, true
	case "west":
		return cube.FaceEast, true
	case "north":
		return cube.FaceSouth, true
	case "east":
		return cube.FaceWest, true
	}
	return 0, false
}

// Table holds the rules. It does not change after New, so any region may read
// it.
type Table struct {
	blocks *registry.Blocks
	kinds  map[string]string
	rules  map[string]string
	hang   map[string]bool
	solid  map[string]bool
}

// New loads BDS's rules.
func New(blocks *registry.Blocks) (*Table, error) {
	support, err := data.SupportData()
	if err != nil {
		return nil, err
	}
	placement, err := data.PlacementData()
	if err != nil {
		return nil, err
	}
	t := &Table{blocks: blocks, kinds: map[string]string{}, rules: support.Rules, hang: nameSet(support.HangFrom), solid: nameSet(support.Solid)}
	for name, p := range placement {
		if p.Rule == kindTorch || p.Rule == kindLantern || p.Rule == kindLadder {
			t.kinds[name] = p.Rule
		}
	}
	return t, nil
}

func nameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// Supports reports whether face of block holds what is attached to it.
func (t *Table) Supports(block uint32, face cube.Face) bool {
	states := t.blocks.States(block)
	switch t.rules[t.blocks.Name(block)] {
	case "all":
		return true
	case "top":
		return face == cube.FaceUp
	case "bottom":
		return face == cube.FaceDown
	case "ends":
		return isEnd(face)
	case "not_top":
		return face != cube.FaceUp
	case "stairs":
		return stairsHold(states, face)
	case "slab":
		top := stateString(states, "minecraft:vertical_half") == "top"
		return top && face == cube.FaceUp || !top && face == cube.FaceDown
	case "trapdoor":
		return trapdoorHolds(states, face)
	case "shelf":
		side, ok := shelfSide(stateString(states, "minecraft:cardinal_direction"))
		return ok && side == face
	case "chain":
		return stateString(states, "pillar_axis") == "y" && isEnd(face)
	case "rod":
		f := stateInt(states, "facing_direction")
		return (f == int(cube.FaceDown) || f == int(cube.FaceUp)) && isEnd(face)
	case "skull":
		return face == cube.FaceDown && stateInt(states, "facing_direction") == int(cube.FaceUp)
	case "piston_arm":
		f := stateInt(states, "facing_direction")
		if f >= int(cube.FaceNorth) {
			f = int(cube.Face(f).Opposite())
		}
		return f == int(face)
	case "snow":
		return stateInt(states, "height") == snowFull
	case "grindstone":
		switch face {
		case cube.FaceUp:
			return stateString(states, "attachment") == "standing"
		case cube.FaceDown:
			return stateString(states, "attachment") == "hanging"
		}
	}
	return false
}

func isEnd(face cube.Face) bool { return face == cube.FaceDown || face == cube.FaceUp }

// stairsHold is a stair's full faces: its flat side, top or bottom, and its back.
func stairsHold(states map[string]any, face cube.Face) bool {
	upside := stateInt(states, "upside_down_bit") != 0
	switch face {
	case cube.FaceDown:
		return !upside
	case cube.FaceUp:
		return upside
	}
	return int(face) == int(cube.FaceEast)-stateInt(states, "weirdo_direction")
}

// trapdoorHolds is a trapdoor's flat side: its panel shut, or standing open.
func trapdoorHolds(states map[string]any, face cube.Face) bool {
	open := stateInt(states, "open_bit") != 0
	if isEnd(face) {
		return !open && (stateInt(states, "upside_down_bit") != 0) == (face == cube.FaceUp)
	}
	d := stateInt(states, "direction")
	return open && d >= 0 && d < len(trapdoorSides) && trapdoorSides[d] == face
}

func stateString(states map[string]any, key string) string {
	s, _ := states[key].(string)
	return s
}

// stateInt reads a number or bit state, -1 if the block has none.
func stateInt(states map[string]any, key string) int {
	switch v := states[key].(type) {
	case int32:
		return int(v)
	case uint8:
		return int(v)
	}
	return -1
}

// holdsTorch reports whether the block at pos holds a torch on face. A mob
// spawner holds one on any face.
func (t *Table) holdsTorch(r *world.Region, pos cube.Pos, face cube.Face) bool {
	b, ok := r.Block(pos, 0)
	return ok && (t.blocks.Name(b) == mobSpawner || t.Supports(b, face))
}

// isSolid is the check a ladder makes of the block it leans on.
func (t *Table) isSolid(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && t.solid[t.blocks.Name(b)]
}

// canHang reports whether a lantern at pos may hang from the block above it:
// any block whose underside holds things, the thin blocks such as fences and
// chains, and a hopper facing down.
func (t *Table) canHang(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos.Side(cube.FaceUp), 0)
	if !ok {
		return false
	}
	if t.Supports(b, cube.FaceDown) {
		return true
	}
	name := t.blocks.Name(b)
	return t.hang[name] || name == hopper && stateInt(t.blocks.States(b), "facing_direction") == int(cube.FaceDown)
}

// onFloor reports whether the block below pos holds a lantern.
func (t *Table) onFloor(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos.Side(cube.FaceDown), 0)
	return ok && t.Supports(b, cube.FaceUp)
}

// MayPlace is BDS's mayPlace for these blocks: something around pos must hold
// the block, wherever the player clicked. True for every other block.
func (t *Table) MayPlace(r *world.Region, pos cube.Pos, block uint32, clicked cube.Face) bool {
	name := t.blocks.Name(block)
	switch t.kinds[name] {
	case kindTorch:
		return t.torchMayPlace(r, pos, name)
	case kindLantern:
		return t.onFloor(r, pos) || t.canHang(r, pos)
	case kindLadder:
		if isEnd(clicked) {
			return false
		}
		for _, f := range ladderSides {
			if t.isSolid(r, pos.Side(f.Opposite())) {
				return true
			}
		}
		return false
	}
	return true
}

// torchMayPlace refuses a torch in a liquid (an underwater torch only in lava)
// and needs a holder on one of the sides or below. BDS looks at the clicked face
// only later, for which way the torch turns.
func (t *Table) torchMayPlace(r *world.Region, pos cube.Pos, name string) bool {
	if b, ok := r.Block(pos, 0); ok {
		switch t.blocks.Name(b) {
		case lava, flowingLava:
			return false
		case water, flowingWater:
			if name != underwater {
				return false
			}
		}
	}
	for _, f := range torchSides {
		if t.holdsTorch(r, pos.Side(f.Opposite()), f) {
			return true
		}
	}
	return t.holdsTorch(r, pos.Side(cube.FaceDown), cube.FaceUp)
}

// TorchFace is the face of its holder a torch placed at pos sits on: the one
// clicked if that block holds it (never under a block), else the first side that
// does, else the floor.
func (t *Table) TorchFace(r *world.Region, pos cube.Pos, clicked cube.Face) cube.Face {
	if clicked != cube.FaceDown && t.holdsTorch(r, pos.Side(clicked.Opposite()), clicked) {
		return clicked
	}
	for _, f := range torchSides {
		if b, ok := r.Block(pos.Side(f.Opposite()), 0); ok && t.Supports(b, f) {
			return f
		}
	}
	return cube.FaceUp
}

// LanternHangs reports whether a lantern placed at pos hangs: clicked from below
// when it can hang there, or when the floor does not hold it.
func (t *Table) LanternHangs(r *world.Region, pos cube.Pos, clicked cube.Face) bool {
	return clicked == cube.FaceDown && t.canHang(r, pos) || !t.onFloor(r, pos)
}

// LadderFace is the face a ladder placed at pos faces: the one clicked if the
// block behind is solid, else the first side with a solid block behind it.
func (t *Table) LadderFace(r *world.Region, pos cube.Pos, clicked cube.Face) (cube.Face, bool) {
	if t.isSolid(r, pos.Side(clicked.Opposite())) {
		return clicked, true
	}
	for _, f := range ladderSides {
		if t.isSolid(r, pos.Side(f.Opposite())) {
			return f, true
		}
	}
	return 0, false
}

// TorchState is the torch_facing_direction of a torch on face of its holder. The
// state names the way the torch leans, away from the face it sits on.
func TorchState(face cube.Face) string {
	switch face {
	case cube.FaceNorth:
		return "south"
	case cube.FaceSouth:
		return "north"
	case cube.FaceWest:
		return "east"
	case cube.FaceEast:
		return "west"
	}
	return "top"
}

// torchFaceOf is TorchState turned around; an unknown direction counts as the
// floor, as in BDS.
func torchFaceOf(state string) cube.Face {
	switch state {
	case "south":
		return cube.FaceNorth
	case "north":
		return cube.FaceSouth
	case "east":
		return cube.FaceWest
	case "west":
		return cube.FaceEast
	}
	return cube.FaceUp
}

// survives reports whether the block at pos still has its holder after the block
// at from changed. A holder in a chunk that is not loaded counts as there.
func (t *Table) survives(r *world.Region, pos, from cube.Pos, block uint32) bool {
	states := t.blocks.States(block)
	switch t.kinds[t.blocks.Name(block)] {
	case kindTorch:
		face := torchFaceOf(stateString(states, "torch_facing_direction"))
		holder := pos.Side(face.Opposite())
		if from != holder || !loaded(r, holder) {
			return true
		}
		return t.holdsTorch(r, holder, face)
	case kindLantern:
		if stateInt(states, "hanging") > 0 {
			return !loaded(r, pos.Side(cube.FaceUp)) || t.canHang(r, pos)
		}
		return !loaded(r, pos.Side(cube.FaceDown)) || t.onFloor(r, pos)
	case kindLadder:
		f := stateInt(states, "facing_direction")
		if f < int(cube.FaceNorth) || f > int(cube.FaceEast) {
			f = int(cube.FaceUp)
		}
		holder := pos.Side(cube.Face(f).Opposite())
		return !loaded(r, holder) || t.isSolid(r, holder)
	}
	return true
}

func loaded(r *world.Region, pos cube.Pos) bool {
	_, ok := r.Block(pos, 0)
	return ok
}

// FallFunc removes a block that lost its holder and throws out its drops.
type FallFunc func(r *world.Region, pos cube.Pos, block uint32)

// Ticker adds falling off to another BlockTicker.
type Ticker struct {
	world.BlockTicker
	table *Table
	fall  FallFunc
}

// NewTicker returns a Ticker that calls fall for a block that lost its holder and
// hands everything else to next.
func NewTicker(table *Table, fall FallFunc, next world.BlockTicker) *Ticker {
	return &Ticker{BlockTicker: next, table: table, fall: fall}
}

// NeighbourChanged breaks a block whose holder is gone.
func (t *Ticker) NeighbourChanged(r *world.Region, pos, from cube.Pos, block uint32, layer uint8) {
	if layer == 0 && !t.table.survives(r, pos, from, block) {
		t.fall(r, pos, block)
		return
	}
	t.BlockTicker.NeighbourChanged(r, pos, from, block, layer)
}
