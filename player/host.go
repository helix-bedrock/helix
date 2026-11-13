package player

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/helix-bedrock/helix/command"

	"github.com/google/uuid"
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/attach"
	"github.com/helix-bedrock/helix/circuit"
	"github.com/helix-bedrock/helix/connect"
	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/helix/daytime"
	"github.com/helix-bedrock/helix/fall"
	"github.com/helix-bedrock/helix/farm"
	"github.com/helix-bedrock/helix/fire"
	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/network"
	"github.com/helix-bedrock/helix/portal"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/weather"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/conn"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/mcdb"
)

// Level holds the level.dat values players need, copied once when the
// world opens so player goroutines never touch the world's level.dat.
type Level struct {
	Name       string
	Seed       int64
	GameMode   int32
	Difficulty int32
	Spawn      [3]int32
	Generator  int32
	// Surface is the Y a player stands on in a flat world, used while the
	// spawn has no height yet; 0 when unknown.
	Surface int32
	// KeepInventory, ShowDeathMessages, PVP and NaturalRegeneration are level.dat's
	// game rules the engine reads. GameRules is all of them, as the client is told.
	KeepInventory       bool
	ShowDeathMessages   bool
	PVP                 bool
	NaturalRegeneration bool
	// TNTExplodes, TNTExplosionDropDecay and DoTileDrops are the game rules explosions read.
	TNTExplodes, TNTExplosionDropDecay, DoTileDrops bool
	// RespawnBlocksExplode, DoDayLightCycle and PlayersSleepingPercentage are the game rules
	// beds read.
	RespawnBlocksExplode, DoDayLightCycle bool
	// MobGriefing is whether mobs may change blocks.
	MobGriefing bool
	// DoMobSpawning is whether mobs spawn on their own.
	DoMobSpawning bool
	// DoEntityDrops is whether broken entities such as cushions leave their item.
	DoEntityDrops             bool
	PlayersSleepingPercentage int32
	GameRules                 []protocol.GameRule
}

// HostConfig is what the server gives the players.
type HostConfig struct {
	Log       *slog.Logger
	Registry  *registry.Registry
	Overworld *world.Dimension
	// Nether and End are the other two dimensions; nil leaves them closed.
	Nether, End *world.Dimension
	// Portals are the server's portal blocks and records; nil turns portals off.
	Portals *portal.Portals
	// OnEnterEnd runs when a player arrives in the End through a portal, on its
	// region: the server builds the exit portal the first time.
	OnEnterEnd func()
	Store      mcdb.PlayerStore
	Level      Level
	// ViewDistance is the largest view radius in chunks a player gets.
	ViewDistance int32
	// MaxPlayers is how many players may be online at once.
	MaxPlayers int
	// Redstone is the numbers redstone runs by. Nil means BDS's.
	Redstone *circuit.Settings
	// Farm is farming: bone meal, hoes, trampling and where crops may go. Nil turns them off.
	Farm *farm.Ticker
	// Fire is fire's ticker, which lightning asks where fire may go. Nil: lightning sets none.
	Fire *fire.Ticker
	// OnPanic is told of a panic in a player's or the global goroutine, recovered so the
	// server can tell everyone and stop. Nil only logs it.
	OnPanic func(v any, stack []byte)
}

// Host runs the players of one server: it admits them, owns the player
// list (on the global owner) and holds the frozen handlers and gameplay
// they run on.
type Host struct {
	// enchantIDs numbers enchanting tables' options.
	enchantIDs atomic.Uint32
	// soundHandles numbers the sounds that are stopped later.
	soundHandles atomic.Uint64
	log          *slog.Logger
	onPanic      func(v any, stack []byte)
	reg          *registry.Registry
	recipes      crafting.Book
	overworld    *world.Dimension
	nether, end  *world.Dimension
	portals      *portal.Portals
	farm         *farm.Ticker
	onEnterEnd   func()
	playerStore  mcdb.PlayerStore
	level        Level
	viewDistance int32
	maxPlayers   int
	// projectileSpecs are what each kind of projectile does, read when first needed.
	projectileSpecs sync.Map
	// redstone is the numbers redstone runs by.
	redstone *circuit.Settings

	handlers handlers
	// commands are the server's commands, shared with the Setup they were registered on.
	commands *command.Manager
	game     *gameplay
	global   *global
	// connections works out the arms of fences and the like as they are placed.
	connections *connect.Table
	// attach is what holds torches, lanterns and ladders.
	attach *attach.Table
	// circuits is what redstone answers for. Set before any player joins.
	circuits CircuitHooks
	// clock is the world's time. Set before any player joins.
	clock *daytime.Clock
	// weather is the world's weather and chunkWeather what says where it rains; nil for none.
	weather      *weather.Weather
	chunkWeather *weather.Ticker
	// falling is how falling blocks land; nil lands nothing. Set before any player joins.
	falling *fall.Table
	// fire is fire's ticker, which lightning asks where fire may go; nil sets none.
	fire *fire.Ticker
	// chests holds which blocks are chests and what lets them open.
	chests *chestTable
	// furnaces are BDS's fuel, experience and cook times.
	furnaces *furnaceFacts
	// discs is the music discs by item name.
	discs map[string]data.MusicDisc
	// drops holds what each block drops.
	drops *loot.Table
	// entityLoot are the entity loot tables: mob death drops and equipment.
	entityLoot *loot.EntityTables
	// foods holds what each item does when eaten, from the items' own data.
	foods map[string]FoodItem
	// runtimeIDs numbers players' entities; stackIDs numbers item stacks
	// for the network, as BDS does with one counter for the whole server.
	runtimeIDs atomic.Uint64
	stackIDs   atomic.Int32
	// storageIDs numbers storage items (bundles) for the network, BDS's dynamic
	// container IDs: one counter for the level, the first being 0.
	storageIDs atomic.Int32
	// itemLifetime is the live age items vanish at; 0 is vanilla's.
	itemLifetime atomic.Int32
	// mobs runs every mob from its entity definition.
	mobs *mob.Runtime
	// spawning is the natural mob spawner; nil when its data did not load.
	spawning *spawning
	// mobScratch holds the mob runtime's entity scan buffers (mobScratch).
	mobScratch sync.Pool
	// tuning is the live value of every Tune.
	tuning atomic.Pointer[tuneTable]
	wg     sync.WaitGroup
}

