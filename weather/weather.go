// Package weather is BDS 1.26.60's weather: rain and thunder coming and going (Weather::tick and
// its server tick, sub_CD9E590 and sub_CD9E6B0), /weather's change (Level::updateWeather,
// sub_CDA0560), sleep clearing it (sub_CDA6DD0), and on every ticking chunk lightning, ice and
// snow (LevelChunk::tick, sub_CF951A0). Only the overworld has weather.
package weather

import (
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"

	"github.com/helix-bedrock/protocol/packet"
)

// BDS's numbers for the weather cycle.
const (
	// fade is how far a level moves towards its target each tick.
	fade = 0.01
	// clearSpan and stormSpan are the random part of how long clear weather and rain or
	// thunder last, in ticks; minSpan is added to clear weather and rain, thunderMin to thunder.
	clearSpan  = 168000
	stormSpan  = 12000
	minSpan    = 12000
	thunderMin = 3600
	// eventScale turns a level into a level event's data.
	eventScale = 65535
	// RainingLevel is the rain level above which it rains on blocks.
	RainingLevel = 0.2
)

// Levels is the weather as level.dat keeps it.
type Levels struct {
	Rain, Thunder         float32
	RainTime, ThunderTime int32
}

// Broadcast sends a level event to every player online. Runs: the caller of the method that
// changed the weather.
type Broadcast func(event, data int32)

// Weather is the world's weather. Its levels are read from any goroutine; Tick, Set and Stop
// change it and may be called from any goroutine, one at a time each tick by the server.
type Weather struct {
	mu        sync.Mutex
	rng       *rand.Rand
	broadcast Broadcast

	// rain and thunder are the levels now, old the levels a tick ago, target where they
	// head; all float32 bits.
	rain, oldRain, rainTarget          atomic.Uint32
	thunder, oldThunder, thunderTarget atomic.Uint32
	// rainTime and thunderTime count down to the next change, level.dat's.
	rainTime, thunderTime int32
	// savedRain and savedThunder are level.dat's levels: the levels now, but set at once by
	// a change.
	savedRain, savedThunder float32
	cycle                   atomic.Bool
}

// New makes the weather from level.dat's: it heads for the saved levels from none, as BDS's
// does when the world loads. cycle is the doweathercycle game rule.
func New(l Levels, cycle bool, seed uint64, broadcast Broadcast) *Weather {
	w := &Weather{rng: rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15)), broadcast: broadcast} //nolint:gosec // weather, not a secret
	w.rainTarget.Store(math.Float32bits(l.Rain))
	w.thunderTarget.Store(math.Float32bits(l.Thunder))
	w.rainTime, w.thunderTime = l.RainTime, l.ThunderTime
	w.savedRain, w.savedThunder = l.Rain, l.Thunder
	w.cycle.Store(cycle)
	return w
}

// NewTimes are a new world's timers: BDS starts both on a clear spell.
func NewTimes(seed uint64) (rainTime, thunderTime int32) {
	r := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15)) //nolint:gosec // weather, not a secret
	return int32(r.IntN(clearSpan)) + minSpan, int32(r.IntN(clearSpan)) + minSpan
}

func load(a *atomic.Uint32) float32     { return math.Float32frombits(a.Load()) }
func store(a *atomic.Uint32, v float32) { a.Store(math.Float32bits(v)) }

// Rain is the rain level now, 0 to 1; 0 on a nil Weather. Safe from any goroutine.
func (w *Weather) Rain() float32 {
	if w == nil {
		return 0
	}
	return load(&w.rain)
}

// Thunder is the thunder level now, 0 to 1; 0 on a nil Weather. Safe from any goroutine.
func (w *Weather) Thunder() float32 {
	if w == nil {
		return 0
	}
	return load(&w.thunder)
}

// OldRain is the rain level a tick ago. Safe from any goroutine.
func (w *Weather) OldRain() float32 {
	if w == nil {
		return 0
	}
	return load(&w.oldRain)
}

