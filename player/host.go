package player

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/attach"
	"github.com/helix-bedrock/helix/circuit"
	"github.com/helix-bedrock/helix/connect"
	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/network"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
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
	GameRules                                       []protocol.GameRule
}

// HostConfig is what the server gives the players.
type HostConfig struct {
	Log       *slog.Logger
	Registry  *registry.Registry
	Overworld *world.Dimension
	Store     mcdb.PlayerStore
	Level     Level
	// ViewDistance is the largest view radius in chunks a player gets.
	ViewDistance int32
	// MaxPlayers is how many players may be online at once.
	MaxPlayers int
	// Redstone is the numbers redstone runs by. Nil means BDS's.
	Redstone *circuit.Settings
}

// Host runs the players of one server: it admits them, owns the player
// list (on the global owner) and holds the frozen handlers and gameplay
// they run on.
type Host struct {
	// enchantIDs numbers enchanting tables' options.
	enchantIDs   atomic.Uint32
	log          *slog.Logger
	reg          *registry.Registry
	recipes      crafting.Book
	overworld    *world.Dimension
	playerStore  mcdb.PlayerStore
	level        Level
	viewDistance int32
	maxPlayers   int
	// projectileSpecs are what each kind of projectile does, read when first needed.
	projectileSpecs sync.Map
	// redstone is the numbers redstone runs by.
	redstone *circuit.Settings

	handlers handlers
	game     *gameplay
	global   *global
	// connections works out the arms of fences and the like as they are placed.
	connections *connect.Table
	// attach is what holds torches, lanterns and ladders.
	attach *attach.Table
	// circuits is what redstone answers for. Set before any player joins.
	circuits CircuitHooks
	// chests holds which blocks are chests and what lets them open.
	chests *chestTable
	// discs is the music discs by item name.
	discs map[string]data.MusicDisc
	// drops holds what each block drops.
	drops *loot.Table
	// foods holds what each item does when eaten, from the items' own data.
	foods map[string]FoodItem
	// runtimeIDs numbers players' entities; stackIDs numbers item stacks
	// for the network, as BDS does with one counter for the whole server.
	runtimeIDs atomic.Uint64
	stackIDs   atomic.Int32
	// itemLifetime is the live age items vanish at; 0 is vanilla's.
	itemLifetime atomic.Int32
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
	connections, err := connect.New(conf.Registry.Blocks)
	if err != nil {
		return nil, fmt.Errorf("player: connections: %w", err)
	}
	attached, err := attach.New(conf.Registry.Blocks)
	if err != nil {
		return nil, fmt.Errorf("player: attached blocks: %w", err)
	}
	discs, err := loadDiscs()
	if err != nil {
		return nil, err
	}
	chests, err := newChestTable(conf.Registry.Blocks)
	if err != nil {
		return nil, fmt.Errorf("player: chests: %w", err)
	}
	drops, err := loot.New()
	if err != nil {
		return nil, fmt.Errorf("player: drops: %w", err)
	}
	for name, rule := range g.dropRules {
		drops.Set(name, rule)
	}
	if conf.Redstone == nil {
		conf.Redstone = circuit.NewSettings(circuit.DefaultConfig())
	}
	h := &Host{
		redstone: conf.Redstone,
		drops:    drops, foods: foodTable(conf.Registry.Items),
		log: conf.Log, reg: conf.Registry, overworld: conf.Overworld, playerStore: conf.Store, level: conf.Level,
		viewDistance: conf.ViewDistance, maxPlayers: conf.MaxPlayers,
		handlers: setup.freeze(), game: g, global: newGlobal(), connections: connections, attach: attached, chests: chests, discs: discs,
	}
	h.itemLifetime.Store(g.sys.ItemLifetime)
	h.ClearAllTuning()
	conf.Overworld.SetEntityLoader(h.loadEntity)
	conf.Overworld.SetBlockEntities(chestEntities{s: h})
	return h, nil
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
	h.global.call(func() {
		_, dup = h.global.players[id]
		full = len(h.global.players) >= h.maxPlayers
		if !dup && !full {
			h.global.add(p)
		}
	})
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
	go func() {
		defer h.wg.Done()
		p.play()
		h.global.call(func() {
			h.global.remove(p)
			pk := &packet.PlayerList{Entries: []protocol.PlayerListEntry{{ActionType: protocol.PlayerListActionRemove, UUID: p.uuid}}}
			for _, o := range h.global.order {
				o.sendNow(pk)
			}
		})
		if err := c.Close(); err != nil && !isClosed(err) {
			h.log.Debug("close connection", "name", p.name, "err", err)
		}
		h.log.Info("Player disconnected", "name", p.name, "xuid", p.xuid)
		changed()
	}()
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

// Close disconnects every player with reason, waits for them to be saved
// and stops the global owner.
func (h *Host) Close(reason int32) {
	for _, p := range h.global.online() {
		h.refuse(p.conn, reason)
	}
	h.wg.Wait()
	h.global.close()
}

// isClosed reports whether err only says the connection was closed.
func isClosed(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrDeadlineExceeded)
}
