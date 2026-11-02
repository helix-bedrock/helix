package liquid

import (
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// horizontal are the four sides, in the order BDS spreads to them.
func horizontal(i int) cube.Pos {
	switch i {
	case 0:
		return cube.Pos{-1, 0, 0}
	case 1:
		return cube.Pos{1, 0, 0}
	case 2:
		return cube.Pos{0, 0, -1}
	}
	return cube.Pos{0, 0, 1}
}

// opposite is the side opposite side i of horizontal.
func opposite(i int) int { return i ^ 1 }

// drop is how much a liquid's depth grows per block it spreads: 1 for
// water, 2 for lava, 1 for lava in an ultra-warm dimension.
func (l *Liquids) drop(k Kind) int {
	if k == Lava && !l.UltraWarm {
		return 2
	}
	return 1
}

// tick is BDS's liquid tick at pos: the liquid's depth is worked out again
// from its neighbours (a source never changes), then it falls if it can,
// else it spreads sideways towards the nearest way down.
func (l *Liquids) tick(r *world.Region, pos cube.Pos, k Kind) {
	lq := &l.liq[k]
	inLayer0 := l.layerOf(r, pos, lq.typ) == 0
	depth := l.depthOf(r, pos, k)
	drop := l.drop(k)
	delay := l.TickDelay(k)
	unchanged := true
	if depth > 0 {
		newDepth := l.settle(r, pos, k, drop)
		if k == Lava && depth <= 7 && newDepth <= 7 && newDepth > depth && l.local(r).rng.IntN(4) != 0 {
			delay *= 4
		}
		if newDepth != depth {
			unchanged = false
			l.change(r, pos, k, newDepth, inLayer0, delay)
			depth = newDepth
		}
	}
	if unchanged {
		l.lavaUnderWater(r, pos)
	}
	l.flow(r, pos, k, depth, drop)
}

// settle is the depth the liquid at pos should have: one more than its
// shallowest neighbour, falling under liquid above, and a source where
// water has two sources beside it and something solid or a source under
// it. -1 when it should dry up.
func (l *Liquids) settle(r *world.Region, pos cube.Pos, k Kind, drop int) int {
	sources, shallowest := 0, -100
	for i := range 4 {
		d := horizontal(i)
		shallowest = l.shallower(r, pos.Add(d), k, shallowest, &sources)
	}
	depth := shallowest + drop
	if depth >= falling || shallowest < 0 {
		depth = -1
	}
	above := pos.Add(cube.Pos{0, 1, 0})
	aboveDepth := l.depthOf(r, above, k)
	if aboveDepth >= 0 {
		depth = aboveDepth
		if aboveDepth < falling {
			depth += falling
		}
	}
	if sources == 1 && aboveDepth == 0 {
		sources = 2
	}
	if sources < 2 || k != Water {
		return depth
	}
	below := pos.Add(cube.Pos{0, -1, 0})
	if b, ok := r.Block(below, 0); ok && l.blocks.Properties(b).SolidMaterial {
		return 0
	}
	if l.depthOf(r, below, k) == 0 {
		return 0
	}
	return depth
}

// shallower is BDS's getHighest: the smaller of shallowest and the depth
// of liquid k at pos (falling liquid counts as 0), counting sources.
func (l *Liquids) shallower(r *world.Region, pos cube.Pos, k Kind, shallowest int, sources *int) int {
	d := l.depthOf(r, pos, k)
	if d < 0 {
		return shallowest
	}
	if d == 0 {
		*sources++
	}
	if d >= falling {
		d = 0
	}
	if shallowest < 0 || d < shallowest {
		return d
	}
	return shallowest
}

// change sets the liquid at pos to depth, or removes it below 0, and ticks
// it again after delay.
func (l *Liquids) change(r *world.Region, pos cube.Pos, k Kind, depth int, inLayer0 bool, delay int32) {
	air := l.blocks.Air()
	if depth < 0 {
		if inLayer0 {
			l.set(r, pos, 0, air)
			return
		}
		l.set(r, pos, 1, air)
		return
	}
	b, ok := l.Block(k, depth)
	if !ok {
		return
	}
	layer := uint8(1)
	if inLayer0 {
		layer = 0
	}
	if !l.set(r, pos, layer, b) {
		return
	}
	r.UpdateNeighbours(pos)
	r.ScheduleTick(pos, b, delay, 0)
}

// lavaUnderWater turns a lava source with water right above it into
// obsidian.
func (l *Liquids) lavaUnderWater(r *world.Region, pos cube.Pos) {
	b := l.liquidAt(r, pos)
	if i, ok := l.at(b); !ok || i.kind != Lava || i.flowing || i.depth != 0 {
		return
	}
	above, _ := r.Block(pos.Add(cube.Pos{0, 1, 0}), 0)
	if l.material(above) == registry.MaterialWater && l.obsidian != noBlock {
		r.SetBlock(pos, 0, l.obsidian)
	}
}

// flow moves the liquid at pos of depth on: down if it can, where lava
// meeting water makes stone, else sideways, along the shortest ways down,
// when it is a source or rests on something it cannot fall through.
func (l *Liquids) flow(r *world.Region, pos cube.Pos, k Kind, depth, drop int) {
	below := pos.Add(cube.Pos{0, -1, 0})
	if below[1] >= r.Dimension().Range()[0] {
		if _, ok := r.Block(below, 0); ok {
			bm := l.material(l.liquidAt(r, below))
			if bm != registry.MaterialLava && bm != l.liq[k].material && !l.blocking(r, below) {
				if k == Lava {
					b0, _ := r.Block(below, 0)
					if bm == registry.MaterialWater || l.material(b0) == registry.MaterialBubbleColumn {
						if l.stone != noBlock {
							l.set(r, below, 0, l.stone)
						}
						l.fizz(r, below)
						return
					}
				}
				d := depth
				if d < falling {
					d += falling
				}
				l.spreadTo(r, below, k, d)
				return
			}
		}
	}
	if depth < 0 || depth != 0 && !l.blocking(r, below) {
		return
	}
	var costs [4]int
	for i := range costs {
		costs[i] = noCost
		d := horizontal(i)
		n := pos.Add(d)
		if l.blocking(r, n) || l.source(r, n, k) {
			continue
		}
		if !l.blocking(r, n.Add(cube.Pos{0, -1, 0})) {
			costs[i] = 0
			continue
		}
		reach := 1
		if here := l.depthOf(r, pos, k); here >= 5 && here <= 7 {
			reach = here - 3
		}
		costs[i] = l.slope(r, n, k, reach, i)
	}
	least := min(costs[0], costs[1], costs[2], costs[3])
	next := 1
	if depth < falling {
		next = depth + drop
	}
	if next >= falling {
		return
	}
	for i, c := range costs {
		if c == least {
			d := horizontal(i)
			l.spreadTo(r, pos.Add(d), k, next)
		}
	}
}

// source reports whether a source of liquid k is at pos.
func (l *Liquids) source(r *world.Region, pos cube.Pos, k Kind) bool {
	return l.depthOf(r, pos, k) == 0
}

// slope is BDS's getSlopeDistance: how many blocks from pos the nearest
// way down is, looking up to the liquid's reach and never back the way it
// came; noCost if there is none.
func (l *Liquids) slope(r *world.Region, pos cube.Pos, k Kind, dist, from int) int {
	reach := waterSlopeReach
	if k == Lava {
		reach = lavaSlopeReach
	}
	best := noCost
	for i := range 4 {
		if i == opposite(from) {
			continue
		}
		d := horizontal(i)
		n := pos.Add(d)
		if l.blocking(r, n) || l.source(r, n, k) {
			continue
		}
		if !l.blocking(r, n.Add(cube.Pos{0, -1, 0})) {
			return dist
		}
		if dist < reach {
			best = min(best, l.slope(r, n, k, dist+1, i))
		}
	}
	return best
}

// blocking is BDS's isWaterBlocking for a liquid moving into pos: true
// when the block there stops liquids. Lava there never does. Blocks whose
// own shape decides it (stairs, slabs) are not ported and let liquid out
// on every side.
func (l *Liquids) blocking(r *world.Region, pos cube.Pos) bool {
	if l.material(l.liquidAt(r, pos)) == registry.MaterialLava {
		return false
	}
	b, ok := r.Block(pos, 0)
	return !ok || l.blocks.Properties(b).LiquidReaction == registry.LiquidBlocking
}

// spreadTo is BDS's trySpreadTo: liquid k of depth flows into pos unless
// liquid of its kind or lava is there or the block stops it. Lava makes a
// block it flows into fizz; other liquids replace the block or, if it
// takes liquid in its second layer, go there. The liquid is ticked again
// after its delay.
func (l *Liquids) spreadTo(r *world.Region, pos cube.Pos, k Kind, depth int) {
	if pos[1] < r.Dimension().Range()[0] {
		return
	}
	if _, ok := r.Block(pos, 0); !ok {
		return
	}
	m := l.material(l.liquidAt(r, pos))
	if m == registry.MaterialLava || m == l.liq[k].material || l.blocking(r, pos) {
		return
	}
	layer := uint8(0)
	if b0, _ := r.Block(pos, 0); b0 != l.blocks.Air() {
		switch {
		case k == Lava:
			l.fizz(r, pos)
		case l.blocks.Properties(b0).LiquidReaction == registry.LiquidNoReaction:
			layer = 1
		}
	}
	b, ok := l.Block(k, depth)
	if !ok || !l.set(r, pos, layer, b) {
		return
	}
	r.ScheduleTick(pos, l.liq[k].states[0], l.TickDelay(k), 0)
}

// solidifyAround checks lava at pos against its neighbours to the south,
// north, east, west and above, in BDS's order.
func (l *Liquids) solidifyAround(r *world.Region, pos cube.Pos, k Kind) {
	if k != Lava {
		return
	}
	for _, d := range [5]cube.Pos{{0, 0, 1}, {0, 0, -1}, {1, 0, 0}, {-1, 0, 0}, {0, 1, 0}} {
		l.solidify(r, pos, pos.Add(d))
	}
}

// solidify is BDS's solidify: checks that come while another runs wait
// their turn, so lava turning to stone next to more lava is handled in
// order rather than nested.
func (l *Liquids) solidify(r *world.Region, pos, from cube.Pos) {
	loc := l.local(r)
	if loc.solidifying {
		loc.queue = append(loc.queue, solidifyCheck{pos, from})
		return
	}
	loc.solidifying = true
	l.solidifyOnce(r, pos, from)
	for len(loc.queue) > 0 {
		c := loc.queue[0]
		loc.queue = loc.queue[1:]
		l.solidifyOnce(r, c.pos, c.from)
	}
	loc.queue = loc.queue[:0]
	loc.solidifying = false
}

// solidifyOnce turns lava on layer 0 at pos into obsidian (a source) or
// cobblestone when water is beside or above it at from, and into basalt
// when from is blue ice and soul soil is under it.
func (l *Liquids) solidifyOnce(r *world.Region, pos, from cube.Pos) {
	b, ok := r.Block(pos, 0)
	i, isLiq := l.at(b)
	if !ok || !isLiq || i.kind != Lava || from[1] < pos[1] {
		return
	}
	to := noBlock
	if l.material(l.liquidAt(r, from)) == registry.MaterialWater {
		to = l.obsidian
		if i.depth != 0 {
			to = l.cobble
		}
	} else if f, _ := r.Block(from, 0); f == l.blueIce && l.blueIce != noBlock {
		if under, _ := r.Block(pos.Add(cube.Pos{0, -1, 0}), 0); under == l.soulSoil {
			to = l.basalt
		}
	}
	if to == noBlock {
		return
	}
	if l.set(r, pos, 0, to) {
		l.fizz(r, pos)
	}
}