// SetCycle sets the doweathercycle game rule. Safe from any goroutine.
func (w *Weather) SetCycle(on bool) { w.cycle.Store(on) }

// Cycle reports the doweathercycle game rule. Safe from any goroutine.
func (w *Weather) Cycle() bool { return w.cycle.Load() }

// Saved is the weather as level.dat keeps it. Safe from any goroutine.
func (w *Weather) Saved() Levels {
	w.mu.Lock()
	defer w.mu.Unlock()
	return Levels{Rain: w.savedRain, Thunder: w.savedThunder, RainTime: w.rainTime, ThunderTime: w.thunderTime}
}

// Kind is what /weather query answers.
type Kind int

// The weather kinds.
const (
	Clear Kind = iota
	Raining
	Thundering
)

// Query is BDS's /weather query: thunder when level.dat's thunder level is above 0, rain when
// its rain level is, else clear.
func (w *Weather) Query() Kind {
	l := w.Saved()
	switch {
	case l.Thunder > 0:
		return Thundering
	case l.Rain > 0:
		return Raining
	}
	return Clear
}

// JoinEvent is the level event BDS sends a joining player: thunder at its level if there is
// any, else rain at its level if there is any. ok is false when it is clear.
func (w *Weather) JoinEvent() (event, data int32, ok bool) {
	l := w.Saved()
	switch {
	case l.Thunder != 0:
		return packet.LevelEventStartThunderstorm, scaled(l.Thunder), true
	case l.Rain > 0:
		return packet.LevelEventStartRaining, scaled(l.Rain), true
	}
	return 0, 0, false
}

// scaled is a level as a level event's data.
func scaled(level float32) int32 { return int32(math.Floor(float64(level * eventScale))) }

// approach moves v a step towards target, kept within 0 and 1.
func approach(v, target float32) float32 {
	if target > v {
		v = min(v+fade, target)
	} else {
		v = max(v-fade, target)
	}
	return min(max(v, 0), 1)
}

// Tick is one world tick of the weather (Weather::tick): the levels fade towards their
// targets, and with doweathercycle the timers count down and rain and thunder come and go.
func (w *Weather) Tick() {
	w.mu.Lock()
	defer w.mu.Unlock()
	store(&w.oldRain, load(&w.rain))
	store(&w.oldThunder, load(&w.thunder))
	store(&w.thunder, approach(load(&w.thunder), load(&w.thunderTarget)))
	store(&w.rain, approach(load(&w.rain), load(&w.rainTarget)))
	if w.cycle.Load() {
		w.cycleTick()
	}
	w.savedRain, w.savedThunder = load(&w.rain), load(&w.thunder)
}

// cycleTick is Weather's server tick: both timers count down; when one runs out, rain or
// thunder flips and a new time is drawn. A rain change is told to everyone with the new rain
// time scaled as BDS does; a thunder change is told to no one until they join again.
func (w *Weather) cycleTick() {
	if w.thunderTime >= 2 && w.rainTime >= 2 {
		w.thunderTime--
		w.rainTime--
		return
	}
	raining, thundering := load(&w.rainTarget) > 0, load(&w.thunderTarget) > 0
	thunder := thundering != (w.thunderTime < 2)
	thunderTime := w.thunderTime - 1
	if w.thunderTime <= 1 {
		if thunder {
			thunderTime = int32(w.rng.IntN(stormSpan)) + thunderMin
		} else {
			thunderTime = int32(w.rng.IntN(clearSpan)) + minSpan
		}
	}
	rain := raining != (w.rainTime < 2)
	rainTime := w.rainTime - 1
	if w.rainTime <= 1 {
		span := clearSpan
		if rain {
			span = stormSpan
		}
		rainTime = int32(w.rng.IntN(span)) + minSpan
	}
	w.thunderTime, w.rainTime = thunderTime, rainTime
	if thunder != thundering {
		store(&w.thunderTarget, level(thunder))
	}
	if rain == raining {
		return
	}
	store(&w.rainTarget, level(rain))
	if w.broadcast == nil {
		return
	}
	event := int32(packet.LevelEventStartRaining)
	if !rain {
		event = packet.LevelEventStopRaining
	}
	w.broadcast(event, scaled(float32(rainTime)))
}

