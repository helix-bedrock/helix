package farm

import (
	"math"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// The other farmable plants, from BDS 1.26.60 (the research note is private):
//
//   - NetherWartBlock: random tick 0xD543800 (one in 10, to age 3, no light), survival
//     0xD543BD0 (soul sand under it, its mayPlaceOn 0xD543C10). No bone meal.
//   - SweetBerryBushBlock: random tick 0xD4DFAF0 (one in 5, to growth 3), growth 0xD4DFCE0,
//     bone meal 0xD4DFF30 (one stage, to the end for a creative player or rapid fertilizer),
//     survival 0xD4DFCA0 on BDS's plant soil list (0xD4DFC20, the list tall grass uses),
//     picking 0xD4DF790 and 0xD4E0390 (from growth 2: the bush's drops, back to growth 1).
//   - CocoaBlock: random tick 0xCB5C640 (one in 5, to age 2), survival 0xCB5C940 (a jungle
//     log or wood, stripped or not, on the side its direction names), bone meal 0xCB5D0E0
//     (one stage, to the end for rapid fertilizer).
//   - SugarCaneBlock (reeds): random tick 0xD4DE740 (with air above and fewer than 3 canes in
//     the column, at age 15 a new cane on top and age 0, else age + 1), survival 0xD4DEB10
//     (cane under it, or a dirt or sand block with water or frosted ice beside it), bone meal
//     0xD4DEFE0 (the column grows to 3 where there is air).
//   - CactusBlock: random tick 0xCB87BA0 (as sugar cane; at age 7, one in 10 or with 3
//     high one in 4, a cactus flower on top instead), survival 0xCB88240 (nothing solid and
//     no lava beside it, no lava above, cactus or sand under it). No bone meal.
//   - PitcherCropBlock: random tick 0xD551570 (the lower half, light 9, the crops' odds),
//     growth 0xD552330 (to growth 4, from 3 two blocks tall), survival 0xD5517F0 (the upper
//     half on its lower half; the lower on farmland, from growth 3 under its upper half),
//     bone meal 0xD552B30 (one stage, to the end for rapid fertilizer, not on the upper half).
//   - TorchflowerCropBlock: its stage for an age above 6 is a torchflower (0xD46D860).
const (
	netherWart   = "minecraft:nether_wart"
	soulSand     = "minecraft:soul_sand"
	berryBush    = "minecraft:sweet_berry_bush"
	cocoa        = "minecraft:cocoa"
	sugarCane    = "minecraft:reeds"
	cactus       = "minecraft:cactus"
	cactusFlower = "minecraft:cactus_flower"
	pitcherCrop  = "minecraft:pitcher_crop"
	frostedIce   = "minecraft:frosted_ice"
	stateAge     = "age"
	stateDir     = "direction"
	// tagSand is what sugar cane and cactus stand on besides their own kind.
	tagSand = "sand"
)

// Numbers of these plants in BDS.
const (
	wartMaxAge, wartOdds = 3, 10
	berryMaxAge          = 3
	berryOdds            = 5
	// berryPickAge is the growth a bush can be picked from; picking leaves berryPicked.
	berryPickAge, berryPicked = 2, 1
	cocoaMaxAge, cocoaOdds    = 2, 5
	// columnMaxAge is sugar cane's and cactus's age at which they grow a block taller;
	// columnMaxHeight is how tall they grow.
	columnMaxAge, columnMaxHeight = 15, 3
	// cactusFlowerAge is the age a cactus may grow a flower at, at odds of
	// cactusFlowerLow in 100, or cactusFlowerHigh when the column is full.
	cactusFlowerAge                        = 7
	cactusFlowerLow, cactusFlowerHigh      = 10, 25
	cactusFlowerOutOf                      = 100
	pitcherMaxAge, pitcherTallAge          = 4, 3
	torchflowerGrownAge                    = 7
	horizontalSides                        = 4
	materialLava                           = registry.MaterialLava
	cocoaDirections                        = 4
	pitcherHalves                          = 2
	pitcherLower, pitcherUpper        int8 = 0, 1
)

// cocoaLogs are the blocks cocoa hangs on.
var cocoaLogs = [...]string{"minecraft:jungle_log", "minecraft:jungle_wood", "minecraft:stripped_jungle_log", "minecraft:stripped_jungle_wood"}

// cocoaOffset is the x and z of the block a cocoa pod of each direction hangs on (BDS's
// direction tables).
var cocoaOffset = [cocoaDirections][2]int{{0, 1}, {-1, 0}, {0, -1}, {1, 0}}

// plantKit is the states of the plants in this file, made once by NewTicker.
type plantKit struct {
	wart      [wartMaxAge + 1]uint32
	berry     [berryMaxAge + 1]uint32
	cocoa     [cocoaMaxAge + 1][cocoaDirections]uint32
	cane      [columnMaxAge + 1]uint32
	cactus    [columnMaxAge + 1]uint32
	pitcher   [pitcherMaxAge + 1][pitcherHalves]uint32
	flower    uint32
	soulSand  uint32
	frosted   uint32
	torch     uint32
	cocoaLogs map[uint32]bool
	sand      map[uint32]bool
}

// newPlantKit fills the plant states into t.info and returns their kit.
func (t *Ticker) newPlantKit() *plantKit {
	b := t.blocks
	k := &plantKit{cocoaLogs: map[uint32]bool{}, sand: map[uint32]bool{}}
	k.flower, k.soulSand = blockOrNone(b, cactusFlower), blockOrNone(b, soulSand)
	k.frosted, k.torch = blockOrNone(b, frostedIce), blockOrNone(b, torchflower)
	ages := func(name, state string, kd kind, top int, out []uint32) {
		for age := range int32(top + 1) {
			id, ok := b.StateID(name, map[string]any{state: age})
			if !ok {
				continue
			}
			out[age] = id
			t.info[id] = stateInfo{kind: kd, age: int8(age)}
		}
	}
	ages(netherWart, stateAge, kindWart, wartMaxAge, k.wart[:])
	ages(sugarCane, stateAge, kindCane, columnMaxAge, k.cane[:])
	ages(cactus, stateAge, kindCactus, columnMaxAge, k.cactus[:])
	for g := range int32(8) {
		if id, ok := b.StateID(berryBush, map[string]any{stateGrowth: g}); ok {
			if g <= berryMaxAge {
				k.berry[g] = id
			}
			t.info[id] = stateInfo{kind: kindBerry, age: int8(g)}
		}
	}
	for age := range int32(cocoaMaxAge + 1) {
		for d := range int32(cocoaDirections) {
			if id, ok := b.StateID(cocoa, map[string]any{stateAge: age, stateDir: d}); ok {
				k.cocoa[age][d] = id
				t.info[id] = stateInfo{kind: kindCocoa, age: int8(age), facing: int8(d)}
			}
		}
	}
	for g := range int32(8) {
		for half := range int8(pitcherHalves) {
			id, ok := b.StateID(pitcherCrop, map[string]any{stateGrowth: g, stateUpper: uint8(half)}) //nolint:gosec // 0 or 1
			if !ok {
				continue
			}
			if g <= pitcherMaxAge {
				k.pitcher[g][half] = id
			}
			t.info[id] = stateInfo{kind: kindPitcher, age: int8(g), facing: half, keepsSoil: true}
		}
	}
	for _, name := range cocoaLogs {
		for id := range uint32(b.Count()) { //nolint:gosec // block counts fit
			if b.Name(id) == name {
				k.cocoaLogs[id] = true
			}
		}
	}
	for id := range uint32(b.Count()) { //nolint:gosec // block counts fit
		for _, tag := range b.Tags(id) {
			if tag == tagSand {
				k.sand[id] = true
			}
		}
	}
	return k
}

// plantTick runs the random tick of a plant in this file.
func (t *Ticker) plantTick(r *world.Region, pos cube.Pos, s stateInfo) {
	k, rng := t.plantKit, t.rng(r)
	switch s.kind {
	case kindWart:
		if s.age < wartMaxAge && rng.IntN(wartOdds) == 0 {
			t.set(r, pos, k.wart[s.age+1])
		}
	case kindBerry:
		if rng.IntN(berryOdds) == 0 {
			t.growBerry(r, pos, s, false)
		}
	case kindCocoa:
		if rng.IntN(cocoaOdds) == 0 && s.age < cocoaMaxAge {
			t.set(r, pos, k.cocoa[s.age+1][s.facing])
		}
	case kindCane:
		if !t.plantSurvives(r, pos, s) {
			if t.fall != nil {
				b, _ := r.Block(pos, 0)
				t.fall(r, pos, b)
			}
			return
		}
		t.columnTick(r, pos, s, k.cane[:])
	case kindCactus:
		t.columnTick(r, pos, s, k.cactus[:])
	case kindBamboo, kindBambooSapling:
		t.bambooTick(r, pos, s)
	case kindSapling:
		t.saplingTick(r, pos, s)
	case kindPitcher:
		if s.facing != pitcherLower || r.Brightness(pos, t.skyDarken()) < DefaultCrop().GrowLight {
			return
		}
		b, _ := r.Block(pos, 0)
		if rng.IntN(int(DefaultCrop().GrowthBase/t.growthSpeed(r, pos, b))+1) == 0 {
			t.growPitcher(r, pos, s, false)
		}
	}
}

// columnTick is sugar cane's and cactus's random tick: with air above and fewer than three
// in the column, at age 15 a new block on top, else a year older; a cactus at age 7 may grow
// a flower on top instead.
func (t *Ticker) columnTick(r *world.Region, pos cube.Pos, s stateInfo, ages []uint32) {
	above := pos.Side(cube.FaceUp)
	if !t.isAir(r, above) {
		return
	}
	height := t.columnBelow(r, pos, s.kind) + 1
	if s.kind == kindCane && height >= columnMaxHeight {
		return
	}
	if s.kind == kindCactus && height >= columnMaxHeight && s.age >= cactusFlowerAge+1 {
		return
	}
	switch {
	case int(s.age) >= columnMaxAge && height < columnMaxHeight:
		t.set(r, above, ages[0])
		t.set(r, pos, ages[0])
		return
	case int(s.age) >= columnMaxAge:
		return
	case s.kind == kindCactus && s.age == cactusFlowerAge && t.plantSurvives(r, above, s):
		odds := cactusFlowerLow
		if height >= columnMaxHeight {
			odds = cactusFlowerHigh
		}
		if t.rng(r).IntN(cactusFlowerOutOf) < odds && t.plantKit.flower != math.MaxUint32 {
			t.set(r, above, t.plantKit.flower)
			t.set(r, pos, ages[0])
			return
		}
	}
	t.set(r, pos, ages[s.age+1])
}

// columnBelow counts the blocks of the same kind under pos.
func (t *Ticker) columnBelow(r *world.Region, pos cube.Pos, kd kind) int {
	n := 0
	for at := pos.Side(cube.FaceDown); ; at = at.Side(cube.FaceDown) {
		b, ok := r.Block(at, 0)
		if !ok || t.state(b).kind != kd {
			return n
		}
		n++
	}
}

// isAir reports whether the block at pos is air.
func (t *Ticker) isAir(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && b == t.blocks.Air()
}

// growBerry is the bush's growth: a stage, or to the end when full. True if it grew.
func (t *Ticker) growBerry(r *world.Region, pos cube.Pos, s stateInfo, full bool) bool {
	if s.age >= berryMaxAge {
		return false
	}
	age := s.age + 1
	if full {
		age = berryMaxAge
	}
	t.set(r, pos, t.plantKit.berry[age])
	return true
}

// growPitcher is the pitcher crop's growth from its lower half at pos: a stage, or to the end
// when full, two blocks tall from growth 3. True if it grew.
func (t *Ticker) growPitcher(r *world.Region, pos cube.Pos, s stateInfo, full bool) bool {
	if s.age >= pitcherMaxAge {
		return false
	}
	age := s.age + 1
	if full {
		age = pitcherMaxAge
	}
	above := pos.Side(cube.FaceUp)
	if age >= pitcherTallAge && s.age < pitcherTallAge && !t.isAir(r, above) {
		return false
	}
	k := t.plantKit
	t.set(r, pos, k.pitcher[age][pitcherLower])
	if age >= pitcherTallAge {
		t.set(r, above, k.pitcher[age][pitcherUpper])
	}
	return true
}

// plantSurvives is the canSurvive of a plant in this file at pos.
func (t *Ticker) plantSurvives(r *world.Region, pos cube.Pos, s stateInfo) bool {
	switch s.kind {
	case kindBamboo, kindBambooSapling:
		return t.bambooSurvives(r, pos, s)
	case kindKelp:
		return t.kelpSurvives(r, pos)
	}
	k := t.plantKit
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok {
		return true
	}
	switch s.kind {
	case kindWart:
		return below == k.soulSand
	case kindBerry:
		return t.grassKit.soils[below]
	case kindCocoa:
		o := cocoaOffset[s.facing]
		b, ok := r.Block(cube.Pos{pos[0] + o[0], pos[1], pos[2] + o[1]}, 0)
		return !ok || k.cocoaLogs[b]
	case kindCane:
		return t.caneSurvives(r, pos, below)
	case kindCactus:
		return t.cactusSurvives(r, pos, below)
	case kindPitcher:
		if s.facing == pitcherUpper {
			return t.state(below).kind == kindPitcher && t.state(below).facing == pitcherLower
		}
		if t.state(below).kind != kindFarmland {
			return false
		}
		if s.age < pitcherTallAge {
			return true
		}
		above, ok := r.Block(pos.Side(cube.FaceUp), 0)
		return !ok || t.state(above).kind == kindPitcher
	}
	return true
}

// caneSurvives is sugar cane's: cane under it, or a dirt or sand block with water or frosted
// ice beside that block.
func (t *Ticker) caneSurvives(r *world.Region, pos cube.Pos, below uint32) bool {
	if t.state(below).kind == kindCane {
		return true
	}
	if !t.dirtSoil(below) && !t.plantKit.sand[below] {
		return false
	}
	under := pos.Side(cube.FaceDown)
	for _, f := range [horizontalSides]cube.Face{cube.FaceNorth, cube.FaceSouth, cube.FaceWest, cube.FaceEast} {
		at := under.Side(f)
		if t.water(r, at) {
			return true
		}
		if b, ok := r.Block(at, 0); ok && b == t.plantKit.frosted {
			return true
		}
	}
	return false
}

// cactusSurvives is the cactus's: nothing solid and no lava beside it, no lava above, cactus
// or sand under it.
func (t *Ticker) cactusSurvives(r *world.Region, pos cube.Pos, below uint32) bool {
	for _, f := range [horizontalSides]cube.Face{cube.FaceNorth, cube.FaceSouth, cube.FaceWest, cube.FaceEast} {
		b, ok := r.Block(pos.Side(f), 0)
		if !ok {
			continue
		}
		if p := t.blocks.Properties(b); p.SolidMaterial || p.Material == materialLava {
			return false
		}
	}
	if b, ok := r.Block(pos.Side(cube.FaceUp), 0); ok && t.blocks.Properties(b).Material == materialLava {
		return false
	}
	return t.state(below).kind == kindCactus || t.plantKit.sand[below]
}

// fertilizePlant is bone meal on a plant in this file. True if it grew.
func (t *Ticker) fertilizePlant(r *world.Region, pos cube.Pos, s stateInfo, creative, rapid bool) bool {
	k := t.plantKit
	switch s.kind {
	case kindSapling:
		return t.fertilizeSapling(r, pos, s, creative, rapid)
	case kindBerry:
		return t.growBerry(r, pos, s, creative || rapid)
	case kindCocoa:
		if s.age >= cocoaMaxAge {
			return false
		}
		age := s.age + 1
		if rapid {
			age = cocoaMaxAge
		}
		t.set(r, pos, k.cocoa[age][s.facing])
		return true
	case kindCane:
		return t.fertilizeCane(r, pos)
	case kindPitcher:
		if s.facing == pitcherUpper {
			return false
		}
		return t.growPitcher(r, pos, s, rapid)
	case kindBamboo, kindBambooSapling:
		return t.fertilizeBamboo(r, pos, s)
	case kindKelp:
		return t.fertilizeKelp(r, pos)
	}
	return false
}

// fertilizeCane is bone meal on sugar cane: the column grows to three blocks where there is
// air above it. True if it was shorter than three.
func (t *Ticker) fertilizeCane(r *world.Region, pos cube.Pos) bool {
	bottom, top := pos, pos
	for {
		b, ok := r.Block(bottom.Side(cube.FaceDown), 0)
		if !ok || t.state(b).kind != kindCane {
			break
		}
		bottom = bottom.Side(cube.FaceDown)
	}
	for {
		b, ok := r.Block(top.Side(cube.FaceUp), 0)
		if !ok || t.state(b).kind != kindCane {
			break
		}
		top = top.Side(cube.FaceUp)
	}
	missing := columnMaxHeight - (top[1] - bottom[1] + 1)
	for i := 1; i <= missing; i++ {
		at := cube.Pos{top[0], top[1] + i, top[2]}
		if !t.isAir(r, at) {
			break
		}
		t.set(r, at, t.plantKit.cane[0])
	}
	return missing > 0
}

// PickBerries is picking a sweet berry bush at pos (BDS's use of the bush): from growth 2 it
// goes back to growth 1, and drop is called with the bush as it was, whose drops are the
// berries. False if there was nothing to pick. Runs: owner of the region.
func (t *Ticker) PickBerries(r *world.Region, pos cube.Pos, drop func(block uint32)) bool {
	b, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	s := t.state(b)
	if s.kind != kindBerry || s.age < berryPickAge {
		return false
	}
	drop(b)
	t.set(r, pos, t.plantKit.berry[berryPicked])
	return true
}

// BerryBush reports whether block is a sweet berry bush and whether it is fully grown.
func (t *Ticker) BerryBush(block uint32) (bush, grown bool) {
	s := t.state(block)
	return s.kind == kindBerry, s.kind == kindBerry && s.age >= berryMaxAge
}
