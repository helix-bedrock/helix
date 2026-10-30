package helix

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/network"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// TEMPORARY: until the world and player designs exist, a player who joins
// floats in an empty world, so the networking can be tested in game. Only
// the level.dat values (name, seed, game mode, difficulty, spawn), a saved
// player's position and the registry packets are real; nothing else
// follows BDS yet, and nothing is saved. It is replaced as soon as there is a world.

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
	lv := s.level
	worldSpawn := protocol.BlockPos{lv.spawn[0], lv.spawn[1], lv.spawn[2]}
	if worldSpawn[1] < -64 || worldSpawn[1] > 320 {
		// BDS stores a height out of range ("find the surface") until it
		// has placed the spawn; with no terrain there is no surface.
		worldSpawn[1] = 100
	}
	spawn := mgl32.Vec3{float32(worldSpawn[0]) + 0.5, float32(worldSpawn[1]) + 1.62, float32(worldSpawn[2]) + 0.5}
	if pos, ok := s.savedPosition(c); ok {
		spawn = pos
	}
	_ = c.WritePacket(&packet.StartGame{
		EntityUniqueID: runtimeID, EntityRuntimeID: runtimeID, PlayerGameMode: lv.gameMode, PlayerPosition: spawn,
		WorldSeed: lv.seed, Generator: 1, WorldGameMode: lv.gameMode, Difficulty: lv.difficulty, WorldSpawn: worldSpawn,
		AchievementsDisabled: true, MultiPlayerGame: true, LANBroadcastEnabled: true, CommandsEnabled: true,
		PlayerPermissions: packet.PermissionLevelMember, BaseGameVersion: protocol.CurrentVersion,
		WorldName: lv.name, PlayerMovementSettings: protocol.PlayerMovementSettings{ServerAuthoritativeBlockBreaking: true},
		MultiPlayerCorrelationID: uuid.NewString(), ServerAuthoritativeInventory: true, GameVersion: protocol.CurrentVersion,
		UseBlockNetworkIDHashes: true, ChatRestrictionLevel: 0,
	})
	// The registry packets, in the order BDS 1.26.60 sends them (other
	// join packets between them are not sent yet).
	_ = c.WritePacket(s.reg.Items.RegistryPacket())
	_ = c.WritePacket(s.reg.Biomes.DefinitionPacket())
	_ = c.WritePacket(s.reg.EntityTypes.IdentifiersPacket())
	_ = c.WritePacket(s.reg.Creative.Packet())
	_ = c.WritePacket(s.reg.Crafting.Packet())
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
			centre := protocol.BlockPos{int32(spawn[0]), int32(spawn[1]), int32(spawn[2])}
			_ = c.WritePacket(&packet.NetworkChunkPublisherUpdate{Position: centre, Radius: uint32(r) * 16})
			cx, cz := centre[0]>>4, centre[2]>>4
			for x := cx - r; x <= cx+r; x++ {
				for z := cz - r; z <= cz+r; z++ {
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

// savedPosition returns the position the player was saved at, if the
// player store has them. The saved Pos is used as it is; whether BDS saves
// the feet or the eyes is settled with the player design.
func (s *Server) savedPosition(c *network.Conn) (mgl32.Vec3, bool) {
	id := c.Identity()
	data, ok, err := s.playerStore.LoadPlayer(playerIDs(id.Identity, c.Client().SelfSignedID, id.XUID))
	if err != nil {
		s.log.Error("load player", "name", id.DisplayName, "err", err)
		return mgl32.Vec3{}, false
	}
	if !ok {
		return mgl32.Vec3{}, false
	}
	pos, _ := data["Pos"].([]any)
	if len(pos) != 3 {
		return mgl32.Vec3{}, false
	}
	var v mgl32.Vec3
	for i, p := range pos {
		f, ok := p.(float32)
		if !ok {
			return mgl32.Vec3{}, false
		}
		v[i] = f
	}
	return v, true
}
