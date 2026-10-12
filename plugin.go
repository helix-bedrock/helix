package helix

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Info describes a plugin.
type Info struct {
	// Name identifies the plugin. Required, and unique on a server.
	Name string
	// Description says what the plugin does. Required.
	Description string
	// Version is shown in logs and plugin lists.
	Version string
	// Dependencies are the names of plugins that must load first. If one is
	// missing, the plugin refuses to load.
	Dependencies []string
}

// Plugin is embedded in every plugin struct. It gives the plugin its logger
// and server, and default Enable and Disable methods that do nothing.
type Plugin struct {
	state *pluginState
}

func (p *Plugin) base() *Plugin { return p }

// Log returns the plugin's logger, which names the plugin. It is nil until
// the plugin is loaded.
func (p *Plugin) Log() *slog.Logger {
	if p.state == nil {
		return nil
	}
	return p.state.log
}

// Server returns the server the plugin runs on. It is nil until the plugin
// is loaded.
func (p *Plugin) Server() *Server {
	if p.state == nil {
		return nil
	}
	return p.state.srv
}

// Enable is called when the plugin is enabled, after its listeners are
// active. Override it for the plugin's own start-up work.
func (p *Plugin) Enable() {}

// Disable is called when the plugin is disabled, before its listeners stop.
// Override it to save data.
func (p *Plugin) Disable() {}

// plugin is what a plugin struct must implement. base is promoted from the
// embedded Plugin, so only structs embedding it qualify.
type plugin interface {
	Info() Info
	Setup(r *Setup)
	Enable()
	Disable()
	base() *Plugin
}

// pluginState is the server's record of one loaded plugin.
type pluginState struct {
	p       plugin
	info    Info
	log     *slog.Logger
	srv     *Server
	enabled atomic.Bool
	limit   PanicLimit

	panicMu sync.Mutex
	panics  []time.Time

	taskMu sync.Mutex
	tasks  map[*Task]struct{}

	dir string // data folder
}

// PanicLimit disables a plugin that panics Count times within Window. The
// zero value never disables.
type PanicLimit struct {
	Count  int
	Window time.Duration
}

// Enabled reports whether the plugin's listeners run. It implements
// hook.Owner.
func (s *pluginState) Enabled() bool { return s.enabled.Load() }

// Run runs a listener of the plugin. It implements hook.Owner.
func (s *pluginState) Run(event string, f func()) { s.call(event, f) }

// call runs f as plugin code: a panic is recovered and logged with the
// plugin's name and counted towards the panic limit.
func (s *pluginState) call(what string, f func()) {
	defer func() {
		if v := recover(); v != nil {
			s.log.Error("plugin panicked", "in", what, "panic", v, "stack", string(debug.Stack()))
			s.countPanic()
		}
	}()
	f()
}

func (s *pluginState) countPanic() {
	if s.limit.Count <= 0 {
		return
	}
	now := time.Now()
	s.panicMu.Lock()
	s.panics = append(s.panics, now)
	s.panics = slices.DeleteFunc(s.panics, func(t time.Time) bool { return now.Sub(t) > s.limit.Window })
	over := len(s.panics) >= s.limit.Count
	if over {
		s.panics = nil
	}
	s.panicMu.Unlock()
	if over {
		s.log.Error("plugin reached its panic limit and is disabled", "limit", s.limit.Count, "window", s.limit.Window)
		// Disable from another goroutine: this may run inside a listener.
		go func() { _ = s.srv.plugins.disable(s) }()
	}
}

// Plugins manages the plugins of a server. Its methods are safe to call
// from anywhere.
type Plugins struct {
	srv *Server

	mu     sync.Mutex
	order  []*pluginState
	byName map[string]*pluginState
}

// PluginStatus describes a loaded plugin.
type PluginStatus struct {
	Info    Info
	Enabled bool
}

// List returns every loaded plugin in load order.
func (ps *Plugins) List() []PluginStatus {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := make([]PluginStatus, 0, len(ps.order))
	for _, s := range ps.order {
		out = append(out, PluginStatus{Info: s.info, Enabled: s.enabled.Load()})
	}
	return out
}

