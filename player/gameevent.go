package player

import (
	"math"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Game events (vibrations) as BDS 1.26.60: the GameEvent table (initializer 0xCA80031: an ID and
// flags each), the frequency table (0xCA53420, 0x1C3278C) and the checks every vibration listener
// makes before anything else (VibrationListener::handleGameEvent 0xCA54180, _shouldIgnoreVibration):
// sneaking, vibration dampers (wool and carpets, a block component BDS sets at registration) under
// the actor or as the event's block. The world dispatches them (world.Region.PostGameEvent); sculk
// sensors and shriekers hear them (sculk.go).
const (
	gameEventBlockChange      int32 = 3
	gameEventBlockDestroy     int32 = 6
	gameEventBlockPlace       int32 = 9
	gameEventEntityMove       int32 = 22
	gameEventHitGround        int32 = 31
	gameEventItemInteractEnd  int32 = 32
	gameEventItemInteractBeg  int32 = 33
	gameEventProjectileLand   int32 = 40
	gameEventProjectileShoot  int32 = 41
	gameEventResonate1        int32 = 42
	gameEventSculkTouch       int32 = 57
	gameEventTendrilsClicking int32 = 58
	gameEventShriek           int32 = 60
	gameEventSwim             int32 = 63
	// gameEventLast is the highest ID.
	gameEventLast int32 = 65
	// gameEventUnequip is the last ID, whose frequency BDS reads from past its table.
	gameEventUnequip int32 = 65
	// gameEventUnequipFrequency is what BDS's table read gives the unequip event.
	gameEventUnequipFrequency int32 = 0x20001
)

// A game event's flags.
const (
	// gameEventHeard marks a vibration: what sculk sensors hear.
	gameEventHeard uint8 = 1 << iota
	// gameEventSneakQuiet is not heard when the actor (or a projectile's owner) sneaks.
	gameEventSneakQuiet
	// gameEventDampedUnder is not heard when the actor stands on or in a damper.
	gameEventDampedUnder
	// gameEventDampedBlock is not heard when the event's block is a damper.
	gameEventDampedBlock
	// gameEventDampedActor is not heard when the actor dampens vibrations (BDS's vibration damper
	// component, which nothing in Helix has yet).
	gameEventDampedActor
)

// gameEventFlags is each game event's flags, by ID.
func gameEventFlags(id int32) uint8 {
	switch id {
	case 0, gameEventItemInteractBeg, gameEventTendrilsClicking, gameEventShriek:
		return 0
	case gameEventBlockDestroy, gameEventBlockPlace, gameEventProjectileLand:
		return gameEventHeard | gameEventDampedBlock
	case gameEventEntityMove, gameEventHitGround:
		return gameEventHeard | gameEventSneakQuiet | gameEventDampedUnder
	case gameEventItemInteractEnd, gameEventProjectileShoot, gameEventSwim:
		return gameEventHeard | gameEventSneakQuiet
	case gameEventSculkTouch:
		return gameEventHeard | gameEventDampedActor
	}
	if id < 0 || id > gameEventLast {
		return 0
	}
	return gameEventHeard
}

// gameEventFrequencies is BDS's frequency of IDs 1 to 64; the resonate events are 42 to 56.
var gameEventFrequencies = [...]int32{
	10, 10, 11, 9, 9, 12, 9, 10, 13, 2, 9, 10, 10, 8, 8, 4, 7, 15, 5, 6,
	6, 1, 14, 4, 4, 5, 15, 1, 12, 13, 2, 3, 0, 14, 6, 10, 9, 10, 10, 2,
	3, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 0, 6, 0, 3,
	2, 1, 14, 4,
}

// gameEventFrequency is the frequency a sensor reads from an event: what its comparator gives
// and what a calibrated sensor filters on.
func gameEventFrequency(id int32) int32 {
	switch {
	case id == gameEventUnequip:
		return gameEventUnequipFrequency
	case id < 1 || int(id) > len(gameEventFrequencies):
		return 0
	}
	return gameEventFrequencies[id-1]
}

// dampsVibrations reports a vibration damper: the wool blocks and the wool carpets.
func (s *Host) dampsVibrations(block uint32) bool {
	name := s.reg.Blocks.Name(block)
	return strings.HasSuffix(name, "_wool") ||
		strings.HasSuffix(name, "_carpet") && !strings.HasSuffix(name, "moss_carpet")
}

// occludesVibrations reports a block a vibration cannot pass: wool.
func (s *Host) occludesVibrations(block uint32) bool {
	return strings.HasSuffix(s.reg.Blocks.Name(block), "_wool")
}

// postGameEvent tells the listeners near at about the event, caused by src (a *Player, or nil
// for nothing), about block when hasBlock is set. One that no listener would hear is dropped
// here: every listener so far is a vibration listener. Runs: owner of the region.
func (s *Host) postGameEvent(r *world.Region, id int32, at mgl32.Vec3, src *Player, block uint32, hasBlock bool) {
	if s.vibrationIgnored(r, id, src, block, hasBlock) {
		return
	}
	ev := world.GameEvent{ID: id, Pos: at, Block: block, HasBlock: hasBlock}
	if src != nil {
		ev.Source = src
	}
	r.PostGameEvent(ev)
}

// postBlockGameEvent is postGameEvent at the middle of the block at pos, as BDS's block form.
func (s *Host) postBlockGameEvent(r *world.Region, id int32, pos cube.Pos, src *Player, block uint32, hasBlock bool) {
	s.postGameEvent(r, id, blockCenter(pos), src, block, hasBlock)
}

// vibrationIgnored is BDS's _shouldIgnoreVibration, by the event's flags.
func (s *Host) vibrationIgnored(r *world.Region, id int32, src *Player, block uint32, hasBlock bool) bool {
	flags := gameEventFlags(id)
	if flags&gameEventDampedBlock != 0 && hasBlock && s.dampsVibrations(block) {
		return true
	}
	if src == nil {
		return false
	}
	if flags&gameEventSneakQuiet != 0 && src.Moving(MoveSneaking) {
		return true
	}
	if flags&gameEventDampedUnder == 0 {
		return false
	}
	feet := src.feet()
	for _, at := range [...]cube.Pos{standingOn(feet), vecPos(feet)} {
		if b, ok := r.Block(at, 0); ok && s.dampsVibrations(b) {
			return true
		}
	}
	return false
}

// standingOn is the block under feet, as BDS's getBlockPosCurrentlyStandingOn finds it for a
// player standing on top of a block or on a lower one (a sensor, a slab): the block 0.2 under
// the feet.
func standingOn(feet mgl32.Vec3) cube.Pos {
	return vecPos(feet.Sub(mgl32.Vec3{0, standProbe, 0}))
}

// standProbe is how far under the feet the block stood on is looked for.
const standProbe = 0.2

// vibrationSteps is a player's minecraft:game_event_movement_tracking, which BDS gives every mob
// in code (Mob::_initHardCodedComponents, vertical tracking on): moving on the ground, climbing or
// in liquid adds three times the distance moved, and each time it passes the next step an
// entity_move (swim in liquid) is posted at the feet. Players do not flap.
type vibrationSteps struct {
	last    mgl32.Vec3
	walked  float32
	next    float32
	started bool
}

// trackVibrations is GameEventMovementTrackingSystem::_trackMovement for a player after it
// moved, and BDS's landing (a block's onFallOn posts hit_ground at the feet) and standing on
// sculk (onStandOn). Runs: the player's region.
func (p *Player) trackVibrations(r *world.Region, m *MoveInfo) {
	if p.dead() || p.spectating() {
		return
	}
	feet := p.feet()
	st := &p.steps
	if !st.started {
		st.last, st.next, st.started = feet, 1, true
	}
	if m.OnGround && p.move.fall > 0 {
		p.s.postGameEvent(r, gameEventHitGround, feet, p, 0, false)
	}
	if m.OnGround {
		p.standOnSculk(r, standingOn(feet))
	}
	held := p.heldInPlace(r, feet)
	if !m.OnGround && !m.Climbing && !held && !m.InWater {
		return
	}
	d := feet.Sub(st.last)
	vertical := float32(0)
	if m.InWater || m.Climbing || held {
		vertical = 9
	}
	st.walked += float32(math.Sqrt(float64(9*(d[0]*d[0]+d[2]*d[2]) + vertical*d[1]*d[1])))
	st.last = feet
	if st.walked < st.next {
		return
	}
	st.next = float32(math.Floor(float64(st.walked + 1)))
	if m.InWater {
		p.s.postGameEvent(r, gameEventSwim, feet, p, 0, false)
		return
	}
	p.s.postGameEvent(r, gameEventEntityMove, feet, p, 0, false)
}

// heldInPlace reports the blocks BDS counts like ground for movement vibrations: scaffolding,
// powder snow and cobwebs at the feet.
func (p *Player) heldInPlace(r *world.Region, feet mgl32.Vec3) bool {
	b, ok := r.Block(vecPos(feet), 0)
	if !ok {
		return false
	}
	switch p.s.reg.Blocks.Name(b) {
	case "minecraft:scaffolding", "minecraft:powder_snow", "minecraft:web":
		return true
	}
	return false
}
