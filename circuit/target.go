package circuit

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/redstone"
	"github.com/helix-bedrock/world/cube"
)

const (
	// targetSpan is the distance from the centre of a face to its corner.
	targetSpan = float32(math.Sqrt2 / 2)
	// targetSteps is what a hit at the very centre of a face works out to, before the
	// signal's top of 15 caps it.
	targetSteps = 16
	// targetEdge is how close to the largest of the three distances a distance counts as
	// the same, BDS's float epsilon.
	targetEdge = 1.1920929e-07
)

// targetOffCentre is how far a hit is from the centre of the face it landed on. The face is
// the axis it is furthest along, so that axis is left out. BDS takes the first two others:
// on an edge or corner, where axes tie, that leaves fewer.
func targetOffCentre(pos cube.Pos, hit mgl32.Vec3) float32 {
	d := [3]float32{hit[0] - float32(pos[0]) - 0.5, hit[1] - float32(pos[1]) - 0.5, hit[2] - float32(pos[2]) - 0.5}
	far := max(abs32(d[0]), abs32(d[1]), abs32(d[2]))
	var left [2]float32
	n := 0
	for _, v := range d {
		if abs32(far-abs32(v)) > targetEdge {
			left[n] = v
			n++
		}
	}
	if n == 0 {
		left[0] = d[1]
	}
	return float32(math.Sqrt(float64(left[0]*left[0] + left[1]*left[1])))
}

// targetSignal is the power a hit gives: 15 near the centre of a face, always at least 1.
func targetSignal(pos cube.Pos, hit mgl32.Vec3) int32 {
	power := int32((targetSpan - targetOffCentre(pos, hit)) * targetSteps / targetSpan)
	return min(max(power, 1), fullPower)
}

// HitTarget is a projectile landing at hit on the target block at pos, BDS's
// BDS's code: power by how near the centre, for longer if it was an arrow
// or trident. A hit while it is on starts the wait again. A player's hand does nothing to a
// target in BDS. False if the block is not a target. Runs: owner of the region.
func (t *Ticker) HitTarget(r *world.Region, pos cube.Pos, hit mgl32.Vec3, arrow bool) bool {
	block, ok := r.Block(pos, 0)
	if !ok || t.kind(block) != kindTarget {
		return false
	}
	c, p := t.circuitOf(r), toPos(pos)
	c.sys.Remove(p)
	if comp := c.sys.Create(p, redstone.KindProducer, redstone.NoFace); comp != nil {
		comp.SetStrength(targetSignal(pos, hit))
		comp.SetAllowUp(true)
	}
	conf := &t.settings.Get().Target
	wait := conf.Ticks
	if arrow {
		wait = conf.ArrowTicks
	}
	r.CancelTick(pos, block)
	r.ScheduleTick(pos, block, wait, 0)
	return true
}

// targetTick makes a target an ordinary conducting block again when its wait is over.
func (t *Ticker) targetTick(r *world.Region, pos cube.Pos) {
	c, p := t.circuitOf(r), toPos(pos)
	c.sys.Remove(p)
	targetConducts(c, p)
}

// targetConducts makes the target at p what BDS has while it is not hit: a block that
// carries power on like any solid block, which wire joins from every side.
func targetConducts(c *regionCircuit, p redstone.Pos) {
	if comp := c.sys.Create(p, redstone.KindPowered, redstone.NoFace); comp != nil {
		comp.SetAnyDirection(true)
	}
}
