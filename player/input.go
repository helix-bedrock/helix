package player

import (
	"sync"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/helix-bedrock/helix/world"
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
	p.tickMaxes()
	p.tickEffects()
	p.tickHurt()
	p.tickShield()
	p.tickSleep()
	p.reseat()
	run := p.input.take()
	for i, pk := range run {
		if h := p.s.handlers.handler(pk.ID()); h != nil {
			h(p, pk)
		}
		run[i] = nil
	}
	p.tickUsing()
	if !p.dead() {
		p.s.game.sys.FoodTick(p)
	}
	p.tickRespawn()
	p.tickPickup()
	p.tickChest()
	p.tickUILeftovers()
	p.s.checkVoid(p.entity)
}

// vanillaHandlers registers vanilla's packet handlers and systems. They go
// through the Setup like anyone else's, so each can be replaced or wrapped.
func vanillaHandlers(s *Setup) error {
	if err := Handle(s, handlePlayerAuthInput); err != nil {
		return err
	}
	if err := Handle(s, handleItemStackRequest); err != nil {
		return err
	}
	if err := Handle(s, handleMobEquipment); err != nil {
		return err
	}
	if err := Handle(s, handleBlockPick); err != nil {
		return err
	}
	if err := Handle(s, handleInventoryTransaction); err != nil {
		return err
	}
	if err := Handle(s, handleInteract); err != nil {
		return err
	}
	if err := Handle(s, handleContainerClose); err != nil {
		return err
	}
	if err := Handle(s, handleToggleCrafterSlot); err != nil {
		return err
	}
	if err := Handle(s, handleRespawn); err != nil {
		return err
	}
	if err := Handle(s, handlePlayerAction); err != nil {
		return err
	}
	if err := Handle(s, handleAnimate); err != nil {
		return err
	}
	if err := vanillaGameplay(s); err != nil {
		return err
	}
	return s.Movement(applyMovement)
}

// handlePlayerAuthInput is vanilla's PlayerAuthInput handler: it takes the
// packet apart and hands each part to its system: block actions first,
// before movement, as BDS does, then the movement states and jump, the
// movement, and the fall and exhaustion that follow it.
func handlePlayerAuthInput(p *Player, pk *packet.PlayerAuthInput) {
	sys := p.s.game.sys
	if !p.dead() {
		if actions, ok := pk.BlockActions.Value(); ok {
			p.handleBlockActions(actions)
		} else {
			p.handleBlockActions(nil)
		}
		sys.MoveFlags(p, pk.InputData)
		if pk.InputData.Load(packet.InputFlagStartJumping) {
			sys.Jump(p)
		}
	}
	p.move.velocity = pk.Delta
	p.move.forward = pk.MoveVector[1]
	if p.vehicle != nil && pk.InputData.Load(packet.InputFlagStartSneaking) {
		letOff(p.vehicle, p)
	}
	collided := pk.InputData.Load(packet.InputFlagVerticalCollision)
	var from mgl32.Vec3
	if p.entity != nil {
		from = p.entity.Position()
	}
	pos := pk.Position
	if p.vehicle != nil {
		pos = p.seatPosition()
	}
	p.s.handlers.movement(p, world.Movement{
		Pos: pos, Rot: world.Rotation{Pitch: pk.Pitch, Yaw: pk.Yaw, HeadYaw: pk.HeadYaw},
		OnGround: collided, Tick: pk.Tick,
	})
	if !p.dead() {
		p.afterMove(from, collided)
	}
}

// moveTo is a movement the server decides.
func moveTo(pos mgl32.Vec3, rot world.Rotation, tick uint64) world.Movement {
	return world.Movement{Pos: pos, Rot: rot, Tick: tick}
}

// applyMovement is vanilla's movement: the client's position is taken as
// it is (BDS's checks and corrections come next), and the zones follow the
// player into each new chunk.
func applyMovement(p *Player, m world.Movement) {
	p.entity.Apply(m)
	p.moveZones(m.Pos)
}
