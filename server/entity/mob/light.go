package mob

import (
	"math"

	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/world"
)

// brightness is the light at pos as spawning and burning see it: block
// light, or sky light dimmed by the time of day and the weather.
//
// The darkening is vanilla's classic Level.updateSkyBrightness (Java
// Edition), recalled rather than read from a source, and not yet compared
// with BDS.
func brightness(tx *world.Tx, pos cube.Pos) int {
	sky := int(tx.SkyLight(pos)) - skyDarken(tx)
	return max(int(tx.BlockLight(pos)), sky, 0)
}

func skyDarken(tx *world.Tx) int {
	t := float64(tx.World().Time() % 24000)
	d := t/24000 - 0.25
	d -= math.Floor(d)
	angle := (d*2 + (0.5 - math.Cos(d*math.Pi)/2)) / 3
	v := 1 - (math.Cos(angle*2*math.Pi)*2 + 0.2)
	v = 1 - math.Max(0, math.Min(1, v))
	if tx.Raining() {
		v *= 1 - 5.0/16
	}
	if tx.Thundering() {
		v *= 1 - 5.0/16
	}
	return int((1 - v) * 11)
}
