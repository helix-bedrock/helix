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
		if p.traceSends.Load() > 0 && p.traceSends.Add(-1) >= 0 {
			p.s.log.Debug("packet to player", "name", p.name, "packet", describePacket(pk))
		}
		if mv, ok := pk.(*packet.MovePlayer); ok && mv.Mode == packet.MoveModeTeleport && mv.EntityRuntimeID == p.runtimeID {
			p.teleportWait.start()
		}
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
// addViewer adds the player's zones to d, sending through the player's connection, and
// taking packets encoded once for everyone (the movement fan-out) as they are when the
// client can.
func (p *Player) addViewer(d *world.Dimension) *world.Viewer {
	v := d.AddViewer(func(pk packet.Packet) { p.send(pk) }, p.flush)
	v.SetRaw(p.sendRaw)
	return v
}

// sendRaw writes a packet already encoded in the latest version, false if the client
// needs it as a packet instead (another version, a packet-out hook).
func (p *Player) sendRaw(b []byte) bool {
	if p.conn == nil || !p.conn.RawOK() {
		return false
	}
	if err := p.conn.WriteRaw(b); err != nil {
		p.logSendError(err, nil)
	}
	return true
}

// releasePacket hands a handled packet back to the connection, which reuses the ones it
// reads many times a second.
func (p *Player) releasePacket(pk packet.Packet) {
	if p.conn != nil {
		p.conn.Release(pk)
	}
}

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
func (p *Player) sendHeld() { p.Inventory().sendSlot(p, int(p.inventory.held), false) }

// sendOffhand sends the offhand as the server has it.
func (p *Player) sendOffhand() { p.Offhand().sendSlot(p, 0, false) }

// sendArmour sends the worn armour as the server has it.
func (p *Player) sendArmour() { p.Armour().sendAll(p, false) }

// sendOwnItems sends the player's own slots, in BDS 1.26.60's join order: inventory,
// armour, UI, offhand, then the selected hotbar slot.
func (p *Player) sendOwnItems() {
	invs := p.inventories()
	// Storage items get their IDs before their slots are sent.
	p.syncStorageIDs()
	invs.main.sendAll(p, false)
	invs.armour.sendAll(p, false)
	invs.ui.sendAll(p, false)
	invs.offhand.sendAll(p, false)
	p.send(&packet.PlayerHotBar{SelectedHotBarSlot: uint32(p.inventory.held), WindowID: protocol.WindowIDInventory, SelectHotBarSlot: true})
	p.storageSent = nil
	p.syncStorage()
}

// blockPos is a block position as packets carry it.
func blockPos(pos cube.Pos) protocol.BlockPos {
	return protocol.BlockPos{int32(pos[0]), int32(pos[1]), int32(pos[2])} //nolint:gosec // block coordinates fit int32
}

// describePacket is a short text of pk for the debug trace: large payloads left out.
func describePacket(pk packet.Packet) string {
	switch pk := pk.(type) {
	case *packet.LevelChunk:
		return fmt.Sprintf("LevelChunk dim=%d pos=%v limit=%v", pk.Dimension, pk.Position, pk.SubChunkLimit)
	case *packet.SubChunk:
		res := make([]string, len(pk.SubChunkEntries))
		for i, e := range pk.SubChunkEntries {
			res[i] = fmt.Sprintf("%v:%d", e.Offset, e.Result)
		}
		return fmt.Sprintf("SubChunk dim=%d pos=%v %v", pk.Dimension, pk.Position, res)
	case *packet.UpdateBlock, *packet.ChangeDimension, *packet.PlayerAction, *packet.MovePlayer,
		*packet.NetworkChunkPublisherUpdate, *packet.PlayStatus, *packet.RemoveActor:
		return fmt.Sprintf("%T %+v", pk, pk)
	}
	return fmt.Sprintf("%T", pk)
}
