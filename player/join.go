package player

import (
	"fmt"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// removeWait is how long a leaving player waits for its region to take the
// entity out before it is saved anyway from the last known state.
const removeWait = 5 * time.Second

// play runs the player's whole stay: it joins, enters the world, reads its
// packets until it leaves, and is saved.
func (p *Player) play() {
	spawn, worldSpawn, rot := p.spawnPoint()
	p.announce()
	if !p.sendJoin(spawn, worldSpawn, rot) {
		return
	}
	done := make(chan struct{})
	defer close(done)
	p.startFlusher(done)
	p.enterWorld(spawn, rot)
	defer p.leaveWorld(lastPlace{spawn, rot})
	p.readLoop(done)
}

// spawnPoint is where the player appears: where it was saved, else the
// world spawn.
func (p *Player) spawnPoint() (spawn mgl32.Vec3, worldSpawn protocol.BlockPos, rot world.Rotation) {
	spawn, worldSpawn = p.worldSpawn()
	p.dim = p.s.overworld
	if pos, r, ok := p.load(); ok {
		spawn, rot = pos, r
	}
	p.abilities.SetGameMode(p.gameMode, p.s.level.GameMode)
	return spawn, worldSpawn, rot
}

// worldSpawn is the world's spawn: the eye position there and its block.
func (p *Player) worldSpawn() (mgl32.Vec3, protocol.BlockPos) {
	lv := p.s.level
	at := protocol.BlockPos{lv.Spawn[0], lv.Spawn[1], lv.Spawn[2]}
	if r := p.s.overworld.Range(); int(at[1]) < r[0] || int(at[1]) > r[1] {
		// BDS stores a height out of range ("find the surface") until it
		// has placed the spawn. Flat worlds know their surface; anywhere
		// else the player starts high up until spawning is built.
		at[1] = 100
		if lv.Surface != 0 {
			at[1] = lv.Surface
		}
	}
	return mgl32.Vec3{float32(at[0]) + 0.5, float32(at[1]) + eyeHeight, float32(at[2]) + 0.5}, at
}

// announce tells everyone else of the player before it gets StartGame,
// as BDS broadcasts PlayerList.
func (p *Player) announce() {
	g := p.s.global
	g.call(func() { g.join(p) })
}

// sendJoin sends the join packets in the order BDS 1.26.60 sends them
// (SyncActorProperty, PlayerFog and the camera packets between
// them are not sent yet). False if the connection failed.
func (p *Player) sendJoin(spawn mgl32.Vec3, worldSpawn protocol.BlockPos, rot world.Rotation) bool {
	s := p.s
	p.send(s.reg.JigsawStructures, s.reg.VoxelShapes, p.startGame(spawn, worldSpawn, rot))
	p.send(s.reg.Items.RegistryPacket())
	p.sendWorldState(worldSpawn)
	p.sendSessionData()
	p.sendOwnItems()
	p.sendEffects()
	p.send(p.recipesPacket())
	p.send(p.commandsPacket())
	p.sendJoinActorData()
	return p.sendNow()
}

// startGame is the player's StartGame: who and where it is, and the world it joins.
func (p *Player) startGame(spawn mgl32.Vec3, worldSpawn protocol.BlockPos, rot world.Rotation) *packet.StartGame {
	s, lv := p.s, p.s.level
	var dayTime int64
	if s.clock != nil {
		dayTime = int64(s.clock.Time())
	}
	return &packet.StartGame{
		Time:           dayTime,
		EntityUniqueID: p.uniqueID, EntityRuntimeID: p.runtimeID, PlayerGameMode: p.gameMode, PlayerPosition: spawn, Dimension: s.dimensionID(p.dim),
		Pitch: rot.Pitch, Yaw: rot.Yaw,
		WorldSeed: lv.Seed, Generator: lv.Generator, WorldGameMode: lv.GameMode, Difficulty: lv.Difficulty, WorldSpawn: worldSpawn,
		AchievementsDisabled: true, MultiPlayerGame: true, LANBroadcastEnabled: true, CommandsEnabled: true,
		PlayerPermissions: packet.PermissionLevelMember, BaseGameVersion: protocol.CurrentVersion,
		WorldName: lv.Name, PlayerMovementSettings: protocol.PlayerMovementSettings{RewindHistorySize: 40, ServerAuthoritativeBlockBreaking: true},
		MultiPlayerCorrelationID: uuid.NewString(), ServerAuthoritativeInventory: true, GameVersion: protocol.CurrentVersion,
		UseBlockNetworkIDHashes: true, ChatRestrictionLevel: 0, Blocks: s.reg.Blocks.Definitions(),
		RainLevel: s.weather.Rain(), LightningLevel: s.weather.Thunder(),
	}
}

// sendWorldState sends the joining player the world's spawn, time, weather and
// difficulty.
func (p *Player) sendWorldState(worldSpawn protocol.BlockPos) {
	s := p.s
	p.send(&packet.SetSpawnPosition{SpawnType: packet.SpawnTypeWorld, Position: worldSpawn, SpawnPosition: worldSpawn})
	if s.clock != nil {
		p.send(s.clockRegistry())
	}
	p.sendWeather()
	p.send(&packet.SetDifficulty{Difficulty: uint32(s.level.Difficulty)}, //nolint:gosec // difficulties are small
		&packet.SetCommandsEnabled{Enabled: true})
}

// sendSessionData sends the joining player its abilities, the game rules, the player
// list, the registries and its attributes. The adventure settings and abilities are
// broadcast, so the player gets its abilities twice, as from BDS.
func (p *Player) sendSessionData() {
	s := p.s
	adventure := &packet.UpdateAdventureSettings{AutoJump: true}
	abilities := &packet.UpdateAbilities{AbilityData: p.abilities.Data(p.uniqueID)}
	p.broadcast(adventure, abilities)
	p.send(adventure, abilities, &packet.GameRulesChanged{GameRules: s.level.GameRules}, s.global.listPacket(), abilities,
		s.reg.Biomes.DefinitionPacket(), s.reg.EntityTypes.IdentifiersPacket(),
		&packet.UpdateAttributes{EntityRuntimeID: p.runtimeID, Attributes: p.attributes.list()},
		s.reg.Creative.Packet(), s.reg.Trims.Packet())
}

// sendJoinActorData sends the player its own synched data, as BDS does on its first
// tick, then the breathing flag BDS sends the next tick once it finds air.
func (p *Player) sendJoinActorData() {
	p.send(actorData(p.runtimeID, p.ownMetadata()),
		actorData(p.runtimeID, protocol.EntityMetadata{
			protocol.EntityDataKeyFlags: int64(ownFlags | 1<<protocol.EntityDataFlagBreathing),
		}))
}

// startFlusher starts the goroutine that sends what regions queued for
// the player: region ticks must never wait on the network. It stops when
// done closes.
func (p *Player) startFlusher(done <-chan struct{}) {
	kick := make(chan struct{}, 1)
	go func() {
		defer p.s.recovered()
		for {
			select {
			case <-done:
				return
			case <-kick:
				p.sendNow()
			}
		}
	}()
	p.flush = func() {
		select {
		case kick <- struct{}{}:
		default:
		}
	}
}

// enterWorld adds the player's viewer and entity to the dimension it was saved in.
// leaveWorld takes them out again.
func (p *Player) enterWorld(spawn mgl32.Vec3, rot world.Rotation) {
	p.zone.pos = spawn
	p.viewer = p.addViewer(p.dim)
	p.removed = make(chan lastPlace, 1)
	// Until its first tick, others see where it spawns.
	p.seen.store(PlayerState{Position: spawn.Sub(mgl32.Vec3{0, eyeHeight, 0}), Rotation: rot, GameMode: p.gameMode, Level: p.Level()})
	p.entity = p.dim.AddEntity(p.entityConfig(spawn, rot))
}

// leaveWorld takes the player's entity and viewer out of the world and saves the player
// where its region last had it, or at fallback if the region does not answer in time.
func (p *Player) leaveWorld(fallback lastPlace) {
	at := fallback
	p.entity.Remove()
	select {
	case at = <-p.removed:
	case <-time.After(removeWait):
		p.s.log.Warn("player entity not removed in time; saving the last position read", "name", p.name)
	}
	p.save(at.pos, at.rot)
	p.currentViewer().Close()
}

// lastPlace is where the player's entity was when its region took it out.
type lastPlace struct {
	pos mgl32.Vec3
	rot world.Rotation
}

// entityConfig is the player's entity at pos. Its removal either finishes a move to
// another dimension or, when the player leaves, hands back where it was.
func (p *Player) entityConfig(pos mgl32.Vec3, rot world.Rotation) world.EntityConfig {
	return world.EntityConfig{
		UniqueID: p.uniqueID, RuntimeID: p.runtimeID, Pos: pos, Rot: rot, Viewer: p.viewer, Owner: p,
		Spawn: p.spawnPackets, Move: p.movePacket, Think: p.think, Input: p.handleInput,
		Removed: p.entityRemoved,
	}
}

// entityRemoved runs on the region when it takes the player's entity out: it lets go of
// what the player held there, then finishes a move to another dimension or, when the
// player leaves, hands back where it was.
func (p *Player) entityRemoved(e *world.Entity) {
	p.StopSleeping()
	p.leaveChest(nil, false)
	p.dropLeashes(e.Region())
	if mv := p.moving; mv != nil {
		p.moving = nil
		p.arrive(mv)
		return
	}
	p.removed <- lastPlace{e.Position(), e.Rotation()}
}

// currentViewer is the player's viewer; it changes when the player changes
// dimension. Runs: anywhere.
func (p *Player) currentViewer() *world.Viewer {
	p.zone.mu.Lock()
	defer p.zone.mu.Unlock()
	return p.viewer
}

// readLoop reads the player's packets until the connection closes. Packets
// with a world handler are queued for the region; the session's own are
// answered here, on the network goroutine.
func (p *Player) readLoop(done <-chan struct{}) {
	s := p.s
	spawned := false
	for {
		pk, err := p.conn.ReadPacket()
		if err != nil {
			if !isClosed(err) {
				s.log.Info("read failed", "name", p.name, "err", err)
			}
			return
		}
		if pk.ID() == packet.IDPlayerAuthInput {
			p.clientTicks.add(time.Now())
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
			radius := max(min(pk.ChunkRadius, s.viewDistance), 1)
			p.send(&packet.ChunkRadiusUpdated{ChunkRadius: radius})
			p.setRadius(radius)
			if !spawned {
				spawned = true
				go p.spawnWhenReady(done)
			}
		case *packet.SubChunkRequest:
			p.send(p.currentViewer().SubChunks(pk))
			p.flush()
		case *packet.SetLocalPlayerAsInitialised:
			p.initialised.Store(true)
			s.log.Info("Player spawned", "name", p.name)
		}
	}
}

// spawnWhenReady sends PlayStatus PlayerSpawn once the chunks around the
// player are sent, as BDS lets the client in.
func (p *Player) spawnWhenReady(done <-chan struct{}) {
	defer p.s.recovered()
	select {
	case <-p.currentViewer().Ready():
		p.send(&packet.PlayStatus{Status: packet.PlayStatusPlayerSpawn})
		p.flush()
	case <-done:
	}
}
