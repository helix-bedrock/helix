package player

import "sync/atomic"

// teleportAckInputs is how many inputs movement waits for a teleport's acknowledgement
// before it is taken again anyway, for a client that never acknowledges one (one sent
// while it was dead or loading): 2 seconds.
const teleportAckInputs = 40

// teleportWait holds back the client's movement after a teleport until the client says it
// took it (InputFlagHandledTeleport): until then it still sends where it was before, which
// would move the player back to where it was. Helix's own rule, not checked against BDS.
type teleportWait struct {
	pending atomic.Bool
	inputs  atomic.Int32
}

// start begins a wait, when a teleport is sent. Runs: anywhere.
func (w *teleportWait) start() {
	w.inputs.Store(0)
	w.pending.Store(true)
}

// holds reports whether an input must be skipped: one from before the client took the
// teleport. handled is the input's teleport acknowledgement. Region tick.
func (w *teleportWait) holds(handled bool) bool {
	if !w.pending.Load() {
		return false
	}
	if handled || w.inputs.Add(1) > teleportAckInputs {
		w.pending.Store(false)
		return false
	}
	return true
}
