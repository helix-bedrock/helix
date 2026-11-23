package player

import (
	"github.com/go-gl/mathgl/mgl32"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Phase check: Helix's own rule, beyond BDS. Helix takes the client's position, so a cheat client
// walks through walls and floors by sending a position on the other side. Each move is swept
// against the blocks' collision boxes as the client's own collision does (vertical first, then
// the two horizontal axes, and once more with a step up); a move no sweep reaches is cut where
// the blocks stop it and the client is corrected there. Entities are not walls here, and nothing
// is checked where a block is not loaded, so a vanilla client is never stopped.

const (
	// phaseSlack is how far, on any axis, a move may end from where the sweep reached: float
	// rounding and block shapes the client works out a little differently.
	phaseSlack = 0.05
	// phaseStep is how high a step up may go (slabs, stairs, paths), with room to spare.
	phaseStep = 0.6
	// phaseFar is the longest move swept; a longer one with no teleport is cut.
	phaseFar = 16
	// phaseLowHeight and phaseSneakHeight are the player's box height while swimming, gliding or
	// crawling, and while sneaking.
	phaseLowHeight   = 0.6
	phaseSneakHeight = 1.5
	// phaseResend is the most blocks sent back when a move is cut, so a client that missed a
	// block change sees it.
	phaseResend = 27
)

// phaseCheck returns where the move of the player's eyes from from to to may end: to itself,
// or where the blocks stop it, after correcting the client.
func (p *Player) phaseCheck(r *world.Region, from, to mgl32.Vec3, pk *packet.PlayerAuthInput) mgl32.Vec3 {
	if p.spectating() || p.dead() || to == from {
		return to
	}
	d := to.Sub(from)
	lo, hi := p.phaseBox(from)
	if d.Len() <= phaseFar {
		// A move the sweep reached within phaseSlack still ends where the sweep reached, not
		// where the client said: else a cheat creeps into a wall phaseSlack at a time, and once
		// inside a block's box that box no longer stops it.
		got, ok := p.s.sweepMove(r, lo, hi, d, 0)
		if !ok {
			return to
		}
		if reached(got, d) {
			return from.Add(got)
		}
		stepped, ok := p.s.sweepMove(r, lo, hi, d, phaseStep)
		if !ok {
			return to
		}
		if reached(stepped, d) {
			return from.Add(stepped)
		}
		if flatDist(stepped, d) < flatDist(got, d) {
			got = stepped
		}
		return p.cutMove(r, from, from.Add(got), to, pk)
	}
	return p.cutMove(r, from, from, to, pk)
}

// phaseBox is the player's collision box with its eyes at eyes, by its pose.
func (p *Player) phaseBox(eyes mgl32.Vec3) (lo, hi mgl32.Vec3) {
	height := float32(playerHeight)
	switch {
	// Swimming counts only in water: a cheat claims it anywhere to fit under blocks.
	case p.Moving(MoveSwimming) && p.move.inWater, p.Moving(MoveGliding), p.Moving(MoveCrawling):
		height = phaseLowHeight
	case p.Moving(MoveSneaking):
		height = phaseSneakHeight
	}
	feet := eyes[1] - eyeHeight
	return mgl32.Vec3{eyes[0] - playerHalfWidth, feet, eyes[2] - playerHalfWidth},
		mgl32.Vec3{eyes[0] + playerHalfWidth, feet + height, eyes[2] + playerHalfWidth}
}

// cutMove corrects the client to at, sends back the blocks where it tried to go and returns at.
func (p *Player) cutMove(r *world.Region, from, at, to mgl32.Vec3, pk *packet.PlayerAuthInput) mgl32.Vec3 {
	p.s.log.Debug("move through blocks cut", "name", p.name, "from", from, "to", to, "at", at)
	lo, hi := p.phaseBox(to)
	sent := 0
	for x := floorInt(lo[0]); x <= floorInt(hi[0]) && sent < phaseResend; x++ {
		for y := floorInt(lo[1]); y <= floorInt(hi[1]) && sent < phaseResend; y++ {
			for z := floorInt(lo[2]); z <= floorInt(hi[2]) && sent < phaseResend; z++ {
				p.sendBlock(r, cube.Pos{int(x), int(y), int(z)})
				sent++
			}
		}
	}
	p.markMoveFix()
	p.move.velocity = mgl32.Vec3{}
	return at
}

// sweeper moves a box against the blocks one axis at a time.
type sweeper struct {
	s      *Host
	r      *world.Region
	lo, hi mgl32.Vec3
	moved  mgl32.Vec3
	// unloaded is set once a block the box would cross is not loaded.
	unloaded bool
}

// move moves the box along axis by up to by.
func (w *sweeper) move(axis int, by float32) {
	if w.unloaded {
		return
	}
	got, ok := w.s.sweepAxis(w.r, w.lo, w.hi, axis, by)
	w.unloaded = !ok
	w.lo[axis] += got
	w.hi[axis] += got
	w.moved[axis] += got
}

// sweepMove moves the box lo..hi by d against the blocks, vertical first, then x and z, as
// entities collide. With step above 0 the box first goes up by up to step and comes back down
// after. It returns how far the box went, and false if a block it would cross is not loaded.
func (s *Host) sweepMove(r *world.Region, lo, hi, d mgl32.Vec3, step float32) (mgl32.Vec3, bool) {
	w := sweeper{s: s, r: r, lo: lo, hi: hi}
	if step > 0 {
		w.move(1, step)
	} else {
		w.move(1, d[1])
	}
	w.move(0, d[0])
	w.move(2, d[2])
	if step > 0 {
		w.move(1, d[1]-w.moved[1])
	}
	return w.moved, !w.unloaded
}

// sweepAxis is clipSweep that also reports whether every block it looked at was loaded. Blocks
// whose shape depends on who asks (scaffolding, powder snow) are left out: they never stop a
// vanilla client wrongly that way.
func (s *Host) sweepAxis(r *world.Region, lo, hi mgl32.Vec3, axis int, d float32) (float32, bool) {
	if d == 0 {
		return 0, true
	}
	sweepLo, sweepHi := lo, hi
	if d < 0 {
		sweepLo[axis] += d
	} else {
		sweepHi[axis] += d
	}
	for x := itemFloor(sweepLo[0]); x <= itemFloor(sweepHi[0]); x++ {
		for y := itemFloor(sweepLo[1]); y <= itemFloor(sweepHi[1]); y++ {
			for z := itemFloor(sweepLo[2]); z <= itemFloor(sweepHi[2]); z++ {
				b, ok := r.Block(cube.Pos{x, y, z}, 0)
				if !ok {
					return d, false
				}
				origin := mgl32.Vec3{float32(x), float32(y), float32(z)}
				for _, box := range s.boxesAt(r, cube.Pos{x, y, z}, b) {
					d = clipBox(lo, hi, origin, box, axis, d)
				}
			}
		}
	}
	return d, true
}

// reached reports whether a sweep reached the asked move on every axis.
func reached(got, want mgl32.Vec3) bool {
	for i := range 3 {
		if got[i]-want[i] > phaseSlack || want[i]-got[i] > phaseSlack {
			return false
		}
	}
	return true
}

// flatDist is the horizontal distance between two moves.
func flatDist(a, b mgl32.Vec3) float32 {
	return flatSpeed(a.Sub(b))
}
