package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Slime and honey on items, by the same rules BDS runs for every actor (and entity physics
// runs for mobs): an item landing on slime fast enough bounces back up by as much as it fell,
// one resting on slime or honey keeps only 0.4 plus a fifth of its vertical speed of its
// horizontal speed, and each honey block its box is in keeps 0.4 of its horizontal speed and
// holds its fall at 0.12 a tick. Items never sneak, so nothing turns these off.
const (
	// itemMinBounceFall is the slowest fall that bounces, BDS's |vel.y| >= 0.08000012.
	itemMinBounceFall = 0.08000012
	// slimeRestitution is slime's bounciness: the fall comes back whole.
	slimeRestitution = 1

	standOnKeep      = 0.4
	standOnKeepPerVY = 0.2
	standOnMaxVY     = 0.1

	honeyKeep    = 0.4
	honeyMaxFall = -0.12
)

// itemBounce is the speed upward an item gets for landing at feet while falling by fall
// (negative), and whether it bounces: only off slime, and only falling fast enough.
func (s *Host) itemBounce(r *world.Region, feet mgl32.Vec3, fall float32) (float32, bool) {
	if fall >= 0 || -fall < itemMinBounceFall {
		return 0, false
	}
	b, ok := r.Block(standingOn(feet), 0)
	if !ok || s.reg.Blocks.Name(b) != nameSlimeBlock {
		return 0, false
	}
	return -fall * slimeRestitution, true
}

// itemStickyBlocks slows an item by the slime or honey it rests on, then by the honey blocks
// its box is in, after its move as BDS's stand-on and entity inside systems run.
func (i *ItemActor) itemStickyBlocks(r *world.Region, pos mgl32.Vec3, inWater bool) {
	if i.onGround {
		i.standOnSlow(r, pos)
	}
	if !inWater {
		i.insideHoney(r, pos)
	}
}

// standOnSlow is slime's and honey's stand-on slowdown for an item resting on one.
func (i *ItemActor) standOnSlow(r *world.Region, pos mgl32.Vec3) {
	if i.vel[1] >= standOnMaxVY {
		return
	}
	b, ok := r.Block(standingOn(pos.Sub(mgl32.Vec3{0, itemHalfSize, 0})), 0)
	if !ok {
		return
	}
	if name := i.s.reg.Blocks.Name(b); name != nameSlimeBlock && name != nameHoneyBlock {
		return
	}
	k := abs32(i.vel[1])*standOnKeepPerVY + standOnKeep
	i.vel[0] *= k
	i.vel[2] *= k
}

// insideHoney slows an item once for each honey block its box is in.
func (i *ItemActor) insideHoney(r *world.Region, pos mgl32.Vec3) {
	half := mgl32.Vec3{itemHalfSize - insideShrink, itemHalfSize - insideShrink, itemHalfSize - insideShrink}
	from, to := floorPos(pos.Sub(half)), floorPos(pos.Add(half))
	for x := from[0]; x <= to[0]; x++ {
		for y := from[1]; y <= to[1]; y++ {
			for z := from[2]; z <= to[2]; z++ {
				b, ok := r.Block(cube.Pos{x, y, z}, 0)
				if !ok || i.s.reg.Blocks.Name(b) != nameHoneyBlock {
					continue
				}
				i.vel[0] *= honeyKeep
				i.vel[2] *= honeyKeep
				i.vel[1] = max(honeyMaxFall, i.vel[1])
			}
		}
	}
}
