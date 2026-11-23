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
// flies, hovers, jumps in the air, climbs walls, steps up whole blocks, stands on water or drops
// faster than it falls by sending a position the client's physics never reaches. Each move's
// rise and drop are held between what the client's own physics allows from the vertical speed
// the player had.
//
// Walking, falling and jumping: the last move's speed less gravity (Slow Falling's lower gravity
// while falling, Levitation's pull up), times air drag, both ways. From server-checked ground a
// jump (Jump Boost in it) is allowed, a step up of a slab or stair when the move ends on a block,
// and a bounce as fast as the fall that landed on a slime block or bed. Ladders, vines,
// scaffolding and powder snow allow climbing up and down; cobwebs, honey and berry bushes slow
// falling; a move that ends on the ground may stop a fall. A liquid touching the player but not
// deep enough to swim in allows a jump out of it for a second (a swimmer bobbing at the surface
// goes through it quickly), and no longer: a cheat keeping its feet just under the surface falls.
// Over a bubble column its push is allowed. The other ways of moving have their own limits
// (movemode.go): swimming, gliding, creative flying, a Riptide launch, a held rocket. Pushes from
// the server (knockback, explosions, wind, pistons, pulls) are allowed until the client takes
// them, the first move after the way of moving changed may rise or drop as far as a jump out of
// water, and a teleport frees one move. A move past the limits is moved straight up or down as
// far as they and the blocks allow, and the client is corrected there.

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
	// vertClimb is the most a climb (ladders, vines, scaffolding, powder snow) moves the player up
	// or down each tick, with room to spare.
	vertClimb = 0.25
	// vertShallowRise is the most a player touching a liquid but not swimming may rise: a jump
	// out of water at a block's edge; vertShallowDrop the most it may drop.
	vertShallowRise, vertShallowDrop = 0.35, 1
	// vertShallowTicks is how many moves in a row a player not on the ground may stay in a liquid
	// too shallow to swim in before it is held as in the air.
	vertShallowTicks = 20
	// vertModeChange is the most the first move after the way of moving changed may rise or drop
	// past the rest: a jump out of water at a block's edge.
	vertModeChange = 0.5
	// vertDeep is how far a liquid's surface must be over the feet before the player swims.
	vertDeep = 0.2
	// vertLiquidPush is the most a swimmer's stroke moves it up or down each tick past the drag.
	vertLiquidPush = 0.1
	// vertBubbleUp and vertBubbleDown are the most a bubble column pushes the player up (launched
	// out of its surface) and pulls it down; vertBubbleStep how much a tick adds.
	vertBubbleUp, vertBubbleDown, vertBubbleStep = 1.8, 0.95, 0.1
	// vertGlideLift is the elytra's lift against gravity, vertGlidePull how much of the horizontal
	// speed pulling up turns into rising, vertGlideDive how much of the falling speed a dive gives
	// back.
	vertGlideLift, vertGlidePull, vertGlideDive = 0.06, 0.13, 0.1
	// vertGlideTrade is how much rising pulling up gives for each block a tick of horizontal speed
	// it takes.
	vertGlideTrade = 3.2
	// vertFlyDrag is what a flyer keeps of its vertical speed each tick, vertFlyPush how many times
	// the fly speed (times the vertical fly speed) a tick adds, with room to spare.
	vertFlyDrag, vertFlyPush = 0.6, 6
	// vertSlack is how far a move may go past the limits before it costs anything: float
	// rounding, and the client working gravity out a little differently.
	vertSlack = 0.03
	// vertBank and vertRefill are how far past the limits and slack a player may go in all, and
	// how much comes back each move: a client and server that disagree for a moment about the
	// blocks under the player are not corrected.
	vertBank, vertRefill = 0.2, 0.01
	// vertLiquidCell is a liquid's height in ninths of a block for depth 0 (a source).
	vertLiquidCell = 9
	// vertBedBounce is how much of the fall a bed gives back.
	vertBedBounce = 0.75
	// vertReach is how far beside the player's box a block still counts as touching it: honey
	// walls slow a slide from just beside them.
	vertReach = 0.1
	// vertModeSteady is how many moves the way of moving must have stayed the same before a change
	// earns vertModeChange: a cheat flipping it every move (starting and stopping gliding) earns
	// it once. vertWhyEvery is how many moves pass between two logs of why a high move passed.
	vertModeSteady = 10
	vertWhyEvery   = 20
	// vertJumpDelay is how many moves must pass between two jumps while the jump key stays held,
	// and vertJumpRise the least rise off the ground that counts as a jump.
	vertJumpDelay = 10
	vertJumpRise  = 0.1
)

