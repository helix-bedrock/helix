package mob

import (
	"encoding/json"
	"math"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/world"
)

// despawnSpec is a mob's minecraft:despawn component.
type despawnSpec struct {
	// fromDistance: despawn beyond max blocks from every player, and beyond
	// min with a 1 in chance chance once inactive for inactivity ticks.
	// Defaults from Microsoft's documentation of the component: 32, 128,
	// 1 in 800, 30 seconds.
	fromDistance       bool
	min, max           float64
	chance, inactivity int
	filters            json.RawMessage
}

func parseDespawn(raw json.RawMessage) *despawnSpec {
	d := &despawnSpec{filters: field(raw, "filters")}
	if dist := field(raw, "despawn_from_distance"); dist != nil {
		d.fromDistance = true
		d.min = numberField(dist, "min_distance", 32)
		d.max = numberField(dist, "max_distance", 128)
	}
	d.chance = int(numberField(raw, "min_range_random_chance", 800))
	d.inactivity = int(numberField(raw, "min_range_inactivity_timer", 30) * 20)
	return d
}

// despawn removes the mob if its despawn rules say so. Named and otherwise
// persistent mobs never despawn.
func (m *Mob) despawn(tx *world.Tx) bool {
	d := m.s.spec.despawn
	if d == nil || m.s.persistent {
		return false
	}
	dist := nearestPlayerDist(tx, m.data.Pos)
	if math.IsInf(dist, 1) {
		// No players in the region: nothing to measure from.
		return false
	}
	gone := false
	if d.fromDistance {
		if dist > d.min {
			m.s.inactive++
		} else {
			m.s.inactive = 0
		}
		gone = dist > d.max || (dist > d.min && m.s.inactive >= d.inactivity && m.s.rand.IntN(max(d.chance, 1)) == 0)
	}
	if !gone && len(d.filters) > 0 && m.s.ticks%20 == 0 {
		if dist > 32 {
			m.s.inactive += 20
		}
		gone = evalFilter(d.filters, filterEnv{tx: tx, self: m})
	}
	if gone {
		_ = m.Close()
	}
	return gone
}

// nearestPlayerDist is the distance from pos to the nearest player in the
// region. Players further away are in other regions, which are always
// further than any despawn distance apart.
func nearestPlayerDist(tx *world.Tx, pos mgl64.Vec3) float64 {
	best := math.Inf(1)
	for p := range tx.Players() {
		best = math.Min(best, p.Position().Sub(pos).Len())
	}
	return best
}
