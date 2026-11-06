// Package attach is what torches, lanterns and ladders need from the blocks
// around them, as BDS 1.26.60 does it: which faces of a block hold something,
// where such a block may be placed, how it turns to what holds it, and when it
// falls off.
package attach

import (
	"strings"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/direction"
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
	glowstone    = "minecraft:glowstone"
	lamp         = "minecraft:redstone_lamp"
	litLamp      = "minecraft:lit_redstone_lamp"
	wire         = "minecraft:redstone_wire"
	repeaterOff  = "minecraft:unpowered_repeater"
	repeaterOn   = "minecraft:powered_repeater"
	comparator   = "minecraft:unpowered_comparator"
	comparatorOn = "minecraft:powered_comparator"
	flowingLava  = "minecraft:flowing_lava"
	// Banners stand on a solid block or hang on the side of one.
	standingBanner = "minecraft:standing_banner"
	wallBanner     = "minecraft:wall_banner"
)

// snowFull is the height state of a full block of snow layers.
const snowFull = 7

// shelfSide is the face a shelf holds things on: its back, away from its direction.
func shelfSide(state string) (cube.Face, bool) {
	c, ok := direction.Parse(state)
	return c.Opposite().Face(), ok
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
		return direction.Vertical(face)
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
		side, ok := shelfSide(stateString(states, direction.CardinalState))
		return ok && side == face
	case "chain":
		return stateString(states, "pillar_axis") == "y" && direction.Vertical(face)
	case "rod":
		f := stateInt(states, "facing_direction")
		return (f == int(cube.FaceDown) || f == int(cube.FaceUp)) && direction.Vertical(face)
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

// stairsHold is a stair's full faces: its flat side, top or bottom, and its back.
func stairsHold(states map[string]any, face cube.Face) bool {
	upside := stateInt(states, "upside_down_bit") != 0
	switch face {
	case cube.FaceDown:
		return !upside
	case cube.FaceUp:
		return upside
	}
	c, ok := direction.FromEastFirst(stateInt(states, "weirdo_direction"))
	return ok && c.Face() == face
}

// trapdoorHolds is a trapdoor's flat side: its panel shut, or standing open.
func trapdoorHolds(states map[string]any, face cube.Face) bool {
	open := stateInt(states, "open_bit") != 0
	if direction.Vertical(face) {
		return !open && (stateInt(states, "upside_down_bit") != 0) == (face == cube.FaceUp)
	}
	c, ok := direction.FromEastFirst(stateInt(states, "direction"))
	return open && ok && c.Opposite().Face() == face
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

// CanHang reports whether something can hang from the block above pos, as a lantern or a
// bell does.
func (t *Table) CanHang(r *world.Region, pos cube.Pos) bool { return t.canHang(r, pos) }

// onFloor reports whether the block below pos holds a lantern.
func (t *Table) onFloor(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos.Side(cube.FaceDown), 0)
	return ok && t.Supports(b, cube.FaceUp)
}

// tripwireHook hangs on the side of a block.
const tripwireHook = "minecraft:tripwire_hook"

// MayPlace is BDS's mayPlace for these blocks: something around pos must hold
// the block, wherever the player clicked. True for every other block.
func (t *Table) MayPlace(r *world.Region, pos cube.Pos, block uint32, clicked cube.Face) bool {
	name := t.blocks.Name(block)
	switch name {
	case wire:
		return t.WireSurvives(r, pos)
	case repeaterOff, repeaterOn, comparator, comparatorOn:
		return t.DiodeSurvives(r, pos)
	}
	if strings.HasSuffix(name, "_pressure_plate") {
		return t.DiodeSurvives(r, pos)
	}
	if name == "minecraft:rail" || name == "minecraft:golden_rail" || name == "minecraft:detector_rail" || name == "minecraft:activator_rail" {
		return t.onFloor(r, pos)
	}
	if strings.HasSuffix(name, "_button") {
		b, ok := r.Block(pos.Side(clicked.Opposite()), 0)
		return ok && t.Supports(b, clicked)
	}
	if name == standingBanner || name == wallBanner || isSign(name) {
		return t.bannerHeld(r, pos, block)
	}
	if isHangingSign(name) {
		return t.hangingSignHeld(r, pos, block)
	}
	if name == tripwireHook {
		b, ok := r.Block(pos.Side(clicked.Opposite()), 0)
		return ok && !direction.Vertical(clicked) && t.Supports(b, clicked)
	}
	switch t.kinds[name] {
	case kindTorch:
		return t.torchMayPlace(r, pos, name)
	case kindLantern:
		return t.onFloor(r, pos) || t.canHang(r, pos)
	case kindLadder:
		if direction.Vertical(clicked) {
			return false
		}
		for _, f := range direction.Sides() {
			if t.isSolid(r, pos.Side(f.Opposite())) {
				return true
			}
		}
		return false
	}
	return true
}

// WireSurvives is BDS's canSurvive for redstone wire: no water in the block, and
// below it a top face that holds things, glowstone or a lamp.
func (t *Table) WireSurvives(r *world.Region, pos cube.Pos) bool {
	if b, ok := r.Block(pos, 1); ok {
		if n := t.blocks.Name(b); n == water || n == flowingWater {
			return false
		}
	}
	b, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok {
		return false
	}
	if t.Supports(b, cube.FaceUp) {
		return true
	}
	n := t.blocks.Name(b)
	return n == glowstone || n == lamp || n == litLamp
}

// ButtonHeld is BDS's canSurvive for buttons: the block behind holds things on the face the
// button is on. What is not loaded counts as holding.
func (t *Table) ButtonHeld(r *world.Region, pos cube.Pos, block uint32) bool {
	face := cube.Face(stateInt(t.blocks.States(block), "facing_direction")) //nolint:gosec // state values are small
	b, ok := r.Block(pos.Side(face.Opposite()), 0)
	return !ok || t.Supports(b, face)
}

// DiodeSurvives is BDS's canSurvive for repeaters and comparators: below them a
// top face that holds things.
func (t *Table) DiodeSurvives(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos.Side(cube.FaceDown), 0)
	return ok && t.Supports(b, cube.FaceUp)
}

