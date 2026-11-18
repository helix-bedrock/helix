package mob

import (
	"slices"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/physics"
)

// The mob's body components, around its physics tick, as BDS 1.26.60's movement systems:
// the physics-style components copied into the body (entity's Set.ApplyBody), buoyant
// floating (Set.BeforeMove), block_climber's flags and the block slowdown of cobwebs,
// sweet berry bushes and powder snow.

// The blocks that slow a mob inside them.
const (
	nameWeb        = "minecraft:web"
	nameBerryBush  = "minecraft:sweet_berry_bush"
	namePowderSnow = "minecraft:powder_snow"
	nameScaffold   = "minecraft:scaffolding"
	nameWither     = "minecraft:wither"
	nameMagmaCube  = "minecraft:magma_cube"
)

// effectWeaving is BDS's Weaving effect: a weaving mob is slowed less by cobwebs.
const effectWeaving = 33

// insideShrink is how far BDS's EntityInside system shrinks the box before it looks for
// the blocks the mob is in.
const insideShrink = 0.001

// The block slowdown multipliers, per axis, of BDS 1.26.60's entity-inside systems: cobweb,
// sweet berry bush and powder snow, and the cobweb's
// for a mob with Weaving.
var (
	slowWeb        = physics.Vec3{X: 0.25, Y: 0.05, Z: 0.25}
	slowBerryBush  = physics.Vec3{X: 0.8, Y: 0.75, Z: 0.8}
	slowPowderSnow = physics.Vec3{X: 0.9, Y: 1.5, Z: 0.9}
	slowWeaving    = physics.Vec3{X: 0.5, Y: 0.25, Z: 0.5}
)

// applyBody copies the physics-style components into the body before its tick. The size
// stays Helix's own (scaled, and a goal's box), and the auto step falls back to the default
// when the mob has no variable_max_auto_step.
func (m *Mob) applyBody() {
	b := &m.body
	b.LavaSlime = m.typ == nameMagmaCube
	if m.states == nil {
		return
	}
	w, h := b.Width, b.Height
	b.MaxAutoStep = defaultMaxAutoStep
	b.PowderSnowWalker = false
	_, jumpPrevented := b.LowJumpBlock(physWorld{&m.v})
	m.states.ApplyBody(b, jumpPrevented, m.wasdControlled())
	b.Width, b.Height = w, h
}

// wasdControlled reports a rider steering the mob with its keys (BDS's
// keyboard-controlled flag), which picks variable_max_auto_step's controlled step.
func (m *Mob) wasdControlled() bool { return m.flags.Has(physics.FlagKeyboardControlled) }

// beforeMove runs buoyant before the mob moves.
func (m *Mob) beforeMove() {
	if m.states == nil || m.stateEnv.Self == nil || m.v.r == nil {
		return
	}
	m.states.BeforeMove(&m.stateEnv, liquids{&m.v})
}

// liquids answers buoyant's liquid heights.
type liquids struct{ v *view }

// LiquidHeight matches BDS: the top of the water or lava in the
// block at p, below p when it holds none.
func (l liquids) LiquidHeight(p behavior.Pos) float32 {
	b := l.v.liquid(p)
	switch l.v.blocks.Path(b).Material {
	case path.MaterialWater, path.MaterialLava:
	default:
		return float32(p.Y) - 1
	}
	depth, _ := l.v.blocks.reg.States(b)["liquid_depth"].(int32)
	if depth >= liquidFalling {
		depth = 0
	}
	return float32(p.Y) + 1 - float32(depth)/liquidLevels
}

