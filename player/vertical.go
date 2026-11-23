package player

import (
	"github.com/go-gl/mathgl/mgl32"

	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Vertical check: Helix's own rule, beyond BDS. Helix takes the client's position, so a cheat
// flies, hovers, jumps in the air, climbs walls, steps up whole blocks or stands on water by
// sending a position the client's physics never reaches. Each move's rise is held to what the
// client's own physics allows from the vertical speed the player had: the last move's speed
// less gravity (Slow Falling's lower gravity, Levitation's pull up), times air drag. From the
// ground a jump (Jump Boost in it) is allowed, a step up of a slab or stair when the move ends
// on a block, and a bounce as fast as the fall that landed on a slime block or bed. Ladders,
// vines, scaffolding and powder snow allow climbing, cobwebs, honey and berry bushes slow
// falling, a move that ends on the ground may stop a fall, and pushes from the server
// (knockback, explosions, wind) are allowed until the client takes them. Deep water or lava,
// gliding, riding, creative flying and Riptide are not held; their speed carries over, and the
// first move out of them may rise as far as a jump out of water. A move past the limit is cut
// where it would have fallen and the client is corrected there.

const (
	// vertGravity and vertSlowGravity are what gravity takes from the vertical speed each tick,
	// without and with Slow Falling; vertDrag is what the air leaves of it.
	vertGravity, vertSlowGravity = 0.08, 0.01
	vertDrag                     = 0.98
	// vertJump is a jump's speed and vertJumpBoost what each Jump Boost level adds.
	vertJump, vertJumpBoost = 0.42, 0.1
	// vertLevitation is the speed each Levitation level pulls toward, and vertLevitationPull how
	// much of the gap it closes each tick.
	vertLevitation, vertLevitationPull = 0.05, 0.2
	// vertClimb is the most a climb (ladders, vines, scaffolding, powder snow) or a slowing block
	// (cobwebs, honey, berry bushes) lets the player rise each tick, with room to spare.
	vertClimb = 0.25
	// vertShallowRise is the most a player touching a liquid but not swimming may rise: a jump
	// out of water at a block's edge.
	vertShallowRise = 0.35
	// vertFreeExit is the most the first move out of water, gliding or flying may rise: a jump
	// out of water at a block's edge.
	vertFreeExit = 0.5
	// vertDeep is how far a liquid's surface must be over the feet before the player swims and is
	// not held.
	vertDeep = 0.2
	// vertSlack is how far a move may rise past the limit before it costs anything: float
	// rounding, and the client working gravity out a little differently.
	vertSlack = 0.03
	// vertBank and vertRefill are how far past the limit and slack a player may go in all, and how
	// much comes back each move: a client and server that disagree for a moment about the blocks
	// under the player are not corrected.
	vertBank, vertRefill = 0.2, 0.01
	// vertLiquidCell is a liquid's height in ninths of a block for depth 0 (a source).
	vertLiquidCell = 9
	// vertBedBounce is how much of the fall a bed gives back.
	vertBedBounce = 0.75
	// vertReach is how far beside the player's box a block still counts as touching it: honey
	// walls slow a slide from just beside them.
	vertReach = 0.1
)

// vertState is what the vertical check knows of the player's vertical speed. Region tick.
type vertState struct {
	// vy is the vertical speed, in blocks a tick, the player had on its last move.
	vy float32
	// fall is how fast the player was falling when it last landed: a slime block or bed may give
	// it back.
	fall float32
	// free is set when the last move was not held; reset when the next one is not held either
	// (a teleport).
	free, reset bool
	// push is an upward speed the server gave the player that its client has not used yet, and
	// pushTicks how many more moves it waits.
	push      float32
	pushTicks int32
	// bank is how far past the limit the player may still go.
	bank float32
	// liquid is how far a liquid's surface is over the player's feet at the start of the move.
	liquid float32
}

// vertSurround is what touches the player's box over a move.
type vertSurround struct {
	// climb is set for a climbable block, slow for a block that slows falling.
	climb, slow bool
}

// noteVerticalPush allows the player the upward speed of v, which the server gave it. Region
// tick.
func (p *Player) noteVerticalPush(v float32) {
	s := &p.move.vert
	if v <= 0 {
		return
	}
	if s.pushTicks <= 0 || v > s.push {
		s.push = v
	}
	s.pushTicks = speedPushTicks
}

// noteTeleportMove lets the player's next move go anywhere the speed check carries it: the server
// moved it, or input from before a move the server made is dropped. Region tick.
func (p *Player) noteTeleportMove() {
	p.notePushSpeed(speedTeleportCarry)
	p.move.vert.reset = true
}

// noteLiquid finds how deep in water or lava the player is at from, its eyes, before the move.
func (p *Player) noteLiquid(r *world.Region, from mgl32.Vec3) {
	p.move.vert.liquid = p.liquidDepth(r, from)
}

// deepInLiquid reports whether the player swims: a liquid's surface is well over its feet.
func (p *Player) deepInLiquid() bool { return p.move.vert.liquid >= vertDeep }

// verticalCheck returns where the move from from to to may end: to itself, or where the player
// would have fallen, after correcting the client.
func (p *Player) verticalCheck(r *world.Region, from, to mgl32.Vec3, pk *packet.PlayerAuthInput) mgl32.Vec3 {
	s := &p.move.vert
	dy := to[1] - from[1]
	if s.reset || p.speedFree() {
		s.reset, s.free, s.vy, s.pushTicks, s.fall = false, true, dy, 0, 0
		return to
	}
	groundFrom := p.groundUnder(r, from)
	groundTo := p.groundUnder(r, to)
	limit := p.vertLimit(r, from, to, dy, groundFrom, groundTo)
	if s.free {
		limit = max(limit, vertFreeExit)
	}
	if s.pushTicks > 0 {
		if dy > limit+vertSlack && dy <= s.push+vertSlack {
			// The client took the push now.
			s.pushTicks = 0
		} else {
			s.pushTicks--
		}
		limit = max(limit, s.push)
	}
	s.free = false
	s.bank = min(s.bank+vertRefill, vertBank)
	over := dy - limit - vertSlack
	if over <= 0 || over <= s.bank {
		if over > 0 {
			s.bank -= over
		}
		p.vertMoved(dy, groundFrom, groundTo)
		return to
	}
	return p.vertCut(r, from, to, limit, pk)
}

// vertMoved keeps the vertical speed and the landing speed of a move the check let through.
func (p *Player) vertMoved(dy float32, groundFrom, groundTo bool) {
	s := &p.move.vert
	switch {
	case groundTo && dy <= 0:
		if !groundFrom {
			// Landed: the speed it would have fallen at, which a slime block gives back.
			s.fall = max(-(s.vy-vertGravity)*vertDrag, 0)
		} else {
			s.fall = 0
		}
		s.vy = 0
	default:
		s.vy = dy
	}
}

// vertCut moves the player from from as far as its vertical speed limit takes it, straight up or
// down and no further than the blocks let it, corrects the client there and returns it.
func (p *Player) vertCut(r *world.Region, from, to mgl32.Vec3, limit float32, pk *packet.PlayerAuthInput) mgl32.Vec3 {
	s := &p.move.vert
	lo, hi := p.phaseBox(from)
	got, ok := p.s.sweepMove(r, lo, hi, mgl32.Vec3{0, limit, 0}, 0)
	if !ok {
		got = mgl32.Vec3{}
	}
	at := from.Add(got)
	s.vy = got[1]
	p.s.log.Debug("move too high cut", "name", p.name, "from", from, "to", to, "at", at, "dy", to[1]-from[1], "limit", limit)
	p.send(&packet.CorrectPlayerMovePrediction{
		PredictionType: packet.PredictionTypePlayer, Position: at,
		Rotation: mgl32.Vec2{pk.Pitch, pk.Yaw}, OnGround: p.groundUnder(r, at), Tick: pk.Tick,
	})
	p.move.velocity = mgl32.Vec3{}
	p.move.speed.last = 0
	return at
}

// vertLimit is the most the player may rise this move, from the speed it had and what holds,
// pushes or slows it. The blocks around are looked at only for a move of dy past the rest.
func (p *Player) vertLimit(r *world.Region, from, to mgl32.Vec3, dy float32, groundFrom, groundTo bool) float32 {
	s := &p.move.vert
	gravity := float32(vertGravity)
	if _, ok := p.Effect(EffectSlowFalling); ok {
		gravity = vertSlowGravity
	}
	limit := (s.vy - gravity) * vertDrag
	if amp, ok := p.Effect(EffectLevitation); ok {
		pull := vertLevitation * float32(amp+1)
		limit = max(limit, (s.vy+(pull-s.vy)*vertLevitationPull)*vertDrag)
	}
	if groundFrom {
		jump := float32(vertJump)
		if amp, ok := p.Effect(EffectJumpBoost); ok {
			jump += vertJumpBoost * float32(amp+1)
		}
		limit = max(limit, jump, p.vertBounce(r, from))
		if groundTo {
			limit = max(limit, phaseStep)
		}
	}
	if groundTo {
		// Landing stops a fall, standing keeps the player where it is.
		limit = max(limit, 0)
	}
	if dy <= limit+vertSlack {
		// Within the limit already: the blocks around need not be looked at.
		return limit
	}
	if s.liquid > 0 || p.liquidDepth(r, to) > 0 {
		limit = max(limit, vertShallowRise)
	}
	if p.move.climbing {
		limit = max(limit, vertClimb)
	}
	if sur := p.vertSurroundings(r, from, to); sur.climb || sur.slow {
		limit = max(limit, vertClimb)
	}
	return limit
}

// vertBounce is how fast a slime block or bed under the player at from sends it back up.
func (p *Player) vertBounce(r *world.Region, from mgl32.Vec3) float32 {
	fall := p.move.vert.fall
	if fall <= 0 {
		return 0
	}
	b, ok := r.Block(vecPos(from.Sub(mgl32.Vec3{0, eyeHeight + 0.5, 0})), 0)
	if !ok {
		return fall
	}
	switch p.s.reg.Blocks.Name(b) {
	case nameSlimeBlock:
		return fall
	case blockuse.Bed, blockuse.StrawBed:
		return fall * vertBedBounce
	}
	return 0
}

// vertSurroundings is what touches the player's box at from or at to.
func (p *Player) vertSurroundings(r *world.Region, from, to mgl32.Vec3) vertSurround {
	var sur vertSurround
	lo, hi := playerBox(from)
	tlo, thi := playerBox(to)
	for i := range 3 {
		lo[i], hi[i] = min(lo[i], tlo[i]), max(hi[i], thi[i])
	}
	lo[0], lo[2], hi[0], hi[2] = lo[0]-vertReach, lo[2]-vertReach, hi[0]+vertReach, hi[2]+vertReach
	blocks := p.s.reg.Blocks
	for x := itemFloor(lo[0]); x <= itemFloor(hi[0]); x++ {
		for y := itemFloor(lo[1]); y <= itemFloor(hi[1]); y++ {
			for z := itemFloor(lo[2]); z <= itemFloor(hi[2]); z++ {
				b, ok := r.Block(cube.Pos{x, y, z}, 0)
				if !ok {
					continue
				}
				if p.s.climbableBlock(b) {
					sur.climb = true
				}
				switch blocks.Name(b) {
				case nameCobweb, nameHoneyBlock, nameBerryBush:
					sur.slow = true
				}
			}
		}
	}
	return sur
}

// liquidDepth is how far the surface of water or lava is over the feet of the player whose eyes
// are at eyes, 0 when no liquid touches its box.
func (p *Player) liquidDepth(r *world.Region, eyes mgl32.Vec3) float32 {
	lo, hi := playerBox(eyes)
	var depth float32
	for x := itemFloor(lo[0]); x <= itemFloor(hi[0]); x++ {
		for y := itemFloor(lo[1]); y <= itemFloor(hi[1]); y++ {
			for z := itemFloor(lo[2]); z <= itemFloor(hi[2]); z++ {
				pos := cube.Pos{x, y, z}
				top, ok := p.liquidTop(r, pos)
				if !ok || top <= lo[1] {
					continue
				}
				depth = max(depth, min(top, hi[1])-lo[1])
			}
		}
	}
	return depth
}

// liquidTop is the height of the surface of the water or lava at pos: the whole block under the
// same liquid or while falling, else its depth's share of it.
func (p *Player) liquidTop(r *world.Region, pos cube.Pos) (float32, bool) {
	m, depth, ok := p.liquidIn(r, pos)
	if !ok {
		return 0, false
	}
	if above, _, ok := p.liquidIn(r, pos.Add(cube.Pos{0, 1, 0})); ok && above == m || depth&liquidFalling != 0 {
		return float32(pos[1]) + 1, true
	}
	return float32(pos[1]) + float32(vertLiquidCell-1-depth)/vertLiquidCell, true
}

// liquidIn is the liquid at pos, in either layer, and its depth.
func (p *Player) liquidIn(r *world.Region, pos cube.Pos) (material uint8, depth int32, ok bool) {
	for layer := range uint8(2) {
		b, has := r.Block(pos, layer)
		if !has {
			continue
		}
		m := p.s.reg.Blocks.Properties(b).Material
		if m == registry.MaterialWater || m == registry.MaterialLava {
			d, _ := p.s.reg.Blocks.States(b)["liquid_depth"].(int32)
			return m, d, true
		}
	}
	return 0, 0, false
}
