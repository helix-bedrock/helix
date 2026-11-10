package player

import (
	"fmt"
	"log/slog"

	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// Setup is the player part of the boot Setup: the handlers of the packets
// that change the world, the movement and gameplay systems, and the
// enchantments, effects and block behaviours they use. Vanilla registers
// through it first (NewSetup), then the server's Config.Setup: there is no
// back door. It is frozen into a Host before a transport opens.
type Setup struct {
	owner string
	h     *handlerState
}

type handlerState struct {
	log      *slog.Logger
	frozen   bool
	packets  map[uint32]handlerEntry
	movement movementEntry
	think    []thinkEntry
	game     *gameState
	commands *command.Manager
}

// PacketHandler handles a packet from a player on the player's region
// tick, so it may change the player and the world directly.
type PacketHandler[T packet.Packet] func(p *Player, pk T)

// MovementHandler applies a player's movement, on the player's region tick.
type MovementHandler func(p *Player, m world.Movement)

type handlerEntry struct {
	owner string
	fn    func(p *Player, pk packet.Packet)
}

type movementEntry struct {
	owner string
	fn    MovementHandler
}

// ThinkHandler is a player's think step: it sees the packets the player sent since the last
// tick before any packet handler runs, and works out what it needs from them, such as a
// server-side simulation of the player's movement for an anti-cheat. In a crowded region it
// runs for many players at once on different goroutines, at the same time as mobs think.
// So it may only read the world, the player and other entities, and keep what it works out
// in the player's Local state, for packet and movement handlers to use on the region tick.
// It must not change the player or the world, send packets, or keep the packets past its
// return.
type ThinkHandler func(p *Player, input []packet.Packet)

type thinkEntry struct {
	owner string
	fn    ThinkHandler
}

// Think adds a think step that runs on every player each tick (ThinkHandler). Steps run in
// the order they were added. Vanilla adds none.
func (s *Setup) Think(fn ThinkHandler) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	if fn != nil {
		s.h.think = append(s.h.think, thinkEntry{owner: s.owner, fn: fn})
	}
	return nil
}

// NewSetup returns a Setup with vanilla's handlers and systems registered.
func NewSetup(log *slog.Logger) (*Setup, error) {
	s := newSetup(log)
	if err := vanillaHandlers(s); err != nil {
		return nil, fmt.Errorf("player: vanilla setup: %w", err)
	}
	return s, nil
}

func newSetup(log *slog.Logger) *Setup {
	return &Setup{owner: "minecraft", h: &handlerState{log: log, packets: map[uint32]handlerEntry{}, game: newGameState(), commands: command.NewManager(log)}}
}

// As returns the same Setup acting for owner, a plugin's name in the log.
func (s *Setup) As(owner string) *Setup { return &Setup{owner: owner, h: s.h} }

// Handle sets the handler of the packet type T, replacing vanilla's or an
// earlier one; the log says who replaced whom. Without a handler a packet
// that changes the world is ignored.
func Handle[T packet.Packet](s *Setup, fn PacketHandler[T]) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	var zero T
	id := zero.ID()
	if old, ok := s.h.packets[id]; ok && old.owner != s.owner {
		s.h.log.Info("packet handler replaced", "packet", fmt.Sprintf("%T", zero), "by", s.owner, "was", old.owner)
	}
	s.h.packets[id] = handlerEntry{owner: s.owner, fn: func(p *Player, pk packet.Packet) {
		if v, ok := pk.(T); ok {
			fn(p, v)
		}
	}}
	return nil
}

// WrapHandle runs fn around the current handler of T: fn decides whether
// and when next (the handler it wraps) runs. Wraps stack in the order they
// are added.
func WrapHandle[T packet.Packet](s *Setup, fn func(p *Player, pk T, next PacketHandler[T])) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	var zero T
	id := zero.ID()
	inner := s.h.packets[id].fn
	next := func(p *Player, pk T) {
		if inner != nil {
			inner(p, pk)
		}
	}
	s.h.packets[id] = handlerEntry{owner: s.owner, fn: func(p *Player, pk packet.Packet) {
		if v, ok := pk.(T); ok {
			fn(p, v, next)
		}
	}}
	return nil
}

// Movement sets how a player's movement is applied, replacing vanilla's.
// Vanilla's PlayerAuthInput handler calls it.
func (s *Setup) Movement(fn MovementHandler) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	if old := s.h.movement; old.fn != nil && old.owner != s.owner {
		s.h.log.Info("movement replaced", "by", s.owner, "was", old.owner)
	}
	s.h.movement = movementEntry{owner: s.owner, fn: fn}
	return nil
}

// WrapMovement runs fn around the current movement handler.
func (s *Setup) WrapMovement(fn func(p *Player, m world.Movement, next MovementHandler)) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	inner := s.h.movement.fn
	next := func(p *Player, m world.Movement) {
		if inner != nil {
			inner(p, m)
		}
	}
	s.h.movement = movementEntry{owner: s.owner, fn: func(p *Player, m world.Movement) { fn(p, m, next) }}
	return nil
}

// handlers are the frozen handlers: a slice indexed by packet ID, so a
// packet costs one lookup and one call on the tick.
type handlers struct {
	packets  []func(p *Player, pk packet.Packet)
	movement MovementHandler
	think    []thinkEntry
}

// freeze ends the handler part of the Setup.
func (s *Setup) freeze() handlers {
	s.h.frozen = true
	var top uint32
	for id := range s.h.packets {
		top = max(top, id)
	}
	h := handlers{packets: make([]func(*Player, packet.Packet), top+1), movement: s.h.movement.fn, think: s.h.think}
	for id, e := range s.h.packets {
		h.packets[id] = e.fn
	}
	if h.movement == nil {
		h.movement = func(*Player, world.Movement) {}
	}
	return h
}

// handler returns the handler of packet id, nil if none.
func (h *handlers) handler(id uint32) func(p *Player, pk packet.Packet) {
	if int(id) >= len(h.packets) {
		return nil
	}
	return h.packets[id]
}