// blockClimb is BDS 1.26.60's BDS's code for a mob with
// minecraft:block_climber: the in and over scaffolding flags, and the in-ascending and
// over-descending flags for powder snow it can stand on (scaffolding counts there only for
// players), from the blocks under the box at its feet and one below. A mob with any of them
// set stops counting its fall, unless the block it is over is powder snow.
func (m *Mob) blockClimb() {
	if m.states == nil || !m.states.BlockClimber || m.v.r == nil {
		return
	}
	box := m.body.Box
	feet := floor32(box.Min.Y)
	in := m.layerHas(box, feet, nameScaffold)
	over := m.layerHas(box, feet-1, nameScaffold)
	ascend := m.body.PowderSnowWalker && m.layerHas(box, feet, namePowderSnow)
	descend := m.body.PowderSnowWalker && m.layerHas(box, feet-1, namePowderSnow)
	m.SetFlag(physics.FlagInScaffolding, in)
	m.SetFlag(physics.FlagOverScaffolding, over)
	m.SetFlag(physics.FlagInAscendingBlock, ascend)
	m.SetFlag(flagOverDescendingBlock, descend)
	if (in || over || ascend) && !descend {
		m.body.FallDistance = 0
	}
}

// flagOverDescendingBlock is the actor flag of a mob over a block it can climb down.
const flagOverDescendingBlock = 100

// layerHas reports a block named name in the cells under the box's x and z at height y.
func (m *Mob) layerHas(box path.Box, y int32, name string) bool {
	for x := floor32(box.Min.X); x <= floor32(box.Max.X); x++ {
		for z := floor32(box.Min.Z); z <= floor32(box.Max.Z); z++ {
			if m.rt.Blocks.reg.Name(m.v.block(path.Pos{X: x, Y: y, Z: z}, 0)) == name {
				return true
			}
		}
	}
	return false
}

// blockSlowdown is BDS 1.26.60's block slowdown after the mob moved: each kind of slowing
// block the shrunk box is in sets its multiplier, or lowers the one already set, unless
// minecraft:block_movement_slowdown_immunity lists every block of that kind it is in. A
// mob with Weaving gets a weaker cobweb's, and the wither none
// (BlockMovementSlowdownMultiplierSystemImpl). The next move scales by it and stops the mob
// (physics' Slowdown); a slowed mob stops counting its fall (adjustFallDistance).
func (m *Mob) blockSlowdown() {
	if m.v.r == nil {
		return
	}
	box := m.body.Box
	box.Min = path.Vec3{X: box.Min.X + insideShrink, Y: box.Min.Y + insideShrink, Z: box.Min.Z + insideShrink}
	box.Max = path.Vec3{X: box.Max.X - insideShrink, Y: box.Max.Y - insideShrink, Z: box.Max.Z - insideShrink}
	var slow physics.Vec3
	web := m.slowBy(&slow, box, nameWeb, slowWeb)
	m.slowBy(&slow, box, nameBerryBush, slowBerryBush)
	m.slowBy(&slow, box, namePowderSnow, slowPowderSnow)
	if web && m.HasEffect(effectWeaving) {
		slow = slowWeaving
	}
	if m.typ == nameWither {
		slow = physics.Vec3{}
	}
	m.body.Slowdown = slow
	if slow != (physics.Vec3{}) {
		m.body.FallDistance = 0
	}
}

// slowBy lowers slow to by when the box is in a block named name that the mob is not
// immune to, and reports whether the box is in one.
func (m *Mob) slowBy(slow *physics.Vec3, box path.Box, name string, by physics.Vec3) bool {
	in, immune := false, true
	for x := floor32(box.Min.X); x <= floor32(box.Max.X); x++ {
		for y := floor32(box.Min.Y); y <= floor32(box.Max.Y); y++ {
			for z := floor32(box.Min.Z); z <= floor32(box.Max.Z); z++ {
				if m.rt.Blocks.reg.Name(m.v.block(path.Pos{X: x, Y: y, Z: z}, 0)) != name {
					continue
				}
				in = true
				immune = immune && m.slowdownImmune(name)
			}
		}
	}
	if !in || immune {
		return in
	}
	if *slow == (physics.Vec3{}) {
		*slow = by
	} else {
		*slow = physics.Vec3{X: min(slow.X, by.X), Y: min(slow.Y, by.Y), Z: min(slow.Z, by.Z)}
	}
	return true
}

// slowdownImmune reports that the mob's block_movement_slowdown_immunity lists the block.
func (m *Mob) slowdownImmune(name string) bool {
	si := m.states
	if si == nil || si.SlowdownImmunity == nil {
		return false
	}
	return slices.ContainsFunc(si.SlowdownImmunity.Blocks, func(d string) bool { return ns(d) == name })
}
