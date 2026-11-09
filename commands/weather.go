package commands

import (
	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/helix/weather"
)

// BDS 1.26.60's /weather (WeatherCommand: setup 0xBBA0780, execute 0xBBA16D0): weather
// <clear|rain|thunder> [duration] and weather query, and /toggledownfall (0xBB9C9C0). The
// duration is in ticks, 1 to 1000000; without one, 6000 to 17980.

// weatherMaxTime is the longest duration BDS takes.
const weatherMaxTime = 1000000

var weatherSpec = command.Spec{
	Name: "weather", Description: "commands.weather.description", Permission: command.GameDirectors,
	Overloads: []command.Runnable{weatherSet{}, weatherQuery{}},
}

var toggleDownfall = command.Spec{
	Name: "toggledownfall", Description: "commands.downfall.description", Permission: command.GameDirectors,
	Overloads: []command.Runnable{toggleDownfallRun{}},
}

// weatherSet is /weather <type> [duration].
type weatherSet struct {
	Type     command.Weather
	Duration command.Optional[int32] `cmd:"duration"`
}

// weatherQuery is /weather query.
type weatherQuery struct {
	Query command.Literal `cmd:"query"`
}

// toggleDownfallRun is /toggledownfall.
type toggleDownfallRun struct{}

// worldWeather is the weather of the world the command runs in.
func worldWeather(src command.Source, out *command.Output) *weather.Weather {
	p, ok := player.SourcePlayer(src)
	if !ok || p.Host().Weather() == nil {
		out.Fail("commands.generic.dimension.notFound")
		return nil
	}
	return p.Host().Weather()
}

// Run sets the weather: rain and thunder head for on or off, both timers set to the duration.
func (c weatherSet) Run(src command.Source, out *command.Output) {
	w := worldWeather(src, out)
	if w == nil {
		return
	}
	d := w.RandomTime()
	if c.Duration.Set {
		d = c.Duration.Value
	}
	if !inRange(out, d, 1, weatherMaxTime) {
		return
	}
	var rain, thunder float32
	key := "commands.weather.clear"
	switch c.Type {
	case "rain":
		rain, key = 1, "commands.weather.rain"
	case "thunder":
		rain, thunder, key = 1, 1, "commands.weather.thunder"
	}
	w.Set(d, d, rain, thunder)
	out.Success(key)
}

// Run says the weather.
func (weatherQuery) Run(src command.Source, out *command.Output) {
	w := worldWeather(src, out)
	if w == nil {
		return
	}
	name := "clear"
	switch w.Query() {
	case weather.Raining:
		name = "rain"
	case weather.Thundering:
		name = "thunder"
	}
	out.Success("commands.weather.query", "%commands.weather.query."+name)
}

// Run starts or stops the rain.
func (toggleDownfallRun) Run(src command.Source, out *command.Output) {
	w := worldWeather(src, out)
	if w == nil {
		return
	}
	w.Toggle()
	out.Success("commands.downfall.success")
}
