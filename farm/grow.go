package farm

import (
	"math"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// farmlandTick is FarmBlock's random tick: water within four blocks on x and z, at its level
// or one up, or rain on the block above, wets it fully; otherwise it dries a step, and dry farmland with nothing growing
// on it turns to dirt.
func (t *Ticker) farmlandTick(r *world.Region, pos cube.Pos, s stateInfo) {
	if t.nearWater(r, pos) || t.raining(r, pos.Side(cube.FaceUp)) {
		if s.age != maxMoisture {
			t.set(r, pos, t.moisture[maxMoisture])
		}
		return
	}
	if s.age > 0 {
		t.set(r, pos, t.moisture[s.age-1])
		return
	}
	if above, ok := r.Block(pos.Side(cube.FaceUp), 0); ok && !t.state(above).keepsSoil {
		t.toDirt(r, pos)
	}
}

// nearWater reports whether water is within waterReach of pos on x and z, at its level or one
// up, in either layer.
func (t *Ticker) nearWater(r *world.Region, pos cube.Pos) bool {
	for x := pos[0] - waterReach; x <= pos[0]+waterReach; x++ {
		for y := pos[1]; y <= pos[1]+1; y++ {
			for z := pos[2] - waterReach; z <= pos[2]+waterReach; z++ {
				if t.water(r, cube.Pos{x, y, z}) {
					return true
				}
			}
		}
	}
	return false
}

// water reports whether the liquid at pos is water: the second layer's when it holds one,
// else the block's.
func (t *Ticker) water(r *world.Region, pos cube.Pos) bool {
	if b, ok := r.Block(pos, 1); ok && b != t.blocks.Air() {
		return t.blocks.Properties(b).Material == materialWater
	}
	b, ok := r.Block(pos, 0)
	return ok && t.blocks.Properties(b).Material == materialWater
}

// farmlandSurvives is FarmBlock's canSurvive: nothing solid above it.
func (t *Ticker) farmlandSurvives(r *world.Region, pos cube.Pos) bool {
	above, ok := r.Block(pos.Side(cube.FaceUp), 0)
	return !ok || !t.blocks.Properties(above).SolidMaterial
}

// toDirt is FarmBlock's turn to dirt.
func (t *Ticker) toDirt(r *world.Region, pos cube.Pos) {
	t.set(r, pos, t.dirt)
}

// Trample is FarmBlock's fall-on for a player that fell distance onto the farmland at pos
// with a box of volume width times height times depth, mayTrample being the player's mine
// ability: past half a block the fall's odds of turning it to dirt grow with the fall. True
// if it did. Runs: owner of the region.
func (t *Ticker) Trample(r *world.Region, pos cube.Pos, distance, volume float32, mayTrample bool) bool {
	b, ok := r.Block(pos, 0)
	if !ok || t.state(b).kind != kindFarmland || !mayTrample {
		return false
	}
	if distance-trampleSlack <= t.rng(r).Float32() || volume < trampleVolume {
		return false
	}
	t.toDirt(r, pos)
	return true
}

// survives is a crop's or stem's canSurvive: on farmland, and for a crop lit enough or under
// the open sky.
func (t *Ticker) survives(r *world.Region, pos cube.Pos, block uint32) bool {
	s := t.state(block)
	if s.kind == kindCrop && r.Brightness(pos, t.skyDarken()) < t.crop(s.crop).SurviveLight && !seesSky(r, pos) {
		return false
	}
	return t.onSoil(r, pos)
}

// seesSky stands in for BDS's height map test (nothing above pos dims light): full sky light.
func seesSky(r *world.Region, pos cube.Pos) bool {
	return r.SkyLight(pos) == world.MaxLight
}

// cropTick is CropBlock's random tick: lit enough and not fully grown, it grows a stage at
// odds of one in GrowthBase / speed + 1.
func (t *Ticker) cropTick(r *world.Region, pos cube.Pos, block uint32, s stateInfo) {
	c := t.crop(s.crop)
	if r.Brightness(pos, t.skyDarken()) < c.GrowLight || int(s.age) >= c.MaxAge {
		return
	}
	speed := t.growthSpeed(r, pos, block)
	if t.rng(r).IntN(int(c.GrowthBase/speed)+1) == 0 {
		t.setCrop(r, pos, s.crop, int(s.age)+1)
	}
}

// growthSpeed is CropBlock's getGrowthSpeed, which StemBlock's tick repeats: one, and for
// each farmland block under and around it one, three if wet, a quarter of that off the
// middle; halved when the same plant grows on a diagonal, or on both an x side and a z side.
func (t *Ticker) growthSpeed(r *world.Region, pos cube.Pos, block uint32) float32 {
	speed := float32(1)
	for x := pos[0] - 1; x <= pos[0]+1; x++ {
		for z := pos[2] - 1; z <= pos[2]+1; z++ {
			var add float32
			if b, ok := r.Block(cube.Pos{x, pos[1] - 1, z}, 0); ok {
				if s := t.state(b); s.kind == kindFarmland {
					add = soilDry
					if s.age > 0 {
						add = soilWet
					}
				}
			}
			if x != pos[0] || z != pos[2] {
				add *= offMiddle
			}
			speed += add
		}
	}
	name := t.blocks.Name(block)
	same := func(dx, dz int) bool {
		b, ok := r.Block(cube.Pos{pos[0] + dx, pos[1], pos[2] + dz}, 0)
		return ok && t.blocks.Name(b) == name
	}
	diagonal := same(-1, -1) || same(1, -1) || same(1, 1) || same(-1, 1)
	if diagonal || (same(-1, 0) || same(1, 0)) && (same(0, -1) || same(0, 1)) {
		speed *= nearCrop
	}
	return speed
}

// stemTick is StemBlock's random tick: lit enough (BDS's 9), it grows a stage at the crops'
// odds; fully grown with no fruit, it picks a side and grows its fruit there if the side is
// air over a dirt-tagged block, turning to face it.
func (t *Ticker) stemTick(r *world.Region, pos cube.Pos, block uint32, s stateInfo) {
	if r.Brightness(pos, t.skyDarken()) < DefaultCrop().GrowLight {
		return
	}
	speed := t.growthSpeed(r, pos, block)
	rng := t.rng(r)
	if rng.IntN(int(stemGrowthBase/speed+1)) != 0 {
		return
	}
	k := &t.stems[s.crop]
	if s.age < maxStemAge {
		t.set(r, pos, k.stages[s.age+1][s.facing])
		return
	}
	if s.facing != 0 {
		return
	}
	facing := rng.IntN(4) + 2
	at := pos.Side(facingFace(facing))
	b, ok := r.Block(at, 0)
	if !ok || b != t.blocks.Air() {
		return
	}
	if under, ok := r.Block(at.Side(cube.FaceDown), 0); !ok || !t.state(under).fruitSoil {
		return
	}
	t.set(r, at, k.fruit)
	t.set(r, pos, k.stages[s.age][facing])
}

// letGoOfFruit is the end of StemBlock's neighborChanged: when the block on the side its fruit
// grew on changes, the stem faces nothing again.
func (t *Ticker) letGoOfFruit(r *world.Region, pos, from cube.Pos, s stateInfo) {
	if s.facing == 0 || from[1] != pos[1] {
		return
	}
	var facing int8
	switch {
	case from[0] > pos[0]:
		facing = 5
	case from[0] < pos[0]:
		facing = 4
	case from[2] > pos[2]:
		facing = 3
	case from[2] < pos[2]:
		facing = 2
	}
	if facing == s.facing {
		t.set(r, pos, t.stems[s.crop].stages[s.age][0])
	}
}

// facingFace is the face of a facing_direction: 2 north, 3 south, 4 west, 5 east.
func facingFace(f int) cube.Face {
	switch f {
	case 2:
		return cube.FaceNorth
	case 3:
		return cube.FaceSouth
	case 4:
		return cube.FaceWest
	}
	return cube.FaceEast
}

// Fertilize is bone meal on the block at pos (the block's onFertilized). A grass block grows
// grass and flowers around it, and short grass or a fern grows tall (see bonemeal.go). A crop grows
// BoneMealMin to BoneMealMax stages, or to the end when full (BDS: a creative player, or rapid
// fertilizer); the torchflower crop goes from 0 to 1, else to the end. A stem grows 2 to 4
// stages, to the end only when rapid. True if it grew, false for a block that is fully grown
// or not a crop. Runs: owner of the region.
func (t *Ticker) Fertilize(r *world.Region, pos cube.Pos, creative, rapid bool) bool {
	b, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	switch k := t.grassKit; b {
	case k.grass:
		return t.fertilizeGrass(r, pos)
	case k.short, k.fern:
		return t.growTall(r, pos, b)
	}
	s := t.state(b)
	switch s.kind {
	case kindCrop:
		c := t.crop(s.crop)
		age := int(s.age)
		if age >= c.MaxAge {
			return false
		}
		switch {
		case c.TwoStep:
			age = c.MaxAge
			if s.age == 0 && !rapid {
				age = 1
			}
		case creative || rapid:
			age = c.MaxAge
		default:
			age = min(age+c.BoneMealMin+t.rng(r).IntN(c.BoneMealMax-c.BoneMealMin+1), c.MaxAge)
		}
		t.setCrop(r, pos, s.crop, age)
		return true
	case kindStem:
		if s.age >= maxStemAge {
			return false
		}
		age := min(int(s.age)+stemBoneMealMin+t.rng(r).IntN(stemBoneMealSpread), maxStemAge)
		if rapid {
			age = maxStemAge
		}
		t.set(r, pos, t.stems[s.crop].stages[age][s.facing])
		return true
	}
	if s.kind.plant() {
		return t.fertilizePlant(r, pos, s, creative, rapid)
	}
	return false
}

// setCrop puts the crop of kind i at pos at age; the torchflower crop past age 6 becomes a
// torchflower (TorchflowerCropBlock's stage for an age, 0xD46D860).
func (t *Ticker) setCrop(r *world.Region, pos cube.Pos, i uint8, age int) {
	if t.blocks.Name(t.crops[i].stages[0]) == torchflowerCro && age >= torchflowerGrownAge && t.plantKit.torch != math.MaxUint32 {
		t.set(r, pos, t.plantKit.torch)
		return
	}
	t.set(r, pos, t.crops[i].stages[age])
}

// Fertilizable reports whether bone meal works on block at all (BDS's canBeFertilized).
func (t *Ticker) Fertilizable(block uint32) bool {
	switch k := t.grassKit; block {
	case k.grass, k.short, k.fern:
		return true
	}
	s := t.state(block)
	switch s.kind {
	case kindCrop, kindStem, kindBerry, kindCocoa, kindCane:
		return true
	case kindPitcher:
		return s.facing == pitcherLower
	}
	return false
}
