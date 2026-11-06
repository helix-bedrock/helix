package fire

import (
	"math/rand/v2"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// burnFaces are the blocks a fire tick tries to burn, in BDS's order.
func burnFaces() [6]cube.Face {
	return [...]cube.Face{cube.FaceEast, cube.FaceWest, cube.FaceDown, cube.FaceUp, cube.FaceNorth, cube.FaceSouth}
}

// lavaFaces are the blocks around a lava-flammable block that lava sets fire to, the first
// empty one in BDS's order.
func lavaFaces() [6]cube.Face {
	return [...]cube.Face{cube.FaceEast, cube.FaceWest, cube.FaceUp, cube.FaceDown, cube.FaceSouth, cube.FaceNorth}
}

// allFaces are the six blocks around one.
func allFaces() [6]cube.Face {
	return [...]cube.Face{cube.FaceDown, cube.FaceUp, cube.FaceNorth, cube.FaceSouth, cube.FaceWest, cube.FaceEast}
}

// tick is fire's tick: turn to soul fire, go out if nothing holds it, age, tick again,
// burn out, then burn the blocks around it and spread.
func (t *Ticker) tick(r *world.Region, pos cube.Pos, block uint32) {
	if t.toSoulFire(r, pos) {
		return
	}
	if !t.survives(r, pos) {
		t.remove(r, pos)
		return
	}
	rules := t.Rules()
	rng := t.rng(r)
	if !rules.DoFireTick {
		t.again(r, pos, rng)
		return
	}
	belowPos := pos.Side(cube.FaceDown)
	below, _ := r.Block(belowPos, 0)
	forever := t.is(below, infiniburn)
	age := t.age(block)
	if !forever && age < maxAge {
		if older := min(age+rng.IntN(3)/2, maxAge); older != age {
			block = t.fire[older]
			age = older
			l := t.local(r)
			l.aging = true
			t.set(r, pos, block)
			l.aging = false
		}
	}
	t.again(r, pos, rng)
	if !forever {
		belowProps := t.blocks.Properties(below)
		switch {
		case belowProps.Material == materialExplosive && !rules.TNTExplodes:
			if age < tntStayAge {
				return
			}
			t.remove(r, pos)
			return
		case t.flammableAround(r, pos) && t.blocks.Properties(t.liquidAt(r, belowPos)).Material != materialWater:
			if age == maxAge && belowProps.FlameOdds == 0 && rng.IntN(oldBurnOut) == 0 {
				t.remove(r, pos)
				return
			}
		default:
			if age <= stayAge && t.support.Supports(below, cube.FaceUp) {
				return
			}
			t.remove(r, pos)
			return
		}
	}
	t.spread(r, pos, block, age, rules, rng)
}

// again asks for the fire's next tick, unless one is waiting.
func (t *Ticker) again(r *world.Region, pos cube.Pos, rng *rand.Rand) {
	if !r.HasScheduledTick(pos, t.fire[0]) {
		r.ScheduleTick(pos, t.fire[0], int32(tickBase+rng.IntN(tickSpread)), 0) //nolint:gosec // small
	}
}

// liquidAt is the block in the second layer at pos if there is one, else layer 0.
func (t *Ticker) liquidAt(r *world.Region, pos cube.Pos) uint32 {
	if b, ok := r.Block(pos, 1); ok && b != t.blocks.Air() {
		return b
	}
	b, _ := r.Block(pos, 0)
	return b
}

// spread burns the six blocks around the fire, then may light fire in the empty blocks
// around it from one below to four above, readily where the blocks around them catch
// fire.
func (t *Ticker) spread(r *world.Region, pos cube.Pos, block uint32, age int, rules Rules, rng *rand.Rand) {
	humid := t.humid(r, pos)
	less := 0
	if humid {
		less = humidOdds
	}
	for _, f := range burnFaces() {
		odds := sideOdds - less
		if f == cube.FaceDown || f == cube.FaceUp {
			odds = verticalOdds - less
		}
		t.burn(r, pos.Side(f), odds, age, rng)
	}
	difficulty := 0
	if rules.Difficulty >= difficultyEasy && rules.Difficulty <= difficultyHard {
		difficulty = int(rules.Difficulty) * spreadPerDifficulty
	}
	air := t.blocks.Air()
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			for dy := -1; dy <= spreadReach; dy++ {
				if dx == 0 && dy == 0 && dz == 0 {
					continue
				}
				against := float32(spreadUnit)
				if dy >= 2 {
					against = float32(dy * spreadUnit)
				}
				p := pos.Add(cube.Pos{dx, dy, dz})
				if b, ok := r.Block(p, 0); !ok || b != air {
					continue
				}
				flame := t.flameAround(r, p)
				if flame == 0 {
					continue
				}
				odds := float32(difficulty+int(flame)+spreadBase) / float32(age+tickBase)
				if humid {
					odds *= 0.5
				}
				if odds > 0 && odds >= against*rng.Float32() {
					t.set(r, p, block)
				}
			}
		}
	}
}

