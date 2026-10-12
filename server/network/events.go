package network

import (
	"net"

	"github.com/helix-bedrock/helix/server/hook"
	"github.com/helix-bedrock/protocol/conn"
	"github.com/helix-bedrock/protocol/login"
	"github.com/helix-bedrock/protocol/packet"
)

// Events holds the listener lists of packet events (Helix, RFC 0001).
// Listeners run on the connection's network goroutine, not on a region:
// they may read and change the packet but must not touch the world.
type Events struct {
	PacketIn  hook.List[PacketInEvent]
	PacketOut hook.List[PacketOutEvent]
}

// Packet is embedded in packet events.
type Packet struct {
	hook.Cancellable
	// Identity is who the connection belongs to. It is empty before login.
	Identity login.IdentityData
	// Addr is the client's address.
	Addr net.Addr
	// Packet is the packet. A listener may change its fields or replace it.
	Packet packet.Packet
}

// PacketInEvent is called for every packet a client sends, after it is
// decoded. Cancelling drops it.
type PacketInEvent struct{ Packet }

// PacketOutEvent is called for every packet sent to a client, before it is
// encoded. Cancelling drops it.
type PacketOutEvent struct{ Packet }

// hooks returns connection hooks that fire ev, added to base (which may be
// nil).
func (ev *Events) hooks(base *conn.Hooks) *conn.Hooks {
	if ev == nil {
		return base
	}
	if base == nil {
		base = &conn.Hooks{}
	}
	base.OnPacketIn(func(e *conn.PacketEvent) conn.Verdict {
		if !ev.PacketIn.Active() {
			return conn.Allow
		}
		return ev.fire(e, true)
	})
	base.OnPacketOut(func(e *conn.PacketEvent) conn.Verdict {
		if !ev.PacketOut.Active() {
			return conn.Allow
		}
		return ev.fire(e, false)
	})
	return base
}

// fire runs the packet listeners for one packet and says whether it
// continues.
func (ev *Events) fire(e *conn.PacketEvent, in bool) conn.Verdict {
	p := Packet{Packet: e.Packet}
	if e.Conn != nil {
		p.Identity, p.Addr = e.Conn.IdentityData(), e.Conn.RemoteAddr()
	}
	var cancelled bool
	if in {
		x := &PacketInEvent{p}
		cancelled = ev.PacketIn.Fire(x)
		e.Packet = x.Packet.Packet
	} else {
		x := &PacketOutEvent{p}
		cancelled = ev.PacketOut.Fire(x)
		e.Packet = x.Packet.Packet
	}
	if cancelled || e.Packet == nil {
		return conn.Drop
	}
	return conn.Allow
}
