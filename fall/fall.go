// Package fall is gravity for blocks, as BDS 1.26.60 does it: sand, gravel, concrete
// powder, anvils, the dragon egg and snow layers turn into a falling block when what is
// under them lets them fall, and the falling block settles where it lands.
//
// This package decides when a block falls and how it settles; the falling block itself is
// an entity, made by the StartFunc the Ticker is given (the engine's player package).
//
// Read from BDS 1.26.60 (Linux, IDA): FallingBlock's onPlace listener sub_CAF7900,
// neighborChanged sub_CAF8870, tick listener sub_CAF7B40 -> sub_CAF8640, isFreeToFall
// sub_CAF84C0, startFalling sub_CAF7ED0, tickBlocksAround2D sub_CAF7B90, tryToPlace
// sub_CB7BD70; TopSnowBlock's tick listener sub_D4E9720, neighborChanged sub_D4EAC10,
// isFreeToFall sub_D4EB730, its support check (vtable slot 68) sub_D4EA3C0, startFalling
// sub_D4EB7F0, shouldStopFalling sub_D4EB630, tryToPlace sub_D4EA610.
package fall

import (
	"strings"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Kind is how a block state falls.
type Kind uint8

// The kinds of falling.
const (
	// None does not fall.
	None Kind = iota
	// Block is BDS's FallingBlock: it falls when what is below it is free to fall into,
	// lowest block of a column first.
	Block
	// Snow is BDS's TopSnowBlock: it only looks again when the block right under it
	// changes, falls through leaves too, and lands by adding its layers to snow below.
	Snow
	// Scaffold is BDS's ScaffoldingBlock: it falls or breaks once nothing holds it up
	// (scaffolding.go).
	Scaffold
)

// BDS's tick delays: onPlace and a neighbour other than the one below wait 2 ticks, the
// block below changing 1.
const (
	delayPlaced = 2
	delayBelow  = 1
	delayAround = 2
)

// Materials the rules read (BDS MaterialType, bds/block_materials.json).
const (
	materialAir           = 0
	materialWater         = 5
	materialLava          = 6
	materialLeaves        = 7
	materialPlant         = 8
	materialIce           = 13
	materialBubble        = 18
	materialBarrier       = 19
	materialStructureVoid = 22
)

// Blocks the rules name.
const (
	nameSnowLayer   = "minecraft:snow_layer"
	nameFire        = "minecraft:fire"
	nameSoulFire    = "minecraft:soul_fire"
	nameMovingBlock = "minecraft:moving_block"
	stateHeight     = "height"
	stateCovered    = "covered_bit"
	// snowFull is the height state of a snow layer that fills its block; snowLayers how
	// many layers that is.
	snowFull   = 7
	snowLayers = snowFull + 1
)

// fallingNames are the blocks BDS registers as FallingBlock subclasses that fall the plain
// way (the 1.26.60 block registration, sub_5BC7DE0): sand and red sand (SandBlock), gravel,
// the dragon egg, the anvils and every concrete powder (whose hardening in water is its
// own). Scaffolding, pointed dripstone and suspicious sand and gravel are FallingBlocks
// too, but with their own tick: they are not here.
var fallingNames = map[string]bool{
	"minecraft:sand": true, "minecraft:red_sand": true, "minecraft:gravel": true,
	"minecraft:dragon_egg": true, "minecraft:anvil": true, "minecraft:chipped_anvil": true,
	"minecraft:damaged_anvil": true, "minecraft:deprecated_anvil": true,
}

// corals are the coral plants and fans a falling block falls into (sub_CAF84C0's five
// block lists): live and dead, plants, fans and wall fans.
var corals = [...]string{"tube", "brain", "bubble", "fire", "horn"}

// stateInfo is what the rules read of a block state, kept per state so the checks that run
// on every neighbour change do not look up names.
type stateInfo struct {
	kind Kind
	// free is FallingBlock's isFreeToFall for this block below it.
	free bool
	// leaves is set for leaves material, which snow also falls through.
	leaves bool
	// replaceable is BDS's canBeBuiltOver: a landing block takes its place.
	replaceable bool
	// snowHeight is a snow layer's height + 1, 0 for any other block.
	snowHeight int8
	covered    bool
	// snowlogs is a block that snow covers by moving it to the second layer.
	snowlogs bool
	// noSnow is ice and barrier material, which never hold snow.
	noSnow   bool
	material uint8
	// stability and checked are a scaffolding state's stability and stability_check.
	stability int8
	checked   bool
}

// SupportFunc reports whether block holds something on face, as BDS's
// canProvideSupport (attach.Table.Supports).
type SupportFunc func(block uint32, face cube.Face) bool

// Table holds the falling rules. It does not change after New, so any region may read it.
type Table struct {
	blocks  *registry.Blocks
	air     uint32
	info    []stateInfo
	support SupportFunc
	// snow is the snow layer by height, and covered the same with covered_bit set.
	snow, covered [snowLayers]uint32
	// scaffold is the scaffolding state by stability and stability_check.
	scaffold [scaffoldLoose + 1][2]uint32
}

// New reads the rules for every block state. support tells which blocks hold a snow layer
// on top.
func New(blocks *registry.Blocks, support SupportFunc) (*Table, error) {
	bp, err := data.BreakPlaceData()
	if err != nil {
		return nil, err
	}
	replaceable := map[string]bool{}
	for _, n := range bp.Replaceable {
		replaceable[n] = true
	}
	free := map[string]bool{nameFire: true, nameSoulFire: true, nameSnowLayer: true}
	for _, c := range corals {
		for _, dead := range [...]string{"", "dead_"} {
			for _, part := range [...]string{"_coral", "_coral_fan", "_coral_wall_fan"} {
				free["minecraft:"+dead+c+part] = true
			}
		}
	}
	t := &Table{blocks: blocks, air: blocks.Air(), info: make([]stateInfo, blocks.Count()), support: support}
	for id := range t.info {
		b := uint32(id) //nolint:gosec // state counts fit uint32
		name, props := blocks.Name(b), blocks.Properties(b)
		m := props.Material
		in := stateInfo{
			material: m, leaves: m == materialLeaves, noSnow: m == materialIce || m == materialBarrier,
			replaceable: b == t.air || replaceable[name],
			snowlogs:    props.Precipitation == data.PrecipitationSnowlogging,
			free: free[name] || m == materialAir || m == materialPlant || m == materialStructureVoid ||
				m == materialWater || m == materialLava || m == materialBubble,
		}
		switch {
		case fallingNames[name] || strings.HasSuffix(name, "_concrete_powder"):
			in.kind = Block
		case name == nameSnowLayer:
			in.kind = Snow
			states := blocks.States(b)
			h, _ := states[stateHeight].(int32)
			c, _ := states[stateCovered].(uint8)
			in.snowHeight, in.covered = int8(min(max(h, 0), snowFull))+1, c != 0 //nolint:gosec // clamped
		case name == nameScaffolding:
			in.kind = Scaffold
			in.stability, in.checked = scaffoldStates(blocks.States(b))
		}
		t.info[id] = in
	}
	t.findScaffolds()
	for h := range snowLayers {
		for c := range uint8(2) {
			id, ok := blocks.StateID(nameSnowLayer, map[string]any{stateHeight: int32(h), stateCovered: c}) //nolint:gosec // h is at most 7
			if !ok {
				continue
			}
			if c == 0 {
				t.snow[h] = id
			} else {
				t.covered[h] = id
			}
		}
	}
	return t, nil
}

func (t *Table) state(b uint32) *stateInfo {
	if int(b) < len(t.info) {
		return &t.info[b]
	}
	return &stateInfo{}
}

// Kind returns how block falls.
func (t *Table) Kind(block uint32) Kind { return t.state(block).kind }

// Layers returns a snow layer's number of layers (1 to 8), 0 for any other block.
func (t *Table) Layers(block uint32) int { return int(t.state(block).snowHeight) }

// FreeBelow reports whether a block of kind k at pos can fall: BDS's isFreeToFall, which
// looks at the block under it. Air, fire, liquids, plants, snow layers, corals and
// structure voids let a block fall into them; snow falls through leaves as well.
func (t *Table) FreeBelow(r *world.Region, pos cube.Pos, k Kind) bool {
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok {
		return false
	}
	s := t.state(below)
	return s.free || (k == Snow && s.leaves)
}

// SnowSurvives is TopSnowBlock's support check (vtable slot 68): the block at pos must be air,
// snow or a plant snow covers, and the one below a full snow layer, leaves, or a block
// that holds things on top that is not ice or a barrier.
func (t *Table) SnowSurvives(r *world.Region, pos cube.Pos) bool {
	at, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	if a := t.state(at); at != t.air && a.kind != Snow && !a.snowlogs {
		return false
	}
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok {
		return false
	}
	s := t.state(below)
	switch {
	case s.snowHeight == snowLayers, s.leaves:
		return true
	case s.noSnow:
		return false
	}
	return t.support != nil && t.support(below, cube.FaceUp)
}

// StopsFalling reports whether a falling block of block at pos lands this tick, given
// whether it stands on something: BDS's shouldStopFalling. On ground it stops unless a
// moving block (a piston pushing) is in its box stretched a block down and up; a falling
// snow layer also stops over water.
func (t *Table) StopsFalling(r *world.Region, pos cube.Pos, block uint32, onGround bool) bool {
	if t.state(block).kind == Snow {
		if below, ok := r.Block(pos.Side(cube.FaceDown), 0); ok && t.state(below).material == materialWater {
			return true
		}
	}
	if !onGround {
		return false
	}
	for y := pos[1] - 1; y <= pos[1]+1; y++ {
		b, ok := r.Block(cube.Pos{pos[0], y, pos[2]}, 0)
		if ok && t.blocks.Name(b) == nameMovingBlock {
			return false
		}
	}
	return true
}

// Land settles a falling block at pos, as BDS's tryToPlace, and reports whether it did;
// if not, the falling block breaks into its item. A plain falling block needs a block it
// replaces there. A snow layer needs the snow support and adds its layers to snow already
// there, up to a full block; layers left over go on top if snow can lie there.
func (t *Table) Land(r *world.Region, pos cube.Pos, block uint32) bool {
	s := t.state(block)
	if s.kind == Snow {
		return t.landSnow(r, pos, int(s.snowHeight))
	}
	at, ok := r.Block(pos, 0)
	if !ok || !t.state(at).replaceable {
		return false
	}
	r.SetBlock(pos, 0, block)
	r.UpdateNeighbours(pos)
	return true
}

func (t *Table) landSnow(r *world.Region, pos cube.Pos, layers int) bool {
	if layers <= 0 || !t.SnowSurvives(r, pos) {
		return false
	}
	at, _ := r.Block(pos, 0)
	h := int(t.state(at).snowHeight)
	if h == 0 {
		t.addSnow(r, pos, layers)
		return true
	}
	room := snowLayers - h
	t.addSnow(r, pos, min(room, layers))
	if left := layers - room; left >= 1 && left < snowLayers {
		up := pos.Side(cube.FaceUp)
		if t.SnowSurvives(r, up) {
			t.addSnow(r, up, left)
		}
	}
	return true
}

// addSnow is BDS's snow builder (sub_D4E8C60) with n layers: n more on a snow layer, a new
// one on air, a covered one over a plant snow covers, which moves to the second layer.
func (t *Table) addSnow(r *world.Region, pos cube.Pos, n int) {
	b, ok := r.Block(pos, 0)
	if !ok || n <= 0 {
		return
	}
	s := t.state(b)
	next := t.snow[min(n, snowLayers)-1]
	switch {
	case s.snowHeight > 0:
		h := min(int(s.snowHeight)+n, snowLayers) - 1
		next = t.snow[h]
		if s.covered {
			next = t.covered[h]
		}
	case b != t.air && s.snowlogs:
		next = t.covered[min(n, snowLayers)-1]
		r.SetBlock(pos, 1, b)
	}
	r.SetBlock(pos, 0, next)
	r.UpdateNeighbours(pos)
}

// Lift empties pos of a block that starts to fall, as startFalling's setBlock to air. The
// world moves a second layer down when layer 0 turns to air, so a covered snow layer
// leaves the plant it covered.
func (t *Table) Lift(r *world.Region, pos cube.Pos) {
	r.SetBlock(pos, 0, t.air)
	r.UpdateNeighbours(pos)
}
