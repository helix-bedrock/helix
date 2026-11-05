// Package daytime is the world's clock and what the time of day means for the sky, as BDS
// 1.26.60 has it: the overworld clock, the sun's angle, how dark the sky is, and what a
// daylight sensor reads.
package daytime

import (
	"math"
	"sync/atomic"

	"github.com/helix-bedrock/helix/direction"
)

// DayLength is the ticks in one day.
const DayLength = 24000

// OverworldClockName is the name of the clock days run on, and OverworldClockID its ID.
const (
	OverworldClockName        = "minecraft:overworld"
	OverworldClockID   uint64 = 7194480507151251734
)

// MarkerPeriod is how often each marker comes round.
const MarkerPeriod = DayLength

// Marker is a named moment of the day on a clock.
type Marker struct {
	ID   uint64
	Name string
	Time int32
}

// Markers are the overworld clock's markers, in BDS's order, read from a running BDS.
var Markers = [...]Marker{
	{2625810911898139949, "minecraft:sunrise", 23000},
	{4918950784056990566, "minecraft:night", 13000},
	{6827470627776846754, "minecraft:noon", 6000},
	{11262090321339182944, "minecraft:midnight", 18000},
	{13638948813458750018, "minecraft:day", 1000},
	{16664792990819124822, "minecraft:sunset", 12000},
}

// clockState is the clock's reading at a world tick.
type clockState struct {
	time  int32
	at    int64
	cycle bool
}

// Clock is the overworld's clock. It is worked out from the world's tick rather than counted
// up, so regions on any core read the same time without sharing a lock. Safe from any goroutine.
type Clock struct {
	ticks func() int64
	state atomic.Pointer[clockState]
}

// NewClock returns a clock showing time now. ticks is the world's tick; cycle is the
// dodaylightcycle game rule: without it the clock stands still.
func NewClock(ticks func() int64, time int32, cycle bool) *Clock {
	c := &Clock{ticks: ticks}
	c.state.Store(&clockState{time: time, at: ticks(), cycle: cycle})
	return c
}

// Time returns the clock's time. It stops at the largest int32 rather than wrapping.
func (c *Clock) Time() int32 {
	s := c.state.Load()
	if !s.cycle {
		return s.time
	}
	t := int64(s.time) + c.ticks() - s.at
	if t > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(t) //nolint:gosec // range checked above
}

// Set changes the time.
func (c *Clock) Set(time int32) {
	c.state.Store(&clockState{time: time, at: c.ticks(), cycle: c.state.Load().cycle})
}

// Cycle reports whether days pass, the dodaylightcycle game rule.
func (c *Clock) Cycle() bool { return c.state.Load().cycle }

// SetCycle starts or stops days passing.
func (c *Clock) SetCycle(on bool) {
	c.state.Store(&clockState{time: c.Time(), at: c.ticks(), cycle: on})
}

// tableScale turns radians into steps of BDS's sine table, which all of this is read through.
const tableScale = 10430.378

const pi32 = float32(math.Pi)

// tableCos is the cosine as BDS's table gives it, to the same step.
func tableCos(x float32) float32 {
	return direction.Cos(x * tableScale)
}

// SunAngle is BDS's sun angle, 0 to 1 with 0 at noon. A clock that runs is a tick ahead here.
func SunAngle(time int32, cycle bool) float32 {
	var ahead float32
	if cycle {
		ahead = 1
	}
	x := (float32(time%DayLength)+ahead)/DayLength - 0.25
	if x < 0 {
		x++
	}
	if x > 1 {
		x--
	}
	return ((((tableCos(pi32*x)+1)*-0.5 + 1) - x) / 3) + x
}

// SkyDarken is how much the sky takes off sky light, 0 at noon and 11 at midnight. Weather is
// left out: Helix has none yet, and BDS's terms for it take nothing off without rain.
func SkyDarken(time int32, cycle bool) int32 {
	a := SunAngle(time, cycle)
	c := tableCos(a*pi32 + a*pi32)
	bright := 1 - min(1, max(0, 1-(c+c+0.5)))
	return int32((1 - bright) * 11)
}

// SensorSignal is the power of a daylight sensor with sky light at it. An inverted one gives
// what the sky lacks. The other follows the sun, its angle pulled a fifth of the way to noon.
func SensorSignal(sky uint8, angle float32, darken int32, inverted bool) int32 {
	light := int32(sky) - darken
	if inverted {
		return min(max(15-light, 0), 15)
	}
	if light <= 0 {
		return 0
	}
	a := angle*pi32 + angle*pi32
	var noon float32
	if a >= pi32 {
		noon = 2 * pi32
	}
	a += (noon - a) * 0.2
	power := int32(math.Round(float64(float32(light) * tableCos(a))))
	return min(max(power, 0), 15)
}
