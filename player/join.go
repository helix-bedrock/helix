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
	leave := p.enterWorld(spawn, rot)
	defer leave()
	p.readLoop(done)
}

// spawnPoint is where the player appears: where it was saved, else the
// world spawn.
func (p *Player) spawnPoint() (spawn mgl32.Vec3, worldSpawn protocol.BlockPos, rot world.Rotation) {
	spawn, worldSpawn = p.worldSpawn()
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
	g.call(func() {
		pk := &packet.PlayerList{Entries: []protocol.PlayerListEntry{p.listEntry()}}
		for _, o := range g.order {
			if o != p {
				o.sendNow(pk)
			}
		}
	})
}

// sendJoin sends the join packets in the order BDS 1.26.60 sends them
// (others between them are not sent yet). False if the connection failed.
func (p *Player) sendJoin(spawn mgl32.Vec3, worldSpawn protocol.BlockPos, rot world.Rotation) bool {
	s, lv := p.s, p.s.level
	p.send(s.reg.JigsawStructures, s.reg.VoxelShapes, &packet.StartGame{
		EntityUniqueID: p.uniqueID, EntityRuntimeID: p.runtimeID, PlayerGameMode: p.gameMode, PlayerPosition: spawn,
		Pitch: rot.Pitch, Yaw: rot.Yaw,
		WorldSeed: lv.Seed, Generator: lv.Generator, WorldGameMode: lv.GameMode, Difficulty: lv.Difficulty, WorldSpawn: worldSpawn,
		AchievementsDisabled: true, MultiPlayerGame: true, LANBroadcastEnabled: true, CommandsEnabled: true,
		PlayerPermissions: packet.PermissionLevelMember, BaseGameVersion: protocol.CurrentVersion,
		WorldName: lv.Name, PlayerMovementSettings: protocol.PlayerMovementSettings{RewindHistorySize: 40, ServerAuthoritativeBlockBreaking: true},
		MultiPlayerCorrelationID: uuid.NewString(), ServerAuthoritativeInventory: true, GameVersion: protocol.CurrentVersion,
		UseBlockNetworkIDHashes: true, ChatRestrictionLevel: 0, Blocks: s.reg.Blocks.Definitions(),
	})
	p.send(s.reg.Items.RegistryPacket())

	// The adventure settings and abilities are broadcast, so the player
	// gets its abilities twice, as from BDS.
	adventure := &packet.UpdateAdventureSettings{AutoJump: true}
	abilities := &packet.UpdateAbilities{AbilityData: p.abilities.Data(p.uniqueID)}
	p.broadcast(adventure, abilities)
	list := &packet.PlayerList{}
	for _, o := range s.global.online() {
		list.Entries = append(list.Entries, o.listEntry())
	}
	p.send(adventure, abilities, list, abilities,
		s.reg.Biomes.DefinitionPacket(), s.reg.EntityTypes.IdentifiersPacket(),
		&packet.UpdateAttributes{EntityRuntimeID: p.runtimeID, Attributes: p.attributes.list()},
		s.reg.Creative.Packet(), s.reg.Trims.Packet())
	p.send(p.inventory.contentPackets(s.reg)...)
	p.send(p.recipesPacket())

	// BDS sends the player its own synched data on its first tick, then
	// the next tick the breathing flag once it finds air.
	p.send(&packet.SetActorData{EntityRuntimeID: p.runtimeID, EntityMetadata: p.ownMetadata()},
		&packet.SetActorData{EntityRuntimeID: p.runtimeID, EntityMetadata: protocol.EntityMetadata{
			protocol.EntityDataKeyFlags: int64(ownFlags | 1<<protocol.EntityDataFlagBreathing),
		}})
	return p.sendNow()
}

// startFlusher starts the goroutine that sends what regions queued for
// the player: region ticks must never wait on the network. It stops when
// done closes.
func (p *Player) startFlusher(done <-chan struct{}) {
	kick := make(chan struct{}, 1)
	go func() {
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

// enterWorld adds the player's viewer and entity to the overworld. The
// returned leave takes them out and saves the player where its region
// last had it.
func (p *Player) enterWorld(spawn mgl32.Vec3, rot world.Rotation) (leave func()) {
	s := p.s
	p.zone.pos = spawn
	p.viewer = s.overworld.AddViewer(func(pk packet.Packet) { p.send(pk) }, p.flush)
	type last struct {
		pos mgl32.Vec3
		rot world.Rotation
	}
	removed := make(chan last, 1)
	p.entity = s.overworld.AddEntity(world.EntityConfig{
		UniqueID: p.uniqueID, RuntimeID: p.runtimeID, Pos: spawn, Rot: rot, Viewer: p.viewer, Owner: p,
		Spawn: p.spawnPackets, Move: p.movePacket, Input: p.handleInput,
		Removed: func(e *world.Entity) {
			p.leaveChest(nil, false)
			removed <- last{e.Position(), e.Rotation()}
		},
	})
	return func() {
		pos := spawn
		p.entity.Remove()
		select {
		case l := <-removed:
			pos, rot = l.pos, l.rot
		case <-time.After(removeWait):
			s.log.Warn("player entity not removed in time; saving the last position read", "name", p.name)
		}
		p.save(pos, rot)
		p.viewer.Close()
	}
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
			p.send(p.viewer.SubChunks(pk))
			p.flush()
		case *packet.SetLocalPlayerAsInitialised:
			s.log.Info("Player spawned", "name", p.name)
		}
	}
}

// spawnWhenReady sends PlayStatus PlayerSpawn once the chunks around the
// player are sent, as BDS lets the client in.
func (p *Player) spawnWhenReady(done <-chan struct{}) {
	select {
	case <-p.viewer.Ready():
		p.send(&packet.PlayStatus{Status: packet.PlayStatusPlayerSpawn})
		p.flush()
	case <-done:
	}
}
