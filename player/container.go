package player

import (
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

// windowState is the player's container window: BDS's per-player window ID
// counter and whether a window is open. Owned by the player's region.
type windowState struct {
	id   byte
	open bool
}

// handleInteract is vanilla's Interact handler. Only opening the inventory
// is built: BDS opens it when the target is within 12 blocks and no
// container is open.
func handleInteract(p *Player, pk *packet.Interact) {
	if pk.ActionType != packet.InteractActionOpenInventory || p.entity == nil {
		return
	}
	// The player is the only entity a target can be so far, so BDS's
	// 12-block distance to the target always holds.
	if pk.TargetEntityRuntimeID != p.runtimeID || p.window.open {
		return
	}
	p.openInventory()
}

// openInventory opens the player's own inventory as BDS does: the next
// window ID, the position floored, no entity.
func (p *Player) openInventory() {
	w := &p.window
	w.id++
	if w.id >= maxWindowID {
		w.id = 1
	}
	w.open = true
	p.send(&packet.ContainerOpen{
		WindowID: w.id, ContainerType: containerTypeInventory,
		ContainerPosition: blockPosOf(p.entity.Position()), ContainerEntityUniqueID: -1,
	})
	p.flush()
}

// handleContainerClose is vanilla's ContainerClose handler: BDS closes
// whatever is open, whatever window the client names, and answers with
// its own window ID and no type.
func handleContainerClose(p *Player, _ *packet.ContainerClose) {
	p.window.open = false
	p.send(&packet.ContainerClose{WindowID: p.window.id, ContainerType: containerTypeNone})
	p.flush()
}
