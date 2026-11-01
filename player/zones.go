package player

import (
	"sync"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/mcdb"
)

// zoneState is where the player's chunk zones are centred and how wide,
// set from the region (movement) and the network goroutine
// (RequestChunkRadius), so it has its own lock.
type zoneState struct {
	mu        sync.Mutex
	pos       mgl32.Vec3
	centre    mcdb.ChunkPos
	radius    int32
	published bool
}

// moveZones moves the player's zones when it entered another chunk, and
// tells the client where chunks are published. Runs: anywhere.
func (p *Player) moveZones(pos mgl32.Vec3) {
	z := &p.zone
	z.mu.Lock()
	defer z.mu.Unlock()
	z.pos = pos
	cp := world.ChunkPos(floorInt(pos[0]), floorInt(pos[2]))
	if z.radius <= 0 || (cp == z.centre && z.published) {
		return
	}
	z.centre, z.published = cp, true
	p.send(&packet.NetworkChunkPublisherUpdate{
		Position: blockPosOf(pos), Radius: uint32(z.radius) * 16, //nolint:gosec // radius is positive
	})
	p.viewer.Move(cp, z.radius)
	p.flush()
}

// setRadius sets the player's view radius, from RequestChunkRadius, and
// publishes chunks around where it is. Network goroutine.
func (p *Player) setRadius(radius int32) {
	z := &p.zone
	z.mu.Lock()
	z.radius, z.published = radius, false
	pos := z.pos
	z.mu.Unlock()
	p.moveZones(pos)
}

// blockPosOf is the block a position is in.
func blockPosOf(pos mgl32.Vec3) protocol.BlockPos {
	return protocol.BlockPos{floorInt(pos[0]), floorInt(pos[1]), floorInt(pos[2])}
}

func floorInt(f float32) int32 {
	i := int32(f)
	if f < float32(i) {
		i--
	}
	return i
}
