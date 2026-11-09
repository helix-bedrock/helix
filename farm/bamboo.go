package farm

import (
	"math"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Bamboo and kelp, from BDS 1.26.60 (the research note is private):
//
//   - BambooSaplingBlock: random tick 0xCCC3DC0 (age_bit 0 becomes 1; at 1, one in 3, with
//     air above lit to 9, it grows), growth 0xCCC40F0 (it becomes a thin bare bamboo with a
//     thin small-leaved bamboo above), bone meal 0xCCC43E0 (grows with air above), mayPlace
//     0xCCC4510.
//   - BambooStalkBlock: random tick 0xCCC59E0 (as the sapling's, then grows on top while the
//     column is shorter than 12 + a hash of x and z mod 5), the new top's state 0xCCC5EC0,
//     onPlace 0xCCC4AE0 (the stalk under a new top gets small leaves, the one under that
//     none, thick once the column has leaves two down), bone meal 0xCCC6A00 (one block on top
//     while shorter than that height), mayPlace 0xCCC6670.
//   - KelpBlock: onPlace 0xCAD7BE0 (on anything but kelp, a random age below 25 and a growth
//     tick), tick 0xCAD8760 (with a water source above and an age below 25, kelp one older on
//     top, which gets its own growth tick), the growth tick 0xCAD8BD0 (its delay one more than
//     a geometric draw with BDS's p), canGrow 0xCAD8AA0, mayPlace 0xCAD8F90, bone meal
//     0xCAD9340 (the top kelp's tick brought to the next tick).
const (
	bamboo         = "minecraft:bamboo"
	bambooSapling  = "minecraft:bamboo_sapling"
	kelp           = "minecraft:kelp"
	magma          = "minecraft:magma"
	stateAgeBit    = "age_bit"
	stateLeaves    = "bamboo_leaf_size"
	stateThickness = "bamboo_stalk_thickness"
	stateKelpAge   = "kelp_age"
	stateDepth     = "liquid_depth"
	tagGravel      = "gravel"
)

// Numbers of bamboo and kelp in BDS.
const (
	bambooOdds = 3
	// bambooBaseHeight and bambooHeightSpread: a column grows to 12 plus its hash mod 5.
	bambooBaseHeight, bambooHeightSpread = 12, 5
	// The hash of a column's x and z that sets its height.
	bambooHashX, bambooHashZ, bambooHashMul, bambooHashAdd, bambooHashShift = 3129871, 116129781, 42317861, 11, 24
	leafNone, leafSmall, leafLarge                                          = 0, 1, 2
	thin, thick                                                             = 0, 1
	kelpMaxAge                                                              = 25
	// kelpGrowP is the p of the geometric draw of kelp's growth tick delay (BDS's double
	// 0x3F1AE147B0000000).
	kelpGrowP = 0.00010253906293655746
)

// The names of bamboo_leaf_size and bamboo_stalk_thickness, by their number in BDS.
var (
	leafNames  = [...]string{"no_leaves", "small_leaves", "large_leaves"}
	thickNames = [...]string{"thin", "thick"}
)

// bambooKit is the states of bamboo and kelp, made once by NewTicker.
type bambooKit struct {
	// stalk is bamboo by thickness, leaf size and age_bit.
	stalk   [2][3][2]uint32
	sapling [2]uint32
	kelp    [kelpMaxAge + 1]uint32
	grass   uint32
	magma   uint32
	soul    uint32
	gravel  map[uint32]bool
}

func (t *Ticker) newBambooKit() *bambooKit {
	b := t.blocks
	k := &bambooKit{gravel: map[uint32]bool{}}
	k.grass, k.magma, k.soul = blockOrNone(b, grassBlock), blockOrNone(b, magma), blockOrNone(b, soulSand)
	for th := range 2 {
		for lf := range 3 {
			for ab := range uint8(2) {
				id, ok := b.StateID(bamboo, map[string]any{stateThickness: thickNames[th], stateLeaves: leafNames[lf], stateAgeBit: ab})
				if !ok {
					continue
				}
				k.stalk[th][lf][ab] = id
				t.info[id] = stateInfo{kind: kindBamboo, age: int8(ab), facing: int8(lf), crop: uint8(th)} //nolint:gosec // small
			}
		}
	}
	for ab := range uint8(2) {
		if id, ok := b.StateID(bambooSapling, map[string]any{stateAgeBit: ab}); ok {
			k.sapling[ab] = id
			t.info[id] = stateInfo{kind: kindBambooSapling, age: int8(ab)} //nolint:gosec // 0 or 1
		}
	}
	for age := range int32(kelpMaxAge + 1) {
		if id, ok := b.StateID(kelp, map[string]any{stateKelpAge: age}); ok {
			k.kelp[age] = id
			t.info[id] = stateInfo{kind: kindKelp, age: int8(age)}
		}
	}
	for id := range uint32(b.Count()) { //nolint:gosec // block counts fit
		for _, tag := range b.Tags(id) {
			if tag == tagGravel {
				k.gravel[id] = true
			}
		}
	}
	return k
}

// bambooTick is the random tick of a bamboo sapling or stalk.
func (t *Ticker) bambooTick(r *world.Region, pos cube.Pos, s stateInfo) {
	k := t.bambooKit
	if s.age == 0 {
		if s.kind == kindBambooSapling {
			t.quietSet(r, pos, k.sapling[1])
		} else {
			t.quietSet(r, pos, k.stalk[s.crop][s.facing][1])
		}
		return
	}
	above := pos.Side(cube.FaceUp)
	if t.rng(r).IntN(bambooOdds) != 0 || !t.isAir(r, above) || r.Brightness(above, t.skyDarken()) < DefaultCrop().GrowLight {
		return
	}
	if s.kind == kindBambooSapling {
		t.growBambooSapling(r, pos)
		return
	}
	if t.columnBelow(r, pos, kindBamboo)+1 < bambooHeight(pos) {
		t.set(r, above, t.newBambooTop(r, above))
	}
}

// bambooHeight is how tall BDS lets the bamboo column at pos grow.
func bambooHeight(pos cube.Pos) int {
	h := uint32(int32(pos[0])*bambooHashX) ^ uint32(int32(pos[2])*bambooHashZ) //nolint:gosec // BDS's 32-bit hash
	v := h * (h*bambooHashMul + bambooHashAdd) >> bambooHashShift
	return int(v%bambooHeightSpread) + bambooBaseHeight
}

// growBambooSapling turns the sapling at pos into bamboo with a small-leaved stalk above.
func (t *Ticker) growBambooSapling(r *world.Region, pos cube.Pos) {
	k := t.bambooKit
	t.set(r, pos, k.stalk[thin][leafNone][0])
	t.set(r, pos.Side(cube.FaceUp), k.stalk[thin][leafSmall][0])
}

// newBambooTop is the state BDS gives a new bamboo at pos: on bamboo, thick with large leaves
// when the stalk under is thick or the one under that has leaves, else thin with small
// leaves; on a bamboo sapling thin with small leaves; else bare.
func (t *Ticker) newBambooTop(r *world.Region, pos cube.Pos) uint32 {
	k := t.bambooKit
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok {
		return k.stalk[thin][leafNone][0]
	}
	switch bs := t.state(below); bs.kind {
	case kindBamboo:
		twoDown, ok := r.Block(cube.Pos{pos[0], pos[1] - 2, pos[2]}, 0)
		bare := !ok || t.state(twoDown).kind != kindBamboo || t.state(twoDown).facing == leafNone
		if bs.crop == thick || !bare {
			return k.stalk[thick][leafLarge][0]
		}
		return k.stalk[thin][leafSmall][0]
	case kindBambooSapling:
		return k.stalk[thin][leafSmall][0]
	}
	return k.stalk[thin][leafNone][0]
}

// bambooPlaced is the stalk's onPlace for a new top at pos: a sapling under it becomes bare
// bamboo; a stalk under it gets small leaves, thick unless it is thin with no bamboo under
// it, and a bamboo two down loses its leaves and turns thick.
func (t *Ticker) bambooPlaced(r *world.Region, pos cube.Pos) {
	k := t.bambooKit
	if above, ok := r.Block(pos.Side(cube.FaceUp), 0); ok && t.state(above).kind == kindBamboo {
		return
	}
	down := pos.Side(cube.FaceDown)
	below, ok := r.Block(down, 0)
	if !ok {
		return
	}
	bs := t.state(below)
	if bs.kind == kindBambooSapling {
		t.set(r, down, k.stalk[thin][leafNone][0])
		return
	}
	if bs.kind != kindBamboo {
		return
	}
	twoDown := down.Side(cube.FaceDown)
	b2, ok := r.Block(twoDown, 0)
	stalkTwoDown := ok && t.state(b2).kind == kindBamboo
	if bs.crop == thin && !stalkTwoDown {
		t.set(r, down, k.stalk[thin][leafSmall][0])
		return
	}
	t.set(r, down, k.stalk[thick][leafSmall][0])
	if stalkTwoDown {
		t.set(r, twoDown, k.stalk[thick][leafNone][0])
	}
}

// bambooSurvives is the stalk's and the sapling's mayPlace: no water or lava in the block,
// and on bamboo (a stalk), a bamboo sapling (a stalk), a grass block, or a dirt (not
// farmland), sand or gravel block.
func (t *Ticker) bambooSurvives(r *world.Region, pos cube.Pos, s stateInfo) bool {
	if b, ok := r.Block(pos, 1); ok && b != t.blocks.Air() {
		if m := t.blocks.Properties(b).Material; m == materialWater || m == materialLava {
			return false
		}
	}
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok {
		return true
	}
	k := t.bambooKit
	if s.kind == kindBamboo {
		if bk := t.state(below).kind; bk == kindBamboo || bk == kindBambooSapling || below == k.grass {
			return true
		}
	}
	return t.dirtSoil(below) || t.plantKit.sand[below] || k.gravel[below]
}

// dirtSoil is a block of BDS's dirt tag other than farmland, the one block its plants leave
// out of the tag.
func (t *Ticker) dirtSoil(b uint32) bool {
	return t.state(b).fruitSoil && t.state(b).kind != kindFarmland
}

// fertilizeBamboo is bone meal on a bamboo sapling or stalk. True if it grew.
func (t *Ticker) fertilizeBamboo(r *world.Region, pos cube.Pos, s stateInfo) bool {
	if s.kind == kindBambooSapling {
		if !t.isAir(r, pos.Side(cube.FaceUp)) {
			return false
		}
		t.growBambooSapling(r, pos)
		return true
	}
	bottom, top := pos[1], pos[1]
	for {
		b, ok := r.Block(cube.Pos{pos[0], bottom - 1, pos[2]}, 0)
		if !ok || t.state(b).kind != kindBamboo {
			break
		}
		bottom--
	}
	for {
		b, ok := r.Block(cube.Pos{pos[0], top + 1, pos[2]}, 0)
		if !ok || t.state(b).kind != kindBamboo {
			break
		}
		top++
	}
	if top-bottom+1 >= bambooHeight(pos) {
		return false
	}
	at := cube.Pos{pos[0], top + 1, pos[2]}
	if !t.isAir(r, at) {
		return false
	}
	t.set(r, at, t.newBambooTop(r, at))
	return true
}

// kelpCanGrow is kelp's canGrow at pos: younger than 25 with a water source above.
func (t *Ticker) kelpCanGrow(r *world.Region, pos cube.Pos, s stateInfo) bool {
	if s.age >= kelpMaxAge {
		return false
	}
	above, ok := r.Block(pos.Side(cube.FaceUp), 0)
	if !ok || t.blocks.Properties(above).Material != materialWater {
		return false
	}
	d, _ := t.blocks.States(above)[stateDepth].(int32)
	return d == 0
}

// kelpGrowthTick gives the kelp at pos its growth tick, as BDS's: one more tick than a
// geometric draw.
func (t *Ticker) kelpGrowthTick(r *world.Region, pos cube.Pos, block uint32) {
	u := t.rng(r).Float64()
	delay := int32(math.Floor(math.Log(1-u)/math.Log(1-kelpGrowP))) + 1
	r.ScheduleTick(pos, block, delay, 0)
}

// kelpPlaced is kelp's onPlace: on anything but kelp it takes a random age below 25 and,
// where it can grow, its growth tick.
func (t *Ticker) kelpPlaced(r *world.Region, pos cube.Pos) {
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok || t.state(below).kind == kindKelp {
		return
	}
	k := t.bambooKit
	age := t.rng(r).IntN(kelpMaxAge)
	s := stateInfo{kind: kindKelp, age: int8(age)} //nolint:gosec // below 25
	if !t.kelpCanGrow(r, pos, s) {
		return
	}
	t.quietSet(r, pos, k.kelp[age])
	t.kelpGrowthTick(r, pos, k.kelp[age])
}

// kelpTick is kelp's tick: gone when it cannot stay; where it can grow, kelp one older on top
// with its own growth tick.
func (t *Ticker) kelpTick(r *world.Region, pos cube.Pos, s stateInfo) {
	if !t.kelpSurvives(r, pos) {
		if b, ok := r.Block(pos, 0); ok && t.fall != nil {
			t.fall(r, pos, b)
		}
		return
	}
	if !t.kelpCanGrow(r, pos, s) {
		return
	}
	above, grown := pos.Side(cube.FaceUp), t.bambooKit.kelp[s.age+1]
	t.set(r, above, grown)
	if t.kelpCanGrow(r, above, stateInfo{kind: kindKelp, age: s.age + 1}) {
		t.kelpGrowthTick(r, above, grown)
	}
}

// kelpSurvives is kelp's mayPlace: a water source in the block's water layer, and on kelp or
// on a solid block that is not magma or soul sand.
func (t *Ticker) kelpSurvives(r *world.Region, pos cube.Pos) bool {
	// Before kelp is placed the water is the block itself; after, it is in the second layer.
	w, ok := r.Block(pos, 1)
	if ok && w == t.blocks.Air() {
		w, ok = r.Block(pos, 0)
	}
	if !ok || t.blocks.Properties(w).Material != materialWater {
		return false
	}
	if d, _ := t.blocks.States(w)[stateDepth].(int32); d != 0 {
		return false
	}
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok {
		return true
	}
	k := t.bambooKit
	if below == k.magma || below == k.soul {
		return false
	}
	return t.state(below).kind == kindKelp || t.grassKit.solid != nil && t.grassKit.solid.Solid(below)
}

// fertilizeKelp is bone meal on kelp: the top kelp of the column, where it can grow, ticks on
// the next tick.
func (t *Ticker) fertilizeKelp(r *world.Region, pos cube.Pos) bool {
	top := pos
	for {
		b, ok := r.Block(top.Side(cube.FaceUp), 0)
		if !ok || t.state(b).kind != kindKelp {
			break
		}
		top = top.Side(cube.FaceUp)
	}
	b, ok := r.Block(top, 0)
	if !ok || !t.kelpCanGrow(r, top, t.state(b)) {
		return false
	}
	r.ScheduleTick(top, b, 1, 0)
	return true
}

// quietGuard is set while quietSet changes a block's state, so its Placed does nothing.
type quietGuard struct{ on bool }

// quietSet changes the state of the block at pos without its onPlace, as BDS changes a
// block's own state (an age bit, kelp's age) without placing a new block.
func (t *Ticker) quietSet(r *world.Region, pos cube.Pos, block uint32) {
	g := t.quiet(r)
	g.on = true
	r.SetBlock(pos, 0, block)
	g.on = false
}

// quiet is the region's quietGuard.
func (t *Ticker) quiet(r *world.Region) *quietGuard {
	return r.Local(t.bambooKit, func() any { return &quietGuard{} }).(*quietGuard)
}

// Placed is the onPlace of bamboo and kelp, and the next ticker's for every block.
func (t *Ticker) Placed(r *world.Region, pos cube.Pos, block uint32, layer uint8) {
	if layer == 0 && !t.quiet(r).on {
		switch t.state(block).kind {
		case kindBamboo:
			t.bambooPlaced(r, pos)
		case kindKelp:
			t.kelpPlaced(r, pos)
		case kindComposter:
			if t.state(block).age == composterReady {
				r.ScheduleTick(pos, block, composterDelay, 0)
			}
		}
	}
	t.BlockTicker.Placed(r, pos, block, layer)
}
