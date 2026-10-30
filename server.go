// Package helix is the Helix server: a Minecraft: Bedrock Edition server
// engine. A Server accepts players over NetherNet and RakNet, logs them in
// the way the Bedrock Dedicated Server does and hands them to the game.
package helix

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/network"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/nethernet"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/auth"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/mcdb"
)

// Config configures a Server. Start from [DefaultConfig].
type Config struct {
	// Log receives the server's log. If nil, slog.Default().
	Log *slog.Logger
	// Name is the server name in the server list.
	Name string
	// LevelName is the world's name: its folder in WorldsDir, the name a
	// new world gets, and the name in the server list on LAN.
	LevelName string
	// WorldsDir is the folder worlds are kept in, as BDS's worlds folder.
	WorldsDir string
	// Storage replaces where the world and players are kept.
	Storage StorageConfig
	// Generator makes the overworld's chunks the world has not saved. Nil
	// means the generator level.dat names.
	Generator world.Generator
	// ViewDistance is the largest view radius in chunks a player gets,
	// BDS's view-distance.
	ViewDistance int32
	// TickDistance is the radius in chunks kept loaded and ticked around
	// each player, BDS's tick-distance.
	TickDistance int32
	// TickCores is how many goroutines tick the world's regions, shared by
	// every dimension. 0 means one per CPU; 1 ticks regions one after
	// another in a fixed order, for debugging and exact reproduction.
	TickCores int
	// Setup runs at boot after vanilla is registered, before everything is
	// frozen: plugins register, replace and delete blocks, items, biomes
	// and entity types, and replace or wrap packet handlers here. Nil means
	// vanilla only.
	Setup func(s *Setup) error
	// MaxPlayers is how many players may be online at once.
	MaxPlayers int
	// OnlineMode requires every player to be signed in to Xbox Live.
	OnlineMode bool
	// NetherNet configures the NetherNet transport, BDS's default.
	NetherNet NetherNetConfig
	// RakNet configures the RakNet transport.
	RakNet RakNetConfig
	// Network configures the login sequence.
	Network network.Config
	// Auth configures the Xbox Live token checks used in online mode.
	Auth auth.Config
}

// NetherNetConfig configures the NetherNet transport.
type NetherNetConfig struct {
	// Enabled turns the transport on.
	Enabled bool
	// Address is the TCP address of the HTTP signaling endpoint.
	Address string
	// IdentityKeyPath is the server identity key file. When it is missing,
	// a key is generated for this run only, as BDS does, unless
	// SaveIdentityKey is set.
	IdentityKeyPath string
	// SaveIdentityKey saves a generated identity key to IdentityKeyPath, so
	// players do not have to trust the server again after a restart. BDS
	// saves it only on the "serveridentity save" command.
	SaveIdentityKey bool
	// UDPPort carries every connection over one UDP port when not 0.
	UDPPort uint16
	// AllowAnonymous accepts clients that send no NetherNet identity. BDS
	// 1.26.60 refuses them (signaling error 37, IdentityNotAllowed) even
	// with online mode off, so it is off by default; turn it on only for
	// custom clients on a trusted network.
	AllowAnonymous bool
}

// RakNetConfig configures the RakNet transport.
type RakNetConfig struct {
	// Enabled turns the transport on.
	Enabled bool
	// Address is the UDP address listened on.
	Address string
}

// DefaultConfig returns BDS 1.26.60's defaults where Helix has the same
// setting: the name, the world worlds/Bedrock level, 10 players, a view
// distance of 32 chunks and a tick distance of 4, online mode, NetherNet on port 19132 with the identity key in
// keys/server_identity_key.pem. RakNet is on as well, on
// UDP port 19132 next to NetherNet's TCP port, for clients that cannot use
// NetherNet.
func DefaultConfig() Config {
	return Config{
		Name:         "Dedicated Server",
		LevelName:    "Bedrock level",
		WorldsDir:    "worlds",
		MaxPlayers:   10,
		ViewDistance: 32,
		TickDistance: world.DefaultConfig().SimulationDistance,
		OnlineMode:   true,
		NetherNet: NetherNetConfig{
			Enabled:         true,
			Address:         ":19132",
			IdentityKeyPath: nethernet.DefaultIdentityKeyPath,
		},
		RakNet:  RakNetConfig{Enabled: true, Address: ":19132"},
		Network: network.DefaultConfig(),
	}
}

// Server is a Helix server. Its methods are safe for use by many goroutines
// at once.
type Server struct {
	conf     Config
	log      *slog.Logger
	auth     *auth.Service
	listener *network.Listener
	serverID int64
	reg      *registry.Registry

	db          *mcdb.DB
	worldStore  mcdb.WorldStore
	playerStore mcdb.PlayerStore
	level       level
	ticker      *world.Scheduler
	overworld   *world.Dimension

	handlers   handlers
	global     *global
	runtimeIDs atomic.Uint64
	wg         sync.WaitGroup
}