// vertState is what the vertical check knows of the player's vertical speed. Region tick.
type vertState struct {
	// vy is the vertical speed, in blocks a tick, the player had on its last move.
	vy float32
	// fall is how fast the player was falling when it last landed: a slime block or bed may give
	// it back.
	fall float32
	// mode is the way the player moved on its last move; changed is set when the next move is the
	// first after it was not held, and reset when it is not held either (a teleport).
	mode           moveMode
	changed, reset bool
	// up and down are speeds the server pushed the player with that its client has not used yet,
	// and pushTicks how many more moves they wait.
	up, down  float32
	pushTicks int32
	// bank is how far past the limits the player may still go.
	bank float32
	// liquid is how far a liquid's surface is over the player's feet at the start of the move;
	// shallowTicks how many moves in a row it stayed in a liquid too shallow to swim in.
	liquid       float32
	shallowTicks int32
	// jumpWait is how many more moves the next jump must wait while the jump key stays held.
	jumpWait int32
	// modeTicks is how many moves the way of moving stayed the same; whyWait how many more moves
	// until a high move that passed is logged again.
	modeTicks, whyWait int32
	// glidePull is how much a glide rose past gravity, lift and a dive on its last move: pulling
	// up turned that much horizontal speed (over vertGlideTrade) into rising.
	glidePull float32
}

// vertSurround is what touches the player's box over a move.
type vertSurround struct {
	// climb is set for a climbable block, slow for a block that slows falling, bubble for a bubble
	// column.
	climb, slow, bubble bool
}

// vertMove is one move the vertical check holds: where from and to, and whether the server finds
// ground under each end.
type vertMove struct {
	from, to             mgl32.Vec3
	dy                   float32
	groundFrom, groundTo bool
	mode                 moveMode
	pk                   *packet.PlayerAuthInput
}

// noteVerticalPush allows the player the vertical speed of v, up or down, which the server gave
// it. Region tick.
func (p *Player) noteVerticalPush(v float32) {
	s := &p.move.vert
	if v == 0 {
		return
	}
	if s.pushTicks <= 0 {
		s.up, s.down = 0, 0
	}
	if v > 0 {
		s.up = max(s.up, v)
	} else {
		s.down = max(s.down, -v)
	}
	s.pushTicks = speedPushTicks
}

