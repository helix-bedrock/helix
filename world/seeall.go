package world

import (
	"github.com/helix-bedrock/protocol/packet"
)

// Players who see everything: a plugin can let a player past the anti X-ray (an admin, a
// spectator, a cheat test), so it is sent every block as it is (hide.go, subhide.go).

// revealChunksPerTick bounds how many chunks a player who sees everything is sent the hidden
// blocks of a tick, so turning it on does not make one tick long.
const revealChunksPerTick = 4

// SetSeeHidden lets the player see past the anti X-ray, or not: sub chunks and blocks it was sent
// disguised are sent as they are, a few chunks a tick, and nothing more is hidden from it. Turned
// off, what it was sent stays until those chunks are sent again. Runs: anywhere.
func (v *Viewer) SetSeeHidden(on bool) { v.seeAll.Store(on) }

// SeesHidden reports whether the player sees past the anti X-ray. Runs: anywhere.
func (v *Viewer) SeesHidden() bool { return v.seeAll.Load() }

// revealAll sends a player who sees everything the hidden blocks of the chunks its client has
// that it was not sent as they are yet: in full where it was answered a stand-in, else only
// the disguised blocks. Past the simulation distance, from the network form, which disguises
// them still. Runs: the viewer's region.
func (v *Viewer) revealAll(r *Region) {
	v.netMu.Lock()
	out := v.revealOut[:0]
	done := 0
	for pos, nc := range v.nets {
		if done == revealChunksPerTick {
			break
		}
		st := v.revealOf(pos, nc)
		if st.all {
			continue
		}
		done++
		st.all = true
		c, loaded := r.chunks[pos]
		for i := range int32(min(len(nc.subs), maxHiddenSubs)) {
			bit := uint64(1) << i
			standIn := (st.fake|st.dry)&bit != 0
			switch {
			case loaded && standIn:
				out = r.appendSubBlocks(out, c, i, nc.bottom+i, subAll)
			case loaded:
				out = r.appendSubBlocks(out, c, i, nc.bottom+i, subDisguised)
			case standIn:
				out = r.appendNetSubBlocks(out, nc, i, nc.bottom+i, subFull)
			}
		}
		st.real |= st.fake | st.dry
		st.fake, st.dry = 0, 0
	}
	v.netMu.Unlock()
	v.sendAll(out)
	v.revealOut = out[:0]
}

// sendAll sends out and clears it.
func (v *Viewer) sendAll(out []packet.Packet) {
	for i, pk := range out {
		v.send(pk)
		out[i] = nil
	}
}
