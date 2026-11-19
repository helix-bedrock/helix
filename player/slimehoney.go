package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Slime and honey blocks, as BDS 1.26.60 does them. Landing on slime never hurts: its
// fall-on clears the distance fallen before the damage, sneaking or not. Landing on honey
// counts half the distance. An entity inside a honey block's cell that moves down along its
// side, below its top, is sliding: the fall distance is cleared each tick, and the slide
// sound plays every few ticks. The slide's slowing and the bounce off slime are movement,
// which the client runs for a player and entity physics for a mob.
const (
	nameSlimeBlock = "minecraft:slime"
	nameHoneyBlock = "minecraft:honey_block"

	// honeyFallShare is the part of the distance fallen that a landing on honey counts.
	honeyFallShare = 0.5
	// honeyTop is the top of a honey block's shape: an entity slides only below it.
	honeyTop = 0.9375
	// honeySlideReach is added to half the entity's width: the centre must be farther than
	// that from the block's centre along x or z for it to slide down the side.
	honeySlideReach = 0.43125
	// honeySlideSoundTicks is how often the slide sound plays, by the entity's tick count.
	honeySlideSoundTicks = 5
)

// fallOnDistance is the distance a landing at feet counts after the block landed on has its
// say: none on slime, half on honey, all of it elsewhere.
func (s *Host) fallOnDistance(r *world.Region, feet mgl32.Vec3, distance float32) float32 {
	b, ok := r.Block(standingOn(feet), 0)
	if !ok {
		return distance
	}
	switch s.reg.Blocks.Name(b) {
	case nameSlimeBlock:
		return 0
	case nameHoneyBlock:
		return distance * honeyFallShare
	}
	return distance
}

// honeySlide reports whether an entity at pos (BDS's position: the eyes for a player) with
// the box lo..hi, moving down by dy, slides down a honey block's side, and which block.
func (s *Host) honeySlide(r *world.Region, pos, lo, hi mgl32.Vec3, width, dy float32) (cube.Pos, bool) {
	if dy >= 0 {
		return cube.Pos{}, false
	}
	shrink := mgl32.Vec3{insideShrink, insideShrink, insideShrink}
	from, to := floorPos(lo.Add(shrink)), floorPos(hi.Sub(shrink))
	reach := width*0.5 + honeySlideReach
	for x := from[0]; x <= to[0]; x++ {
		for y := from[1]; y <= to[1]; y++ {
			for z := from[2]; z <= to[2]; z++ {
				at := cube.Pos{x, y, z}
				b, ok := r.Block(at, 0)
				if !ok || s.reg.Blocks.Name(b) != nameHoneyBlock || pos[1] > float32(y)+honeyTop {
					continue
				}
				if abs32(float32(x)+0.5-pos[0]) > reach || abs32(float32(z)+0.5-pos[2]) > reach {
					return at, true
				}
			}
		}
	}
	return cube.Pos{}, false
}

// honeySlidePlayer is the honey slide for a player that moved from m.From to m.To: the fall
// distance is cleared, and the slide sound plays for the others every few ticks.
func (p *Player) honeySlidePlayer(m *MoveInfo) {
	r := p.entity.Region()
	if r == nil || m.InWater {
		return
	}
	lo, hi := playerBox(m.To)
	at, ok := p.s.honeySlide(r, m.To, lo, hi, playerHalfWidth*2, m.To[1]-m.From[1])
	if !ok {
		return
	}
	p.move.fall = 0
	if m.OnGround || p.entity.Tick()%honeySlideSoundTicks != 0 {
		return
	}
	b, ok := r.Block(at, 0)
	if !ok {
		return
	}
	feet := m.To.Sub(mgl32.Vec3{0, eyeHeight, 0})
	playSound(r, at, p.s.blockSound(packet.SoundEventLand, feet, b).by("minecraft:player"), p.viewer)
}
