package player

import (
	"fmt"
	"strings"

	"github.com/helix-bedrock/protocol"
)

// requestSlots is every slot a request's moving actions name, as the client sent them.
func requestSlots(req protocol.ItemStackRequest) []protocol.StackRequestSlotInfo {
	var out []protocol.StackRequestSlotInfo
	for _, a := range req.Actions {
		switch a := a.(type) {
		case *protocol.TakeStackRequestAction:
			out = append(out, a.Source, a.Destination)
		case *protocol.PlaceStackRequestAction:
			out = append(out, a.Source, a.Destination)
		case *protocol.SwapStackRequestAction:
			out = append(out, a.Source, a.Destination)
		case *protocol.DropStackRequestAction:
			out = append(out, a.Source)
		case *protocol.DestroyStackRequestAction:
			out = append(out, a.Source)
		case *protocol.ConsumeStackRequestAction:
			out = append(out, a.Source)
		}
	}
	return out
}

// describeServerSlots is what the server holds in each slot a refused request names,
// next to the network ID the client gave, for the debug log: a refusal from a client
// and server that disagree on a slot shows which slot and since when.
func (p *Player) describeServerSlots(req protocol.ItemStackRequest) string {
	var b strings.Builder
	for _, info := range requestSlots(req) {
		fmt.Fprintf(&b, " [container=%d slot=%d client=%d", info.Container.ContainerID, info.Slot, info.StackNetworkID)
		if info.Container.ContainerID == protocol.ContainerDynamic {
			b.WriteString(" server=storage]")
			continue
		}
		ref, ok := p.slot(&p.inventory, info.Container.ContainerID, info.Slot)
		if !ok {
			b.WriteString(" server=no such slot]")
			continue
		}
		s := *ref.stack
		fmt.Fprintf(&b, " server=%s x%d id=%d]", s.Name, s.Count, s.netID)
	}
	return b.String()
}
