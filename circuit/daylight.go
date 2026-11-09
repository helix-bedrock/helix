package circuit

import (
	"maps"

	"github.com/helix-bedrock/helix/daytime"
	"github.com/helix-bedrock/helix/weather"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/redstone"
	"github.com/helix-bedrock/world/cube"
)

// BDS's name for a daylight sensor's block entity.
const daylightEntityID = "DaylightDetector"

// daylightInterval is how often in clock ticks a sensor reads the sky.
const daylightInterval = 20

// SetClock sets the clock daylight sensors read. Call it before any player joins. Without one
// they give no power.
func (t *Ticker) SetClock(c *daytime.Clock) { t.clock = c }

// SetWeather sets the weather that dims what daylight sensors read. Call it before any player
// joins.
func (t *Ticker) SetWeather(w *weather.Weather) { t.weather = w }

// daylightEntity is a daylight sensor's block entity: what makes it read the sky.
type daylightEntity struct {
	t     *Ticker
	saved map[string]any
}

// ensureDaylight gives the sensor at pos its block entity if the world holds none.
func (t *Ticker) ensureDaylight(r *world.Region, pos cube.Pos) {
	if be, ok := r.BlockEntity(pos); ok {
		if _, ok := be.(*daylightEntity); ok {
			return
		}
	}
	r.SetBlockEntity(pos, &daylightEntity{t: t})
}

// SaveData is BDS's compound for it.
func (e *daylightEntity) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(e.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = daylightEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	return m
}

// NetworkData is the same: nothing in it is hidden.
func (e *daylightEntity) NetworkData(pos cube.Pos) map[string]any { return e.SaveData(pos) }

// TickBlockEntity reads the sky on every twentieth tick of the clock, as BDS's block actor.
func (e *daylightEntity) TickBlockEntity(r *world.Region, pos cube.Pos) {
	t := e.t
	if t.clock == nil || t.clock.Time()%daylightInterval != 0 {
		return
	}
	block, ok := r.Block(pos, 0)
	if !ok || t.kind(block) != kindDaylight {
		return
	}
	time, cycle := t.clock.Time(), t.clock.Cycle()
	power := daytime.SensorSignal(r.SkyLight(pos), daytime.SunAngle(time, cycle), daytime.SkyDarken(time, cycle, t.weather.Rain(), t.weather.Thunder()), t.blocks.Name(block) == nameDaylightInverted)
	if next := t.with(block, stateSignal, power); next != block && r.SetBlock(pos, 0, next) {
		r.UpdateNeighbours(pos)
	}
	if comp := t.circuitOf(r).sys.Component(toPos(pos), redstone.KindProducer); comp != nil {
		comp.SetStrength(power)
	}
}
