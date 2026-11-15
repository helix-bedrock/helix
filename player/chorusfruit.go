package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Chorus fruit's teleport as BDS 1.26.60 runs a food's on_use_action "chorus_teleport"
// (FoodItemComponentLegacy::useTimeDepleted 0xC9963A0 calls 0xB3A9A10 after the food's
// effects and before it is eaten; one try is 0xB3A93F0). The research note is private.
const (
	// onUseChorusTeleport is the on_use_action that teleports.
	onUseChorusTeleport = "chorus_teleport"
	// onUseSuspiciousStew is the suspicious stew's on_use_action, not built here.
	onUseSuspiciousStew = "suspicious_stew_effect"
	// chorusTries is how many random places are tried.
	chorusTries = 16
	// gameEventTeleport is posted where the player left.
	gameEventTeleport int32 = 64
)

// chorusGroundless are the blocks a teleport never stands on besides air: barrier and border
// blocks (BDS compares their block types).
var chorusGroundless = [...]string{"minecraft:barrier", "minecraft:border_block"}

// chorusTeleport moves the player to a random free place within reach (x, y, z blocks each
// way) of where it is, as BDS's random teleport: up to 16 places drawn from the level random,
// the height kept inside the dimension; each walks down to ground and needs room. True if the
// player moved.
func (p *Player) chorusTeleport(reach [3]float32) bool {
	r := p.entity.Region()
	if r == nil || p.moving != nil {
		return false
	}
	from := p.entity.Position()
	bounds := r.Dimension().Range()
	for range chorusTries {
		to := mgl32.Vec3{
			from[0] + (reach[0]+reach[0])*p.rng.Float32() - reach[0],
			from[1] + (reach[1]+reach[1])*p.rng.Float32() - reach[1],
			0,
		}
		to[1] = min(max(to[1], float32(bounds[0]+1)), float32(bounds[1]))
		to[2] = from[2] + (reach[2]+reach[2])*p.rng.Float32() - reach[2]
		feet, ok := p.s.chorusLanding(r, to)
		if !ok {
			continue
		}
		p.chorusArrive(r, from, feet)
		return true
	}
	return false
}

// chorusLanding is one try of the teleport: from the block at to it walks down to the first
// block below that can be stood on (not air, a barrier or border block, a portal, water or
// lava), stands on top of its collision boxes, and needs the player's box there free of
// block boxes and liquid. It returns the feet position.
func (s *Host) chorusLanding(r *world.Region, to mgl32.Vec3) (mgl32.Vec3, bool) {
	pos := cube.Pos{int(math.Floor(float64(to[0]))), int(math.Floor(float64(to[1]))), int(math.Floor(float64(to[2])))}
	if _, ok := r.Block(pos, 0); !ok {
		return mgl32.Vec3{}, false
	}
	floor := r.Dimension().Range()[0]
	if pos[1] <= floor {
		return mgl32.Vec3{}, false
	}
	var ground uint32
	for {
		below, ok := r.Block(pos.Side(cube.FaceDown), 0)
		if !ok {
			return mgl32.Vec3{}, false
		}
		if s.chorusGround(below) {
			ground = below
			break
		}
		pos = pos.Side(cube.FaceDown)
		if pos[1] <= floor {
			return mgl32.Vec3{}, false
		}
	}
	feet := mgl32.Vec3{to[0], float32(pos[1]), to[2]}
	if top := s.boxTop(r, pos.Side(cube.FaceDown), ground); top != 1 {
		feet[1] = float32(pos[1]) - 1 + top
	}
	lo := mgl32.Vec3{feet[0] - playerHalfWidth, feet[1], feet[2] - playerHalfWidth}
	hi := mgl32.Vec3{feet[0] + playerHalfWidth, feet[1] + playerHeight, feet[2] + playerHalfWidth}
	if s.boxBlocked(r, lo, hi) {
		return mgl32.Vec3{}, false
	}
	return feet, true
}

// chorusGround reports whether a teleport may stand on b.
func (s *Host) chorusGround(b uint32) bool {
	blocks := s.reg.Blocks
	if b == blocks.Air() {
		return false
	}
	name := blocks.Name(b)
	for _, n := range chorusGroundless {
		if name == n {
			return false
		}
	}
	switch blocks.Properties(b).Material {
	case registry.MaterialPortal, registry.MaterialWater, registry.MaterialLava:
		return false
	}
	return true
}

// boxTop is how high above its own block the collision boxes of b at pos reach: 1 for a full
// block, 0.5 for a bottom slab, 0 for none.
func (s *Host) boxTop(r *world.Region, pos cube.Pos, b uint32) float32 {
	var top float32
	for _, box := range s.boxesAt(r, pos, b) {
		top = max(top, box[4])
	}
	return top
}

// boxBlocked reports whether the box lo..hi touches a block's collision box or holds a liquid.
func (s *Host) boxBlocked(r *world.Region, lo, hi mgl32.Vec3) bool {
	x0, y0, z0 := int(math.Floor(float64(lo[0]))), int(math.Floor(float64(lo[1]))), int(math.Floor(float64(lo[2])))
	x1, y1, z1 := int(math.Ceil(float64(hi[0]))), int(math.Ceil(float64(hi[1]))), int(math.Ceil(float64(hi[2])))
	for x := x0; x < x1; x++ {
		for y := y0; y < y1; y++ {
			for z := z0; z < z1; z++ {
				pos := cube.Pos{x, y, z}
				b, ok := r.Block(pos, 0)
				if !ok {
					return true
				}
				if _, wet := s.liquidIn(r, pos); wet {
					return true
				}
				for _, box := range s.boxesFor(r, pos, b, lo, hi) {
					if boxesOverlap(lo, hi, pos, box) {
						return true
					}
				}
			}
		}
	}
	return false
}

// boxesOverlap reports whether lo..hi overlaps box, a block box at pos.
func boxesOverlap(lo, hi mgl32.Vec3, pos cube.Pos, box registry.Box) bool {
	x, y, z := float32(pos[0]), float32(pos[1]), float32(pos[2])
	return lo[0] < x+box[3] && hi[0] > x+box[0] &&
		lo[1] < y+box[4] && hi[1] > y+box[1] &&
		lo[2] < z+box[5] && hi[2] > z+box[2]
}

// chorusArrive teleports the player to feet with the chorus fruit cause, then plays the
// teleport sound where it left, posts the teleport game event there, and plays the sound
// where it arrived.
func (p *Player) chorusArrive(r *world.Region, from, feet mgl32.Vec3) {
	if p.vehicle != nil {
		letOff(p.vehicle, p)
	}
	p.teleportFeetCause(feet, p.entity.Rotation(), packet.TeleportCauseChorusFruit)
	at := cube.Pos{int(math.Floor(float64(from[0]))), int(math.Floor(float64(from[1]))), int(math.Floor(float64(from[2])))}
	playSound(r, at, soundEvent{name: packet.SoundEventTeleport, pos: from, extra: -1, entity: "minecraft:player"}, nil)
	p.s.postGameEvent(r, gameEventTeleport, from, p, 0, false)
	now := p.entity.Position()
	if nr := p.entity.Region(); nr != nil {
		playSound(nr, p.blockPos(), soundEvent{name: packet.SoundEventTeleport, pos: now, extra: -1, entity: "minecraft:player"}, nil)
	}
}
