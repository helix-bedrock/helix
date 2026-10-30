package helix

import (
	"fmt"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/network"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/mcdb"
)

// Player is a player online. It is three parts (API_DESIGN.md, "Players and
// entities"): the session (the connection, identity and skin, safe from
// anywhere), the entity in the world (owned by its region) and the profile
// (server-wide facts, owned by the global owner).
type Player struct {
	s    *Server
	conn *network.Conn

	// Session: fixed once the player joined. Runs: anywhere.
	uuid      uuid.UUID
	name      string
	xuid      string
	skin      protocol.Skin
	ids       mcdb.PlayerIDs
	uniqueID  int64
	runtimeID uint64

	// Entity: the player's body in the world.
	entity *world.Entity
	viewer *world.Viewer

	// input carries world packets from the network goroutine to the
	// region's tick.
	input inputQueue
	// flush sends what was queued for the client. Runs: anywhere.
	flush func()

	// The zones' centre and radius, set from the region (movement) and the
	// network goroutine (RequestChunkRadius).
	zoneMu    sync.Mutex
	pos       mgl32.Vec3
	centre    mcdb.ChunkPos
	radius    int32
	published bool

	// saved is the player's data as BDS saved it, written back with what
	// Helix changed so nothing Helix does not use yet is lost.
	saved    map[string]any
	gameMode int32
}

// Name returns the player's display name. Runs: anywhere.
func (p *Player) Name() string { return p.name }

// UUID returns the player's identity UUID. Runs: anywhere.
func (p *Player) UUID() uuid.UUID { return p.uuid }

// XUID returns the player's Xbox user ID, empty for offline players. Runs:
// anywhere.
func (p *Player) XUID() string { return p.xuid }

// Conn returns the player's connection, for packets. Runs: anywhere.
func (p *Player) Conn() *network.Conn { return p.conn }

// Entity returns the player's entity in the world. Runs: anywhere; its
// position and rotation are read on its region.
func (p *Player) Entity() *world.Entity { return p.entity }

// eyeHeight is how far above its feet a player's position is: the client
// sends and is sent eye positions.
const eyeHeight = 1.62

// newPlayer builds the session of a connection that finished the login.
func (s *Server) newPlayer(c *network.Conn, id uuid.UUID) (*Player, error) {
	skin, err := c.Client().Skin()
	if err != nil {
		return nil, fmt.Errorf("skin: %w", err)
	}
	ident := c.Identity()
	p := &Player{
		s: s, conn: c, uuid: id, name: ident.DisplayName, xuid: ident.XUID, skin: skin,
		ids: playerIDs(ident.Identity, c.Client().SelfSignedID, ident.XUID), runtimeID: s.runtimeIDs.Add(1),
		gameMode: s.level.gameMode,
	}
	p.uniqueID = int64(p.runtimeID) //nolint:gosec // runtime IDs are small
	return p, nil
}

// listEntry is the player's PlayerList entry.
func (p *Player) listEntry() protocol.PlayerListEntry {
	return protocol.PlayerListEntry{
		ActionType: protocol.PlayerListActionAdd, UUID: p.uuid, EntityUniqueID: p.uniqueID, Username: p.name, XUID: p.xuid,
		PlayFabID: p.conn.Client().PlayFabID, BuildPlatform: int32(p.conn.Client().DeviceOS), Skin: p.skin,
	}
}

// load reads the player's saved data: where they were, and the unique ID
// BDS gave them.
func (p *Player) load() (pos mgl32.Vec3, rot world.Rotation, ok bool) {
	data, found, err := p.s.playerStore.LoadPlayer(p.ids)
	if err != nil {
		p.s.log.Error("load player", "name", p.name, "err", err)
		return pos, rot, false
	}
	if !found {
		return pos, rot, false
	}
	p.saved = data
	if id, isInt := data["UniqueID"].(int64); isInt {
		p.uniqueID = id
	}
	if r, isList := floats(data["Rotation"]); isList && len(r) == 2 {
		rot = world.Rotation{Yaw: r[0], Pitch: r[1], HeadYaw: r[0]}
	}
	if v, isList := floats(data["Pos"]); isList && len(v) == 3 {
		return mgl32.Vec3{v[0], v[1], v[2]}, rot, true
	}
	return pos, rot, false
}

// floats reads an NBT list of floats.
func floats(v any) ([]float32, bool) {
	switch l := v.(type) {
	case []float32:
		return l, true
	case []any:
		out := make([]float32, 0, len(l))
		for _, x := range l {
			f, ok := x.(float32)
			if !ok {
				return nil, false
			}
			out = append(out, f)
		}
		return out, true
	}
	return nil, false
}

