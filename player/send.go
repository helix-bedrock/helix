package player

import (
	"fmt"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// send queues packets for the player; they go out on the next flush. It
// stops at the first that fails and returns false. A failure means the
// connection is gone: the read loop sees it and the player leaves, so
// it is logged once, not per packet. Runs: anywhere.
func (p *Player) send(pks ...packet.Packet) bool {
	if p.conn == nil {
		return true // a player with no client to tell, as in tests
	}
	for _, pk := range pks {
		if err := p.conn.WritePacket(pk); err != nil {
			p.logSendError(err, pk)
			return false
		}
	}
	return true
}

// sendNow sends packets and flushes them at once, for code that is not on
// a region tick (the network goroutine, the global owner). Runs: anywhere.
func (p *Player) sendNow(pks ...packet.Packet) bool {
	if p.conn == nil {
		return true
	}
	if !p.send(pks...) {
		return false
	}
	if err := p.conn.Flush(); err != nil {
		p.logSendError(err, nil)
		return false
	}
	return true
}

// logSendError logs the first failed send of the player's connection: at
// debug level when the connection was closed (a player leaving), as a
// warning otherwise.
func (p *Player) logSendError(err error, pk packet.Packet) {
	if p.sendErrLogged.Swap(true) {
		return
	}
	what := "flush"
	if pk != nil {
		what = fmt.Sprintf("%T", pk)
	}
	if isClosed(err) {
		p.s.log.Debug("send to a closed connection", "name", p.name, "packet", what)
		return
	}
	p.s.log.Warn("send failed", "name", p.name, "packet", what, "err", err)
}

// sendBlock sends the block at pos as the region has it, to undo what the
// client predicted.
func (p *Player) sendBlock(r *world.Region, pos cube.Pos) {
	for layer := range uint8(2) {
		b, ok := r.Block(pos, layer)
		if !ok {
			return
		}
		p.send(&packet.UpdateBlock{
			Position:          protocol.BlockPos{int32(pos[0]), int32(pos[1]), int32(pos[2])}, //nolint:gosec // block coordinates fit int32
			NewBlockRuntimeID: p.s.reg.Blocks.Hash(b), Flags: world.UpdateBlockFlags, Layer: uint32(layer),
		})
	}
}

// sendHeld sends the held slot as the server has it.
func (p *Player) sendHeld() {
	inv := &p.inventory
	p.send(&packet.InventorySlot{
		WindowID: protocol.WindowIDInventory, Slot: uint32(inv.held), NewItem: inv.main[inv.held].network(p.s.reg),
	})
}
