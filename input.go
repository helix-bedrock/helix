package helix

import (
	"sync"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// maxQueuedInput is how many world packets a player may have waiting for
// its region's tick. A client sends one PlayerAuthInput a client tick (20
// a second), so a full queue means a client flooding the server; what
// does not fit is dropped.
const maxQueuedInput = 256

// inputQueue holds a player's world packets, in the order the client sent
// them, from its network goroutine to its region's tick. The two buffers
// are swapped, so a steady stream allocates nothing.
type inputQueue struct {
	mu      sync.Mutex
	in      []packet.Packet
	run     []packet.Packet
	dropped int
}

// push queues pk; false if the queue was full. Network goroutine.
func (q *inputQueue) push(pk packet.Packet) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.in) >= maxQueuedInput {
		q.dropped++
		return false
	}
	q.in = append(q.in, pk)
	return true
}

// take returns the queued packets and empties the queue. The slice is
// valid until the next take. Region tick.
func (q *inputQueue) take() []packet.Packet {
	q.mu.Lock()
	q.run, q.in = q.in, q.run[:0]
	q.mu.Unlock()
	return q.run
}

// handleInput runs the player's queued world packets through the frozen
// handlers, in order, on its region's tick (world.EntityConfig.Input).
func (p *Player) handleInput(*world.Entity) {
	run := p.input.take()
	for i, pk := range run {
		if h := p.s.handlers.handler(pk.ID()); h != nil {
			h(p, pk)
		}
		run[i] = nil
	}
}

// vanillaHandlers registers vanilla's packet handlers and systems. They go
// through the Setup like anyone else's, so each can be replaced or wrapped.
func vanillaHandlers(s *Setup) error {
	if err := Handle(s, handlePlayerAuthInput); err != nil {
		return err
	}
	return s.Movement(applyMovement)
}

// handlePlayerAuthInput is vanilla's PlayerAuthInput handler: it takes the
// packet apart and hands each part to its system. So far only movement;
// block actions and item use come with block breaking and inventory.
func handlePlayerAuthInput(p *Player, pk *packet.PlayerAuthInput) {
	p.s.handlers.movement(p, world.Movement{
		Pos: pk.Position, Rot: world.Rotation{Pitch: pk.Pitch, Yaw: pk.Yaw, HeadYaw: pk.HeadYaw},
		OnGround: pk.InputData.Load(packet.InputFlagVerticalCollision), Tick: pk.Tick,
	})
}

// applyMovement is vanilla's movement: the client's position is taken as
// it is (BDS's checks and corrections come next), and the zones follow the
// player into each new chunk.
func applyMovement(p *Player, m world.Movement) {
	p.entity.Apply(m)
	p.moveZones(m.Pos)
}

// moveZones moves the player's zones when it entered another chunk, and
// tells the client where chunks are published. Runs: anywhere.
func (p *Player) moveZones(pos mgl32.Vec3) {
	p.zoneMu.Lock()
	defer p.zoneMu.Unlock()
	p.pos = pos
	cp := world.ChunkPos(floorInt(pos[0]), floorInt(pos[2]))
	if p.radius <= 0 || (cp == p.centre && p.published) {
		return
	}
	p.centre, p.published = cp, true
	_ = p.conn.WritePacket(&packet.NetworkChunkPublisherUpdate{
		Position: protocol.BlockPos{floorInt(pos[0]), floorInt(pos[1]), floorInt(pos[2])}, Radius: uint32(p.radius) * 16, //nolint:gosec // radius is positive
	})
	p.viewer.Move(cp, p.radius)
	p.flush()
}

// setRadius sets the player's view radius, from RequestChunkRadius, and
// publishes chunks around where it is. Network goroutine.
func (p *Player) setRadius(radius int32) {
	p.zoneMu.Lock()
	p.radius, p.published = radius, false
	pos := p.pos
	p.zoneMu.Unlock()
	p.moveZones(pos)
}

func floorInt(f float32) int32 {
	i := int32(f)
	if f < float32(i) {
		i--
	}
	return i
}
