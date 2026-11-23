package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Ground check: Helix's own rule, beyond BDS. The client's vertical collision flag says the
// player landed; cheat clients set it in the air (no fall, fly that jumps off nothing). It
// counts only when a block's collision box, or an entity's box (boats, carts), has its top
// right under the player's feet.

const (
	// groundBelow is how far under the feet a top still holds the player, groundAbove how far
	// over them (float rounding of positions the client sends).
	groundBelow = 0.1
	groundAbove = 0.05
	// groundDepth is how many blocks under the feet are looked at: fences and walls reach 1.5.
	groundDepth = 1
)

// groundUnder reports whether something solid holds the player whose eyes are at eyes.
func (p *Player) groundUnder(r *world.Region, eyes mgl32.Vec3) bool {
	feet := eyes.Sub(mgl32.Vec3{0, eyeHeight, 0})
	lo := mgl32.Vec3{feet[0] - playerHalfWidth, feet[1], feet[2] - playerHalfWidth}
	hi := mgl32.Vec3{feet[0] + playerHalfWidth, eyes[1], feet[2] + playerHalfWidth}
	return p.groundUnderBox(r, lo, hi, nil)
}

// groundUnderBox reports whether something solid holds the box lo..hi up: a block's or an
// entity's top right under its bottom. skip is an entity that does not count (the box's own).
func (p *Player) groundUnderBox(r *world.Region, lo, hi mgl32.Vec3, skip *world.Entity) bool {
	feet := mgl32.Vec3{(lo[0] + hi[0]) / 2, lo[1], (lo[2] + hi[2]) / 2}
	x0, x1 := int(math.Floor(float64(lo[0]))), int(math.Floor(float64(hi[0])))
	z0, z1 := int(math.Floor(float64(lo[2]))), int(math.Floor(float64(hi[2])))
	y1 := int(math.Floor(float64(feet[1] + groundAbove)))
	for y := y1 - groundDepth; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				pos := cube.Pos{x, y, z}
				b, ok := r.Block(pos, 0)
				if !ok {
					// An unloaded block under the player is not the client's fault.
					return true
				}
				for _, box := range p.s.boxesFor(r, pos, b, lo, hi) {
					top := float32(y) + box[4]
					if top < feet[1]-groundBelow || top > feet[1]+groundAbove {
						continue
					}
					if float32(x)+box[0] < hi[0] && float32(x)+box[3] > lo[0] && float32(z)+box[2] < hi[2] && float32(z)+box[5] > lo[2] {
						return true
					}
				}
			}
		}
	}
	return p.entityUnder(r, feet, lo, hi, skip)
}

// entityUnder reports an entity box (a boat, a cart) whose top is under the feet.
func (p *Player) entityUnder(r *world.Region, feet, lo, hi mgl32.Vec3, skip *world.Entity) bool {
	p.touching = r.AppendEntitiesIn(p.touching[:0], lo[0]-1, lo[2]-1, hi[0]+1, hi[2]+1)
	for _, e := range p.touching {
		if e == p.entity || e == skip {
			continue
		}
		b, ok := e.Owner().(interface {
			BlastBox() (lo, hi mgl32.Vec3, ok bool)
		})
		if !ok {
			continue
		}
		elo, ehi, ok := b.BlastBox()
		if !ok || ehi[1] < feet[1]-groundBelow || ehi[1] > feet[1]+groundAbove {
			continue
		}
		if elo[0] < hi[0] && ehi[0] > lo[0] && elo[2] < hi[2] && ehi[2] > lo[2] {
			return true
		}
	}
	return false
}
