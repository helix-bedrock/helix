package player

import (
	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/world"
)

// Entities that move by physics work their move out in their think step (EntityConfig.Think),
// which in a crowded region runs on idle cores while other entities think: only block reads
// and their own fields. Input then applies the move and does everything that touches the
// world or other entities, in runtime ID order. An entity added during this tick's Input
// phase had no think step and works its move out in Input.

// thought is a move worked out in an entity's think step, applied in its Input.
type thought struct {
	pos  mgl64.Vec3
	hitY bool
	ok   bool
	// lava is an XP orb thrown up by lava, whose fizz is played in Input.
	lava bool
	// rest is an item resting on the ground, which does not move this tick.
	rest bool
	// inWater is an item in water, which puts out its fire.
	inWater bool
}

// clipSteps moves pos by vel through the blocks' collision axis by axis (y, x, z), the way
// items, XP orbs, TNT and falling blocks move. A side that hits loses its speed; hitY reports
// the vertical one hitting. Runs: a think step or the owner.
func (s *Host) clipSteps(r *world.Region, pos mgl64.Vec3, half float32, vel *mgl64.Vec3) (mgl64.Vec3, bool) {
	hitY := false
	for _, axis := range [...]int{1, 0, 2} {
		d := s.clipMove(r, pos, float64(half), axis, vel[axis])
		if d != vel[axis] {
			if axis == 1 {
				hitY = true
			} else {
				vel[axis] = 0
			}
		}
		pos[axis] += d
	}
	return pos, hitY
}
