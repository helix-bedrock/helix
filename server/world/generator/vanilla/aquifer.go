package vanilla

import "math"

// Aquifers, ported from Pumpkin's
// pumpkin-world/src/generation/noise/aquifer_sampler.rs: underground
// pockets of water and lava with their own levels, so caves below the sea
// are mostly dry instead of flooded.

// wayBelowMinY marks "no fluid level", Java's DimensionType.WAY_BELOW_MIN_Y.
const wayBelowMinY = -2032 << 4

type fluidLevel struct {
	maxY int32
	kind uint8
}

func (f fluidLevel) at(y int32) uint8 {
	if y < f.maxY {
		return f.kind
	}
	return kindAir
}

// chunkOffsets are the chunks around a position whose surface decides its
// water level, as Java lists them.
var chunkOffsets = [13][2]int32{{0, 0}, {-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {-3, 0}, {-2, 0}, {-1, 0}, {1, 0}, {-2, 1}, {-1, 1}, {0, 1}, {1, 1}}

type aquifer struct {
	g                          *Generator
	c                          *evalCtx
	sea, lava                  fluidLevel
	sx, sy, sz, ny, nz         int32
	points                     [][3]int32
	levels                     []*fluidLevel
	skipAbove                  int32
	skipSet                    bool
	minSX, minSZ, maxSX, maxSZ int32
	heights                    map[[2]int32]int32
}

func (g *Generator) newAquifer(c *evalCtx, cx, cz int32) *aquifer {
	a := &aquifer{g: g, c: c, heights: map[[2]int32]int32{}}
	a.sea = fluidLevel{maxY: g.r.set.SeaLevel, kind: kindWater}
	a.lava = fluidLevel{maxY: -54, kind: kindLava}
	a.sx = floorDiv(cx<<4, 16) - 1
	ex := floorDiv((cx<<4)+15, 16) + 1
	nx := ex - a.sx + 1
	a.sy = floorDiv(g.r.set.MinY, 12) - 1
	ey := floorDiv(g.r.set.MinY+g.r.set.Height, 12) + 1
	a.ny = ey - a.sy + 1
	a.sz = floorDiv(cz<<4, 16) - 1
	ez := floorDiv((cz<<4)+15, 16) + 1
	a.nz = ez - a.sz + 1
	a.minSX, a.minSZ = a.sx<<4, a.sz<<4
	a.maxSX, a.maxSZ = (ex<<4)+9, (ez<<4)+9
	size := nx * a.ny * a.nz
	a.points = make([][3]int32, size)
	a.levels = make([]*fluidLevel, size)
	for ox := range nx {
		for oy := range a.ny {
			for oz := range a.nz {
				x, y, z := a.sx+ox, a.sy+oy, a.sz+oz
				r := g.aquiferSplit.atPos(x, y, z)
				rx := x*16 + r.nextIntN(10)
				ry := y*12 + r.nextIntN(9)
				rz := z*16 + r.nextIntN(10)
				a.points[(ox*a.nz+oz)*a.ny+oy] = [3]int32{rx, ry, rz}
			}
		}
	}
	return a
}

// fluid is the plain fluid level: lava deep down, the sea above.
func (a *aquifer) fluid(y int32) fluidLevel {
	if y < min(a.lava.maxY, a.sea.maxY) {
		return a.lava
	}
	return a.sea
}

func (a *aquifer) height(x, z int32) int32 {
	k := [2]int32{x &^ 3, z &^ 3}
	if h, ok := a.heights[k]; ok {
		return h
	}
	h := a.g.preliminarySurface(a.c, x, z)
	a.heights[k] = h
	return h
}

func (a *aquifer) index(x, y, z int32) (int32, bool) {
	lx, ly, lz := x-a.sx, y-a.sy, z-a.sz
	if lx < 0 || ly < 0 || lz < 0 || ly >= a.ny || lz >= a.nz {
		return 0, false
	}
	i := (lx*a.nz+lz)*a.ny + ly
	return i, int(i) < len(a.points)
}

func (a *aquifer) skipSamplingAbove() int32 {
	if a.skipSet {
		return a.skipAbove
	}
	top := int32(math.MinInt32)
	for z := a.minSZ; z <= a.maxSZ; z += 4 {
		for x := a.minSX; x <= a.maxSX; x += 4 {
			top = max(top, a.height(x, z))
		}
	}
	a.skipAbove = (floorDiv(top+8+12, 12)+1)*12 + 11 - 1
	a.skipSet = true
	return a.skipAbove
}

// at decides what fills an empty block at x, y, z with the density given:
// kindStone if the aquifer's barrier keeps it solid.
func (a *aquifer) at(x, y, z int32, density float64) uint8 {
	if density > 0 {
		return kindStone
	}
	fl := a.fluid(y)
	if y > a.skipSamplingAbove() {
		return fl.at(y)
	}
	if fl.at(y) == kindLava {
		return kindLava
	}
	gx, gy, gz := floorDiv(x-5, 16), floorDiv(y+1, 12), floorDiv(z-5, 16)
	i00, ok := a.index(gx, gy-1, gz)
	if !ok {
		return fl.at(y)
	}
	i01, i10 := i00+a.ny, i00+a.ny*a.nz
	i11 := i10 + a.ny
	if int(i11+2) >= len(a.points) {
		return fl.at(y)
	}
	type near struct {
		i    int32
		dist int32
	}
	nearest := [4]near{{-1, math.MaxInt32}, {-1, math.MaxInt32}, {-1, math.MaxInt32}, {-1, math.MaxInt32}}
	for _, i := range [12]int32{i11 + 2, i10 + 2, i01 + 2, i00 + 2, i11 + 1, i10 + 1, i01 + 1, i00 + 1, i11, i10, i01, i00} {
		p := a.points[i]
		dx, dy, dz := p[0]-x, p[1]-y, p[2]-z
		h := dx*dx + dy*dy + dz*dz
		if nearest[3].dist > h {
			nearest[3] = near{i, h}
			if nearest[2].dist > h {
				nearest[3] = nearest[2]
				nearest[2] = near{i, h}
			}
			if nearest[1].dist > h {
				nearest[2] = nearest[1]
				nearest[1] = near{i, h}
			}
			if nearest[0].dist > h {
				nearest[1] = nearest[0]
				nearest[0] = near{i, h}
			}
		}
	}
	l2 := a.waterLevel(nearest[0].i)
	state := l2.at(y)
	sim12 := similarity(nearest[0].dist, nearest[1].dist)
	if sim12 <= 0 {
		return state
	}
	if state == kindWater && a.fluid(y-1).at(y-1) == kindLava {
		return state
	}
	barrier := math.NaN()
	l3 := a.waterLevel(nearest[1].i)
	if density+sim12*a.pressure(&barrier, x, y, z, l2, l3) > 0 {
		return kindStone
	}
	l4 := a.waterLevel(nearest[2].i)
	if sim13 := similarity(nearest[0].dist, nearest[2].dist); sim13 > 0 {
		if density+sim12*sim13*a.pressure(&barrier, x, y, z, l2, l4) > 0 {
			return kindStone
		}
	}
	if sim23 := similarity(nearest[1].dist, nearest[2].dist); sim23 > 0 {
		if density+sim12*sim23*a.pressure(&barrier, x, y, z, l3, l4) > 0 {
			return kindStone
		}
	}
	return state
}

func similarity(a, b int32) float64 {
	return float64(1 - float32(abs32(b-a))/25)
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// pressure is the barrier between two fluid levels at a position, Java's
// calculatePressure.
func (a *aquifer) pressure(barrier *float64, x, y, z int32, l1, l2 fluidLevel) float64 {
	b1, b2 := l1.at(y), l2.at(y)
	if (b1 == kindLava && b2 == kindWater) || (b1 == kindWater && b2 == kindLava) {
		return 2
	}
	diff := abs32(l1.maxY - l2.maxY)
	if diff == 0 {
		return 0
	}
	avg := 0.5 * float64(l1.maxY+l2.maxY)
	scaled := float64(y) + 0.5 - avg
	half := float64(diff) / 2
	o := half - math.Abs(scaled)
	var q float64
	if scaled > 0 {
		if o > 0 {
			q = o / 1.5
		} else {
			q = o / 2.5
		}
	} else {
		p := 3 + o
		if p > 0 {
			q = p / 3
		} else {
			q = p / 10
		}
	}
	r := 0.0
	if q >= -2 && q <= 2 {
		if math.IsNaN(*barrier) {
			*barrier = a.g.r.barrier.eval(a.c, x, y, z)
		}
		r = *barrier
	}
	return 2 * (r + q)
}

// waterLevel is the fluid level of the aquifer cell whose point is i.
func (a *aquifer) waterLevel(i int32) fluidLevel {
	if l := a.levels[i]; l != nil {
		return *l
	}
	p := a.points[i]
	l := a.computeLevel(p[0], p[1], p[2])
	a.levels[i] = &l
	return l
}

func (a *aquifer) computeLevel(x, y, z int32) fluidLevel {
	fl := a.fluid(y)
	up, down := y+12, y-12
	flooded := false
	minSurface := int32(math.MaxInt32)
	for _, off := range chunkOffsets {
		ox, oz := x+off[0]<<4, z+off[1]<<4
		n := a.height(ox, oz)
		o := n + 8
		here := off == [2]int32{0, 0}
		if here && down > o {
			return fl
		}
		above := up > o
		if above || here {
			if a.fluid(o).at(o) != kindAir {
				if here {
					flooded = true
				}
				if above {
					return a.fluid(o)
				}
			}
		}
		minSurface = min(minSurface, n)
	}
	level := a.fluidBlockY(x, y, z, fl, minSurface, flooded)
	return fluidLevel{maxY: level, kind: a.fluidKind(x, y, z, fl, level)}
}

func (a *aquifer) fluidBlockY(x, y, z int32, def fluidLevel, surface int32, flooded bool) int32 {
	r := a.g.r
	var d, e float64
	if r.aquiferExclusion.eval(a.c, x, y, z) > 0 {
		// Deep dark: no water.
		d, e = -1, -1
	} else {
		f := 0.0
		if flooded {
			f = clampedMap(float64(surface+8-y), 0, 64, 1, 0)
		}
		gv := clamp(r.floodedness.eval(a.c, x, y, z), -1, 1)
		h := mapRange(f, 1, 0, -0.3, 0.8)
		k := mapRange(f, 1, 0, -0.8, 0.4)
		d, e = gv-k, gv-h
	}
	switch {
	case e > 0:
		return def.maxY
	case d > 0:
		gx, gy, gz := floorDiv(x, 16), floorDiv(y, 40), floorDiv(z, 16)
		sample := r.spread.eval(a.c, gx, gy, gz) * 10
		level := int32(math.Floor(sample/3))*3 + gy*40 + 20
		return min(surface, level)
	}
	return wayBelowMinY
}

func (a *aquifer) fluidKind(x, y, z int32, def fluidLevel, level int32) uint8 {
	if level <= -10 && level != wayBelowMinY && def.kind != kindLava {
		if math.Abs(a.g.r.lava.eval(a.c, floorDiv(x, 64), floorDiv(y, 40), floorDiv(z, 64))) > 0.3 {
			return kindLava
		}
	}
	return def.kind
}

func mapRange(v, fromLo, fromHi, toLo, toHi float64) float64 {
	return lerp((v-fromLo)/(fromHi-fromLo), toLo, toHi)
}

func clampedMap(v, fromLo, fromHi, toLo, toHi float64) float64 {
	return lerp(clamp((v-fromLo)/(fromHi-fromLo), 0, 1), toLo, toHi)
}
