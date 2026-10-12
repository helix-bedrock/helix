// Command protect is an example Helix server with one plugin: it stops
// players breaking diamond ore and logs every block placed.
package main

import (
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/event"
	"github.com/helix-bedrock/helix/server"
	"github.com/helix-bedrock/helix/server/cmd"
	"github.com/helix-bedrock/helix/server/world"
)

// Protect is the plugin. Its data lives in its fields.
type Protect struct {
	helix.Plugin
	conf    Config
	blocked atomic.Int64
}

// Config is the plugin's config.toml in plugins/Protect.
type Config struct {
	Message string
}

// Info names the plugin.
func (p *Protect) Info() helix.Info {
	return helix.Info{Name: "Protect", Description: "Protects diamond ore.", Version: "1.0.0"}
}

// Setup registers the plugin's listeners.
func (p *Protect) Setup(r *helix.Setup) {
	r.BlockBreak(func(e *event.BlockBreak) {
		e.Cancel()
		p.blocked.Add(1)
		e.Player.Message(p.conf.Message)
	}, helix.OnlyBlock("minecraft:diamond_ore"), helix.Priority(event.High))

	r.Command(cmd.New("protected", "Shows how many breaks were stopped.", nil, protectedCommand{p}))

	r.BlockPlace(func(e *event.BlockPlace) {
		p.Log().Info("block placed", "player", e.Player.Name(), "pos", e.Pos)
	}, helix.Priority(event.Monitor), helix.IgnoreCancelled())
}

// Enable loads the config and starts a task that reports every minute.
func (p *Protect) Enable() {
	p.conf = Config{Message: "§cDiamond ore is protected."}
	if err := p.Config(&p.conf); err != nil {
		p.Log().Error("config", "err", err)
	}
	p.Every(time.Minute, func() {
		p.Log().Info("breaks stopped so far", "count", p.blocked.Load())
	})
}

// protectedCommand is /protected.
type protectedCommand struct{ p *Protect }

func (c protectedCommand) Run(_ cmd.Source, o *cmd.Output, _ *world.Tx) {
	o.Printf("%d diamond ore breaks stopped.", c.p.blocked.Load())
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
	go srv.Console(os.Stdin, os.Stdout)
	srv.Run()
}
