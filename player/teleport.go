package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
)

// Teleport moves the player to feet in dim facing rot, as /teleport does: off any
// vehicle first, then a teleport in its own dimension, or a dimension change (with its
// loading screen) that places it at feet on arrival. A nil dim is the player's own. It
// reports false while the player is not in the world or already changing dimension.
// Runs: the player's region (use Run from elsewhere).
func (p *Player) Teleport(dim *world.Dimension, feet mgl32.Vec3, rot world.Rotation) bool {
	if p.entity == nil || p.moving != nil {
		return false
	}
	if p.vehicle != nil {
		letOff(p.vehicle, p)
	}
	if dim == nil || dim == p.dim {
		p.teleportFeet(feet, rot)
		return true
	}
	p.changeDimension(&dimMove{to: dim, eyes: feet.Add(mgl32.Vec3{0, eyeHeight, 0}), rot: rot, arrive: func(*world.Region) bool {
		p.teleportFeet(feet, rot)
		return true
	}})
	return true
}

// Dimension is the dimension the player is in. Runs: the player's region; elsewhere,
// read State().Region.Dimension().
func (p *Player) Dimension() *world.Dimension { return p.dim }

// EyeHeight is how far a standing player's eyes are above its feet.
const EyeHeight = eyeHeight
