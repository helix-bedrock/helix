package helix

import (
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Container window rules from BDS 1.26.60.
const (
	// maxWindowID is where BDS's window ID counter wraps back to 1.
	maxWindowID = 100
	// containerTypeNone is the type BDS puts on the ContainerClose it
	// answers with (-9).
	containerTypeNone byte = 0xF7
	// containerTypeInventory is the player's own inventory (-1).
	containerTypeInventory byte = 0xFF
)

// handleInteract is vanilla's Interact handler. Only opening the inventory
// is built: BDS opens it when the target is within 12 blocks and no
// container is open.
func handleInteract(p *Player, pk *packet.Interact) {
	if pk.ActionType != packet.InteractActionOpenInventory || p.entity == nil {
		return
	}
	// The player is the only entity a target can be so far, so BDS's
	// 12-block distance to the target always holds.
	if pk.TargetEntityRuntimeID != p.runtimeID || p.windowOpen {
		return
	}
	p.openInventory()
}

// openInventory opens the player's own inventory as BDS does: the next
// window ID, the position floored, no entity.
func (p *Player) openInventory() {
	p.windowID++
	if p.windowID >= maxWindowID {
		p.windowID = 1
	}
	p.windowOpen = true
	pos := p.entity.Position()
	_ = p.conn.WritePacket(&packet.ContainerOpen{
		WindowID: p.windowID, ContainerType: containerTypeInventory,
		ContainerPosition:       protocol.BlockPos{floorInt(pos[0]), floorInt(pos[1]), floorInt(pos[2])},
		ContainerEntityUniqueID: -1,
	})
	p.flush()
}

// handleContainerClose is vanilla's ContainerClose handler: BDS closes
// whatever is open, whatever window the client names, and answers with
// its own window ID and no type.
func handleContainerClose(p *Player, _ *packet.ContainerClose) {
	p.windowOpen = false
	_ = p.conn.WritePacket(&packet.ContainerClose{WindowID: p.windowID, ContainerType: containerTypeNone})
	p.flush()
}