// NewHost freezes setup and returns the Host that runs players with it.
// Close stops it.
func NewHost(conf HostConfig, setup *Setup) (*Host, error) {
	g, err := setup.freezeGameplay()
	if err != nil {
		return nil, fmt.Errorf("player: gameplay: %w", err)
	}
	t, err := loadHostTables(conf.Registry)
	if err != nil {
		return nil, err
	}
	for name, rule := range g.dropRules {
		t.drops.Set(name, rule)
	}
	if conf.Redstone == nil {
		conf.Redstone = circuit.NewSettings(circuit.DefaultConfig())
	}
	h := &Host{
		log:          conf.Log,
		onPanic:      conf.OnPanic,
		reg:          conf.Registry,
		overworld:    conf.Overworld,
		nether:       conf.Nether,
		end:          conf.End,
		portals:      conf.Portals,
		onEnterEnd:   conf.OnEnterEnd,
		playerStore:  conf.Store,
		level:        conf.Level,
		viewDistance: conf.ViewDistance,
		maxPlayers:   conf.MaxPlayers,
		redstone:     conf.Redstone,
		farm:         conf.Farm,
		fire:         conf.Fire,
		handlers:     setup.freeze(),
		commands:     setup.h.commands,
		game:         g,
		connections:  t.connections,
		attach:       t.attach,
		chests:       t.chests,
		furnaces:     t.furnaces,
		discs:        t.discs,
		drops:        t.drops,
		entityLoot:   t.entityLoot,
		foods:        foodTable(conf.Registry.Items),
	}
	h.global = newGlobal(h.panicked)
	h.setUpCommands()
	h.setUpMobs()
	h.itemLifetime.Store(g.sys.ItemLifetime)
	h.ClearAllTuning()
	h.hookDimensions()
	h.global.run(func() { h.global.tick = h.globalTick })
	return h, nil
}

// hostTables are the block and item tables a Host loads from data when it is made.
type hostTables struct {
	connections *connect.Table
	attach      *attach.Table
	chests      *chestTable
	furnaces    *furnaceFacts
	discs       map[string]data.MusicDisc
	drops       *loot.Table
	entityLoot  *loot.EntityTables
}

// loadHostTables loads the tables a Host reads its blocks' and items' behaviour from.
func loadHostTables(reg *registry.Registry) (t hostTables, err error) {
	if t.connections, err = connect.New(reg.Blocks); err != nil {
		return t, fmt.Errorf("player: connections: %w", err)
	}
	if t.attach, err = attach.New(reg.Blocks); err != nil {
		return t, fmt.Errorf("player: attached blocks: %w", err)
	}
	if t.discs, err = loadDiscs(); err != nil {
		return t, err
	}
	if t.chests, err = newChestTable(reg.Blocks); err != nil {
		return t, fmt.Errorf("player: chests: %w", err)
	}
	if t.drops, err = loot.New(); err != nil {
		return t, fmt.Errorf("player: drops: %w", err)
	}
	if t.entityLoot, err = loot.NewEntityTables(); err != nil {
		return t, fmt.Errorf("player: entity loot: %w", err)
	}
	if t.furnaces, err = loadFurnaceFacts(); err != nil {
		return t, fmt.Errorf("player: furnaces: %w", err)
	}
	return t, nil
}

