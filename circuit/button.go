package circuit

import (
	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// BDS's box a wood button feels arrows in: its pressed shape on each face.
const (
	buttonNear  = 0.125
	buttonFar   = 0.875
	buttonLong  = 0.3125
	buttonWide  = 0.375
	buttonLongE = 0.6875
	buttonWideE = 0.625
)

// buttonBox is the box of a button with this facing_direction, within its block.
func buttonBox(facing int) (lo, hi mgl64.Vec3) {
	switch cube.Face(facing) { //nolint:gosec // block state values are small
	case cube.FaceDown:
		return mgl64.Vec3{buttonLong, buttonFar, buttonWide}, mgl64.Vec3{buttonLongE, 1, buttonWideE}
	case cube.FaceUp:
		return mgl64.Vec3{buttonLong, 0, buttonWide}, mgl64.Vec3{buttonLongE, buttonNear, buttonWideE}
	case cube.FaceNorth:
		return mgl64.Vec3{buttonLong, buttonWide, buttonFar}, mgl64.Vec3{buttonLongE, buttonWideE, 1}
	case cube.FaceSouth:
		return mgl64.Vec3{buttonLong, buttonWide, 0}, mgl64.Vec3{buttonLongE, buttonWideE, buttonNear}
	case cube.FaceWest:
		return mgl64.Vec3{buttonFar, buttonWide, buttonLong}, mgl64.Vec3{1, buttonWideE, buttonLongE}
	}
	return mgl64.Vec3{0, buttonWide, buttonLong}, mgl64.Vec3{buttonNear, buttonWideE, buttonLongE}
}

// arrowTouches says the arrow e is in the box of the button at pos.
func (t *Ticker) arrowTouches(e *world.Entity, pos cube.Pos, block uint32) bool {
	if t.isArrow == nil || !t.isArrow(e) {
		return false
	}
	eLo, eHi, _, ok := t.boxOf(e)
	if !ok {
		return false
	}
	lo, hi := buttonBox(int(intState(t.blocks.States(block), stateFacing)))
	base := mgl64.Vec3{float64(pos[0]), float64(pos[1]), float64(pos[2])}
	lo, hi = lo.Add(base), hi.Add(base)
	return eHi[0] > lo[0] && eLo[0] < hi[0] && eHi[1] > lo[1] && eLo[1] < hi[1] && eHi[2] > lo[2] && eLo[2] < hi[2]
}

// arrowOnButton says an arrow is in the box of the button at pos.
func (t *Ticker) arrowOnButton(c *regionCircuit, pos cube.Pos, block uint32) bool {
	x, z := float64(pos[0]), float64(pos[2])
	c.found = c.r.AppendEntitiesIn(c.found[:0], x-plateReach, z-plateReach, x+1+plateReach, z+1+plateReach)
	for _, e := range c.found {
		if t.arrowTouches(e, pos, block) {
			return true
		}
	}
	return false
}

// arrowHitsButton is an entity touching a button's block: an arrow presses a wood button
// that is up, as BDS's entityInside.
func (t *Ticker) arrowHitsButton(c *regionCircuit, e *world.Entity, pos cube.Pos, block uint32) {
	if !blockuse.ButtonFeelsArrows(t.blocks.Name(block)) || bitState(t.blocks.States(block), statePressed) {
		return
	}
	if t.arrowTouches(e, pos, block) {
		blockuse.PressButton(t.blocks, c.r, pos, block)
	}
}

// buttonTick is BDS's tick of a button: without its holder it breaks off, and a wood button
// with an arrow in it stays in, looking again later. Popping out is blockuse's.
func (t *Ticker) buttonTick(r *world.Region, pos cube.Pos, block uint32) {
	now, ok := r.Block(pos, 0)
	if !ok || t.kind(now) != kindButton {
		return
	}
	if !t.attach.ButtonHeld(r, pos, now) {
		t.fall(r, pos, now)
		return
	}
	name := t.blocks.Name(now)
	if blockuse.ButtonFeelsArrows(name) && bitState(t.blocks.States(now), statePressed) && t.arrowOnButton(t.circuitOf(r), pos, now) {
		r.ScheduleTick(pos, now, blockuse.ButtonTicks(name), 0)
		return
	}
	t.BlockTicker.QueuedTick(r, pos, block)
}
