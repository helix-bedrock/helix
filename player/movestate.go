package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// MoveFlag is one of the movement states a player is in, as BDS keeps it
// in the player's actor flags and other players see it.
type MoveFlag uint8

// The movement flags.
const (
	MoveSneaking MoveFlag = iota
	MoveSprinting
	MoveSwimming
	MoveGliding
	MoveCrawling
	moveFlagCount
)

// moveFlagInputs are each flag's start and stop input flags in
// PlayerAuthInput, and the actor flag BDS sets for it.
var moveFlagInputs = [moveFlagCount]struct {
	start, stop int
	actor       uint32
}{
	MoveSneaking:  {packet.InputFlagStartSneaking, packet.InputFlagStopSneaking, protocol.EntityDataFlagSneaking},
	MoveSprinting: {packet.InputFlagStartSprinting, packet.InputFlagStopSprinting, protocol.EntityDataFlagSprinting},
	MoveSwimming:  {packet.InputFlagStartSwimming, packet.InputFlagStopSwimming, protocol.EntityDataFlagSwimming},
	MoveGliding:   {packet.InputFlagStartGliding, packet.InputFlagStopGliding, protocol.EntityDataFlagGliding},
	MoveCrawling:  {packet.InputFlagStartCrawling, packet.InputFlagStopCrawling, protocol.EntityDataFlagCrawling},
}

// MoveInfo is one movement of a player, as the movement systems after it
// see it. Positions are eye positions, as the client sends them.
type MoveInfo struct {
	From, To mgl32.Vec3
	// OnGround is set when the player collided vertically while not
	// moving up.
	OnGround bool
	// InWater is set when water fills the block at the player's feet or
	// eyes; HeadInWater when it fills the block at its eyes.
	InWater, HeadInWater bool
	// Climbing is set when the block at the player's feet is climbable.
	Climbing bool
}

// Moving reports whether the player is in movement state f. Runs: the
// player's region.
func (p *Player) Moving(f MoveFlag) bool { return f < moveFlagCount && p.move.flags&(1<<f) != 0 }

// SetMoving puts the player in or out of movement state f and shows the
// change to the players who see it. Runs: the player's region.
func (p *Player) SetMoving(f MoveFlag, on bool) {
	if f >= moveFlagCount || p.Moving(f) == on {
		return
	}
	p.move.flags ^= 1 << f
	r := p.entity.Region()
	if r == nil {
		return
	}
	// The client set the flag itself when it asked; the others learn it
	// from the player's actor data.
	r.Broadcast(p.blockPos(), &packet.SetActorData{
		EntityRuntimeID: p.runtimeID,
		EntityMetadata:  protocol.EntityMetadata{protocol.EntityDataKeyFlags: p.actorFlags()},
		Tick:            p.entity.Tick(),
	}, p.viewer)
}

// FallDistance returns how far the player has fallen since it last stood
// or swam. Runs: the player's region.
func (p *Player) FallDistance() float32 { return p.move.fall }

// SetFallDistance sets the player's fall distance. Runs: the player's
// region.
func (p *Player) SetFallDistance(d float32) { p.move.fall = d }

// moveState is the player's movement state on its region.
type moveState struct {
	flags uint8
	fall  float32
}

// actorFlags are the player's actor flags as other players see them.
func (p *Player) actorFlags() int64 {
	f := int64(1<<protocol.EntityDataFlagHasGravity | 1<<protocol.EntityDataFlagBreathing |
		1<<protocol.EntityDataFlagHasCollision | 1<<protocol.EntityDataFlagShowName | 1<<protocol.EntityDataFlagAlwaysShowName)
	for i, in := range moveFlagInputs {
		if p.move.flags&(1<<i) != 0 {
			f |= 1 << in.actor
		}
	}
	return f
}

// vanillaMoveFlags takes the player's movement states from its input's
// start and stop flags. BDS decides them in its own simulation of the
// player's movement; Helix takes the client's movement as it is (the
// simulation belongs to the anti-cheat module), so it takes the states the
// client's simulation reached too.
func vanillaMoveFlags(p *Player, in protocol.InputFlags) {
	for f, fl := range moveFlagInputs {
		switch {
		case in.Load(fl.stop):
			p.SetMoving(MoveFlag(f), false)
		case in.Load(fl.start):
			p.SetMoving(MoveFlag(f), true)
		}
	}
}

// vanillaJump is BDS's JumpExhaustion: a jump costs the sprint jump
// exhaustion while sprinting, else the jump exhaustion; nothing in
// creative.
func vanillaJump(p *Player) {
	if p.creative() {
		return
	}
	g := p.s.game
	cost := g.sys.Exhaustion.Jump
	if p.Moving(MoveSprinting) {
		cost = g.sys.Exhaustion.SprintJump
	}
	g.sys.Exhaust(p, cost)
}

// afterMove runs the systems that follow a movement: fall distance and the
// exhaustion of moving, in BDS's order.
func (p *Player) afterMove(from mgl32.Vec3, collided bool) {
	if p.entity == nil {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	to := p.entity.Position()
	m := MoveInfo{From: from, To: to, OnGround: collided && to[1] <= from[1]}
	feet := vecPos(to.Sub(mgl32.Vec3{0, eyeHeight, 0}))
	eyes := vecPos(to)
	m.HeadInWater = p.water(r, eyes)
	m.InWater = m.HeadInWater || p.water(r, feet)
	if b, ok := r.Block(feet, 0); ok {
		m.Climbing = p.s.reg.Blocks.Properties(b).Climbable
	}
	sys := p.s.game.sys
	sys.Fall(p, &m)
	if cost := sys.MoveCost(p, &m); cost > 0 {
		sys.Exhaust(p, cost)
	}
}

// water reports whether either layer of the block at pos is water.
func (p *Player) water(r *world.Region, pos cube.Pos) bool {
	for layer := range uint8(2) {
		if b, ok := r.Block(pos, layer); ok && p.s.reg.Blocks.Properties(b).Material == registry.MaterialWater {
			return true
		}
	}
	return false
}

// vecPos is the block a position is in.
func vecPos(v mgl32.Vec3) cube.Pos {
	return cube.Pos{int(math.Floor(float64(v[0]))), int(math.Floor(float64(v[1]))), int(math.Floor(float64(v[2])))}
}

// vanillaMoveCost is BDS's FoodExhaustionSystem: with the head in water
// the distance moved costs the swim exhaustion, in water the horizontal
// distance does, and on the ground the horizontal distance costs the
// sprint or walk exhaustion. Nothing in creative or in the air.
func vanillaMoveCost(p *Player, m *MoveInfo) float32 {
	if p.creative() {
		return 0
	}
	ex := &p.s.game.sys.Exhaustion
	d := m.To.Sub(m.From)
	flat := float32(math.Sqrt(float64(d[0]*d[0] + d[2]*d[2])))
	switch {
	case m.HeadInWater:
		return d.Len() * ex.Swim
	case m.InWater:
		return flat * ex.Swim
	case m.OnGround && p.Moving(MoveSprinting):
		return flat * ex.Sprint
	case m.OnGround:
		return flat * ex.Walk
	}
	return 0
}
