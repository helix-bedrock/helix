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
	"github.com/helix-bedrock/helix/liquid"
	"log/slog"
	"net"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/helix-bedrock/helix/circuit"
	"github.com/helix-bedrock/helix/commands"
	"github.com/helix-bedrock/helix/daytime"
	"github.com/helix-bedrock/helix/fall"
	"github.com/helix-bedrock/helix/farm"
	"github.com/helix-bedrock/helix/fire"
	"github.com/helix-bedrock/helix/generator"
	"github.com/helix-bedrock/helix/network"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/helix/portal"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/weather"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/nethernet"
	"github.com/helix-bedrock/protocol/auth"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/hxdb"
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
	// WorldType names how chunks the world has not saved are made: a name in
	// Generators, by default "vanilla" (BDS's terrain), "flat" or "void". Empty
	// means the type level.dat names, and vanilla for a new world. A type set
	// here is written to level.dat, so the world stays that type.
	WorldType string
	// WorldTypeOptions is the type's own setting: for vanilla the path of a
	// worldgen JSON file that changes the world (biomes, features,
	// structures, terrain), for flat the layers as FlatWorldLayers JSON.
	// Empty means the type's defaults.
	WorldTypeOptions string
	// Generators are the world types WorldType picks from. DefaultConfig
	// fills in vanilla, flat and void; add your own here. Nil means those
	// three.
	Generators generator.Types
	// Seed is a new world's seed, BDS's level-seed. Nil means a random one.
	// An existing world keeps its own.
	Seed *int64
	// Generator, when set, makes the overworld's chunks instead of the world
	// type: for a generator built outside the type table.
	Generator world.Generator
	// BlockTicker gives blocks their ticks and their reactions to changes
	// beside them. Nil means vanilla's, which so far is water and lava
	// (package liquid).
	BlockTicker world.BlockTicker
	// ViewDistance is the largest view radius in chunks a player gets,
	// BDS's view-distance.
	ViewDistance int32
	// TickDistance is the radius in chunks kept loaded and ticked around
	// each player, BDS's tick-distance.
	TickDistance int32
	// EntityViewRadius2 is the square of the radius in chunks within which
	// players see other players and entities, whatever the view and tick
	// distances; see world.Config.EntityViewRadius2.
	EntityViewRadius2 int32
	// TickCores is how many goroutines tick the world's regions, shared by
	// every dimension. 0 means one per CPU; 1 ticks regions one after
	// another in a fixed order, for debugging and exact reproduction.
	TickCores int
	// Setup runs at boot after vanilla is registered, before everything is
	// frozen: plugins register, replace and delete blocks, items, biomes
	// and entity types, and replace or wrap packet handlers here. Nil means
	// vanilla only.
	Setup func(s *Setup) error
	// Redstone is every number redstone runs by: hopper timing, delays, signal reach and
	// the rest. Its zero value means [circuit.DefaultConfig], BDS's numbers. Change it
	// while running with [Server.SetRedstone].
	Redstone circuit.Config
	// MaxPlayers is how many players may be online at once.
	MaxPlayers int
	// OnlineMode requires every player to be signed in to Xbox Live.
	OnlineMode bool
	// NetherNet configures the NetherNet transport, BDS's default.
	NetherNet NetherNetConfig
	// RakNet configures the RakNet transport.
	RakNet RakNetConfig
	// Transports are more transports to serve besides NetherNet and RakNet: a plugin's own,
	// or a network.Memory for bots in the same process (package sim).
	Transports []network.Transport
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
	// LAN answers players looking for games on this network, BDS's LAN
	// signaling.
	LAN bool
	// UDPPort carries every connection over one UDP port when not 0, so one
	// forwarded port is enough. BDS gives each connection its own port.
	UDPPort uint16
	// PortMappings is BDS's server-port-mappings: public:local single ports.
	PortMappings string
	// SignInID is BDS's nethernet-id, a UUID. When set, the server signs in to
	// Microsoft's signaling service as BDS does.
	SignInID string
	// ApplicationID and ApplicationTenantID are BDS's application-id and
	// application-tenant-id, the owner's Azure application that signs in with
	// the certificate in AppCertificate.pfx.
	ApplicationID, ApplicationTenantID string
	// UDPPorts is BDS's server-udp-ports: ports or ranges, each maybe
	// public:local, with maybe an address first. With UDPPort 0, connections
	// take their local port from them.
	UDPPorts string
	// PublicIP is BDS's server-public-ip, the address players outside this
	// network reach the server on, for a server behind a router. Empty offers
	// only the machine's own addresses.
	PublicIP string
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
// distance of 32 chunks, a tick distance of 4, entities seen within BDS's
// 57-chunk disc, online mode, NetherNet on port 19132 with the identity key in
// keys/server_identity_key.pem, each connection on its own UDP port, and LAN
// signaling on. Helix differs in one way: RakNet is off, as Minecraft is
// removing it. Turn it on with RakNet.Enabled. For one forwarded UDP port, which
// BDS cannot do, set NetherNet.UDPPort.
func DefaultConfig() Config {
	return Config{
		Name:              "Dedicated Server",
		LevelName:         "Bedrock level",
		WorldsDir:         "worlds",
		MaxPlayers:        10,
		ViewDistance:      32,
		TickDistance:      world.DefaultConfig().SimulationDistance,
		EntityViewRadius2: world.DefaultConfig().EntityViewRadius2,
		OnlineMode:        true,
		NetherNet: NetherNetConfig{
			Enabled:         true,
			Address:         ":19132",
			IdentityKeyPath: nethernet.DefaultIdentityKeyPath,
			LAN:             true,
		},
		RakNet:     RakNetConfig{Address: ":19132"},
		Network:    network.DefaultConfig(),
		Redstone:   circuit.DefaultConfig(),
		Generators: generator.Default(),
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
	circuits *circuit.Ticker
	// fire is the vanilla fire ticker, nil when the config gave a BlockTicker.
	fire *fire.Ticker
	farm *farm.Ticker
	// liquids is vanilla's water and lava, also the world's Settler; nil with a custom BlockTicker.
	liquids *liquid.Liquids
	// falling is the vanilla falling block rules, nil when the config gave a BlockTicker.
	falling *fall.Table
	// weather is the world's weather, chunkWeather the overworld's lightning, ice and snow.
	weather      *weather.Weather
	chunkWeather *weather.Ticker
	// clock is the world's time, set when the world opens.
	clock    *daytime.Clock
	redstone *circuit.Settings

	db          *hxdb.DB
	worldStore  mcdb.WorldStore
	playerStore mcdb.PlayerStore
	level       player.Level
	ticker      *world.Scheduler
	overworld   *world.Dimension
	nether, end *world.Dimension
	portals     *portal.Portals
	// endGen is the End's generator; endPodium is set once its exit portal was
	// looked at this run.
	endGen    world.Generator
	endPodium atomic.Bool

	// setup is the frozen-at-Run player setup; players runs them once the
	// world is open.
	setup   *player.Setup
	players atomic.Pointer[player.Host]
	// crash is set by the first recovered panic: Run stops and returns it.
	crash     atomic.Pointer[crashError]
	crashOnce sync.Once
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
	if !conf.NetherNet.Enabled && !conf.RakNet.Enabled && len(conf.Transports) == 0 {
		return nil, errors.New("helix: no transport enabled")
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, fmt.Errorf("helix: server ID: %w", err)
	}
	if conf.Redstone == (circuit.Config{}) {
		conf.Redstone = circuit.DefaultConfig()
	}
	s := &Server{conf: conf, log: conf.Log, serverID: int64(binary.LittleEndian.Uint64(id[:])), redstone: circuit.NewSettings(conf.Redstone)}
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

// Redstone returns the numbers redstone runs by now. Change a copy and give it to
// [Server.SetRedstone].
func (s *Server) Redstone() circuit.Config { return *s.redstone.Get() }

// SetRedstone changes the numbers redstone runs by while the server runs. Each part reads
// them the next time it needs one: a wait already counting down finishes first. Numbers
// that would stop something working are brought back in range.
func (s *Server) SetRedstone(c circuit.Config) { s.redstone.Set(c) }

// Run opens the world, starts the server and runs it until ctx is
// cancelled or a transport fails to open. It closes every connection, then
// saves and closes the world, before it returns.
func (s *Server) Run(ctx context.Context) (err error) {
	defer s.runDone(&err)
	if err := s.openWorld(); err != nil {
		_ = s.listener.Close()
		return err
	}
	defer func() {
		if cerr := s.closeWorld(); cerr != nil {
			err = errors.Join(err, cerr)
		}
	}()
	s.startAuth(ctx)
	host, err := s.newHost()
	if err != nil {
		_ = s.listener.Close()
		return err
	}
	defer host.Close(packet.DisconnectReasonShutdown)
	stopPeriodic := s.startPeriodic(host)
	defer stopPeriodic()
	if err := s.openTransports(); err != nil {
		_ = s.listener.Close()
		return err
	}
	s.updateStatus()

	// Players are told first: closing the listener closes the transports, and with them the
	// sockets the players' connections run over, so a disconnect sent after it never arrives.
	stop := context.AfterFunc(ctx, func() {
		host.Kick(packet.DisconnectReasonShutdown)
		_ = s.listener.Close()
	})
	defer stop()
	s.accept(host)
	return nil
}

// runDone is deferred by Run: it recovers a panic and adds the crash, if any, to Run's
// error.
func (s *Server) runDone(err *error) {
	if v := recover(); v != nil {
		s.panicked(v, debug.Stack())
	}
	if c := s.crash.Load(); c != nil {
		*err = errors.Join(*err, c)
	}
}

// startAuth starts the Xbox Live token checks. BDS sets them up at startup; if
// Microsoft's services are down, players are refused with HostWithoutKeys until a retry
// in the background succeeds.
func (s *Server) startAuth(ctx context.Context) {
	if s.auth == nil {
		return
	}
	if err := s.auth.Start(ctx); err != nil {
		s.log.Warn("Xbox Live checks are not ready; players cannot join until they are", "err", err)
		return
	}
	s.log.Info("Xbox Live checks ready", "issuer", s.auth.Issuer(), "clock offset", s.auth.Offset())
}

// newHost makes the player Host over the open world and hooks it to the server's
// systems.
func (s *Server) newHost() (*player.Host, error) {
	host, err := player.NewHost(player.HostConfig{
		Log:          s.log.With("part", "players"),
		Registry:     s.reg,
		Overworld:    s.overworld,
		Nether:       s.nether,
		End:          s.end,
		Portals:      s.portals,
		OnEnterEnd:   s.enterEnd,
		Store:        s.playerStore,
		WorldStore:   s.worldStore,
		Level:        s.level,
		ViewDistance: s.conf.ViewDistance,
		MaxPlayers:   s.conf.MaxPlayers,
		Redstone:     s.redstone,
		Farm:         s.farm,
		Fire:         s.fire,
		OnPanic:      s.panicked,
	}, s.setup)
	if err != nil {
		return nil, err
	}
	host.SetClock(s.clock)
	host.SetWeather(s.weather, s.chunkWeather)
	if s.falling != nil {
		host.SetFalling(s.falling)
	}
	s.players.Store(host)
	if s.circuits != nil {
		s.hookCircuits(host)
	}
	return host, nil
}

// hookCircuits connects redstone and the players both ways: what redstone asks the
// players' world (dispensers, entity boxes, pistons pushing, carts, TNT) and what
// players do to redstone blocks.
func (s *Server) hookCircuits(host *player.Host) {
	c := s.circuits
	c.SetDispense(host.Dispense)
	c.SetEntityBox(host.EntityBox)
	c.SetPlateBox(host.PlateBox, host.IsArrow)
	c.SetEntityPush(host.PistonPush)
	c.SetCartBox(host.CartBox)
	c.SetPowerTNT(host.PowerTNT)
	host.SetCircuitHooks(player.CircuitHooks{
		Use:           c.UseBlock,
		Attack:        c.AttackBlock,
		JukeboxPower:  c.SetJukeboxPower,
		RingBell:      c.RingBell,
		WindBell:      c.WindBell,
		Destroy:       c.DestroyBlock,
		ChestViewers:  c.SetChestViewers,
		CartOnRail:    c.CartOnRail,
		HitTarget:     c.HitTarget,
		StrikeRod:     c.StrikeRod,
		PulseLectern:  c.PulseLectern,
		ActivateSculk: c.ActivateSculkSensor,
		SculkInput:    c.SculkInput,
		Signal:        c.Signal,
		RedstoneTick:  c.RedstoneTick,
	})
	c.SetSculk(circuit.SculkHooks{SensorQuiet: host.SculkSensorQuiet, ShriekerTick: host.ShriekerTick})
	for _, d := range []*world.Dimension{s.overworld, s.nether, s.end} {
		d.SetBlockEntities(c.Entities(d.BlockEntityMaker()))
	}
}

// accept admits every connection the listener takes until it closes.
func (s *Server) accept(host *player.Host) {
	for {
		c, err := s.listener.Accept()
		if err != nil {
			break
		}
		host.Admit(c, s.updateStatus)
	}
	_ = s.listener.Close()
}

func (s *Server) openTransports() error {
	if s.conf.NetherNet.Enabled {
		key, err := s.identityKey()
		if err != nil {
			return err
		}
		t, err := network.NetherNet(network.NetherNetConfig{
			Address: s.conf.NetherNet.Address, IdentityKey: key, UDPPort: s.conf.NetherNet.UDPPort, UDPPorts: s.conf.NetherNet.UDPPorts, PortMappings: s.conf.NetherNet.PortMappings, LAN: s.conf.NetherNet.LAN, PublicIP: s.conf.NetherNet.PublicIP,
			SignInID: s.conf.NetherNet.SignInID, ApplicationID: s.conf.NetherNet.ApplicationID, ApplicationTenantID: s.conf.NetherNet.ApplicationTenantID,
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
	for _, t := range s.conf.Transports {
		s.listener.Serve(t)
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
	host := s.players.Load()
	if host == nil {
		return false
	}
	_, ok := host.Find(id)
	return ok
}

// buildRegistry registers vanilla (the registry and the player setup),
// runs Config.Setup and freezes the registry. The player setup is frozen
// when Run starts the players. It runs once, in New, before any transport
// can open.
func (s *Server) buildRegistry() error {
	reg := registry.NewSetup(s.log.With("part", "registry"), "minecraft")
	if err := registry.Vanilla(reg); err != nil {
		return err
	}
	ps, err := player.NewSetup(s.log.With("part", "setup"))
	if err != nil {
		return err
	}
	if err := commands.Register(ps); err != nil {
		return fmt.Errorf("helix: commands: %w", err)
	}
	setup := &Setup{Setup: reg, Player: ps}
	if s.conf.Setup != nil {
		if err := s.conf.Setup(setup.As("server")); err != nil {
			return fmt.Errorf("helix: setup: %w", err)
		}
	}
	r, err := reg.Freeze()
	if err != nil {
		return err
	}
	s.reg, s.setup = r, ps
	// Changes to the creative inventory or recipes reach players already
	// online at once. Whether BDS re-sends these packets the same way is
	// still to be checked per packet.
	r.Creative.OnChange(func(p *packet.CreativeContent) { s.broadcast(p) })
	r.Crafting.OnChange(func(*packet.CraftingData) {
		// Each player gets its own list: some have recipes taken away.
		if host := s.players.Load(); host != nil {
			host.SendRecipes()
		}
	})
	r.Trims.OnChange(func(p *packet.TrimData) { s.broadcast(p) })
	s.log.Info("Registry frozen", "block states", r.Blocks.Count(), "items", r.Items.Count(),
		"biomes", r.Biomes.Count(), "entity types", r.EntityTypes.Count())
	return nil
}

// broadcast sends a packet to every player online.
func (s *Server) broadcast(pk packet.Packet) {
	if host := s.players.Load(); host != nil {
		host.Broadcast(pk)
	}
}

// Registry is everything built at boot: blocks, items, biomes and entity
// types (frozen), and the creative inventory and recipes (changeable).
func (s *Server) Registry() *registry.Registry { return s.reg }

// Host returns what runs the players, nil until Run started.
func (s *Server) Host() *player.Host { return s.players.Load() }

// TPS is the server's ticks per second: the mean TPS of every region of every dimension
// (world.Region.TPS), world.MaxTPS with none. One lagging region pulls it down only by its
// share; Dimension.Regions gives each region's own. Runs: anywhere.
func (s *Server) TPS() float64 {
	sum, n := 0.0, 0
	for _, d := range []*world.Dimension{s.overworld, s.nether, s.end} {
		if d == nil {
			continue
		}
		ds, dn := d.SumTPS()
		sum, n = sum+ds, n+dn
	}
	if n == 0 {
		return world.MaxTPS
	}
	return sum / float64(n)
}

// Players returns the players online. Empty until Run started.
func (s *Server) Players() []*player.Player {
	if host := s.players.Load(); host != nil {
		return host.Online()
	}
	return nil
}

// SetItemLifetime changes how many ticks old items vanish at, for every item
// without its own lifetime, also those already in the world. Vanilla's is 6000;
// player.ItemLifetimeForever keeps them. Does nothing until Run started: to set
// it from the start, use player.Systems' ItemLifetime in Config.Setup.
func (s *Server) SetItemLifetime(ticks int32) {
	if host := s.players.Load(); host != nil {
		host.SetItemLifetime(ticks)
	}
}

// SetTuning changes a player.Tune for every player without its own value, false if
// it could not. Does nothing until Run started: to set a default from the start, use
// player.Setup's SetTuning in Config.Setup.
func (s *Server) SetTuning(t player.Tune, v float32) bool {
	host := s.players.Load()
	return host != nil && host.SetTuning(t, v)
}

// EditEnchantment changes a registered enchantment while the server runs. fn gets a
// copy, and may run more than once.
func (s *Server) EditEnchantment(id int16, fn func(e *player.Enchantment)) bool {
	host := s.players.Load()
	return host != nil && host.EditEnchantment(id, fn)
}

// ClearTuning puts a player.Tune back to its default.
func (s *Server) ClearTuning(t player.Tune) {
	if host := s.players.Load(); host != nil {
		host.ClearTuning(t)
	}
}

// ClearAllTuning puts every player.Tune back to its default.
func (s *Server) ClearAllTuning() {
	if host := s.players.Load(); host != nil {
		host.ClearAllTuning()
	}
}

// PlayerCount returns how many players are online.
func (s *Server) PlayerCount() int { return len(s.Players()) }

func (s *Server) updateStatus() {
	st := network.Status{
		Name: s.conf.Name, LevelName: s.conf.LevelName, Players: s.PlayerCount(), MaxPlayers: s.conf.MaxPlayers,
		GameMode: "Survival", ServerID: s.serverID, Port: port(s.conf.NetherNet.Address), PortV6: port(s.conf.NetherNet.Address),
		OnlineAuth: s.conf.OnlineMode, SelfSignedAuth: !s.conf.OnlineMode,
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

// clockSyncTicks is how often BDS tells clients the time.
const clockSyncTicks = 256

// Clock returns the world clock. Valid once the world is open. Safe from any goroutine.
func (s *Server) Clock() *daytime.Clock { return s.clock }

// SetTime changes the time and tells everyone online. Safe from any goroutine.
func (s *Server) SetTime(time int32) {
	s.clock.Set(time)
	if host := s.players.Load(); host != nil {
		host.SyncClocks()
	}
}

// SetRandomTickSpeed changes the randomtickspeed game rule in every dimension from the next
// tick on. level.dat keeps its own value, so a restart goes back to it. Valid once the world is
// open. Safe from any goroutine.
func (s *Server) SetRandomTickSpeed(speed int32) {
	for _, d := range []*world.Dimension{s.overworld, s.nether, s.end} {
		if d != nil {
			d.SetRandomTickSpeed(speed)
		}
	}
}

// crashError is a panic the server recovered and stopped for.
type crashError struct{ v any }

func (c *crashError) Error() string { return fmt.Sprintf("helix: crashed: %v", c.v) }

// panicked is a panic recovered anywhere in the server. Helix's own choice, as BDS sends
// nothing when it crashes: every player is told the server shut down at once, without
// waiting for a tick, then Run stops and saves what it can. Runs: the goroutine that panicked.
func (s *Server) panicked(v any, stack []byte) {
	s.log.Error("Server crashed; stopping", "panic", v, "stack", string(stack))
	s.crashOnce.Do(func() {
		s.crash.Store(&crashError{v: v})
		if h := s.players.Load(); h != nil {
			h.Kick(packet.DisconnectReasonShutdown)
		}
		if s.listener != nil {
			_ = s.listener.Close()
		}
	})
}

// levelSaveInterval is how often level.dat is written while running, so being killed loses little.
const levelSaveInterval = 30 * time.Second

// startPeriodic runs what happens every so often outside any tick: telling clients the time and
// saving level.dat. The func it returns stops it and waits, so closing the world follows it.
func (s *Server) startPeriodic(host *player.Host) (stop func()) {
	quit, done := make(chan struct{}), make(chan struct{})
	go s.periodic(host, quit, done)
	return func() {
		close(quit)
		<-done
	}
}

// periodic is startPeriodic's goroutine: it runs until quit closes, then closes done.
func (s *Server) periodic(host *player.Host, quit <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	defer s.recovered()
	clocks := time.NewTicker(clockSyncTicks * world.TickDuration)
	defer clocks.Stop()
	saves := time.NewTicker(levelSaveInterval)
	defer saves.Stop()
	for {
		select {
		case <-quit:
			return
		case <-clocks.C:
			host.SyncClocks()
		case <-saves.C:
			if err := s.saveLevel(); err != nil {
				s.log.Error("Save level.dat", "err", err)
			}
		}
	}
}

// recovered is deferred by the server's own goroutines: it recovers a panic and passes it
// to panicked.
func (s *Server) recovered() {
	if v := recover(); v != nil {
		s.panicked(v, debug.Stack())
	}
}