// New returns a Server. It listens on nothing until [Server.Run].
func New(conf Config) (*Server, error) {
	if conf.Log == nil {
		conf.Log = slog.Default()
	}
	if conf.MaxPlayers < 1 {
		return nil, errors.New("helix: MaxPlayers must be at least 1")
	}
	if conf.ViewDistance < 1 || conf.TickDistance < 1 {
		return nil, errors.New("helix: ViewDistance and TickDistance must be at least 1")
	}
	if conf.TickCores < 0 {
		return nil, errors.New("helix: TickCores must not be negative")
	}
	if !conf.NetherNet.Enabled && !conf.RakNet.Enabled {
		return nil, errors.New("helix: no transport enabled")
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, fmt.Errorf("helix: server ID: %w", err)
	}
	s := &Server{conf: conf, log: conf.Log, global: newGlobal(), serverID: int64(binary.LittleEndian.Uint64(id[:]))}
	if err := s.buildRegistry(); err != nil {
		return nil, err
	}

	nc := conf.Network
	nc.Log = conf.Log.With("part", "network")
	if shield, ok := s.reg.Items.ByName("minecraft:shield"); ok {
		nc.ShieldID = int32(shield.ID)
	}
	if conf.OnlineMode {
		s.auth = auth.New(conf.Auth)
		nc.Verifier = s.auth
	}
	steps := nc.Steps
	if steps == nil {
		steps = network.DefaultSteps()
	}
	nc.Steps = steps.
		After(network.StepLogin, network.Step{Name: "already_online", Run: s.checkAlreadyOnline}).
		Before(network.StepLoginSuccess, network.Step{Name: "server_full", Run: s.checkServerFull})
	l, err := network.New(nc)
	if err != nil {
		return nil, err
	}
	s.listener = l
	return s, nil
}

// Run opens the world, starts the server and runs it until ctx is
// cancelled or a transport fails to open. It closes every connection, then
// saves and closes the world, before it returns.
func (s *Server) Run(ctx context.Context) (err error) {
	if err := s.openWorld(); err != nil {
		_ = s.listener.Close()
		return err
	}
	defer func() {
		if cerr := s.closeWorld(); cerr != nil {
			err = errors.Join(err, cerr)
		}
	}()
	if s.auth != nil {
		// BDS sets up its token checks at startup. If Microsoft's services
		// are down, players are refused with HostWithoutKeys until a retry
		// in the background succeeds.
		if err := s.auth.Start(ctx); err != nil {
			s.log.Warn("Xbox Live checks are not ready; players cannot join until they are", "err", err)
		} else {
			s.log.Info("Xbox Live checks ready", "issuer", s.auth.Issuer(), "clock offset", s.auth.Offset())
		}
	}
	if err := s.openTransports(); err != nil {
		_ = s.listener.Close()
		return err
	}
	s.updateStatus()

	stop := context.AfterFunc(ctx, func() { _ = s.listener.Close() })
	defer stop()
	for {
		c, err := s.listener.Accept()
		if err != nil {
			break
		}
		s.admit(c)
	}
	_ = s.listener.Close()
	for _, p := range s.global.online() {
		p.conn.Disconnect(packet.DisconnectReasonShutdown, "")
	}
	s.wg.Wait()
	s.global.close()
	return nil
}

func (s *Server) openTransports() error {
	if s.conf.NetherNet.Enabled {
		key, err := s.identityKey()
		if err != nil {
			return err
		}
		t, err := network.NetherNet(network.NetherNetConfig{
			Address: s.conf.NetherNet.Address, IdentityKey: key, UDPPort: s.conf.NetherNet.UDPPort,
			AllowAnonymous: s.conf.NetherNet.AllowAnonymous, Log: s.log.With("part", "nethernet"),
		})
		if err != nil {
			return err
		}
		s.listener.Serve(t)
		s.log.Info("NetherNet listening", "addr", t.Addr())
	}
	if s.conf.RakNet.Enabled {
		t, err := network.RakNet(s.conf.RakNet.Address, s.log.With("part", "raknet"))
		if err != nil {
			return err
		}
		s.listener.Serve(t)
		s.log.Info("RakNet listening", "addr", t.Addr())
	}
	return nil
}

// identityKey loads the server identity key, or makes one as BDS does.
func (s *Server) identityKey() (*ecdsa.PrivateKey, error) {
	path := s.conf.NetherNet.IdentityKeyPath
	if path == "" {
		key, err := nethernet.GenerateIdentityKey()
		s.log.Warn("no identity key file set; players must trust the server again after every restart")
		return key, err
	}
	key, saved, err := nethernet.LoadOrGenerateIdentityKey(path)
	if err != nil {
		return nil, err
	}
	switch {
	case saved:
		s.log.Info("server identity key loaded", "path", path)
	case s.conf.NetherNet.SaveIdentityKey:
		if err := nethernet.SaveIdentityKey(path, key); err != nil {
			return nil, err
		}
		s.log.Info("server identity key generated and saved", "path", path)
	default:
		s.log.Warn("server identity key generated for this run only; players must trust the server again after a restart", "path", path)
	}
	return key, nil
}

