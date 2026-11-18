package player

import (
	"fmt"
	"sync"
	"time"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
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

const (
	// tpsLiveEvery is how often, in the player's ticks, the live readout is sent again.
	tpsLiveEvery = 10
	// tpsGood and tpsPoor are where the readout turns from green to yellow, and to red.
	tpsGood = 19.5
	tpsPoor = 15
)

// tpsLive is a player's live tick rate readout: on while server is set.
type tpsLive struct {
	server func() float64
	ticks  int
}

// ShowTPS turns the live readout above the hotbar on, with server giving the server's TPS,
// or off with nil. It shows the server's, the player's region's (with its milliseconds a
// tick) and the player's client's TPS twice a second. Runs: owner of the player's region.
func (p *Player) ShowTPS(server func() float64) {
	p.tpsShow = tpsLive{server: server}
	if server == nil {
		p.send(&packet.Text{TextType: packet.TextTypeTip, Message: " "})
	}
}

// ShowingTPS reports whether the live readout is on.
func (p *Player) ShowingTPS() bool { return p.tpsShow.server != nil }

// tpsLiveTick sends the live readout every tpsLiveEvery ticks while it is on. Runs: the
// player's region, each tick.
func (p *Player) tpsLiveTick() {
	t := &p.tpsShow
	if t.server == nil || p.entity == nil {
		return
	}
	if t.ticks++; t.ticks%tpsLiveEvery != 0 {
		return
	}
	var mspt time.Duration
	if r := p.entity.Region(); r != nil {
		mspt = r.MSPT()
	}
	server, region, client := t.server(), p.RegionTPS(), p.TPS()
	msg := fmt.Sprintf("Server %s%.1f§r  Region %s%.1f§r (%.1f ms)  Client %s%.1f§r TPS",
		tpsColour(server), server, tpsColour(region), region, float64(mspt.Microseconds())/1000, tpsColour(client), client)
	p.send(&packet.Text{TextType: packet.TextTypeTip, Message: msg})
}

// tpsColour is the colour code a tick rate shows in.
func tpsColour(tps float64) string {
	switch {
	case tps >= tpsGood:
		return "§a"
	case tps >= tpsPoor:
		return "§e"
	}
	return "§c"
}