// save writes the player back where BDS keeps players, from the state the
// entity had when it was removed. The saved Pos is the eye position, as
// the client reports it and StartGame takes it.
func (p *Player) save(pos mgl32.Vec3, rot world.Rotation) {
	data := p.saved
	if data == nil {
		data = map[string]any{}
	}
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Rotation"] = []float32{rot.Yaw, rot.Pitch}
	data["DimensionId"] = int32(mcdb.Overworld)
	data["UniqueID"] = p.uniqueID
	if err := p.s.playerStore.StorePlayer(p.ids, data); err != nil {
		p.s.log.Error("save player", "name", p.name, "err", err)
	}
}

// spawnPackets show the player to another player: AddPlayer, built on the
// player's region tick. The data is a first pass, not yet checked against
// BDS's synched actor data.
func (p *Player) spawnPackets(e *world.Entity) []packet.Packet {
	pos, rot := e.Position(), e.Rotation()
	meta := protocol.EntityMetadata{
		protocol.EntityDataKeyFlags: int64(1<<protocol.EntityDataFlagHasGravity | 1<<protocol.EntityDataFlagBreathing |
			1<<protocol.EntityDataFlagHasCollision | 1<<protocol.EntityDataFlagShowName | 1<<protocol.EntityDataFlagAlwaysShowName),
		protocol.EntityDataKeyName:   p.name,
		protocol.EntityDataKeyWidth:  float32(0.6),
		protocol.EntityDataKeyHeight: float32(1.8),
		protocol.EntityDataKeyScale:  float32(1),
	}
	return []packet.Packet{&packet.AddPlayer{
		UUID: p.uuid, Username: p.name, EntityRuntimeID: p.runtimeID, PlatformChatID: p.conn.Client().PlatformOnlineID,
		Position: pos.Sub(mgl32.Vec3{0, eyeHeight, 0}), Pitch: rot.Pitch, Yaw: rot.Yaw, HeadYaw: rot.HeadYaw,
		GameType: p.gameMode, EntityMetadata: meta,
		AbilityData: protocol.AbilityData{
			EntityUniqueID: p.uniqueID, PlayerPermissions: packet.PermissionLevelMember,
			Layers: []protocol.AbilityLayer{{
				Type: protocol.AbilityLayerTypeBase, Abilities: protocol.AbilityCount - 1, Values: survivalAbilities,
				FlySpeed: 0.05, VerticalFlySpeed: 1, WalkSpeed: 0.1,
			}},
		},
		DeviceID: string(p.conn.Client().DeviceID), BuildPlatform: int32(p.conn.Client().DeviceOS),
	}}
}

// survivalAbilities are what a member in survival may do.
const survivalAbilities = protocol.AbilityBuild | protocol.AbilityMine | protocol.AbilityDoorsAndSwitches |
	protocol.AbilityOpenContainers | protocol.AbilityAttackPlayers | protocol.AbilityAttackMobs |
	protocol.AbilityWalkSpeed | protocol.AbilityFlySpeed | protocol.AbilityVerticalFlySpeed

// movePacket moves the player for the players who see it: MovePlayer, as
// BDS sends another player's movement.
func (p *Player) movePacket(e *world.Entity) packet.Packet {
	rot := e.Rotation()
	return &packet.MovePlayer{
		EntityRuntimeID: p.runtimeID, Position: e.Position(), Pitch: rot.Pitch, Yaw: rot.Yaw, HeadYaw: rot.HeadYaw,
		Mode: packet.MoveModeNormal, OnGround: e.OnGround(), Tick: e.Tick(),
	}
}

// removeWait is how long a leaving player waits for its region to take the
// entity out before it is saved anyway from the last known state.
const removeWait = 5 * time.Second

