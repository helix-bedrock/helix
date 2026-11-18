package drip

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Pointed dripstone losing its hold, as BDS 1.26.60 does it: neighborChanged looks
// at a change right above or below, and when the dripstone cannot stay (canSurvive)
// asks for a tick in 1, for a stalactite on each of its pieces from the tip up. The
// tick breaks a stalagmite that cannot stay, with its drops, and makes a
// stalactite fall (startFalling). A falling tip hurts what it lands on by the
// falling block's fall damage: its height in blocks counts once for each
// dripstone stacked on the tip when it fell, up to 6. Pieces that are not the tip hurt
// nothing.
const (
	// fallReach is how far down neighborChanged looks for a stalactite's tip, and how far
	// canSurvive follows a stalactite up.
	fallReach = 512
	// fallDelay is the tick delay neighborChanged asks for.
	fallDelay = 1
	// stackMost is the most a tip's damage counts the dripstone stacked on it.
	stackMost = 6
)

// Falls is the engine side of dripstone losing its hold. Runs: owner of the region.
type Falls interface {
	// Supports reports whether block holds a pointed dripstone on face, as BDS's
	// canProvideSupport.
	Supports(block uint32, face cube.Face) bool
	// FallStalactite turns the stalactite piece at pos into a falling block. A tip passes
	// how much each block it falls hurts what it lands on; other pieces pass 0.
	FallStalactite(r *world.Region, pos cube.Pos, block uint32, hurtPerBlock float32) bool
	// Break destroys the block at pos with its drops.
	Break(r *world.Region, pos cube.Pos, block uint32)
}

// isDripstone reports whether k is a pointed dripstone.
func isDripstone(k kind) bool {
	return k == kindHanging || k == kindTip || k == kindStanding || k == kindStandingTip
}

// isHanging reports whether k is a stalactite piece.
func isHanging(k kind) bool { return k == kindHanging || k == kindTip }

// NeighbourChanged is neighborChanged: a dripstone whose block above or below changed and
// that cannot stay asks for its tick; a stalactite asks it for every piece, tip first, so the
// tip falls with the pieces above it still in place.
func (t *Ticker) NeighbourChanged(r *world.Region, pos, from cube.Pos, block uint32, layer uint8) {
	if layer == 0 && t.falls != nil && isDripstone(t.kindOfID(block)) &&
		(from == pos.Side(cube.FaceUp) || from == pos.Side(cube.FaceDown)) && !t.survives(r, pos, t.kindOfID(block)) {
		t.askFall(r, pos, block, t.kindOfID(block))
	}
	if t.BlockTicker != nil {
		t.BlockTicker.NeighbourChanged(r, pos, from, block, layer)
	}
}

// askFall asks for the ticks of a dripstone that cannot stay.
func (t *Ticker) askFall(r *world.Region, pos cube.Pos, block uint32, k kind) {
	if !isHanging(k) {
		r.ScheduleTick(pos, block, fallDelay, 0)
		return
	}
	tip, ok := t.fallTip(r, pos)
	if !ok {
		r.ScheduleTick(pos, block, fallDelay, 0)
		return
	}
	maxY := r.Dimension().Range()[1]
	for p := tip; p[1] <= maxY; p = p.Side(cube.FaceUp) {
		b, ok := r.Block(p, 0)
		if !ok || !isDripstone(t.kindOfID(b)) {
			return
		}
		r.ScheduleTick(p, b, fallDelay, 0)
	}
}

// fallTip follows a stalactite down from pos to its tip, within fallReach.
func (t *Ticker) fallTip(r *world.Region, pos cube.Pos) (cube.Pos, bool) {
	return t.lowestHanging(r, pos, fallReach)
}

// lowestHanging follows a stalactite down from pos to its lowest piece, within n blocks. BDS
// finds the piece whose thickness is tip; Helix places pointed dripstone without working out
// its thickness, so every placed piece says tip and the lowest piece is taken instead, which
// is the tip for one with the right states.
func (t *Ticker) lowestHanging(r *world.Region, pos cube.Pos, n int) (cube.Pos, bool) {
	if !isHanging(t.kind(r, pos)) {
		return pos, false
	}
	minY := r.Dimension().Range()[0]
	for range n {
		below := pos.Side(cube.FaceDown)
		if below[1] < minY || !isHanging(t.kind(r, below)) {
			return pos, true
		}
		pos = below
	}
	return pos, false
}

// survives is canSurvive: a stalagmite stands on a stalagmite or on a block holding it up; a
// stalactite hangs from a chain of stalactites that ends under a block holding it down.
func (t *Ticker) survives(r *world.Region, pos cube.Pos, k kind) bool {
	if !isHanging(k) {
		b, ok := r.Block(pos.Side(cube.FaceDown), 0)
		if !ok {
			return true
		}
		if bk := t.kindOfID(b); bk == kindStanding || bk == kindStandingTip {
			return true
		}
		return t.falls.Supports(b, cube.FaceUp)
	}
	maxY := r.Dimension().Range()[1]
	for range fallReach {
		if pos[1] >= maxY || !isDripstone(t.kind(r, pos)) {
			return true
		}
		b, ok := r.Block(pos.Side(cube.FaceUp), 0)
		if !ok {
			return true
		}
		if !isHanging(t.kindOfID(b)) && !t.falls.Supports(b, cube.FaceDown) {
			return false
		}
		pos = pos.Side(cube.FaceUp)
	}
	return true
}

// tickDripstone is the tick listener: a dripstone that cannot stay falls, as a stalactite, or
// breaks with its drops, as a stalagmite.
func (t *Ticker) tickDripstone(r *world.Region, pos cube.Pos, block uint32) {
	k := t.kindOfID(block)
	if t.falls == nil || t.survives(r, pos, k) {
		return
	}
	if !isHanging(k) {
		t.falls.Break(r, pos, block)
		return
	}
	var hurt float32
	if !isHanging(t.kind(r, pos.Side(cube.FaceDown))) {
		hurt = float32(t.stackOn(r, pos))
	}
	t.falls.FallStalactite(r, pos, block, hurt)
}

// stackOn is startFalling's count for a tip: 1 for the tip alone, one more for each hanging
// dripstone right above it, up to stackMost.
func (t *Ticker) stackOn(r *world.Region, tip cube.Pos) int {
	n := 1
	for p := tip.Side(cube.FaceUp); n < stackMost && isHanging(t.kind(r, p)); p = p.Side(cube.FaceUp) {
		n++
	}
	return n
}

// kindOfID is the kind of a block state ID.
func (t *Ticker) kindOfID(b uint32) kind {
	if int(b) >= len(t.kinds) {
		return kindOther
	}
	return t.kinds[b]
}
