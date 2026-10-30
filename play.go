package helix

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/network"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// TEMPORARY: until the world and player designs exist, a player who joins
// floats in an empty world, so the networking can be tested in game. None
// of this follows BDS yet; it is replaced as soon as there is a world.

// voidRadius is the chunk radius of the empty world.
const voidRadius = 4

// overworldSections is the number of 16-block sections from y=-64 to 320.
const overworldSections = 24

// emptyChunk is a chunk with no blocks: every section's biome is plains
// (one palette entry, 0 bits per value; network palettes hold zigzag
// varints, plains is 1), then no border blocks.
var emptyChunk = func() []byte {
	b := make([]byte, 0, overworldSections*2+1)
	for range overworldSections {
		b = append(b, 1, 2)
	}
	return append(b, 0)
}()

// play spawns the player in the empty world and reads its packets until it
// leaves.
func (s *Server) play(c *network.Conn) {
	const runtimeID = 1
	spawn := mgl32.Vec3{0.5, 100 + 1.62, 0.5}
	_ = c.WritePacket(&packet.StartGame{
		EntityUniqueID: runtimeID, EntityRuntimeID: runtimeID, PlayerGameMode: 1, PlayerPosition: spawn,
		Generator: 1, WorldGameMode: 1, Difficulty: 1, WorldSpawn: protocol.BlockPos{0, 100, 0},
		AchievementsDisabled: true, MultiPlayerGame: true, LANBroadcastEnabled: true, CommandsEnabled: true,
		PlayerPermissions: packet.PermissionLevelMember, BaseGameVersion: protocol.CurrentVersion,
		WorldName: s.conf.LevelName, PlayerMovementSettings: protocol.PlayerMovementSettings{ServerAuthoritativeBlockBreaking: true},
		MultiPlayerCorrelationID: uuid.NewString(), ServerAuthoritativeInventory: true, GameVersion: protocol.CurrentVersion,
		UseBlockNetworkIDHashes: true, ChatRestrictionLevel: 0,
	})
	_ = c.WritePacket(&packet.ItemRegistry{})
	if c.Flush() != nil {
		return
	}
	for {
		pk, err := c.ReadPacket()
		if err != nil {
			if !isClosed(err) {
				s.log.Debug("read failed", "name", c.Identity().DisplayName, "err", err)
			}
			return
		}
		switch pk := pk.(type) {
		case *packet.RequestChunkRadius:
			r := min(pk.ChunkRadius, voidRadius)
			_ = c.WritePacket(&packet.ChunkRadiusUpdated{ChunkRadius: r})
			_ = c.WritePacket(&packet.NetworkChunkPublisherUpdate{Position: protocol.BlockPos{0, 100, 0}, Radius: uint32(r) * 16})
			for x := -r; x <= r; x++ {
				for z := -r; z <= r; z++ {
					_ = c.WritePacket(&packet.LevelChunk{Position: protocol.ChunkPos{x, z}, RawPayload: emptyChunk})
				}
			}
			_ = c.WritePacket(&packet.PlayStatus{Status: packet.PlayStatusPlayerSpawn})
			if c.Flush() != nil {
				return
			}
		case *packet.SetLocalPlayerAsInitialised:
			s.log.Info("Player spawned", "name", c.Identity().DisplayName)
		}
	}
}