// play spawns the player and reads its packets until it leaves.
func (p *Player) play() {
	s, c := p.s, p.conn
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
	spawn := mgl32.Vec3{float32(worldSpawn[0]) + 0.5, float32(worldSpawn[1]) + eyeHeight, float32(worldSpawn[2]) + 0.5}
	var rot world.Rotation
	if pos, r, ok := p.load(); ok {
		spawn, rot = pos, r
	}

	// Everyone else hears of the player before it gets StartGame, as BDS
	// broadcasts PlayerList; the player gets the whole list after it.
	s.global.call(func() {
		pk := &packet.PlayerList{Entries: []protocol.PlayerListEntry{p.listEntry()}}
		for _, o := range s.global.order {
			if o != p {
				_ = o.conn.WritePacket(pk)
				_ = o.conn.Flush()
			}
		}
	})
	_ = c.WritePacket(s.reg.JigsawStructures)
	_ = c.WritePacket(s.reg.VoxelShapes)
	_ = c.WritePacket(&packet.StartGame{
		EntityUniqueID: p.uniqueID, EntityRuntimeID: p.runtimeID, PlayerGameMode: p.gameMode, PlayerPosition: spawn,
		Pitch: rot.Pitch, Yaw: rot.Yaw,
		WorldSeed: lv.seed, Generator: lv.generator, WorldGameMode: lv.gameMode, Difficulty: lv.difficulty, WorldSpawn: worldSpawn,
		AchievementsDisabled: true, MultiPlayerGame: true, LANBroadcastEnabled: true, CommandsEnabled: true,
		PlayerPermissions: packet.PermissionLevelMember, BaseGameVersion: protocol.CurrentVersion,
		WorldName: lv.name, PlayerMovementSettings: protocol.PlayerMovementSettings{ServerAuthoritativeBlockBreaking: true},
		MultiPlayerCorrelationID: uuid.NewString(), ServerAuthoritativeInventory: true, GameVersion: protocol.CurrentVersion,
		UseBlockNetworkIDHashes: true, ChatRestrictionLevel: 0, Blocks: s.reg.Blocks.Definitions(),
	})
	// The registry packets and the player list, in the order BDS 1.26.60
	// sends them (other join packets between them are not sent yet).
	_ = c.WritePacket(s.reg.Items.RegistryPacket())
	list := &packet.PlayerList{}
	for _, o := range s.global.online() {
		list.Entries = append(list.Entries, o.listEntry())
	}
	_ = c.WritePacket(list)
	_ = c.WritePacket(s.reg.Biomes.DefinitionPacket())
	_ = c.WritePacket(s.reg.EntityTypes.IdentifiersPacket())
	_ = c.WritePacket(s.reg.Creative.Packet())
	_ = c.WritePacket(s.reg.Crafting.Packet())
	if c.Flush() != nil {
		return
	}

	// Regions queue packets on their ticks, which must not wait for the
	// network: a flusher sends them.
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
	p.flush = func() {
		select {
		case kick <- struct{}{}:
		default:
		}
	}
	p.pos = spawn
	p.viewer = s.overworld.AddViewer(func(pk packet.Packet) { _ = c.WritePacket(pk) }, p.flush)
	defer p.viewer.Close()
	type last struct {
		pos mgl32.Vec3
		rot world.Rotation
	}
	removed := make(chan last, 1)
	p.entity = s.overworld.AddEntity(world.EntityConfig{
		UniqueID: p.uniqueID, RuntimeID: p.runtimeID, Pos: spawn, Rot: rot, Viewer: p.viewer,
		Spawn: p.spawnPackets, Move: p.movePacket, Input: p.handleInput,
		Removed: func(e *world.Entity) { removed <- last{e.Position(), e.Rotation()} },
	})
	pos := spawn
	defer func() {
		p.entity.Remove()
		select {
		case l := <-removed:
			pos, rot = l.pos, l.rot
		case <-time.After(removeWait):
			s.log.Warn("player entity not removed in time; saving the last position read", "name", p.name)
		}
		p.save(pos, rot)
	}()

	p.readLoop(done)
}

// readLoop reads the player's packets until the connection closes. Packets
// with a world handler are queued for the region; the session's own are
// answered here, on the network goroutine.
func (p *Player) readLoop(done <-chan struct{}) {
	s, c := p.s, p.conn
	spawned := false
	for {
		pk, err := c.ReadPacket()
		if err != nil {
			if !isClosed(err) {
				s.log.Info("read failed", "name", p.name, "err", err)
			}
			return
		}
		if s.handlers.handler(pk.ID()) != nil {
			if !p.input.push(pk) {
				s.log.Debug("input queue full, packet dropped", "name", p.name, "packet", fmt.Sprintf("%T", pk))
			}
			continue
		}
		s.log.Debug("packet from player", "name", p.name, "packet", fmt.Sprintf("%T", pk))
		switch pk := pk.(type) {
		case *packet.Disconnect:
			s.log.Info("Player left", "name", p.name, "reason", pk.Reason, "message", pk.Message)
		case *packet.RequestChunkRadius:
			radius := max(min(pk.ChunkRadius, s.conf.ViewDistance), 1)
			_ = c.WritePacket(&packet.ChunkRadiusUpdated{ChunkRadius: radius})
			p.setRadius(radius)
			if !spawned {
				spawned = true
				go func() {
					select {
					case <-p.viewer.Ready():
						_ = c.WritePacket(&packet.PlayStatus{Status: packet.PlayStatusPlayerSpawn})
						p.flush()
					case <-done:
					}
				}()
			}
		case *packet.SubChunkRequest:
			_ = c.WritePacket(p.viewer.SubChunks(pk))
			p.flush()
		case *packet.SetLocalPlayerAsInitialised:
			s.log.Info("Player spawned", "name", p.name)
		}
	}
}
