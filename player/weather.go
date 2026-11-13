package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/weather"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// SetWeather sets the world's weather and what says where it rains. Call it before any player
// joins; without it there is no weather.
func (s *Host) SetWeather(w *weather.Weather, chunks *weather.Ticker) {
	s.weather, s.chunkWeather = w, chunks
}

// Weather returns the world's weather, nil for none. Safe from any goroutine.
func (s *Host) Weather() *weather.Weather { return s.weather }

// BroadcastLevelEvent sends a level event to every player online, as BDS's level-wide events
// (weather) go. Safe from any goroutine.
func (s *Host) BroadcastLevelEvent(event, data int32) {
	s.Broadcast(&packet.LevelEvent{EventType: event, EventData: data})
}

// inRain is BDS's Actor::isInRain: it rains on the block at top, the top of the entity's box.
// Runs: owner of the region.
func (s *Host) inRain(r *world.Region, top mgl64.Vec3) bool {
	if s.chunkWeather == nil {
		return false
	}
	pos := cube.Pos{int(math.Floor(float64(top[0]))), int(math.Floor(float64(top[1]))), int(math.Floor(float64(top[2])))}
	return s.chunkWeather.RainingAt(r, pos)
}

// globalTick is what the global owner runs each tick: sleeping, then the weather.
func (s *Host) globalTick() {
	s.sleepTick()
	if s.weather != nil {
		s.weather.Tick()
	}
}

// sendWeather sends a joining player the weather as BDS does: thunder at its level, else rain
// at its level.
func (p *Player) sendWeather() {
	if len(p.fogs) > 0 {
		p.sendFogs()
	}
	if p.s.weather == nil {
		return
	}
	if event, data, ok := p.s.weather.JoinEvent(); ok {
		p.send(&packet.LevelEvent{EventType: event, EventData: data})
	}
}