// flameAround is the highest flame odds of the blocks around pos.
func (t *Ticker) flameAround(r *world.Region, pos cube.Pos) uint16 {
	var most uint16
	for _, f := range allFaces() {
		if b, ok := r.Block(pos.Side(f), 0); ok {
			most = max(most, t.blocks.Properties(b).FlameOdds)
		}
	}
	return most
}

// humid reports whether the biome at pos is humid.
func (t *Ticker) humid(r *world.Region, pos cube.Pos) bool {
	id, ok := r.Biome(pos)
	if !ok {
		return false
	}
	b, ok := t.biomes.ByID(id)
	return ok && b.Humid
}

// burn is fire burning the block at pos: it may burn away, often turning to fire itself.
// Campfires light and TNT is primed instead.
func (t *Ticker) burn(r *world.Region, pos cube.Pos, odds, age int, rng *rand.Rand) {
	b, ok := r.Block(pos, 0)
	if !ok || rng.IntN(odds) >= int(t.blocks.Properties(b).BurnOdds) {
		return
	}
	camp, explosive := t.is(b, isCampfire), t.is(b, isTNT)
	if rng.IntN(age+keepFire) > keepMax {
		switch {
		case camp:
			t.lightCampfire(r, pos, b)
		case !explosive:
			t.remove(r, pos)
		}
	} else {
		older := min(age+rng.IntN(burnAgeSpread)/burnAgeDiv, maxAge)
		switch {
		case camp:
			t.lightCampfire(r, pos, b)
		case !explosive:
			t.remove(r, pos)
			if t.flammableAround(r, pos) {
				t.set(r, pos, t.fire[older])
			}
		}
	}
	if explosive && t.prime != nil {
		t.prime(r, pos)
	}
}

// lightCampfire lights the campfire at pos unless it is under water.
func (t *Ticker) lightCampfire(r *world.Region, pos cube.Pos, block uint32) {
	if t.blocks.Properties(t.liquidAt(r, pos)).Material == materialWater {
		return
	}
	states := t.blocks.States(block)
	if v, _ := states[extinguishedStat].(uint8); v == 0 {
		return
	}
	lit := make(map[string]any, len(states))
	for k, v := range states {
		lit[k] = v
	}
	lit[extinguishedStat] = uint8(0)
	if id, ok := t.blocks.StateID(t.blocks.Name(block), lit); ok {
		t.set(r, pos, id)
	}
}

// lavaTick is lava's random tick: with fire ticking on and open air or a lava-flammable
// block above, ten tries at a block next to it (corners too); the first that lava lights
// sets fire in the first empty block around it.
func (t *Ticker) lavaTick(r *world.Region, pos cube.Pos) {
	if !t.hasFire || !t.Rules().DoFireTick {
		return
	}
	air := t.blocks.Air()
	if above, ok := r.Block(pos.Side(cube.FaceUp), 0); !ok || above != air && !t.blocks.Properties(above).LavaFlammable {
		return
	}
	rng := t.rng(r)
	for range lavaTries {
		x := rng.IntN(3) - 1
		y := rng.IntN(3) - 1
		z := rng.IntN(3) - 1
		p := pos.Add(cube.Pos{x, y, z})
		if b, ok := r.Block(p, 0); !ok || !t.blocks.Properties(b).LavaFlammable {
			continue
		}
		for _, f := range lavaFaces() {
			if b, ok := r.Block(p.Side(f), 0); ok && b == air {
				t.set(r, p.Side(f), t.fire[0])
				return
			}
		}
	}
}
