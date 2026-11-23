package player

import (
	"github.com/go-gl/mathgl/mgl32"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Speed check: Helix's own rule, beyond BDS. Helix takes the client's position, so a speed or
// bhop cheat moves as fast as it likes. Each move's horizontal length is held to what the
// client's own physics can reach from the speed the player had: the last move slowed by
// friction (the block under it on the ground, air otherwise), plus one tick of walking push
// from the movement attribute (Speed and Slowness in it), sprinting always counted, plus a
// sprint jump's boost when it jumps off the ground, and Soul Speed's boost on soul sand and soul
// soil. Knockback, explosions, pulls, pistons and teleports raise the speed the player may have.
// The other ways of moving have their own limits (movemode.go): in water or lava (once the
// surface is well over the feet, so a cheat walking on the surface is held as on land) the
// liquid's drag and a swimming push, Depth Strider in it; gliding, the elytra's drag and what
// a dive or a rocket adds; creative flying, the air's drag and the fly speed ability; a Riptide
// launch adds its strength. Only spectators, the dead and riders are not held here (vehicles
// have their own check, vehicle.go). A move past the limit is cut to it and the client is
// corrected there.

const (
	// speedAirFriction is what the air leaves of the speed each tick.
	speedAirFriction = 0.91
	// speedAirPush is the push in the air each tick, sprinting.
	speedAirPush = 0.02 * speedSprint
	// speedSprint is how much sprinting adds to the walking push.
	speedSprint = 1.3
	// speedGroundPush is the walking push factor on the ground: the movement attribute times this
	// over the cube of the ground's friction times speedAirFriction.
	speedGroundPush = 0.16277136
	// speedJumpBoost is the push a sprint jump adds.
	speedJumpBoost = 0.2
	// speedMinFriction keeps the ground's friction sane for blocks that give none.
	speedMinFriction = 0.5
	// speedSlack is how far, in blocks, a move may go past the limit before it costs anything:
	// float rounding and block edges the client sees a tick sooner or later.
	speedSlack = 0.03
	// speedBank and speedRefill are how far past the limit and slack a player may go in all, and
	// how much of that comes back each move: a client and server that disagree for a moment about
	// the ground under the player are not corrected.
	speedBank, speedRefill = 0.6, 0.02
	// speedTeleportCarry is the speed allowed right after a teleport or a dimension change.
	speedTeleportCarry = 1
	// speedPushTicks is how many moves a push from the server stays allowed before the client
	// uses it: it arrives a round trip later.
	speedPushTicks = 40
	// speedSoulSpeed and speedSoulSpeedLevel are Soul Speed's boost to the movement attribute on
	// soul sand and soul soil: 0.03 times 1 plus 0.35 a level.
	speedSoulSpeed, speedSoulSpeedLevel = 0.03, 0.35
	// speedWalkFriction is the ground's friction for the push under Depth Strider: a plain block's.
	speedWalkFriction = 0.6 * speedAirFriction
)

// speedState is what the speed check knows of the player's horizontal speed. Region tick.
type speedState struct {
	// last is the speed, in blocks a tick, the player may have had on its last move.
	last float32
	// push is a speed the server gave the player (knockback, a pull, a teleport) that its
	// client has not used yet, and pushTicks how many more moves it waits.
	push      float32
	pushTicks int32
	// bank is how far past the limit the player may still go.
	bank float32
	// knock is a push the client must use within knockWait more moves, or the server puts it on
	// the player (movefix.go).
	knock     mgl32.Vec3
	knockWait int32
	// dir is the way the last move went, flat and of length 1, for the fast stop check; hit is
	// set when the player attacked since its last move.
	dir mgl32.Vec2
	hit bool
}

// notePush allows the player the horizontal speed of v, which the server gave it. Region tick.
func (p *Player) notePush(v mgl32.Vec3) {
	p.notePushSpeed(flatSpeed(v))
	p.noteVerticalPush(v[1])
	p.noteKnock(v)
}

// notePushSpeed allows the player a horizontal speed of v. Region tick.
func (p *Player) notePushSpeed(v float32) {
	s := &p.move.speed
	if s.pushTicks <= 0 || v > s.push {
		s.push = v
	}
	s.pushTicks = speedPushTicks
}

// speedCheck returns where the move from from to to may end: to itself, or as far along it as
// the player's speed reaches, after correcting the client.
func (p *Player) speedCheck(r *world.Region, from, to mgl32.Vec3, pk *packet.PlayerAuthInput) mgl32.Vec3 {
	s := &p.move.speed
	d := to.Sub(from)
	dist := flatSpeed(d)
	if p.moveMode() == modeFree {
		s.last, s.pushTicks = dist, 0
		return to
	}
	friction, push := p.speedPhysics(r, from, d[1] > 0, pk)
	carried := s.last * friction
	limit := max(carried+push, p.boostSpeed(carried))
	stuck := p.move.fix.slowFlat < 1
	if stuck {
		// A cobweb, berry bush or powder snow scales the move and stops what it carried.
		limit *= p.move.fix.slowFlat
	}
	if s.pushTicks > 0 {
		pushed := max(s.push, carried) + push
		if dist > limit+speedSlack && dist <= pushed+speedSlack {
			// The client took the push now.
			s.pushTicks, s.knock, s.knockWait = 0, mgl32.Vec3{}, 0
		} else {
			s.pushTicks--
			if s.knockWait > 0 {
				if s.knockWait--; s.knockWait == 0 {
					return p.enforceKnock(r, from, to, limit)
				}
			}
		}
		limit = max(limit, pushed)
	}
	s.bank = min(s.bank+speedRefill, speedBank)
	over := dist - limit - speedSlack
	if over <= 0 || over <= s.bank {
		s.bank -= max(over, 0)
		s.last = min(dist, limit)
		if stuck {
			s.last = 0
		}
		if !stuck {
			if at, cut := p.fastStop(r, from, to, d, carried, push); cut {
				s.hit = false
				return at
			}
		}
		s.hit = false
		p.noteMoveDir(d)
		return to
	}
	s.hit = false
	p.noteMoveDir(d)
	s.last = limit
	if stuck {
		s.last = 0
	}
	scale := limit / dist
	at := mgl32.Vec3{from[0] + d[0]*scale, to[1], from[2] + d[2]*scale}
	p.s.log.Debug("move too fast cut", "name", p.name, "from", from, "to", to, "at", at, "dist", dist, "limit", limit)
	p.markMoveFix()
	p.move.velocity = mgl32.Vec3{}
	return at
}

// speedFree reports whether the player moves some other way than walking, falling and jumping:
// the speed check is not held or holds it by that way's own rules.
func (p *Player) speedFree() bool { return p.moveMode() != modeWalk }

// speedPhysics returns what is left of the player's speed after one tick, and the most its
// client's push may add this tick, from where it was at from (its eyes) and whether it stood.
// A jump's boost counts only on a move that rises: a cheat claiming the ground under it and a
// jump every tick gets none.
func (p *Player) speedPhysics(r *world.Region, from mgl32.Vec3, rising bool, pk *packet.PlayerAuthInput) (friction, push float32) {
	switch p.moveMode() {
	case modeLiquid:
		return p.liquidPhysics()
	case modeGlide:
		return p.glidePhysics()
	case modeFly:
		return p.flyPhysics()
	}
	if !p.move.onGround {
		return speedAirFriction, speedAirPush
	}
	under := vecPos(from.Sub(mgl32.Vec3{0, eyeHeight + 0.5, 0}))
	ground := max(p.s.frictionOf(r, under), speedMinFriction)
	if b, ok := r.Block(under, 0); ok && len(p.s.reg.Blocks.Properties(b).Collision) == 0 {
		// Standing on the top of a block whose box is lower (a slab, a path): its own block.
		ground = max(ground, p.s.frictionOf(r, under.Add(cube.Pos{0, 1, 0})))
	}
	friction = ground * speedAirFriction
	walk := (max(p.attributes.get(attrMovement), 0) + p.soulSpeed(r, under)) * speedSprint * p.sneakFactor()
	push = walk * speedGroundPush / (friction * friction * friction)
	if rising && (pk.InputData.Load(packet.InputFlagStartJumping) || pk.InputData.Load(packet.InputFlagJumping)) {
		push += speedJumpBoost
	}
	return friction, push
}

// soulSpeed is what Soul Speed on the player's boots adds to its movement attribute on the block
// at under: soul sand and soul soil only.
func (p *Player) soulSpeed(r *world.Region, under cube.Pos) float32 {
	level := heldEnchant(p.inventory.armor[armorFeet], enchantSoulSpeed)
	if level <= 0 {
		return 0
	}
	b, ok := r.Block(under, 0)
	if !ok {
		return 0
	}
	if name := p.s.reg.Blocks.Name(b); name != nameSoulSand && name != nameSoulSoil {
		return 0
	}
	return speedSoulSpeed * (1 + speedSoulSpeedLevel*float32(level))
}

// walkPush is one tick of sprinting push on a plain block, for Depth Strider.
func (p *Player) walkPush() float32 {
	f := float32(speedWalkFriction)
	return max(p.attributes.get(attrMovement), 0) * speedSprint * speedGroundPush / (f * f * f)
}
