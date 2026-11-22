package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol/packet"
)

// Impossible inputs: Helix's own rule, beyond BDS. Cheat clients' "disablers" send extra inputs
// no client can make (positions that are NaN, infinite or two billion blocks away, an infinite
// move stick, tick 0) to break other servers' movement checks. Such an input is dropped whole,
// before anything in it is used.

const (
	// inputPosLimit is the farthest a position may be from 0 on any axis: past the client's own
	// world border (30 million) by a margin.
	inputPosLimit = 32_000_000
	// inputDeltaLimit is the most a client's own velocity may be per tick on any axis: faster
	// than any rocket, trident or explosion moves a player.
	inputDeltaLimit = 100
	// inputStickLimit is how far the move stick may go on either axis: keys give 1, a little
	// over for float rounding.
	inputStickLimit = 1.01
	// badInputLog is how many dropped inputs are logged per player: a disabler sends them
	// every tick.
	badInputLog = 5
)

// badInput reports whether pk is an input no client makes, and logs the first few.
func (p *Player) badInput(pk *packet.PlayerAuthInput) bool {
	why := impossibleInput(pk, p.lastInputTick)
	if why == "" {
		p.lastInputTick = max(p.lastInputTick, pk.Tick)
		return false
	}
	if p.badInputs < badInputLog {
		p.s.log.Info("impossible input dropped", "name", p.name, "why", why, "pos", pk.Position, "tick", pk.Tick)
	}
	p.badInputs++
	return true
}

// impossibleInput is why pk is impossible, or "" if it is not. last is the highest tick the
// player sent so far: once a client has counted its ticks, it never sends tick 0 again.
func impossibleInput(pk *packet.PlayerAuthInput, last uint64) string {
	switch {
	case !vecWithin(pk.Position, inputPosLimit):
		return "position"
	case !vecWithin(pk.Delta, inputDeltaLimit):
		return "velocity"
	case !finite32(pk.MoveVector[0]) || !finite32(pk.MoveVector[1]) || absf(pk.MoveVector[0]) > inputStickLimit || absf(pk.MoveVector[1]) > inputStickLimit:
		return "move stick"
	case !finite32(pk.Pitch) || !finite32(pk.Yaw) || !finite32(pk.HeadYaw):
		return "rotation"
	case pk.Tick == 0 && last > 0:
		return "tick 0"
	}
	return ""
}

// vecWithin reports whether every axis of v is a real number no farther than limit from 0.
func vecWithin(v mgl32.Vec3, limit float32) bool {
	for _, f := range v {
		if !finite32(f) || absf(f) > limit {
			return false
		}
	}
	return true
}