// ErrUnknownPlugin is returned for a plugin name that is not loaded.
var ErrUnknownPlugin = errors.New("helix: no plugin with that name")

// Enable enables the plugin called name: its listeners run again, then its
// Enable method is called. Setup is not run again.
func (ps *Plugins) Enable(name string) error {
	s, err := ps.get(name)
	if err != nil {
		return err
	}
	if s.enabled.Swap(true) {
		return nil
	}
	s.call("Enable", s.p.Enable)
	s.log.Info("plugin enabled")
	return nil
}

// Disable disables the plugin called name: its Disable method is called,
// then its listeners stop running. Its custom blocks and items stay
// registered.
func (ps *Plugins) Disable(name string) error {
	s, err := ps.get(name)
	if err != nil {
		return err
	}
	return ps.disable(s)
}

func (ps *Plugins) disable(s *pluginState) error {
	if !s.enabled.Load() {
		return nil
	}
	s.call("Disable", s.p.Disable)
	s.enabled.Store(false)
	s.log.Info("plugin disabled")
	return nil
}

func (ps *Plugins) get(name string) (*pluginState, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	s, ok := ps.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPlugin, name)
	}
	return s, nil
}

// load checks, orders, sets up and enables plugins. A plugin that fails is
// reported in the returned error and not loaded; the others still are.
func (ps *Plugins) load(list []plugin, log *slog.Logger, limit PanicLimit) error {
	var errs []error
	pending := map[string]plugin{}
	var names []string
	for _, p := range list {
		if p == nil {
			continue
		}
		info := p.Info()
		switch {
		case info.Name == "":
			errs = append(errs, fmt.Errorf("helix: plugin %T has no Name in its Info", p))
			continue
		case info.Description == "":
			errs = append(errs, fmt.Errorf("helix: plugin %q has no Description in its Info", info.Name))
			continue
		}
		if _, dup := pending[info.Name]; dup {
			errs = append(errs, fmt.Errorf("helix: two plugins are called %q", info.Name))
			continue
		}
		pending[info.Name] = p
		names = append(names, info.Name)
	}

	// Load in dependency order; plugins whose dependencies are missing or
	// circular refuse to load.
	loaded := map[string]bool{}
	for progress := true; progress && len(names) > 0; {
		progress = false
		for i := 0; i < len(names); i++ {
			p := pending[names[i]]
			ready := true
			for _, d := range p.Info().Dependencies {
				if !loaded[d] {
					ready = false
				}
			}
			if !ready {
				continue
			}
			if err := ps.add(p, log, limit); err != nil {
				errs = append(errs, err)
			} else {
				loaded[names[i]] = true
			}
			names = slices.Delete(names, i, i+1)
			i--
			progress = true
		}
	}
	for _, n := range names {
		var missing []string
		for _, d := range pending[n].Info().Dependencies {
			if !loaded[d] {
				missing = append(missing, d)
			}
		}
		errs = append(errs, fmt.Errorf("helix: plugin %q is not loaded: dependencies %v are missing, failed or circular", n, missing))
	}
	return errors.Join(errs...)
}

// add sets up and enables one plugin.
func (ps *Plugins) add(p plugin, log *slog.Logger, limit PanicLimit) error {
	info := p.Info()
	s := &pluginState{p: p, info: info, srv: ps.srv, limit: limit,
		log: log.With("plugin", info.Name), dir: filepath.Join(ps.srv.pluginDir, info.Name)}
	p.base().state = s

	r := &Setup{state: s, srv: ps.srv}
	var failed bool
	func() {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("plugin panicked in Setup", "panic", v, "stack", string(debug.Stack()))
				failed = true
			}
		}()
		p.Setup(r)
	}()
	if failed {
		return fmt.Errorf("helix: plugin %q panicked in Setup", info.Name)
	}
	if err := errors.Join(r.errs...); err != nil {
		return fmt.Errorf("helix: plugin %q: %w", info.Name, err)
	}
	r.commit()

	ps.mu.Lock()
	ps.order = append(ps.order, s)
	ps.byName[info.Name] = s
	ps.mu.Unlock()

	s.enabled.Store(true)
	s.call("Enable", p.Enable)
	s.log.Info("plugin loaded", "version", info.Version)
	return nil
}
