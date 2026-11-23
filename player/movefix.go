package player

import (
	"github.com/go-gl/mathgl/mgl32"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Movement corrections and slowdowns: Helix's own rules, beyond BDS.
//
// One correction an input: the phase, speed and vertical checks each only mark the move as cut,
// and one CorrectPlayerMovePrediction with where the move ends after all of them is sent once
// they ran. Two for the same client tick (the speed check's, keeping the client's height, then
// the vertical check's) let a client take the first and keep flying while only its speed was cut.
// After a correction none is sent for the client's round trip and two ticks (at least
// fixQuietMin), as BDS waits between corrections: the inputs the client sent before it took the
// first still start from where it was, and each would be corrected again. Those moves are still
// cut, so the server's player never goes where the client may not.
//
// Slowdowns: a cobweb (0.25 sideways, 0.05 up and down), a sweet berry bush (0.8, 0.75) or
// powder snow (0.9 sideways) touching the player scales the move and stops the speed it carried,
// as the client's own physics does; sneaking scales the push of walking (0.3, Swift Sneak adding
// 0.15 a level). A no slowdown cheat moves as if they were not there and is cut.
//
// Knockback: a push from the server the client has not used after its round trip and a few
// ticks, and big enough to be told from walking, is put on the player by the server, which
// moves it and corrects the client with the push as its speed, so an anti-knockback cheat is
// knocked back anyway.

const (
	// slowWeb, slowWebUp, slowBerry, slowBerryUp and slowPowder are how much of a move a cobweb,
	// a sweet berry bush and powder snow leave, sideways and up or down; slowWeaving what a
	// cobweb leaves a player with Weaving, with room to spare.
	slowWeb, slowWebUp     = 0.25, 0.05
	slowBerry, slowBerryUp = 0.8, 0.75
	slowPowder             = 0.9
	slowWeaving            = 0.5
	// sneakPush is how much of walking's push sneaking leaves, sneakSwift what each Swift Sneak
	// level adds back.
	sneakPush, sneakSwift = 0.3, 0.15
	// enchantSwiftSneak is the leggings enchantment that speeds sneaking, armorLegs the
	// leggings' armour slot.
	enchantSwiftSneak = int16(37)
	armorLegs         = 2
	// knockGrace is how many ticks past its round trip a client may take to use a push, and
	// knockMin the smallest push the server puts on a client that did not.
	knockGrace = 4
	knockMin   = 0.1
	// fixQuietMin is the fewest inputs after a correction that send none, fixQuietExtra how many
	// past the round trip.
	fixQuietMin, fixQuietExtra = 5, 2
)

// moveFix is the correction an input's checks asked for. Region tick.
type moveFix struct {
	pending bool
	// quiet is how many more inputs send no correction.
	quiet int32
	// delta is the speed the client goes on with from the corrected place.
	delta mgl32.Vec3
	// slowFlat and slowUp are how much of the move the blocks around the player leave this
	// input (1 for none).
	slowFlat, slowUp float32
}

// markMoveFix asks for the client to be corrected to where the move ends after every check.
func (p *Player) markMoveFix() {
	p.move.fix.pending = true
}

// sendMoveFix sends the one correction the checks asked for this input, to pos.
func (p *Player) sendMoveFix(r *world.Region, pos mgl32.Vec3, pk *packet.PlayerAuthInput) {
	f := &p.move.fix
	if f.quiet > 0 {
		f.quiet--
	}
	if !f.pending {
		return
	}
	if f.quiet > 0 {
		f.pending, f.delta = false, mgl32.Vec3{}
		return
	}
	f.quiet = max(p.roundTripTicks()+fixQuietExtra, fixQuietMin)
	p.send(&packet.CorrectPlayerMovePrediction{
		PredictionType: packet.PredictionTypePlayer, Position: pos, Delta: f.delta,
		Rotation: mgl32.Vec2{pk.Pitch, pk.Yaw}, OnGround: p.groundUnder(r, pos), Tick: pk.Tick,
	})
	f.pending, f.delta = false, mgl32.Vec3{}
}

// noteSlowdown finds how much the blocks the player touched at from, where its last move ended,
// slow this move: the client finds them after a move and slows the next one.
func (p *Player) noteSlowdown(r *world.Region, from mgl32.Vec3) {
	f := &p.move.fix
	f.slowFlat, f.slowUp = 1, 1
	lo, hi := playerBox(from)
	_, weaving := p.Effect(EffectWeaving)
	blocks := p.s.reg.Blocks
	for x := itemFloor(lo[0]); x <= itemFloor(hi[0]); x++ {
		for y := itemFloor(lo[1]); y <= itemFloor(hi[1]); y++ {
			for z := itemFloor(lo[2]); z <= itemFloor(hi[2]); z++ {
				b, ok := r.Block(cube.Pos{x, y, z}, 0)
				if !ok {
					continue
				}
				switch blocks.Name(b) {
				case nameCobweb:
					if weaving {
						f.slowFlat, f.slowUp = min(f.slowFlat, slowWeaving), min(f.slowUp, slowWeaving)
					} else {
						f.slowFlat, f.slowUp = min(f.slowFlat, slowWeb), min(f.slowUp, slowWebUp)
					}
				case nameBerryBush:
					f.slowFlat, f.slowUp = min(f.slowFlat, slowBerry), min(f.slowUp, slowBerryUp)
				case namePowderSnow:
					f.slowFlat = min(f.slowFlat, slowPowder)
				}
			}
		}
	}
}

// sneakFactor is how much of walking's push the player keeps: all of it unless it sneaks.
func (p *Player) sneakFactor() float32 {
	if !p.Moving(MoveSneaking) {
		return 1
	}
	level := heldEnchant(p.inventory.armor[armorLegs], enchantSwiftSneak)
	return min(sneakPush+sneakSwift*float32(level), 1)
}

// noteKnock remembers a push of v the client must use within its round trip. Region tick.
func (p *Player) noteKnock(v mgl32.Vec3) {
	s := &p.move.speed
	if flatSpeed(v) < knockMin {
		return
	}
	s.knock = v
	s.knockWait = p.roundTripTicks() + knockGrace
}

// roundTripTicks is the connection's round trip in ticks, rounded up.
func (p *Player) roundTripTicks() int32 {
	if p.conn == nil {
		return 0
	}
	rtt := p.conn.Latency()
	if rtt <= 0 {
		return 0
	}
	return int32(min((rtt+world.TickDuration-1)/world.TickDuration, speedPushTicks)) //nolint:gosec // capped at speedPushTicks
}

// enforceKnock puts the push the client did not use on the player: the move goes from from by
// the push as far as the blocks let it, and the client goes on with the push as its speed.
func (p *Player) enforceKnock(r *world.Region, from, to mgl32.Vec3, limit float32) mgl32.Vec3 {
	s := &p.move.speed
	k := s.knock
	s.knock, s.knockWait = mgl32.Vec3{}, 0
	if flatSpeed(k) <= limit+speedSlack {
		// Walking alone gets as far: the client cannot be told to have missed it.
		return to
	}
	lo, hi := p.phaseBox(from)
	got, ok := p.s.sweepMove(r, lo, hi, mgl32.Vec3{k[0], 0, k[2]}, 0)
	if !ok {
		got = mgl32.Vec3{}
	}
	at := mgl32.Vec3{from[0] + got[0], to[1], from[2] + got[2]}
	p.s.log.Debug("knockback not taken, applied", "name", p.name, "from", from, "to", to, "at", at, "push", k)
	s.last, s.pushTicks = flatSpeed(got), 0
	p.markMoveFix()
	p.move.fix.delta = mgl32.Vec3{k[0], k[1], k[2]}
	return at
}

// Fast stop: a client's speed falls off by its friction, and its keys push at most push the
// other way, so each move goes at least the speed it carried less one push along the way the last
// move went, unless a block stops it. A fast stop cheat drops the speed at once (in the air above
// all), and a move that falls short is carried on as far as the physics takes it and corrected.
// Not held: sneaking (edges stop it), climbing, a push from the server, a hit (it slows the
// attacker), soul sand and honey, the blocks that slow (they are held the other way), the other
// ways of moving.

const (
	// stopMin is the least shortfall the fast stop check looks at: below it a client's
	// rounding and the ground's friction stop the player about as fast anyway.
	stopMin = 0.05
)

// fastStop returns where a move that stopped faster than the client's physics lets it must end,
// and true; false when the move is fine. d is the move, carried and push what the speed check
// worked out for it.
func (p *Player) fastStop(r *world.Region, from, to, d mgl32.Vec3, carried, push float32) (mgl32.Vec3, bool) {
	s := &p.move.speed
	least := carried - push - speedSlack
	if least <= stopMin || s.pushTicks > 0 || s.hit || p.move.fix.pending || p.moveMode() != modeWalk ||
		p.Moving(MoveSneaking) || p.move.climbing || p.slowGround(r, from) || p.slowGround(r, to) {
		return to, false
	}
	along := d[0]*s.dir[0] + d[2]*s.dir[1]
	short := least - along
	if short <= 0 {
		return to, false
	}
	if short <= s.bank {
		s.bank -= short
		return to, false
	}
	lo, hi := p.phaseBox(from)
	way := mgl32.Vec3{s.dir[0] * least, 0, s.dir[1] * least}
	if got, ok := p.s.sweepMove(r, lo, hi, way, phaseStep); !ok || !reached(got, way) {
		// A block stops it.
		return to, false
	}
	flat := mgl32.Vec3{d[0] + s.dir[0]*short, 0, d[2] + s.dir[1]*short}
	got, ok := p.s.sweepMove(r, lo, hi, flat, phaseStep)
	if !ok || !reached(got, flat) {
		return to, false
	}
	at := mgl32.Vec3{from[0] + flat[0], to[1], from[2] + flat[2]}
	p.s.log.Debug("move stopped too fast", "name", p.name, "from", from, "to", to, "at", at, "least", least, "along", along)
	s.last = flatSpeed(flat)
	p.markMoveFix()
	p.move.fix.delta[0], p.move.fix.delta[2] = s.dir[0]*carried, s.dir[1]*carried
	return at, true
}

// slowGround reports whether the block under the player whose eyes are at eyes is soul sand or
// honey, which slow walking on them.
func (p *Player) slowGround(r *world.Region, eyes mgl32.Vec3) bool {
	b, ok := r.Block(vecPos(eyes.Sub(mgl32.Vec3{0, eyeHeight + 0.5, 0})), 0)
	if !ok {
		return true
	}
	switch p.s.reg.Blocks.Name(b) {
	case nameSoulSand, nameSoulSoil, nameHoneyBlock:
		return true
	}
	return false
}

// noteMoveDir keeps the way a move of d went, for the fast stop check.
func (p *Player) noteMoveDir(d mgl32.Vec3) {
	if l := flatSpeed(d); l > 1e-3 {
		p.move.speed.dir = mgl32.Vec2{d[0] / l, d[2] / l}
	}
}
