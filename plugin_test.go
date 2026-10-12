package helix

import (
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/helix-bedrock/helix/event"
	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/item/inventory"
	"github.com/helix-bedrock/helix/server/network"
	"github.com/helix-bedrock/helix/server/player"
	"github.com/helix-bedrock/helix/server/world"
)

// testServer is a Server with plugins and no engine, enough to test
// loading and dispatch.
func testServer(t *testing.T, limit PanicLimit, ps ...Pluggable) (*Server, error) {
	t.Helper()
	s := &Server{log: slog.New(slog.DiscardHandler), pluginDir: t.TempDir(),
		playerEvents: &player.Events{}, worldEvents: &world.Events{}, inventoryEvents: &inventory.Events{}, packetEvents: &network.Events{}}
	s.plugins = &Plugins{srv: s, byName: map[string]*pluginState{}}
	return s, s.plugins.load(ps, s.log, limit)
}

type testPlugin struct {
	Plugin
	info    Info
	setup   func(r *Setup)
	enabled atomic.Int32
}

func (p *testPlugin) Info() Info { return p.info }
func (p *testPlugin) Setup(r *Setup) {
	if p.setup != nil {
		p.setup(r)
	}
}
func (p *testPlugin) Enable() { p.enabled.Add(1) }

func named(name string, deps ...string) *testPlugin {
	return &testPlugin{info: Info{Name: name, Description: "test", Dependencies: deps}}
}

func TestInfoIsRequired(t *testing.T) {
	_, err := testServer(t, PanicLimit{}, &testPlugin{info: Info{Name: "x"}}, &testPlugin{info: Info{Description: "y"}})
	if err == nil || !strings.Contains(err.Error(), "Description") || !strings.Contains(err.Error(), "no Name") {
		t.Fatalf("want errors for missing Name and Description, got %v", err)
	}
}

func TestDependencyOrder(t *testing.T) {
	var order []string
	mk := func(name string, deps ...string) *testPlugin {
		p := named(name, deps...)
		p.setup = func(*Setup) { order = append(order, name) }
		return p
	}
	s, err := testServer(t, PanicLimit{}, mk("C", "B"), mk("B", "A"), mk("A"), mk("D", "missing"))
	if err == nil || !strings.Contains(err.Error(), `"D"`) {
		t.Fatalf("want an error for D's missing dependency, got %v", err)
	}
	if strings.Join(order, "") != "ABC" {
		t.Fatalf("setup order %v, want A B C", order)
	}
	if len(s.Plugins().List()) != 3 {
		t.Fatalf("want 3 loaded plugins, got %v", s.Plugins().List())
	}
}

func TestPriorityCancelAndIgnoreCancelled(t *testing.T) {
	var calls []string
	p := named("P")
	p.setup = func(r *Setup) {
		r.Chat(func(e *event.Chat) { calls = append(calls, "monitor") }, Priority(event.Monitor), IgnoreCancelled())
		r.Chat(func(e *event.Chat) { calls = append(calls, "high"); e.Cancel() }, Priority(event.High))
		r.Chat(func(e *event.Chat) { calls = append(calls, "low") }, Priority(event.Low))
		r.Chat(func(e *event.Chat) { calls = append(calls, "highest") }, Priority(event.Highest))
	}
	s, err := testServer(t, PanicLimit{}, p)
	if err != nil {
		t.Fatal(err)
	}
	msg := "hi"
	e := &event.Chat{Message: &msg}
	s.playerEvents.Chat.Fire(e)
	if got := strings.Join(calls, ","); got != "low,high,highest" || !e.Cancelled() {
		t.Fatalf("calls %s cancelled %v; want low,high,highest and cancelled", got, e.Cancelled())
	}
}

func TestOnlyBlockFilters(t *testing.T) {
	var n int
	p := named("P")
	p.setup = func(r *Setup) {
		r.BlockBreak(func(*event.BlockBreak) { n++ }, OnlyBlock("minecraft:diamond_ore"))
	}
	s, err := testServer(t, PanicLimit{}, p)
	if err != nil {
		t.Fatal(err)
	}
	s.playerEvents.BlockBreak.Fire(&event.BlockBreak{Block: block.Stone{}})
	s.playerEvents.BlockBreak.Fire(&event.BlockBreak{Block: block.DiamondOre{}})
	if n != 1 {
		t.Fatalf("listener ran %d times, want 1 (diamond ore only)", n)
	}
}

func TestFilterOnWrongEventFailsToLoad(t *testing.T) {
	p := named("P")
	p.setup = func(r *Setup) { r.Chat(func(*event.Chat) {}, OnlyBlock("minecraft:stone")) }
	if _, err := testServer(t, PanicLimit{}, p); err == nil || !strings.Contains(err.Error(), "OnlyBlock") {
		t.Fatalf("want OnlyBlock error, got %v", err)
	}
}

func TestDisableStopsListeners(t *testing.T) {
	var n int
	p := named("P")
	p.setup = func(r *Setup) { r.Chat(func(*event.Chat) { n++ }) }
	s, err := testServer(t, PanicLimit{}, p)
	if err != nil {
		t.Fatal(err)
	}
	s.playerEvents.Chat.Fire(&event.Chat{})
	if err := s.Plugins().Disable("P"); err != nil {
		t.Fatal(err)
	}
	s.playerEvents.Chat.Fire(&event.Chat{})
	if err := s.Plugins().Enable("P"); err != nil {
		t.Fatal(err)
	}
	s.playerEvents.Chat.Fire(&event.Chat{})
	if n != 2 || p.enabled.Load() != 2 {
		t.Fatalf("listener ran %d times (want 2), Enable ran %d times (want 2)", n, p.enabled.Load())
	}
	if err := s.Plugins().Disable("nope"); !errors.Is(err, ErrUnknownPlugin) {
		t.Fatalf("want ErrUnknownPlugin, got %v", err)
	}
}

func TestPanicsAreRecoveredAndLimited(t *testing.T) {
	p := named("Bad")
	p.setup = func(r *Setup) { r.Chat(func(*event.Chat) { panic("boom") }) }
	s, err := testServer(t, PanicLimit{Count: 3, Window: time.Minute}, p)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		s.playerEvents.Chat.Fire(&event.Chat{}) // must not panic
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.Plugins().List()[0].Enabled {
		if time.Now().After(deadline) {
			t.Fatal("plugin not disabled after reaching its panic limit")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPanicInSetupFailsOnlyThatPlugin(t *testing.T) {
	bad := named("Bad")
	bad.setup = func(*Setup) { panic("setup") }
	s, err := testServer(t, PanicLimit{}, bad, named("Good"))
	if err == nil || len(s.Plugins().List()) != 1 || s.Plugins().List()[0].Info.Name != "Good" {
		t.Fatalf("want only Good loaded and an error, got %v %v", s.Plugins().List(), err)
	}
}
