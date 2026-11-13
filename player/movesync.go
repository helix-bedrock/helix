package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// BDS 1.26.60's movement sync for actors that are not players: clients move the actor
// themselves from the velocity in AddActor, and BDS tells them only where it is, with
// MoveActorDelta, when it moved enough.
const (
	// actorMoveDropped and actorMoveFar are BDS's defaults for a definition's conditional
	// bandwidth optimization (an actor without a definition, such as a falling block, gets
	// these), and actorMoveTicksShare the part of its dropped ticks BDS uses.
	actorMoveDropped    = 10
	actorMoveFar        = 80
	actorMoveTicksShare = 0.75
	// actorMoveMin and actorRestMin are squared distances, actorTurnMin degrees: BDS's limits
	// on when a move is worth sending.
	actorMoveMin = 0.0001
	actorRestMin = 1e-6
	actorTurnMin = 4
	// actorDegreesPerByte is one step of a rotation in a move.
	actorDegreesPerByte = 360.0 / 256
)

// actorMoves is what those who see an actor were last told of its movement: the last move
// sent and the tick each of them last got one. Runs: owner of the actor's entity.
type actorMoves struct {
	pos     mgl32.Vec3
	rot     world.Rotation
	ground  bool
	sentTo  map[*world.Entity]int64
	dropped int32
	far     float32
	// head is set for mobs, whose moves carry the head yaw.
	head bool
}

// start sets where the actor was spawned, which AddActor told its viewers, and how seldom
// far players get its moves.
func (m *actorMoves) start(pos mgl32.Vec3, rot world.Rotation, dropped int32, far float32) {
	m.pos, m.rot, m.dropped, m.far = pos, rot, dropped, far
}

// update is BDS's check of whether a move is worth sending (0xb3c5260, 0x85a4be0): moved
// more than a hundredth of a block, or turned more than 4 degrees, or come to rest.
func (m *actorMoves) update(e *world.Entity, vel mgl32.Vec3) {
	pos, rot := e.Position(), e.Rotation()
	d := pos.Sub(m.pos)
	moved, speed := d.Dot(d), vel.Dot(vel)
	turned := (abs32(float32(rotationByte(rot.Pitch))-float32(rotationByte(m.rot.Pitch))) +
		abs32(float32(rotationByte(rot.Yaw))-float32(rotationByte(m.rot.Yaw)))) * actorDegreesPerByte
	if m.head {
		turned += abs32(float32(rotationByte(rot.HeadYaw))-float32(rotationByte(m.rot.HeadYaw))) * actorDegreesPerByte
	}
	if moved <= actorMoveMin && turned <= actorTurnMin && (moved <= actorRestMin || speed >= actorRestMin) {
		return
	}
	m.send(e)
}

// send is BDS's MoveActorDelta (0x8ce8c80): only the fields that changed since the last one,
// and the head rotation only for mobs. What differs per player is how far they are
// (0x8ce9d60): further off they get it less often, and Ticks says how many ticks it is meant
// to last. It is 0 near by, and 0 for all on the tick it lands or leaves the ground.
func (m *actorMoves) send(e *world.Entity) {
	pos, rot := e.Position(), e.Rotation()
	ground := e.OnGround()
	pk := &packet.MoveActorDelta{EntityRuntimeID: e.RuntimeID(), OnGround: ground}
	if pos[0] != m.pos[0] {
		pk.PositionX = protocol.Option(pos[0])
	}
	if pos[1] != m.pos[1] {
		pk.PositionY = protocol.Option(pos[1])
	}
	if pos[2] != m.pos[2] {
		pk.PositionZ = protocol.Option(pos[2])
	}
	if rotationByte(rot.Pitch) != rotationByte(m.rot.Pitch) {
		pk.RotationX = protocol.Option(rot.Pitch)
	}
	if rotationByte(rot.Yaw) != rotationByte(m.rot.Yaw) {
		pk.RotationY = protocol.Option(rot.Yaw)
	}
	if m.head && rotationByte(rot.HeadYaw) != rotationByte(m.rot.HeadYaw) {
		pk.RotationYHead = protocol.Option(rot.HeadYaw)
	}
	same := ground == m.ground
	m.pos, m.rot, m.ground = pos, rot, ground
	r := e.Region()
	if r == nil {
		return
	}
	now := r.CurrentTick()
	e.EachSeer(func(p *world.Entity) {
		var ticks int64
		if same {
			ticks = moveTicks(m.dropped, m.far, pos.Sub(p.Position()).Len())
		}
		if last, ok := m.sentTo[p]; ok && now < last+ticks {
			return
		}
		if m.sentTo == nil {
			m.sentTo = make(map[*world.Entity]int64)
		}
		m.sentTo[p] = now
		if ticks == 0 {
			p.SendTo(pk)
			return
		}
		far := *pk
		far.Ticks = uint64(ticks)
		p.SendTo(&far)
	})
}

// moveTicks is BDS's count of ticks a move lasts for a player dist away.
func moveTicks(dropped int32, far, dist float32) int64 {
	share := float32(1)
	if far >= dist {
		share = dist / far
	}
	return int64(float32(dropped) * actorMoveTicksShare * share)
}
