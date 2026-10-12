package helix

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/helix-bedrock/helix/server/cmd"
	"github.com/helix-bedrock/helix/server/world"
)

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestEveryPausesWhileDisabled(t *testing.T) {
	p := named("Ticker")
	s, err := testServer(t, PanicLimit{}, p)
	if err != nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	task := p.Every(time.Millisecond, func() { n.Add(1) })
	defer task.Stop()
	waitUntil(t, "runs", func() bool { return n.Load() >= 3 })

	_ = s.Plugins().Disable("Ticker")
	time.Sleep(10 * time.Millisecond) // a run already started may finish
	paused := n.Load()
	time.Sleep(20 * time.Millisecond)
	if n.Load() != paused {
		t.Fatal("task ran while the plugin was disabled")
	}
	_ = s.Plugins().Enable("Ticker")
	waitUntil(t, "runs after enabling", func() bool { return n.Load() > paused })

	task.Stop()
	time.Sleep(5 * time.Millisecond)
	stopped := n.Load()
	time.Sleep(20 * time.Millisecond)
	if n.Load() != stopped {
		t.Fatal("task ran after Stop")
	}
}

func TestAfterRecoversPanics(t *testing.T) {
	p := named("After")
	if _, err := testServer(t, PanicLimit{}, p); err != nil {
		t.Fatal(err)
	}
	var ran atomic.Bool
	p.After(time.Millisecond, func() { ran.Store(true); panic("boom") })
	waitUntil(t, "After to run", ran.Load)
}

func TestDoRunsOnTheWorld(t *testing.T) {
	p := named("Do")
	if _, err := testServer(t, PanicLimit{}, p); err != nil {
		t.Fatal(err)
	}
	w := world.Config{Synchronous: true}.New()
	defer w.Close()
	var got *world.World
	p.Do(w, w.Spawn().Vec3(), func(tx *world.Tx) { got = tx.World() })
	if got != w {
		t.Fatal("Do did not run on the world")
	}
}

type pingCommand struct{ ran *atomic.Int32 }

func (c pingCommand) Run(cmd.Source, *cmd.Output, *world.Tx) { c.ran.Add(1) }

func TestCommandsHiddenWhileDisabled(t *testing.T) {
	var ran atomic.Int32
	p := named("Cmds")
	p.setup = func(r *Setup) {
		r.Command(cmd.New("helixtestping", "Test.", nil, pingCommand{ran: &ran}))
	}
	s, err := testServer(t, PanicLimit{}, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cmd.ByAlias("helixtestping"); !ok {
		t.Fatal("command not registered")
	}
	_ = s.Plugins().Disable("Cmds")
	if _, ok := cmd.ByAlias("helixtestping"); ok {
		t.Fatal("command visible while its plugin is disabled")
	}
	if _, ok := cmd.Commands()["helixtestping"]; ok {
		t.Fatal("command listed while its plugin is disabled")
	}
	_ = s.Plugins().Enable("Cmds")
	c, ok := cmd.ByAlias("helixtestping")
	if !ok {
		t.Fatal("command hidden after enabling")
	}
	var out bytes.Buffer
	c.Execute("", consoleSource{w: &out}, nil)
	if ran.Load() != 1 {
		t.Fatalf("command ran %d times, want 1", ran.Load())
	}

	// A second plugin may not take the name.
	q := named("Thief")
	q.setup = func(r *Setup) { r.Command(cmd.New("helixtestping", "Test.", nil, pingCommand{ran: &ran})) }
	if err := s.plugins.load([]Pluggable{q}, s.log, PanicLimit{}); err == nil || !strings.Contains(err.Error(), "taken") {
		t.Fatalf("want name taken error, got %v", err)
	}
}

type testConfig struct {
	Radius int
	Name   string
}

func TestConfigWritesDefaultsThenReads(t *testing.T) {
	p := named("Conf")
	s, err := testServer(t, PanicLimit{}, p)
	if err != nil {
		t.Fatal(err)
	}
	_ = s
	conf := testConfig{Radius: 100, Name: "spawn"}
	if err := p.Config(&conf); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.state.dir, "config.toml")
	if err := os.WriteFile(path, []byte("Radius = 7\nName = \"hub\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got testConfig
	if err := p.Config(&got); err != nil {
		t.Fatal(err)
	}
	if got != (testConfig{Radius: 7, Name: "hub"}) {
		t.Fatalf("read %+v", got)
	}
}

func TestConsolePluginCommands(t *testing.T) {
	p := named("Con")
	s, err := testServer(t, PanicLimit{}, p)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	s.Console(strings.NewReader("plugins disable Con\nplugins\nplugins enable Con\nnosuchcommand\n"), &out)
	if s.Plugins().List()[0].Enabled != true {
		t.Fatal("plugin not enabled again")
	}
	for _, want := range []string{"Con disabled", "Plugins (1)", "Con enabled", "Unknown command"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("console output %q lacks %q", out.String(), want)
		}
	}
}
