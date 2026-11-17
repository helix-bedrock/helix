package player

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/helix-bedrock/protocol/packet"
)

// packetTrace names the packet types logged whole, both ways, at info level
// (HostConfig.TracePackets): a look at chosen packets for in-game testing, without the
// debug level's mob thinking and every other packet. Empty traces nothing and costs one
// length check per packet.
type packetTrace map[string]bool

// newPacketTrace reads names such as "Interact" or "ItemStackRequest", any case, with or
// without "Packet" after them.
func newPacketTrace(names []string) packetTrace {
	t := packetTrace{}
	for _, n := range names {
		n = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), "packet")
		if n != "" {
			t[n] = true
		}
	}
	return t
}

// traced reports that pk's type is one to log.
func (t packetTrace) traced(pk packet.Packet) bool {
	if len(t) == 0 || pk == nil {
		return false
	}
	return t[strings.ToLower(packetName(pk))]
}

// packetName is the name of pk's type, such as "SetActorData".
func packetName(pk packet.Packet) string {
	ty := reflect.TypeOf(pk)
	if ty.Kind() == reflect.Pointer {
		ty = ty.Elem()
	}
	return ty.Name()
}

// traceIn logs a traced packet the player sent. PlayerAuthInput, sent every client tick,
// is logged only when its input flags change. Runs: the player's read loop.
func (p *Player) traceIn(pk packet.Packet) {
	if !p.s.trace.traced(pk) {
		return
	}
	if in, ok := pk.(*packet.PlayerAuthInput); ok {
		flags := fmt.Sprintf("%v", in.InputData)
		if flags == p.traceFlags {
			return
		}
		p.traceFlags = flags
	}
	p.s.log.Info("packet in", "name", p.name, "type", packetName(pk), "packet", fmt.Sprintf("%+v", pk))
}

// traceOut logs a traced packet sent to the player. Runs: anywhere.
func (p *Player) traceOut(pk packet.Packet) {
	if p.s.trace.traced(pk) {
		p.s.log.Info("packet out", "name", p.name, "type", packetName(pk), "packet", fmt.Sprintf("%+v", pk))
	}
}