// checkAlreadyOnline refuses a player whose identity is already online, as
// BDS does with ServerIdConflict.
func (s *Server) checkAlreadyOnline(_ context.Context, lg *network.Login) error {
	if s.online(lg.Identity.Identity) {
		return network.Refuse(packet.DisconnectReasonServerIdConflict, "", errors.New("already online"))
	}
	return nil
}

// checkServerFull refuses players beyond MaxPlayers, as BDS does after
// encryption with ServerFull.
func (s *Server) checkServerFull(_ context.Context, _ *network.Login) error {
	if s.PlayerCount() >= s.conf.MaxPlayers {
		return network.Refuse(packet.DisconnectReasonServerFull, "", errors.New("server full"))
	}
	return nil
}

func (s *Server) online(identity string) bool {
	id, err := uuid.Parse(identity)
	if err != nil {
		return false
	}
	for _, p := range s.global.online() {
		if p.uuid == id {
			return true
		}
	}
	return false
}

// admit takes a player that finished the login. The checks run again on
// the global owner, which owns the player list, because two logins of the
// same player, or for the last free slot, may pass their steps at once.
func (s *Server) admit(c *network.Conn) {
	id, err := uuid.Parse(c.Identity().Identity)
	if err != nil {
		c.Disconnect(packet.DisconnectReasonNotAuthenticated, "")
		return
	}
	p, err := s.newPlayer(c, id)
	if err != nil {
		s.log.Info("player refused", "name", c.Identity().DisplayName, "err", err)
		c.Disconnect(packet.DisconnectReasonInvalidPlayer, "")
		return
	}
	var dup, full bool
	s.global.call(func() {
		_, dup = s.global.players[id]
		full = len(s.global.players) >= s.conf.MaxPlayers
		if !dup && !full {
			s.global.add(p)
		}
	})
	switch {
	case dup:
		c.Disconnect(packet.DisconnectReasonServerIdConflict, "")
		return
	case full:
		c.Disconnect(packet.DisconnectReasonServerFull, "")
		return
	}
	s.log.Info("Player connected", "name", p.name, "xuid", p.xuid, "addr", c.RemoteAddr())
	s.updateStatus()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		p.play()
		s.global.call(func() {
			s.global.remove(p)
			pk := &packet.PlayerList{Entries: []protocol.PlayerListEntry{{ActionType: protocol.PlayerListActionRemove, UUID: p.uuid}}}
			for _, o := range s.global.order {
				_ = o.conn.WritePacket(pk)
				_ = o.conn.Flush()
			}
		})
		_ = c.Close()
		s.log.Info("Player disconnected", "name", p.name, "xuid", p.xuid)
		s.updateStatus()
	}()
}

// buildRegistry registers vanilla (the registry and the packet handlers),
// runs Config.Setup and freezes it all. It runs once, in New, before any
// transport can open.
func (s *Server) buildRegistry() error {
	reg := registry.NewSetup(s.log.With("part", "registry"), "minecraft")
	if err := registry.Vanilla(reg); err != nil {
		return err
	}
	setup := newSetup(reg, s.log.With("part", "setup"))
	if err := vanillaHandlers(setup); err != nil {
		return err
	}
	if s.conf.Setup != nil {
		if err := s.conf.Setup(setup.As("server")); err != nil {
			return fmt.Errorf("helix: setup: %w", err)
		}
	}
	r, err := reg.Freeze()
	if err != nil {
		return err
	}
	s.reg, s.handlers = r, setup.freeze()
	// Changes to the creative inventory or recipes reach players already
	// online at once. Whether BDS re-sends these packets the same way is
	// still to be checked per packet.
	r.Creative.OnChange(func(p *packet.CreativeContent) { s.broadcast(p) })
	r.Crafting.OnChange(func(p *packet.CraftingData) { s.broadcast(p) })
	s.log.Info("Registry frozen", "block states", r.Blocks.Count(), "items", r.Items.Count(),
		"biomes", r.Biomes.Count(), "entity types", r.EntityTypes.Count())
	return nil
}

// broadcast sends a packet to every player online.
func (s *Server) broadcast(pk packet.Packet) {
	for _, p := range s.global.online() {
		if p.conn.WritePacket(pk) == nil {
			_ = p.conn.Flush()
		}
	}
}

// Registry is everything built at boot: blocks, items, biomes and entity
// types (frozen), and the creative inventory and recipes (changeable).
func (s *Server) Registry() *registry.Registry { return s.reg }

// PlayerCount returns how many players are online.
func (s *Server) PlayerCount() int { return len(s.global.online()) }

func (s *Server) updateStatus() {
	st := network.Status{
		Name: s.conf.Name, LevelName: s.conf.LevelName, Players: s.PlayerCount(), MaxPlayers: s.conf.MaxPlayers,
		GameMode: "Survival", ServerID: s.serverID, Port: port(s.conf.RakNet.Address), PortV6: port(s.conf.RakNet.Address),
	}
	s.listener.SetStatus(st)
}

// port returns the port of an address, or 19132.
func port(addr string) uint16 {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 19132
	}
	var n uint16
	if _, err := fmt.Sscan(p, &n); err != nil {
		return 19132
	}
	return n
}

// isClosed reports whether err only says a connection closed.
func isClosed(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrDeadlineExceeded)
}
