package helix

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/cmd"
	"github.com/helix-bedrock/helix/server/world"
)

// Console reads commands from r, one per line, until r ends or the server
// closes. Besides every registered command, it understands:
//
//	plugins                 list plugins
//	plugins enable <name>   enable a plugin
//	plugins disable <name>  disable a plugin
//	stop                    close the server
//
// Output goes to w. Commands that need a player or a world do not work from
// the console.
func (s *Server) Console(r io.Reader, w io.Writer) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(sc.Text()), "/"))
		if line == "" {
			continue
		}
		args := strings.Fields(line)
		switch args[0] {
		case "stop":
			_, _ = fmt.Fprintln(w, "Stopping the server...")
			_ = s.Close()
			return
		case "plugins", "pl":
			s.pluginsCommand(args[1:], w)
			continue
		}
		c, ok := cmd.ByAlias(args[0])
		if !ok {
			_, _ = fmt.Fprintf(w, "Unknown command %q.\n", args[0])
			continue
		}
		c.Execute(strings.Join(args[1:], " "), consoleSource{w: w}, nil)
	}
}

func (s *Server) pluginsCommand(args []string, w io.Writer) {
	if len(args) == 2 && (args[0] == "enable" || args[0] == "disable") {
		var err error
		if args[0] == "enable" {
			err = s.plugins.Enable(args[1])
		} else {
			err = s.plugins.Disable(args[1])
		}
		if err != nil {
			_, _ = fmt.Fprintln(w, err)
			return
		}
		_, _ = fmt.Fprintf(w, "%s %sd.\n", args[1], args[0])
		return
	}
	if len(args) != 0 {
		_, _ = fmt.Fprintln(w, "Usage: plugins [enable|disable <name>]")
		return
	}
	_, _ = fmt.Fprintln(w, s.pluginList())
}

// pluginList describes the plugins in one line.
func (s *Server) pluginList() string {
	list := s.plugins.List()
	if len(list) == 0 {
		return "No plugins."
	}
	parts := make([]string, 0, len(list))
	for _, p := range list {
		state := "§a"
		if !p.Enabled {
			state = "§c"
		}
		v := ""
		if p.Info.Version != "" {
			v = " " + p.Info.Version
		}
		parts = append(parts, state+p.Info.Name+v+"§r")
	}
	return fmt.Sprintf("Plugins (%d): %s", len(list), strings.Join(parts, ", "))
}

// consoleSource is the console as a command source.
type consoleSource struct{ w io.Writer }

func (consoleSource) Position() mgl64.Vec3 { return mgl64.Vec3{} }
func (consoleSource) Name() string         { return "Console" }

func (c consoleSource) SendCommandOutput(o *cmd.Output) {
	for _, m := range o.Messages() {
		_, _ = fmt.Fprintln(c.w, m.String())
	}
	for _, e := range o.Errors() {
		_, _ = fmt.Fprintln(c.w, e.Error())
	}
}

// pluginsRunnable is the in-game /plugins command: it lists plugins.
// Enabling and disabling is left to the console until Helix has
// permissions.
type pluginsRunnable struct{ s *Server }

func (r pluginsRunnable) Run(_ cmd.Source, o *cmd.Output, _ *world.Tx) {
	o.Print(r.s.pluginList())
}

// Close disables every plugin, so each can save its data, stops their
// tasks and closes the server.
func (s *Server) Close() error {
	if s.plugins != nil {
		for _, st := range s.plugins.List() {
			_ = s.plugins.Disable(st.Info.Name)
		}
		s.plugins.mu.Lock()
		states := append([]*pluginState(nil), s.plugins.order...)
		s.plugins.mu.Unlock()
		for _, st := range states {
			st.stopTasks()
		}
	}
	return s.Server.Close()
}

// CloseOnProgramEnd closes the server, with its plugins, when the program
// is interrupted (ctrl+c) or terminated.
func (s *Server) CloseOnProgramEnd() {
	c := make(chan os.Signal, 2)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-c
		if err := s.Close(); err != nil {
			s.log.Error("close server", "err", err)
		}
	}()
}