// noteMotion allows the player to move by v, or back by as much, which the server moved it by
// (a piston): its client's moves on their way started before it. Region tick.
func (p *Player) noteMotion(v mgl32.Vec3) {
	p.notePushSpeed(flatSpeed(v))
	p.noteVerticalPush(v[1])
	p.noteVerticalPush(-v[1])
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
// could have got, after correcting the client.
func (p *Player) verticalCheck(r *world.Region, from, to mgl32.Vec3, pk *packet.PlayerAuthInput) mgl32.Vec3 {
	s := &p.move.vert
	m := vertMove{from: from, to: to, dy: to[1] - from[1], mode: p.moveMode(), pk: pk}
	if s.reset || m.mode == modeFree {
		s.reset, s.changed, s.vy, s.pushTicks, s.fall, s.shallowTicks = false, true, m.dy, 0, 0, 0
		s.mode = m.mode
		return to
	}
	m.groundFrom, m.groundTo = p.groundUnder(r, from), p.groundUnder(r, to)
	if p.jumpTooSoon(&m, p.vertBounce(r, from) > 0) {
		return p.vertCut(r, from, to, 0, pk)
	}
	limit, floor := p.vertLimits(r, &m)
	switched := s.mode != m.mode
	if s.changed || switched && s.modeTicks >= vertModeSteady {
		limit, floor = max(limit, s.vy+vertModeChange, vertModeChange), min(floor, s.vy-vertModeChange)
	}
	if switched {
		s.modeTicks = 0
	} else {
		s.modeTicks++
	}
	s.mode, s.changed = m.mode, false
	limit, floor = p.vertPushes(m.dy, limit, floor)
	s.bank = min(s.bank+vertRefill, vertBank)
	over := max(m.dy-limit, floor-m.dy) - vertSlack
	// The bank covers drops only: on a rise it would let every jump off the ground go higher
	// than a jump, and a fly cheat cut back to the ground hop that high each time it lands.
	if over <= 0 || m.dy-limit <= vertSlack && over <= s.bank {
		if over > 0 {
			s.bank -= over
		}
		p.vertWhy(&m, limit, switched)
		p.vertMoved(m.dy, m.groundFrom, m.groundTo)
		if p.move.fix.slowUp < 1 {
			// Stuck in the block: the speed it carried is gone.
			s.vy = 0
		}
		return to
	}
	p.s.log.Debug("move up or down cut why", "name", p.name, "vy", s.vy, "limit", limit, "floor", floor,
		"groundFrom", m.groundFrom, "groundTo", m.groundTo, "under", p.blockUnderName(r, from), "underTo", p.blockUnderName(r, to),
		"mode", m.mode, "tick", pk.Tick)
	return p.vertCut(r, from, to, min(max(m.dy, floor), limit), pk)
}

// vertPushes widens limit and floor for the pushes from the server the client has not taken yet.
func (p *Player) vertPushes(dy, limit, floor float32) (float32, float32) {
	s := &p.move.vert
	if s.pushTicks <= 0 {
		return limit, floor
	}
	pushedUp, pushedDown := max(limit, s.up), min(floor, -s.down)
	if dy > limit+vertSlack && dy <= pushedUp+vertSlack || dy < floor-vertSlack && dy >= pushedDown-vertSlack {
		// The client took the push now.
		s.pushTicks = 0
	} else {
		s.pushTicks--
	}
	return pushedUp, pushedDown
}

// vertMoved keeps the vertical speed and the landing speed of a move the check let through.
func (p *Player) vertMoved(dy float32, groundFrom, groundTo bool) {
	s := &p.move.vert
	s.glidePull = 0
	if s.mode == modeGlide {
		vy := s.vy
		s.glidePull = max(dy-(vy-vertGravity+vertGlideLift+vertGlideDive*max(-vy, 0))*vertDrag, 0)
	}
	if groundTo && dy <= 0 {
		if !groundFrom {
			// Landed: the speed it would have fallen at, which a slime block gives back.
			s.fall = max(-(s.vy-vertGravity)*vertDrag, 0)
		} else {
			s.fall = 0
		}
		s.vy = 0
		return
	}
	s.vy = dy
}

// vertCut moves the player from from by dy straight up or down, no further than the blocks let
// it, corrects the client there and returns it.
func (p *Player) vertCut(r *world.Region, from, to mgl32.Vec3, dy float32, pk *packet.PlayerAuthInput) mgl32.Vec3 {
	s := &p.move.vert
	lo, hi := p.phaseBox(from)
	got, ok := p.s.sweepMove(r, lo, hi, mgl32.Vec3{0, dy, 0}, 0)
	if !ok {
		got = mgl32.Vec3{}
	}
	at := from.Add(got)
	s.vy = got[1]
	p.s.log.Debug("move up or down cut", "name", p.name, "from", from, "to", to, "at", at, "dy", to[1]-from[1], "allowed", dy, "tick", pk.Tick)
	p.markMoveFix()
	p.move.fix.delta[1] = s.vy
	p.move.velocity = mgl32.Vec3{}
	p.move.speed.last = 0
	return at
}

// vertLimits are the most the player may rise this move (limit) and the lowest it may go
// (floor, negative for a drop), by the way it moves. The blocks around are looked at only for a
// move past the rest.
func (p *Player) vertLimits(r *world.Region, m *vertMove) (limit, floor float32) {
	s := &p.move.vert
	switch m.mode {
	case modeGlide:
		limit, floor = p.glideLimits()
	case modeFly:
		limit, floor = p.flyLimits()
	case modeLiquid:
		limit = max(s.vy, 0)*modeLiquidDrag + vertLiquidPush
		floor = min(s.vy, 0)*modeLiquidDrag - vertLiquidPush
		if m.pk.InputData.Load(packet.InputFlagHorizontalCollision) {
			// A swimmer against a wall climbs out of the water.
			limit = max(limit, vertShallowRise)
		}
	default:
		limit, floor = p.fallLimits()
		if slow := p.move.fix.slowUp; slow < 1 {
			// A cobweb or berry bush scales the move: a no slowdown cheat falls through at full
			// speed.
			limit, floor = limit*slow, floor*slow
		}
	}
	if (m.mode == modeWalk || m.mode == modeLiquid) && m.groundFrom {
		// Only from the ground: in the air a jump's room would let a hover (no fall at all) by.
		limit = max(limit, p.groundLimit(r, m))
	}
	if m.groundTo {
		// Landing stops a fall, standing keeps the player where it is.
		limit = max(limit, 0)
	}
	if p.move.boost.rocketTicks > 0 {
		limit, floor = max(limit, modeRocketSpeed), min(floor, -modeRocketSpeed)
	}
	if p.spin.active {
		limit = max(limit, s.vy+p.spin.launch+modeRiptideLift)
		floor = min(floor, s.vy-p.spin.launch)
	}
	limit, floor = p.vertShallow(r, m, limit, floor)
	if m.dy >= floor-vertSlack && m.dy <= limit+vertSlack || m.mode == modeGlide || m.mode == modeFly {
		// Within the limits already: the blocks around need not be looked at.
		return limit, floor
	}
	sur := p.vertSurroundings(r, m.from, m.to)
	if p.move.climbing || sur.climb {
		limit, floor = max(limit, vertClimb), min(floor, -vertClimb)
	}
	if sur.slow {
		limit = max(limit, 0)
	}
	if sur.bubble {
		limit = max(limit, min(max(s.vy, 0)+vertBubbleStep, vertBubbleUp))
		floor = min(floor, max(min(s.vy, 0)-vertBubbleStep, -vertBubbleDown))
	}
	return limit, floor
}

// fallLimits are the rise and drop of a player falling or jumping through the air: gravity
// (Slow Falling's while falling) and drag, Levitation's pull.
func (p *Player) fallLimits() (limit, floor float32) {
	vy := p.move.vert.vy
	gravity := float32(vertGravity)
	if _, ok := p.Effect(EffectSlowFalling); ok && vy <= 0 {
		gravity = vertSlowGravity
	}
	limit = (vy - gravity) * vertDrag
	floor = limit
	if amp, ok := p.Effect(EffectLevitation); ok {
		pull := vertLevitation * float32(amp+1)
		lev := (vy + (pull-vy)*vertLevitationPull) * vertDrag
		limit, floor = max(limit, lev), min(floor, lev)
	}
	return limit, floor
}

// glideLimits are the rise and drop of a gliding player: gravity less the elytra's lift, what
// pulling up turns from the horizontal speed into rising, and what a dive gives back.
func (p *Player) glideLimits() (limit, floor float32) {
	vy := p.move.vert.vy
	h := p.move.speed.last
	limit = (vy - vertGravity + vertGlideLift + vertGlidePull*h + vertGlideDive*max(-vy, 0)) * vertDrag
	floor = (vy - vertGravity) * vertDrag
	return limit, floor
}

// flyLimits are the rise and drop of a creative flyer: drag and one tick of the fly speed.
func (p *Player) flyLimits() (limit, floor float32) {
	vy := p.move.vert.vy
	push := max(p.abilities.Speed(AbilityFlySpeed), 0) * max(p.abilities.Speed(AbilityVerticalFlySpeed), 1) * vertFlyPush
	return max(vy, 0)*vertFlyDrag + push, min(vy, 0)*vertFlyDrag - push
}

// groundLimit is the most a player on the ground may rise: a jump, a bounce, a step onto a block.
func (p *Player) groundLimit(r *world.Region, m *vertMove) float32 {
	if !m.groundFrom {
		return 0
	}
	jump := float32(vertJump)
	if amp, ok := p.Effect(EffectJumpBoost); ok {
		jump += vertJumpBoost * float32(amp+1)
	}
	limit := max(jump, p.vertBounce(r, m.from))
	if m.groundTo {
		limit = max(limit, phaseStep)
	}
	return limit
}

// vertShallow widens limit and floor for a player touching a liquid too shallow to swim in, not
// on the ground, for vertShallowTicks moves in a row.
func (p *Player) vertShallow(r *world.Region, m *vertMove, limit, floor float32) (float32, float32) {
	s := &p.move.vert
	if m.mode != modeWalk || s.liquid <= 0 && p.liquidDepth(r, m.to) <= 0 {
		s.shallowTicks = 0
		return limit, floor
	}
	if m.groundFrom || p.move.climbing {
		s.shallowTicks = 0
	} else {
		s.shallowTicks++
	}
	if s.shallowTicks > vertShallowTicks {
		return limit, floor
	}
	return max(limit, vertShallowRise), min(floor, -vertShallowDrop)
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
	// A bubble column under the feet pushes a player just over its surface.
	lo[1]--
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
				case nameBubbleColumn:
					sur.bubble = true
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
				top, ok := p.liquidTop(r, cube.Pos{x, y, z})
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

// jumpTooSoon reports whether m is a jump less than vertJumpDelay moves after the last one with
// the jump key held all along: the client waits that long between jumps while the key stays down
// (no jump delay cheats do not). Letting the key go clears the wait. Only a rise off the ground
// that nothing else explains counts: not a slime block's or bed's bounce, a push, a climb, water, a step onto a block.
func (p *Player) jumpTooSoon(m *vertMove, bounce bool) bool {
	s := &p.move.vert
	held := m.pk.InputData.Load(packet.InputFlagJumpDown)
	if !held {
		s.jumpWait = 0
	} else if s.jumpWait > 0 {
		s.jumpWait--
	}
	if m.mode != modeWalk || !m.groundFrom || m.groundTo || m.dy <= vertJumpRise || s.pushTicks > 0 ||
		bounce || s.liquid > 0 || p.move.climbing || p.move.boost.rocketTicks > 0 || p.spin.active {
		return false
	}
	if s.vy > 0 {
		// Still rising from the last move: the same jump passing a block's top on its way up
		// onto it, not a new one.
		return false
	}
	if held && s.jumpWait > 0 {
		p.s.log.Debug("jump too soon cut", "name", p.name, "wait", s.jumpWait)
		return true
	}
	s.jumpWait = vertJumpDelay
	return false
}

// vertWhy logs, at debug level and at most every vertWhyEvery moves, what let a walking move rise
// past plain gravity: the ground, a way of moving changing, a push, a liquid, a climb. A cheat
// that hovers and is not cut shows which allowance it rides on.
func (p *Player) vertWhy(m *vertMove, limit float32, switched bool) {
	s := &p.move.vert
	if s.whyWait > 0 {
		s.whyWait--
		return
	}
	if m.mode != modeWalk && m.mode != modeGlide {
		return
	}
	plain, _ := p.fallLimits()
	if m.dy <= plain+vertSlack+s.bank {
		return
	}
	s.whyWait = vertWhyEvery
	p.s.log.Debug("move up passed", "name", p.name, "from", m.from, "to", m.to, "dy", m.dy, "plain", plain, "limit", limit,
		"groundFrom", m.groundFrom, "groundTo", m.groundTo, "switched", switched, "gliding", p.Gliding(),
		"pushTicks", s.pushTicks, "up", s.up, "liquid", s.liquid, "shallowTicks", s.shallowTicks,
		"climbing", p.move.climbing, "bank", s.bank, "tick", m.pk.Tick)
}

// blockUnderName is the name of the block right under the feet of the player whose eyes are at
// eyes, for the debug log.
func (p *Player) blockUnderName(r *world.Region, eyes mgl32.Vec3) string {
	b, ok := r.Block(vecPos(eyes.Sub(mgl32.Vec3{0, eyeHeight + 0.01, 0})), 0)
	if !ok {
		return "unloaded"
	}
	return p.s.reg.Blocks.Name(b)
}
