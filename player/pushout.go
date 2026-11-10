package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// BDS 1.26.60's push out of blocks (0xB3C5570), which only items and experience orbs run.
const (
	// pushSpeed is how fast it leaves a block, pushSpeedStairs how fast it leaves stairs.
	pushSpeed       = 0.25
	pushSpeedStairs = 0.1

	pushDown = iota - 2
	pushUp
	pushNorth
	pushSouth
	pushWest
	pushEast
	pushNowhere
)

// pushDirs are BDS's directions, in the order of the push constants.
var pushDirs = [...]mgl32.Vec3{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}

// pushDirection is BDS's choice of way out from a position in a block: the nearest open
// side, tried in the order west, east, down, up, north, south. BDS counts east as a whole
// block away once it picks it, so any other open side beats it; that is copied too. closed
// is in the order of the push constants.
func pushDirection(at cube.Pos, pos mgl32.Vec3, closed *[pushNowhere]bool) int {
	fx, fy, fz := pos[0]-float32(at[0]), pos[1]-float32(at[1]), pos[2]-float32(at[2])
	dist, dir := float32(math.MaxFloat32), pushNowhere
	if !closed[pushWest] && fx < dist {
		dist, dir = fx, pushWest
	}
	if !closed[pushEast] && dist > 1-fx {
		dist, dir = 1, pushEast
	}
	if !closed[pushDown] && dist > fy {
		dist, dir = fy, pushDown
	}
	if !closed[pushUp] && dist > 1-fy {
		dist, dir = 1-fy, pushUp
	}
	if !closed[pushNorth] && dist > fz {
		dist, dir = fz, pushNorth
	}
	if !closed[pushSouth] && dist > 1-fz {
		dir = pushSouth
	}
	return dir
}

// closedSide reports whether the block at pos counts as a closed side. One not loaded does not.
func (s *Host) closedSide(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && s.reg.Blocks.Properties(b).PushesOut
}

// pushOut is the push out of blocks. pos is where the block is looked for, centre the middle
// of the box. It sets the speed outright, so what a moving item had is forgotten. With every
// side closed and a plain block, it rises through the blocks with its collision off until it
// is out.
func (i *ItemActor) pushOut(r *world.Region, e *world.Entity, pos, centre mgl32.Vec3) {
	at := floorPos(pos)
	b, ok := r.Block(at, 0)
	if !ok {
		i.setNoClip(e, false)
		return
	}
	props := i.s.reg.Blocks.Properties(b)
	lo := centre.Sub(mgl32.Vec3{itemHalfSize, itemHalfSize, itemHalfSize})
	hi := centre.Add(mgl32.Vec3{itemHalfSize, itemHalfSize, itemHalfSize})
	origin := blockVec(at)
	if !props.PushesOut || !boxesHit(props.Collision, origin, lo, hi) {
		i.setNoClip(e, false)
		return
	}
	var closed [pushNowhere]bool
	allClosed := true
	for n, side := range [...]cube.Face{cube.FaceDown, cube.FaceUp, cube.FaceNorth, cube.FaceSouth, cube.FaceWest, cube.FaceEast} {
		closed[n] = i.s.closedSide(r, at.Side(side))
		allClosed = allClosed && closed[n]
	}
	i.setNoClip(e, allClosed && len(props.Collision) < 2)

	speed := float32(pushSpeed)
	if props.Stairs {
		speed = pushSpeedStairs
	}
	dir := pushDirection(at, pos, &closed)
	switch {
	case dir == pushNowhere:
		dir = pushUp
	case dir == pushUp:
		// Rising from a lower shape only needs to clear its top, so it goes slower near it.
		height, hit := topBoxHeight(props.Collision, origin, lo, hi)
		if !hit {
			if len(props.Collision) > 1 {
				return
			}
		} else {
			speed *= max(height+2*itemHalfSize+float32(at[1])-pos[1], 0)
		}
	}
	i.vel = pushDirs[dir].Mul(speed)
}

// setNoClip turns its collision off or on, telling those who see an item.
func (i *ItemActor) setNoClip(e *world.Entity, on bool) {
	if i.noClip == on {
		return
	}
	i.noClip = on
	if i.thinking {
		i.flagsDirty = true
		return
	}
	// An item with a view keeps its flags with its name; an orb never sent any.
	if i.entity != e || i.view != nil {
		return
	}
	i.sendFlags(e)
}

// boxesHit reports whether the box lo..hi overlaps the box around all of a block's collision
// boxes, which is the one shape BDS's first check looks at.
func boxesHit(boxes []registry.Box, origin, lo, hi mgl32.Vec3) bool {
	if len(boxes) == 0 {
		return false
	}
	all := boxes[0]
	for _, b := range boxes[1:] {
		for n := range 3 {
			all[n] = min(all[n], b[n])
			all[n+3] = max(all[n+3], b[n+3])
		}
	}
	return boxHits(all, origin, lo, hi)
}

// topBoxHeight is the height of the last of a block's boxes the box lo..hi overlaps.
func topBoxHeight(boxes []registry.Box, origin, lo, hi mgl32.Vec3) (float32, bool) {
	var height float32
	hit := false
	for _, b := range boxes {
		if boxHits(b, origin, lo, hi) {
			height, hit = b[4]-b[1], true
		}
	}
	return height, hit
}

func boxHits(b registry.Box, origin, lo, hi mgl32.Vec3) bool {
	for n := range 3 {
		if lo[n] >= origin[n]+b[n+3] || hi[n] <= origin[n]+b[n] {
			return false
		}
	}
	return true
}
