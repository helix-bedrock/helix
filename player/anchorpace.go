package player

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Auto anchor: Helix's own rule, beyond BDS. A cheat client places a respawn anchor, switches to
// glowstone, fills it and sets it off within a tick or two. A person needs time for each step:
// a use of an anchor needs anchorGapTicks since the player's last use of the same anchor,
// anchorPlacedTicks since it placed it, and anchorSwitchTicks since it changed the held slot
// (by MobEquipment or by the slot a transaction names). A use too soon does nothing.

const (
	// anchorGapTicks is the least number of ticks between a player's uses of one anchor.
	anchorGapTicks = 2
	// anchorPlacedTicks is the least number of ticks from placing an anchor to using it.
	anchorPlacedTicks = 4
	// anchorSwitchTicks is the least number of ticks from changing the held slot to using an
	// anchor.
	anchorSwitchTicks = 3
)

// anchorPace is the anchor a player last placed or used, and the world tick it did.
type anchorPace struct {
	pos    cube.Pos
	at     int64
	set    bool
	placed bool
}

// noteHeld records the tick the held slot changes, when slot is another slot.
func (p *Player) noteHeld(slot uint8) {
	if slot == p.inventory.held || p.entity == nil {
		return
	}
	if r := p.entity.Region(); r != nil {
		p.heldAt = r.CurrentTick()
	}
}

// anchorReady reports whether the player may use the anchor at pos now, and if so counts this
// use.
func (p *Player) anchorReady(r *world.Region, pos cube.Pos) bool {
	now := r.CurrentTick()
	a := &p.anchor
	if !p.creative() {
		if a.set && a.pos == pos {
			gap := int64(anchorGapTicks)
			if a.placed {
				gap = anchorPlacedTicks
			}
			if now-a.at < gap {
				return false
			}
		}
		if now-p.heldAt < anchorSwitchTicks {
			return false
		}
	}
	*a = anchorPace{pos: pos, at: now, set: true}
	return true
}

// anchorPlaced records that the player placed an anchor at pos.
func (p *Player) anchorPlaced(r *world.Region, pos cube.Pos) {
	p.anchor = anchorPace{pos: pos, at: r.CurrentTick(), set: true, placed: true}
}
