package player

import (
	"sync"
	"time"

	"github.com/helix-bedrock/helix/world"
)

// clientTicks times a client's own ticks: a client sends one PlayerAuthInput per client
// tick, 20 a second when it keeps up, so their arrival rate is the client's tick rate.
type clientTicks struct {
	mu    sync.Mutex
	meter world.TickMeter
}

// add records a PlayerAuthInput arriving now. Network goroutine.
func (c *clientTicks) add(now time.Time) {
	c.mu.Lock()
	c.meter.Add(now, 0)
	c.mu.Unlock()
}

// TPS is the player's client's ticks per second over its last 5 seconds of input, at most
// world.MaxTPS: low when the client lags (or its connection holds input back), whatever the
// server does. world.MaxTPS until the client has sent input. Runs: anywhere.
func (p *Player) TPS() float64 {
	p.clientTicks.mu.Lock()
	defer p.clientTicks.mu.Unlock()
	return p.clientTicks.meter.TPS(time.Now())
}

// RegionTPS is the TPS of the region the player is in (world.Region.TPS): low when the
// server lags where the player is. Runs: owner of the player's region.
func (p *Player) RegionTPS() float64 {
	if p.entity == nil {
		return world.MaxTPS
	}
	r := p.entity.Region()
	if r == nil {
		return world.MaxTPS
	}
	return r.TPS()
}
