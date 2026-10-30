package helix

import (
	"fmt"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/network"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// TEMPORARY: until players exist (build step 5), a player who joins gets
// the world's chunks around them, sent by the world package, and nothing
// else: no entity, no inventory, no block changes, nothing saved. Only the
// level.dat values, a saved player's position, the registry packets and
// the chunks are real.

// play spawns the player and reads its packets until it leaves.
func (s *Server) play(c *network.Conn) {
	const runtimeID = 1
	lv := s.level
	worldSpawn := protocol.BlockPos{lv.spawn[0], lv.spawn[1], lv.spawn[2]}
	if r := s.overworld.Range(); int(worldSpawn[1]) < r[0] || int(worldSpawn[1]) > r[1] {
		// BDS stores a height out of range ("find the surface") until it
		// has placed the spawn. Flat worlds know their surface; anywhere
		// else the player starts high up until spawning is built.
		worldSpawn[1] = 100
		if lv.surface != 0 {
			worldSpawn[1] = lv.surface
		}
	}
	spawn := mgl32.Vec3{float32(worldSpawn[0]) + 0.5, float32(worldSpawn[1]) + 1.62, float32(worldSpawn[2]) + 0.5}
	if pos, ok := s.savedPosition(c); ok {
		spawn = pos
	}
	_ = c.WritePacket(&packet.StartGame{
		EntityUniqueID: runtimeID, EntityRuntimeID: runtimeID, PlayerGameMode: lv.gameMode, PlayerPosition: spawn,
		WorldSeed: lv.seed, Generator: lv.generator, WorldGameMode: lv.gameMode, Difficulty: lv.difficulty, WorldSpawn: worldSpawn,
		AchievementsDisabled: true, MultiPlayerGame: true, LANBroadcastEnabled: true, CommandsEnabled: true,
		PlayerPermissions: packet.PermissionLevelMember, BaseGameVersion: protocol.CurrentVersion,
		WorldName: lv.name, PlayerMovementSettings: protocol.PlayerMovementSettings{ServerAuthoritativeBlockBreaking: true},
		MultiPlayerCorrelationID: uuid.NewString(), ServerAuthoritativeInventory: true, GameVersion: protocol.CurrentVersion,
		UseBlockNetworkIDHashes: true, ChatRestrictionLevel: 0, Blocks: s.reg.Blocks.Definitions(),
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

	// The world queues chunks on its tick goroutine, which must not wait
	// for the network: a flusher sends them.
	kick, done := make(chan struct{}, 1), make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-kick:
				_ = c.Flush()
			}
		}
	}()
	flush := func() {
		select {
		case kick <- struct{}{}:
		default:
		}
	}
	v := s.overworld.AddViewer(func(pk *packet.LevelChunk) { _ = c.WritePacket(pk) }, flush)
	defer v.Close()

	var radius int32
	centre := world.ChunkPos(int32(spawn[0]), int32(spawn[2]))
	publish := func(pos mgl32.Vec3) {
		_ = c.WritePacket(&packet.NetworkChunkPublisherUpdate{
			Position: protocol.BlockPos{int32(pos[0]), int32(pos[1]), int32(pos[2])}, Radius: uint32(radius) * 16, //nolint:gosec // radius is positive
		})
		v.Move(centre, radius)
	}
	spawned := false
	for {
		pk, err := c.ReadPacket()
		if err != nil {
			if !isClosed(err) {
				s.log.Info("read failed", "name", c.Identity().DisplayName, "err", err)
			}
			return
		}
		if _, input := pk.(*packet.PlayerAuthInput); !input {
			s.log.Debug("packet from player", "name", c.Identity().DisplayName, "packet", fmt.Sprintf("%T", pk))
		}
		switch pk := pk.(type) {
		case *packet.Disconnect:
			s.log.Info("Player left", "name", c.Identity().DisplayName, "reason", pk.Reason, "message", pk.Message)
		case *packet.RequestChunkRadius:
			radius = max(min(pk.ChunkRadius, s.conf.ViewDistance), 1)
			_ = c.WritePacket(&packet.ChunkRadiusUpdated{ChunkRadius: radius})
			publish(spawn)
			flush()
			if !spawned {
				spawned = true
				go func() {
					select {
					case <-v.Ready():
						_ = c.WritePacket(&packet.PlayStatus{Status: packet.PlayStatusPlayerSpawn})
						flush()
					case <-done:
					}
				}()
			}
		case *packet.PlayerAuthInput:
			spawn = pk.Position
			if cp := world.ChunkPos(int32(math.Floor(float64(pk.Position[0]))), int32(math.Floor(float64(pk.Position[2])))); cp != centre && radius > 0 {
				centre = cp
				publish(pk.Position)
				flush()
			}
		case *packet.SubChunkRequest:
			_ = c.WritePacket(v.SubChunks(pk))
			flush()
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
