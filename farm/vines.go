package farm

import (
	"math"

	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Vines, weeping and twisting vines and cave vines, from BDS 1.26.60 (the research note is
// private):
//
//   - VineBlock (vt 0xFC08BA0): its random tick 0xD499AF0 (one in 4; a random face: up grows a
//     vine above with a random share of its sides that have something to hang on, if fewer
//     than 5 vines are within 4 blocks across and 1 up or down; a side it does not hang on
//     spreads there, round a corner or onto the block, with the same count; down, and every
//     other case, grows or adds sides to the vine below), up 0xD49A930, sideways 0xD49AE20,
//     down 0xD49B7A0. Its neighbour change 0xD49A360 keeps only the sides that still hang on
//     something (0xD49A640: a full block on that side, or the vine above hanging on that side),
//     and with none left its tick 0xD4998F0 breaks it (canSurvive 0xD49BDC0).
//   - WeepingVinesBlock (vt 0xFBFADF0) and TwistingVinesBlock (vt 0xFC03FD0), the same block
//     growing down and up: onPlace 0xD428200 (age 0 takes a random age below 25), the growth
//     tick 0xD429250 (with air beyond its tip and no tick waiting, a tick one more than a
//     geometric draw of p 0.000102539, kelp's), its tick 0xD428620 (broken when it cannot stay,
//     else up to age 25 a vine one older beyond it), survival 0xD429490 (no liquid in the block,
//     and its own vine or a face that holds things where it hangs from), neighbour change
//     0xD429560 (its growth tick again when the block it grows into changes), bone meal
//     0xD429A50 (from the tip, one vine and then each next at 0.826 times the odds, while there
//     is air).
//   - CaveVinesBlock (vt 0xFB9EDA0; cave_vines, cave_vines_body_with_berries,
//     cave_vines_head_with_berries): onPlace 0xCB511B0 (age 0 takes a random age below 25 plus
//     one below 12, at most 25), its random tick 0xCB52170 (a vine with berries turns to the
//     body or head form by whether a cave vine is under it; the tip grows down at odds 0.1 up
//     to age 25, the new tip with berries at 0.11), bone meal 0xCB53340 (berries on one without:
//     the head form at the tip, else the body form), use 0xCB517C0 (picking the berries).
//   - Shears on the tip of weeping, twisting or cave vines stop it growing: age 25 (the use
//     handlers 0xD428AE0, 0xCB517C0 read the held item; their whole bodies were not read).
//
// Not ported: VineBlock's last sideways case (a vine with no side, which BDS does not keep),
// and cave vines' liquid rule (not read).

// Blocks and states of the vines.
const (
	vine              = "minecraft:vine"
	weepingVines      = "minecraft:weeping_vines"
	twistingVines     = "minecraft:twisting_vines"
	caveVines         = "minecraft:cave_vines"
	caveVinesBody     = "minecraft:cave_vines_body_with_berries"
	caveVinesHead     = "minecraft:cave_vines_head_with_berries"
	stateVineBits     = "vine_direction_bits"
	stateWeepingAge   = "weeping_vines_age"
	stateTwistingAge  = "twisting_vines_age"
	stateCaveVinesAge = "growing_plant_age"
)

// Numbers of the vines in BDS.
const (
	// vineSides is how many sides a vine may hang on; vineAllSides is all their bits.
	vineSides    = 4
	vineAllSides = 1<<vineSides - 1
	// vineOdds is the one in vineOdds random ticks a vine grows on.
	vineOdds = 4
	// vineFaces is how many faces a growing vine picks from (BDS's face order).
	vineFaces = 6
	// vineCrowd is the vines within vineReach across and vineReachUp up or down, the vine
	// itself among them, that stop a vine growing up or sideways.
	vineCrowd, vineReach, vineReachUp = 5, 4, 1
	// plantMaxAge is the age at which weeping, twisting and cave vines stop growing.
	plantMaxAge = 25
	// netherVineGrowP is the p of the geometric draw of the growth tick's delay.
	netherVineGrowP = kelpGrowP
	// netherVineBoneMealOdds is how each further vine bone meal grows is less likely.
	netherVineBoneMealOdds = 0.826
	// caveVineAgeSpread is the second draw of a placed cave vine's age.
	caveVineAgeSpread = 12
	// caveVineGrowOdds and caveVineBerryOdds are a cave vine tip's odds to grow and of the new
	// tip having berries.
	caveVineGrowOdds, caveVineBerryOdds = 0.1, 0.11
)

// Which of weeping and twisting vines a kindNetherVine is (stateInfo.facing).
const (
	netherWeeping int8 = iota
	netherTwisting
)

// The three cave vine blocks (stateInfo.facing of a kindCaveVine).
const (
	caveVinePlain int8 = iota
	caveVineBody
	caveVineHead
)

// vineKit is the states of the vines, made once by NewTicker.
type vineKit struct {
	vine   [vineAllSides + 1]uint32
	nether [2][plantMaxAge + 1]uint32
	cave   [3][plantMaxAge + 1]uint32
	shears string
}

// newVineKit fills the vine states into t.info and returns their kit.
func (t *Ticker) newVineKit() *vineKit {
	b := t.blocks
	k := &vineKit{shears: "minecraft:shears"}
	for bits := range int32(vineAllSides + 1) {
		if id, ok := b.StateID(vine, map[string]any{stateVineBits: bits}); ok {
			k.vine[bits] = id
			t.info[id] = stateInfo{kind: kindVine, age: int8(bits)}
		}
	}
	nether := [2]struct{ name, state string }{{weepingVines, stateWeepingAge}, {twistingVines, stateTwistingAge}}
	for i, n := range nether {
		for age := range int32(plantMaxAge + 1) {
			if id, ok := b.StateID(n.name, map[string]any{n.state: age}); ok {
				k.nether[i][age] = id
				t.info[id] = stateInfo{kind: kindNetherVine, age: int8(age), facing: int8(i)} //nolint:gosec // 0 or 1
			}
		}
	}
	for i, name := range [3]string{caveVines, caveVinesBody, caveVinesHead} {
		for age := range int32(plantMaxAge + 1) {
			if id, ok := b.StateID(name, map[string]any{stateCaveVinesAge: age}); ok {
				k.cave[i][age] = id
				t.info[id] = stateInfo{kind: kindCaveVine, age: int8(age), facing: int8(i)} //nolint:gosec // 0 to 2
			}
		}
	}
	return k
}

// vineSideFace is the face of the block a vine hangs on for the side bit i (BDS's bits:
// south, west, north, east).
func vineSideFace(i int) cube.Face { return direction.Cardinal(i).Face() }

// vineSideOf is the side bit of a horizontal face.
func vineSideOf(f cube.Face) int {
	c, _ := direction.Of(f)
	return int(c)
}

// vineHolds reports whether the block at pos holds a vine on its face f: a full block that
// holds things there.
func (t *Ticker) vineHolds(r *world.Region, pos cube.Pos, f cube.Face) bool {
	b, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	if b == t.blocks.Air() {
		return false
	}
	h := t.support.Load()
	return h != nil && (*h)(b, f)
}

// vineAttached is BDS's 0xD49A640: the sides of the vine at pos that still hang on
// something, a block on that side or the vine above hanging on the same side.
func (t *Ticker) vineAttached(r *world.Region, pos cube.Pos, bits int) int {
	if bits <= 0 {
		return bits
	}
	above, _ := r.Block(pos.Side(cube.FaceUp), 0)
	aboveInfo := t.state(above)
	kept := bits
	for i := range vineSides {
		bit := 1 << i
		if bits&bit == 0 {
			continue
		}
		f := vineSideFace(i)
		if t.vineHolds(r, pos.Side(f), f.Opposite()) {
			continue
		}
		if aboveInfo.kind == kindVine && int(aboveInfo.age)&bit != 0 {
			continue
		}
		kept &^= bit
	}
	return kept
}

// vineNeighbourChanged is VineBlock's neighbour change: it keeps only the sides that still
// hang on something, and with none left its tick breaks it.
func (t *Ticker) vineNeighbourChanged(r *world.Region, pos cube.Pos, block uint32, s stateInfo) {
	kept := t.vineAttached(r, pos, int(s.age))
	switch {
	case kept == 0:
		r.ScheduleTick(pos, block, surviveDelay, 0)
	case kept != int(s.age):
		r.SetBlock(pos, 0, t.vineKit.vine[kept])
	}
}

// vineSurvives is VineBlock's canSurvive: a side left that hangs on something.
func (t *Ticker) vineSurvives(r *world.Region, pos cube.Pos, s stateInfo) bool {
	return t.vineAttached(r, pos, int(s.age)) != 0
}

// vineCrowded reports whether vineCrowd vines are around pos, itself among them.
func (t *Ticker) vineCrowded(r *world.Region, pos cube.Pos) bool {
	left := vineCrowd
	for x := pos[0] - vineReach; x <= pos[0]+vineReach; x++ {
		for z := pos[2] - vineReach; z <= pos[2]+vineReach; z++ {
			for y := pos[1] - vineReachUp; y <= pos[1]+vineReachUp; y++ {
				b, ok := r.Block(cube.Pos{x, y, z}, 0)
				if !ok || t.state(b).kind != kindVine {
					continue
				}
				if left < 2 {
					return true
				}
				left--
			}
		}
	}
	return false
}

// vineTick is VineBlock's random tick.
func (t *Ticker) vineTick(r *world.Region, pos cube.Pos, s stateInfo) {
	rng := t.rng(r)
	if rng.IntN(vineOdds) != 0 {
		return
	}
	bits := int(s.age)
	rng2 := r.Dimension().Range()
	face := cube.Face(rng.IntN(vineFaces))
	switch {
	case face == cube.FaceUp:
		above := pos.Side(cube.FaceUp)
		if pos[1] < rng2[1] && t.isAir(r, above) {
			if t.vineCrowded(r, pos) {
				return
			}
			t.vineGrowUp(r, above, rng.IntN(vineAllSides+1)&bits)
			return
		}
	case face != cube.FaceDown:
		bit := 1 << vineSideOf(face)
		if bits&bit == 0 {
			if t.vineCrowded(r, pos) {
				return
			}
			t.vineSpread(r, pos, bits, face)
			return
		}
	}
	t.vineGrowDown(r, pos, bits)
}

// vineGrowUp puts a vine at above with the sides of bits that something holds there.
func (t *Ticker) vineGrowUp(r *world.Region, above cube.Pos, bits int) {
	for i := range vineSides {
		f := vineSideFace(i)
		if bits&(1<<i) != 0 && !t.vineHolds(r, above.Side(f), f.Opposite()) {
			bits &^= 1 << i
		}
	}
	if bits != 0 {
		t.set(r, above, t.vineKit.vine[bits])
	}
}

// vineSpread is a vine growing towards face, a side it does not hang on: into the air
// there, hanging on the block beside it or round the corner, or onto the block there.
func (t *Ticker) vineSpread(r *world.Region, pos cube.Pos, bits int, face cube.Face) {
	side := vineSideOf(face)
	target := pos.Side(face)
	if !t.isAir(r, target) {
		if t.vineHolds(r, target, face.Opposite()) {
			t.set(r, pos, t.vineKit.vine[bits|1<<side])
		}
		return
	}
	cw, ccw := (side+1)%vineSides, (side+vineSides-1)%vineSides
	for _, turn := range [2]int{cw, ccw} {
		f := vineSideFace(turn)
		if bits&(1<<turn) != 0 && t.vineHolds(r, target.Side(f), f.Opposite()) {
			t.set(r, target, t.vineKit.vine[1<<turn])
			return
		}
	}
	back := 1 << vineSideOf(face.Opposite())
	for _, turn := range [2]int{cw, ccw} {
		f := vineSideFace(turn)
		corner := target.Side(f)
		if bits&(1<<turn) != 0 && t.isAir(r, corner) && t.vineHolds(r, pos.Side(f), f.Opposite()) {
			t.set(r, corner, t.vineKit.vine[back])
			return
		}
	}
}

// vineGrowDown grows a vine with a random share of bits below, or adds them to the vine
// there.
func (t *Ticker) vineGrowDown(r *world.Region, pos cube.Pos, bits int) {
	below := pos.Side(cube.FaceDown)
	if pos[1] <= r.Dimension().Range()[0]+1 {
		return
	}
	b, ok := r.Block(below, 0)
	if !ok {
		return
	}
	if b == t.blocks.Air() {
		if grown := t.rng(r).IntN(vineAllSides+1) & bits; grown != 0 {
			t.set(r, below, t.vineKit.vine[grown])
		}
		return
	}
	if s := t.state(b); s.kind == kindVine {
		if merged := int(s.age) | t.rng(r).IntN(vineAllSides+1)&bits; merged != int(s.age) {
			t.set(r, below, t.vineKit.vine[merged])
		}
	}
}

// netherVineDir is the way weeping vines (down) or twisting vines (up) grow.
func netherVineDir(s stateInfo) cube.Face {
	if s.facing == netherTwisting {
		return cube.FaceUp
	}
	return cube.FaceDown
}

// netherVinePlaced is their onPlace: age 0 takes a random age below 25, then the growth tick.
func (t *Ticker) netherVinePlaced(r *world.Region, pos cube.Pos, s stateInfo) {
	block := t.vineKit.nether[s.facing][s.age]
	if s.age == 0 {
		s.age = int8(t.rng(r).IntN(plantMaxAge)) //nolint:gosec // below 25
		block = t.vineKit.nether[s.facing][s.age]
		t.quietSet(r, pos, block)
	}
	t.netherVineGrowthTick(r, pos, block, s)
}

// netherVineGrowthTick is BDS's 0xD429250: with air where it grows and no tick waiting, a
// tick one more than a geometric draw.
func (t *Ticker) netherVineGrowthTick(r *world.Region, pos cube.Pos, block uint32, s stateInfo) {
	if s.age >= plantMaxAge || !t.isAir(r, pos.Side(netherVineDir(s))) || r.HasScheduledTick(pos, block) {
		return
	}
	u := t.rng(r).Float64()
	delay := int32(math.Floor(math.Log(1-u)/math.Log(1-netherVineGrowP))) + 1
	r.ScheduleTick(pos, block, delay, 0)
}

// netherVineTick is their tick: broken when they cannot stay, else a vine one older where
// they grow.
func (t *Ticker) netherVineTick(r *world.Region, pos cube.Pos, block uint32, s stateInfo) {
	if !t.netherVineSurvives(r, pos, s) {
		if t.fall != nil {
			t.fall(r, pos, block)
		}
		return
	}
	next := pos.Side(netherVineDir(s))
	if s.age >= plantMaxAge || !t.isAir(r, next) {
		return
	}
	t.set(r, next, t.vineKit.nether[s.facing][s.age+1])
}

// netherVineSurvives is their survival: no liquid in the block, and their own vine or a face
// that holds things where they hang from.
func (t *Ticker) netherVineSurvives(r *world.Region, pos cube.Pos, s stateInfo) bool {
	if t.liquidAt(r, pos) {
		return false
	}
	from := netherVineDir(s).Opposite()
	b, ok := r.Block(pos.Side(from), 0)
	if !ok {
		return true
	}
	if o := t.state(b); o.kind == kindNetherVine && o.facing == s.facing {
		return true
	}
	h := t.support.Load()
	return h != nil && (*h)(b, from.Opposite())
}

// netherVineNeighbourChanged is their neighbour change: a vine that cannot stay goes on its
// next tick; one whose growing side changed takes its growth tick again.
func (t *Ticker) netherVineNeighbourChanged(r *world.Region, pos, from cube.Pos, block uint32, s stateInfo) {
	if !t.netherVineSurvives(r, pos, s) {
		r.ScheduleTick(pos, block, surviveDelay, 0)
		return
	}
	if from == pos.Side(netherVineDir(s)) {
		t.netherVineGrowthTick(r, pos, block, stateInfo{kind: s.kind, facing: s.facing})
	}
}

// fertilizeNetherVine is bone meal on weeping or twisting vines: from the tip, one vine and
// each next at 0.826 times the odds of the last, while there is air.
func (t *Ticker) fertilizeNetherVine(r *world.Region, pos cube.Pos, s stateInfo) bool {
	dir := netherVineDir(s)
	tip, ts := pos, s
	for {
		b, ok := r.Block(tip.Side(dir), 0)
		if !ok || t.state(b).kind != kindNetherVine || t.state(b).facing != s.facing {
			break
		}
		tip, ts = tip.Side(dir), t.state(b)
	}
	next := tip.Side(dir)
	if !t.isAir(r, next) {
		return true
	}
	age, odds, rng := min(int(ts.age), plantMaxAge-1), float32(1), t.rng(r)
	for t.isAir(r, next) {
		if odds <= rng.Float32() {
			break
		}
		age++
		t.set(r, next, t.vineKit.nether[s.facing][age])
		next = next.Side(dir)
		age = min(age, plantMaxAge-1)
		odds *= netherVineBoneMealOdds
	}
	return true
}

// caveVinePlaced is their onPlace: age 0 takes a random age below 25 plus one below 12, at
// most 25.
func (t *Ticker) caveVinePlaced(r *world.Region, pos cube.Pos, s stateInfo) {
	if s.age != 0 {
		return
	}
	rng := t.rng(r)
	age := min(rng.IntN(plantMaxAge)+rng.IntN(caveVineAgeSpread), plantMaxAge)
	t.quietSet(r, pos, t.vineKit.cave[s.facing][age])
}

// caveVineBelow reports whether a cave vine is under pos.
func (t *Ticker) caveVineBelow(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos.Side(cube.FaceDown), 0)
	return ok && t.state(b).kind == kindCaveVine
}

// caveVineTick is their random tick.
func (t *Ticker) caveVineTick(r *world.Region, pos cube.Pos, s stateInfo) {
	k := t.vineKit
	below := t.caveVineBelow(r, pos)
	switch {
	case s.facing == caveVineHead && below:
		r.SetBlock(pos, 0, k.cave[caveVineBody][s.age])
	case s.facing == caveVineBody && !below:
		r.SetBlock(pos, 0, k.cave[caveVineHead][s.age])
	}
	if below {
		return
	}
	rng := t.rng(r)
	grow, berries := rng.Float32(), rng.Float32()
	next := pos.Side(cube.FaceDown)
	if grow >= caveVineGrowOdds || s.age >= plantMaxAge || !t.isAir(r, next) {
		return
	}
	form := caveVinePlain
	if berries < caveVineBerryOdds {
		form = caveVineHead
	}
	t.set(r, next, k.cave[form][s.age+1])
}

// caveVineSurvives is their survival: a cave vine or a face that holds things above.
func (t *Ticker) caveVineSurvives(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos.Side(cube.FaceUp), 0)
	if !ok || t.state(b).kind == kindCaveVine {
		return true
	}
	h := t.support.Load()
	return h != nil && (*h)(b, cube.FaceDown)
}