// Solid is the check for a solid block, as a ladder makes of what it leans on and
// a comparator of what it reads through.
func (t *Table) Solid(block uint32) bool { return t.solid[t.blocks.Name(block)] }

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
	for _, f := range direction.Clockwise() {
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
	for _, f := range direction.Clockwise() {
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
	for _, f := range direction.Sides() {
		if t.isSolid(r, pos.Side(f.Opposite())) {
			return f, true
		}
	}
	return 0, false
}

// TorchState is the torch_facing_direction of a torch on face of its holder. The
// state names the way the torch leans, away from the face it sits on.
func TorchState(face cube.Face) string {
	if c, ok := direction.Of(face); ok {
		return c.Opposite().String()
	}
	return "top"
}

// torchFaceOf is TorchState turned around; an unknown direction counts as the
// floor, as in BDS.
func torchFaceOf(state string) cube.Face {
	if c, ok := direction.Parse(state); ok {
		return c.Opposite().Face()
	}
	return cube.FaceUp
}

// bannerHolder is the block a banner needs solid, as BannerBlock's canSurvive: below a
// standing banner, behind a wall banner (the side away from its facing).
func (t *Table) bannerHolder(pos cube.Pos, block uint32) (cube.Pos, bool) {
	if n := t.blocks.Name(block); n == standingBanner || strings.HasSuffix(n, "standing_sign") {
		return pos.Side(cube.FaceDown), true
	}
	f := stateInt(t.blocks.States(block), "facing_direction")
	if f < int(cube.FaceNorth) || f > int(cube.FaceEast) {
		return cube.Pos{}, false
	}
	return pos.Side(cube.Face(f).Opposite()), true
}

// bannerHeld reports whether a banner at pos has its holder.
func (t *Table) bannerHeld(r *world.Region, pos cube.Pos, block uint32) bool {
	holder, ok := t.bannerHolder(pos, block)
	return ok && t.solidMaterial(r, holder)
}

// solidMaterial is BDS's Material isSolid of the block at pos, which banners stand on.
func (t *Table) solidMaterial(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && t.blocks.Properties(b).SolidMaterial
}

// survives reports whether the block at pos still has its holder after the block
// at from changed. A holder in a chunk that is not loaded counts as there.
func (t *Table) survives(r *world.Region, pos, from cube.Pos, block uint32) bool {
	states := t.blocks.States(block)
	if n := t.blocks.Name(block); n == standingBanner || n == wallBanner || isSign(n) {
		holder, ok := t.bannerHolder(pos, block)
		return from != holder || !loaded(r, holder) || (ok && t.solidMaterial(r, holder))
	}
	if isHangingSign(t.blocks.Name(block)) {
		above := pos.Side(cube.FaceUp)
		return from != above || !loaded(r, above) || t.hangingSignHeld(r, pos, block)
	}
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

// isSign reports whether name is a standing or wall sign, which SignBlock's canSurvive holds
// up the way a banner is: by solid material below or behind.
func isSign(name string) bool {
	return strings.HasSuffix(name, "standing_sign") || strings.HasSuffix(name, "wall_sign")
}

// isHangingSign reports whether name is a hanging sign.
func isHangingSign(name string) bool { return strings.HasSuffix(name, "_hanging_sign") }

// hangingSignHeld is HangingSignBlock's canSurvive: one hanging from a side always stays,
// one under a block needs something above to hang from. BDS asks the block above for its
// support type; Helix reads that as a lantern's rule (written from BDS 1.26.60, medium).
func (t *Table) hangingSignHeld(r *world.Region, pos cube.Pos, block uint32) bool {
	if stateInt(t.blocks.States(block), "hanging") == 0 {
		return true
	}
	return t.canHang(r, pos)
}
