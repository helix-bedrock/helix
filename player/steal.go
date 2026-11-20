package player

import "github.com/helix-bedrock/protocol"

// Chest stealer: Helix's own rule, beyond BDS. A cheat client empties a container the moment it
// opens, a slot a packet. Taking from a container's slots now waits until the window has been
// open a few ticks, longer than any person needs to see the screen and click, and each take
// spends take credits, earned one a server tick, so a container empties no faster than fast
// clicking does. A refused take is answered like any refused request: the client is sent its
// slots back. Creative players are left alone.

const (
	// takeOpenTicks is how long a window must be open before anything is taken from it.
	takeOpenTicks = 3
	// takeCost is what one take costs, takeCreditMax the most credits kept: one take every two
	// ticks (10 a second), up to 3 in a burst.
	takeCost      = 2
	takeCreditMax = 6
)

// tickTakes earns the player its take credits and counts how long its window is open. Runs:
// the player's region, once a server tick.
func (p *Player) tickTakes() {
	p.takeCredits = min(p.takeCredits+1, takeCreditMax)
	if p.window.open {
		p.window.openTicks = min(p.window.openTicks+1, takeOpenTicks)
	}
}

// mayTake reports whether the player may take an item out of a slot of kind kind now, and
// spends the take if so.
func (p *Player) mayTake(kind byte) bool {
	switch kind {
	case containerLevelEntity, protocol.ContainerShulkerBox:
	default:
		return true
	}
	if p.creative() {
		return true
	}
	if p.window.openTicks < takeOpenTicks || p.takeCredits < takeCost {
		return false
	}
	p.takeCredits -= takeCost
	return true
}
