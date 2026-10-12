package network

import (
	"testing"

	"github.com/helix-bedrock/helix/server/hook"
	"github.com/helix-bedrock/protocol/conn"
	"github.com/helix-bedrock/protocol/packet"
)

type owner struct{}

func (owner) Enabled() bool          { return true }
func (owner) Run(_ string, f func()) { f() }

func TestPacketEventsChangeAndDrop(t *testing.T) {
	ev := &Events{}
	ev.PacketOut.Add(owner{}, func(e *PacketOutEvent) {
		if txt, ok := e.Packet.Packet.(*packet.Text); ok {
			if txt.Message == "drop" {
				e.Cancel()
				return
			}
			e.Packet.Packet = &packet.Text{Message: "changed"}
		}
	}, hook.Normal, false, nil)

	e := &conn.PacketEvent{Packet: &packet.Text{Message: "hi"}}
	if v := ev.fire(e, false); v != conn.Allow {
		t.Fatal("packet dropped")
	}
	if e.Packet.(*packet.Text).Message != "changed" {
		t.Fatal("packet not replaced")
	}
	if v := ev.fire(&conn.PacketEvent{Packet: &packet.Text{Message: "drop"}}, false); v != conn.Drop {
		t.Fatal("cancelled packet not dropped")
	}
	// Nobody listens to incoming packets: they pass untouched.
	in := &conn.PacketEvent{Packet: &packet.Text{Message: "in"}}
	h := ev.hooks(nil)
	_ = h
	if v := ev.fire(in, true); v != conn.Allow || in.Packet.(*packet.Text).Message != "in" {
		t.Fatal("incoming packet changed")
	}
}