// hookDimensions has every open dimension load its saved entities and run its block
// entities through the Host (Nether fortress spawners and bastion chests included).
func (h *Host) hookDimensions() {
	for _, d := range []*world.Dimension{h.overworld, h.nether, h.end} {
		if d != nil {
			d.SetEntityLoader(h.loadEntity)
			d.SetBlockEntities(chestEntities{s: h})
		}
	}
}

// Admit takes a connection that finished the login and plays it until it
// leaves, on its own goroutine. The duplicate and full checks run again on
// the global owner, which owns the player list, because two logins of the
// same player, or for the last free slot, may pass the login at once.
// changed runs when the player count changed. Runs: anywhere.
func (h *Host) Admit(c *network.Conn, changed func()) {
	id, err := uuid.Parse(c.Identity().Identity)
	if err != nil {
		h.refuse(c, packet.DisconnectReasonNotAuthenticated)
		return
	}
	p, err := h.newPlayer(c, id)
	if err != nil {
		h.log.Info("player refused", "name", c.Identity().DisplayName, "err", err)
		h.refuse(c, packet.DisconnectReasonInvalidPlayer)
		return
	}
	var dup, full bool
	h.global.call(func() { dup, full = h.admit(p) })
	switch {
	case dup:
		h.refuse(c, packet.DisconnectReasonServerIdConflict)
		return
	case full:
		h.refuse(c, packet.DisconnectReasonServerFull)
		return
	}
	h.log.Info("Player connected", "name", p.name, "xuid", p.xuid, "addr", c.RemoteAddr())
	changed()
	h.wg.Add(1)
	go h.run(p, changed)
}

// run plays an admitted player until it leaves, then takes it off the player list and
// closes its connection. It is the player's goroutine.
func (h *Host) run(p *Player, changed func()) {
	defer h.playerDone()
	p.play()
	h.global.call(func() { h.global.leave(p) })
	if err := p.conn.Close(); err != nil && !isClosed(err) {
		h.log.Debug("close connection", "name", p.name, "err", err)
	}
	h.log.Info("Player disconnected", "name", p.name, "xuid", p.xuid)
	changed()
}

// playerDone is deferred by a player's goroutine: it recovers a panic and marks the
// goroutine finished for Close.
func (h *Host) playerDone() {
	if v := recover(); v != nil {
		h.panicked(v, debug.Stack())
	}
	h.wg.Done()
}

// admit puts p on the player list unless a player with its identity is already
// online or the server is full, and says which. Global owner only.
func (h *Host) admit(p *Player) (dup, full bool) {
	_, dup = h.global.players[p.uuid]
	full = len(h.global.players) >= h.maxPlayers
	if !dup && !full {
		h.global.add(p)
	}
	return dup, full
}

// refuse disconnects a connection that is not let in.
func (h *Host) refuse(c *network.Conn, reason int32) {
	if err := c.Disconnect(reason, ""); err != nil && !isClosed(err) {
		h.log.Debug("refuse player", "name", c.Identity().DisplayName, "err", err)
	}
}

// Online returns the players online, as of the global owner's last change.
// Runs: anywhere.
func (h *Host) Online() []*Player { return h.global.online() }

// Find returns the online player with the identity UUID id. Runs:
// anywhere.
func (h *Host) Find(id uuid.UUID) (*Player, bool) {
	for _, p := range h.global.online() {
		if p.uuid == id {
			return p, true
		}
	}
	return nil, false
}

// Broadcast sends a packet to every player online. Runs: anywhere.
func (h *Host) Broadcast(pk packet.Packet) {
	for _, p := range h.global.online() {
		p.sendNow(pk)
	}
}

// Kick sends every player online a disconnect with reason and closes their connections,
// without waiting for anything to tick. It returns once each disconnect was delivered or its
// connection gave up waiting (a NetherNet close waits for the client to acknowledge it), all
// players at the same time. Runs: anywhere.
func (h *Host) Kick(reason int32) {
	var wg sync.WaitGroup
	for _, p := range h.global.online() {
		wg.Go(func() { h.refuse(p.conn, reason) })
	}
	wg.Wait()
}

// recovered is deferred in the player goroutines: it recovers a panic and passes it on, so
// the server can tell every player before it stops.
func (h *Host) recovered() {
	if v := recover(); v != nil {
		h.panicked(v, debug.Stack())
	}
}

// panicked passes a recovered panic to OnPanic, or logs it.
func (h *Host) panicked(v any, stack []byte) {
	if h.onPanic != nil {
		h.onPanic(v, stack)
		return
	}
	h.log.Error("Player goroutine panicked", "panic", v, "stack", string(stack))
}

// Close disconnects every player with reason, waits for them to be saved
// and stops the global owner.
func (h *Host) Close(reason int32) {
	h.Kick(reason)
	h.wg.Wait()
	h.global.close()
}

// isClosed reports whether err only says the connection was closed.
func isClosed(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, conn.ErrClosed) || errors.Is(err, os.ErrDeadlineExceeded)
}