func level(on bool) float32 {
	if on {
		return 1
	}
	return 0
}

// Set is BDS's Level::updateWeather, which /weather runs: the levels head for rain and thunder
// (0 to 1) and the timers are set. When a level changes everyone is told: rain and thunder
// starting at their level, or stopping.
func (w *Weather) Set(rainTime, thunderTime int32, rain, thunder float32) {
	w.mu.Lock()
	rainChanged, thunderChanged := w.savedRain != rain, w.savedThunder != thunder
	w.rainTime, w.thunderTime = rainTime, thunderTime
	if !rainChanged && !thunderChanged {
		w.mu.Unlock()
		return
	}
	store(&w.rainTarget, rain)
	store(&w.thunderTarget, thunder)
	w.savedRain, w.savedThunder = rain, thunder
	w.mu.Unlock()
	if w.broadcast == nil {
		return
	}
	if rainChanged {
		if rain > 0 {
			w.broadcast(packet.LevelEventStartRaining, scaled(rain))
		} else {
			w.broadcast(packet.LevelEventStopRaining, 0)
		}
	}
	if thunderChanged {
		if thunder > 0 {
			w.broadcast(packet.LevelEventStartThunderstorm, scaled(thunder))
		} else {
			w.broadcast(packet.LevelEventStopThunderstorm, 0)
		}
	}
}

// RandomTime is BDS's /weather time when none is given: 300 to 900 seconds, in ticks.
func (w *Weather) RandomTime() int32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return int32(20*w.rng.IntN(600)) + 6000
}

// Stop is the weather clearing after a night is slept through (sub_CDA6DD0): with
// doweathercycle, rain or thunder stops at once, both timers are drawn again and everyone is
// told.
func (w *Weather) Stop() {
	if !w.cycle.Load() {
		return
	}
	w.mu.Lock()
	if load(&w.rain) == 0 && load(&w.thunder) == 0 {
		w.mu.Unlock()
		return
	}
	for _, a := range []*atomic.Uint32{&w.rain, &w.oldRain, &w.rainTarget, &w.thunder, &w.oldThunder, &w.thunderTarget} {
		store(a, 0)
	}
	w.savedRain, w.savedThunder = 0, 0
	w.thunderTime = int32(w.rng.IntN(clearSpan)) + minSpan
	w.rainTime = int32(w.rng.IntN(clearSpan)) + minSpan
	w.mu.Unlock()
	if w.broadcast != nil {
		w.broadcast(packet.LevelEventStopRaining, 0)
		w.broadcast(packet.LevelEventStopThunderstorm, 0)
	}
}

// Toggle is BDS's /toggledownfall (sub_BB9C9C0): with no rain, rain starts for 12000 to 23999
// ticks; with rain, rain and thunder stop and both timers are drawn again. Everyone is told.
func (w *Weather) Toggle() {
	w.mu.Lock()
	start := w.savedRain == 0
	span := clearSpan
	if start {
		span = stormSpan
	}
	w.rainTime = int32(w.rng.IntN(span)) + minSpan
	if start {
		w.savedRain = 1
		store(&w.rainTarget, 1)
		w.mu.Unlock()
		if w.broadcast != nil {
			w.broadcast(packet.LevelEventStartRaining, scaled(1))
		}
		return
	}
	w.savedRain, w.savedThunder = 0, 0
	store(&w.rainTarget, 0)
	store(&w.thunderTarget, 0)
	w.thunderTime = int32(w.rng.IntN(clearSpan)) + minSpan
	w.mu.Unlock()
	if w.broadcast != nil {
		w.broadcast(packet.LevelEventStopRaining, 0)
		w.broadcast(packet.LevelEventStopThunderstorm, 0)
	}
}
