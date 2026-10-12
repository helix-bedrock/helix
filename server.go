package helix

import (
	"github.com/helix-bedrock/helix/server/item/inventory"
	"log/slog"

	"github.com/helix-bedrock/helix/server"
	"github.com/helix-bedrock/helix/server/player"
	"github.com/helix-bedrock/helix/server/world"
)

// Config configures a Server.
type Config struct {
	// Server is the engine configuration, such as from server.UserConfig.
	Server server.Config
	// Plugins are loaded in dependency order when the server is created.
	Plugins []Pluggable
	// PanicLimit disables a plugin that panics too often. The zero value
	// never disables.
	PanicLimit PanicLimit
}

// Server is a Helix server with plugins.
type Server struct {
	*server.Server
	log     *slog.Logger
	plugins *Plugins

	playerEvents    *player.Events
	worldEvents     *world.Events
	inventoryEvents *inventory.Events
}

// Pluggable is satisfied by every plugin: a pointer to a struct embedding
// helix.Plugin with Info and Setup methods. Its unexported method means only
// structs embedding helix.Plugin qualify.
type Pluggable = plugin

// New creates the server, loads the plugins and sets the event dispatchers
// on its worlds. Plugins that fail to load are reported in the error, which
// is returned together with a working server: a broken plugin does not stop
// the others. Call Run to start accepting players.
func New(conf Config) (*Server, error) {
	if conf.Server.Log == nil {
		conf.Server.Log = slog.Default()
	}
	s := &Server{log: conf.Server.Log,
		playerEvents: &player.Events{}, worldEvents: &world.Events{}, inventoryEvents: &inventory.Events{}}
	conf.Server.PlayerEvents, conf.Server.WorldEvents, conf.Server.InventoryEvents = s.playerEvents, s.worldEvents, s.inventoryEvents
	s.Server = conf.Server.New()
	s.plugins = &Plugins{srv: s, byName: map[string]*pluginState{}}
	err := s.plugins.load(conf.Plugins, s.log, conf.PanicLimit)
	return s, err
}

// Plugins returns the server's plugins, to list, enable and disable them.
func (s *Server) Plugins() *Plugins { return s.plugins }

// Run listens for players and runs until the server closes.
func (s *Server) Run() {
	s.Listen()
	for range s.Accept() {
	}
}
