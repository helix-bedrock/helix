// Command protect is an example Helix server with one plugin: it stops
// players breaking diamond ore and logs every block placed.
package main

import (
	"log/slog"

	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/event"
	"github.com/helix-bedrock/helix/server"
)

// Protect is the plugin. Its data would live in its fields.
type Protect struct {
	helix.Plugin
}

// Info names the plugin.
func (p *Protect) Info() helix.Info {
	return helix.Info{Name: "Protect", Description: "Protects diamond ore.", Version: "1.0.0"}
}

// Setup registers the plugin's listeners.
func (p *Protect) Setup(r *helix.Setup) {
	r.BlockBreak(func(e *event.BlockBreak) {
		e.Cancel()
		e.Player.Message("§cDiamond ore is protected.")
	}, helix.OnlyBlock("minecraft:diamond_ore"), helix.Priority(event.High))

	r.BlockPlace(func(e *event.BlockPlace) {
		p.Log().Info("block placed", "player", e.Player.Name(), "pos", e.Pos)
	}, helix.Priority(event.Monitor), helix.IgnoreCancelled())
}

func main() {
	conf, err := server.DefaultConfig().Config(slog.Default())
	if err != nil {
		slog.Error("config", "err", err)
		return
	}
	srv, err := helix.New(helix.Config{Server: conf, Plugins: []helix.Pluggable{&Protect{}}})
	if err != nil {
		slog.Error("plugins", "err", err)
	}
	srv.CloseOnProgramEnd()
	srv.Run()
}
