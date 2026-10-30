package helix

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// TestHandlers checks replacing and wrapping handlers, their order, and
// that nothing can be added after boot.
func TestHandlers(t *testing.T) {
	s := newSetup(registry.NewSetup(nil, "minecraft"), slog.New(slog.DiscardHandler))
	if err := vanillaHandlers(s); err != nil {
		t.Fatal(err)
	}
	var order []string
	plugin := s.As("plugin")
	if err := Handle(plugin, func(*Player, *packet.Animate) { order = append(order, "handler") }); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		if err := WrapHandle(plugin, func(p *Player, pk *packet.Animate, next PacketHandler[*packet.Animate]) {
			order = append(order, name)
			next(p, pk)
		}); err != nil {
			t.Fatal(err)
		}
	}
	moved := false
	if err := plugin.WrapMovement(func(p *Player, m world.Movement, _ MovementHandler) { moved = true }); err != nil {
		t.Fatal(err)
	}
	h := s.freeze()
	if !errors.Is(Handle(s, func(*Player, *packet.Animate) {}), registry.ErrFrozen) {
		t.Fatal("handler added after freeze")
	}
	h.handler(packet.IDAnimate)(nil, &packet.Animate{})
	if len(order) != 3 || order[0] != "second" || order[1] != "first" || order[2] != "handler" {
		t.Fatalf("order %v", order)
	}
	if h.handler(packet.IDText) != nil || h.handler(1<<20) != nil {
		t.Fatal("handler for a packet nobody handles")
	}
	// The wrap does not call vanilla's movement, so the player needs no
	// entity.
	p := &Player{s: &Server{handlers: h}}
	h.handler(packet.IDPlayerAuthInput)(p, &packet.PlayerAuthInput{})
	if !moved {
		t.Fatal("movement wrap not called by vanilla's PlayerAuthInput handler")
	}
}

func TestInputQueue(t *testing.T) {
	var q inputQueue
	for range maxQueuedInput {
		if !q.push(&packet.Animate{}) {
			t.Fatal("queue full too early")
		}
	}
	if q.push(&packet.Animate{}) {
		t.Fatal("queue took more than its limit")
	}
	if n := len(q.take()); n != maxQueuedInput || len(q.take()) != 0 {
		t.Fatalf("took %d", n)
	}
}
