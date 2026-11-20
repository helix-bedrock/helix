package player

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Auto anchor: Helix's own rule, beyond BDS. A cheat client places a respawn anchor, fills it
// with glowstone and sets it off all in one tick. A player's uses of the same anchor, placing it
// counted, now need anchorGapTicks between them, as switching items and clicking again takes a
// person at least that long; a use too soon does nothing.

// anchorGapTicks is the least number of ticks between a player's uses of one anchor.
const anchorGapTicks = 2

// anchorPace is the anchor a player last placed or used, and the world tick it did.
type anchorPace struct {
	pos cube.Pos
	at  int64
	set bool
}

// anchorReady reports whether the player may use the anchor at pos now, and if so counts this
// use.
func (p *Player) anchorReady(r *world.Region, pos cube.Pos) bool {
	now := r.CurrentTick()
	a := &p.anchor
	if a.set && a.pos == pos && now-a.at < anchorGapTicks && !p.creative() {
		return false
	}
	*a = anchorPace{pos: pos, at: now, set: true}
	return true
}