// fertilizeCaveVine is bone meal on a cave vine without berries: the head form at the tip,
// else the body form.
func (t *Ticker) fertilizeCaveVine(r *world.Region, pos cube.Pos, s stateInfo) bool {
	if s.facing != caveVinePlain {
		return false
	}
	form := caveVineHead
	if t.caveVineBelow(r, pos) {
		form = caveVineBody
	}
	t.set(r, pos, t.vineKit.cave[form][s.age])
	return true
}

// PickGlowBerries is using a cave vine with berries at pos: drop is called with the vine as it
// was, whose drops are the berries, and it goes back to plain cave vines. False if it has
// none. Runs: owner of the region.
func (t *Ticker) PickGlowBerries(r *world.Region, pos cube.Pos, drop func(block uint32)) bool {
	b, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	s := t.state(b)
	if s.kind != kindCaveVine || s.facing == caveVinePlain {
		return false
	}
	drop(b)
	t.set(r, pos, t.vineKit.cave[caveVinePlain][s.age])
	return true
}

// ShearPlantTip is shears used on the tip of weeping, twisting or cave vines at pos: it
// stops growing at age 25. False for any other block, or one already at 25. Runs: owner of
// the region.
func (t *Ticker) ShearPlantTip(r *world.Region, pos cube.Pos, item string) bool {
	if item != t.vineKit.shears {
		return false
	}
	b, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	s := t.state(b)
	if s.age >= plantMaxAge {
		return false
	}
	switch s.kind {
	case kindNetherVine:
		if !t.isAir(r, pos.Side(netherVineDir(s))) {
			return false
		}
		t.set(r, pos, t.vineKit.nether[s.facing][plantMaxAge])
	case kindCaveVine:
		if t.caveVineBelow(r, pos) {
			return false
		}
		t.set(r, pos, t.vineKit.cave[s.facing][plantMaxAge])
	default:
		return false
	}
	return true
}
