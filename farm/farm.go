// Package farm is BDS 1.26.60's farming: farmland that stays wet near water and dries out,
// crops and melon and pumpkin stems growing on random ticks, bone meal on them, hoes
// tilling dirt, and farmland trampled back to dirt. Read from BDS's code (the research
// note is private):
//
//   - FarmBlock (0xCAF8F80): its random tick 0xCAF93B0, its tick 0xCAF91E0, its survival
//     0xCAF9DE0, its fall-on 0xCAFA050 and its turn to dirt 0xCAF9C40.
//   - CropBlock (0xCB3B3C0, the definition every vanilla crop passes: growth base 25, grows
//     from light 9, age 7, survives from light 8, bone meal 2 to 4): its random tick
//     0xCB3BED0, its growth speed 0xCB0C8F0, bone meal 0xCB3C880, survival 0xCB3C120 and its
//     neighbour change 0xCB3C060. The torchflower crop's bone meal is its own (0xD46D9C0).
//   - StemBlock (0xD506B60): its random tick 0xD506DF0, bone meal 0xCB061F0, survival
//     0xCB06190 and neighbour change 0xD5076E0.
//   - The dirt blocks' use with a hoe (dirt 0xCB13A00, grass 0xCAE3220, dirt path 0xD54D360,
//     rooted dirt 0xD53DCC0) and the hoe's on_tool_used (0xC981EA0).
//
// The other farmable plants (torchflowers, pitcher crops, nether wart, sweet berries, cocoa,
// sugar cane, cactus) are in plants.go. Bamboo and kelp are in bamboo.go. Not ported: saplings, mobs trampling
// (only players do in Helix), and raids eating crops (minecraft:on_raided).
package farm

import (
	"math/rand/v2"
	"sync/atomic"

	"github.com/helix-bedrock/helix/daytime"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/weather"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Blocks farming reads and places.
const (
	Farmland       = "minecraft:farmland"
	dirt           = "minecraft:dirt"
	coarseDirt     = "minecraft:coarse_dirt"
	grassBlock     = "minecraft:grass_block"
	grassPath      = "minecraft:grass_path"
	rootedDirt     = "minecraft:dirt_with_roots"
	torchflower    = "minecraft:torchflower"
	torchflowerCro = "minecraft:torchflower_crop"
	melonStem      = "minecraft:melon_stem"
	pumpkinStem    = "minecraft:pumpkin_stem"
	melon          = "minecraft:melon_block"
	pumpkin        = "minecraft:pumpkin"
	// HangingRoots is the item rooted dirt drops when tilled.
	HangingRoots = "minecraft:hanging_roots"
)

// States farming reads.
const (
	stateGrowth   = "growth"
	stateMoisture = "moisturized_amount"
	stateFacing   = "facing_direction"
)

// Tags of BDS's blocks.
const (
	// tagCrop keeps farmland under it from drying to dirt.
	tagCrop = "minecraft:crop"
	// tagDirt is what a stem's fruit may grow on.
	tagDirt = "dirt"
)

// Numbers of BDS's farming.
const (
	// maxMoisture is farmland's wettest; water within waterReach on x and z, at its level or
	// one up, keeps it there.
	maxMoisture = 7
	waterReach  = 4
	// maxStemAge is a stem's last growth stage; at it the stem grows its fruit.
	maxStemAge = 7
	// stemGrowthBase is the stem's growth chance base, as the crops' definition's.
	stemGrowthBase = 25
	// stemBoneMealMin and stemBoneMealSpread: bone meal adds 2 + up to 2 stages.
	stemBoneMealMin, stemBoneMealSpread = 2, 3
	// soilDry and soilWet are what one farmland block under a crop adds to its growth
	// speed, a quarter of it off the middle; nearCrop halves it.
	soilDry, soilWet = 1, 3
	offMiddle        = 0.25
	nearCrop         = 0.5
	// surviveDelay is the tick that breaks what lost its footing.
	surviveDelay = 1
	// trampleSlack is taken off the fall before the trample odds; trampleVolume is the
	// smallest box (width times height times depth) that tramples.
	trampleSlack  = 0.5
	trampleVolume = 0.512
	// materialWater is BDS's water MaterialType.
	materialWater = registry.MaterialWater
)

// Crop is one crop's definition, BDS's CropBlock definition. Every vanilla crop uses
// DefaultCrop.
type Crop struct {
	// GrowthBase is the growth chance: one in GrowthBase / speed + 1 per random tick.
	GrowthBase float32
	// GrowLight is the light the crop needs to grow, SurviveLight to stay without the sky.
	GrowLight, SurviveLight uint8
	// MaxAge is its last growth stage.
	MaxAge int
	// BoneMealMin and BoneMealMax are how many stages bone meal adds.
	BoneMealMin, BoneMealMax int
	// TwoStep is the torchflower crop's bone meal: from 0 to 1, then to the end.
	TwoStep bool
}

// DefaultCrop is the definition every vanilla CropBlock passes (0xCB495D0 and the others:
// 25, 9, 7, 8, 2, 4).
func DefaultCrop() Crop {
	return Crop{GrowthBase: 25, GrowLight: 9, SurviveLight: 8, MaxAge: 7, BoneMealMin: 2, BoneMealMax: 4}
}

// FallFunc breaks a block that lost what held it, with its drops. Runs: owner of the region.
type FallFunc func(r *world.Region, pos cube.Pos, block uint32)

// kind is what farming knows a block state as.
type kind uint8

const (
	kindNone kind = iota
	kindFarmland
	kindCrop
	kindStem
	kindWart
	kindBerry
	kindCocoa
	kindCane
	kindCactus
	kindPitcher
	kindBamboo
	kindBambooSapling
	kindKelp
)

// plant reports whether k is one of the plants in plants.go.
func (k kind) plant() bool { return k >= kindWart }

// stateInfo is one block state's part in farming.
type stateInfo struct {
	kind kind
	// age is a crop's or stem's growth stage, or farmland's moisture.
	age int8
	// facing is a stem's facing_direction, 0 when no fruit hangs on it; a cocoa pod's
	// direction; a pitcher crop's half, 1 for the upper.
	facing int8
	// crop is the index of a crop's definition; for a stem, of its fruit.
	crop uint8
	// keepsSoil is a block that keeps the farmland under it: a crop, a stem or a
	// torchflower.
	keepsSoil bool
	// fruitSoil is a block a stem's fruit may grow on (BDS's dirt tag).
	fruitSoil bool
}

// cropKind is one kind of crop: its definition and its state at each stage.
type cropKind struct {
	def    Crop
	stages []uint32
}

// stemKind is one kind of stem: its fruit and its state at each stage and facing.
type stemKind struct {
	fruit  uint32
	stages [maxStemAge + 1][6]uint32
}

// Ticker adds farming to another BlockTicker. It does not change after NewTicker except for
// its clock and crop definitions, so one Ticker serves every dimension.
type Ticker struct {
	world.BlockTicker
	blocks *registry.Blocks
	fall   FallFunc
	clock  atomic.Pointer[daytime.Clock]
	// weather and rain are the world's weather and what says where it rains; nil for none.
	weather atomic.Pointer[weather.Weather]
	rain    atomic.Pointer[weather.Ticker]
	info    []stateInfo
	crops   []cropKind
	defs    atomic.Pointer[[]Crop]
	stems   []stemKind
	// moisture holds farmland at each moisture; dirt is the block it dries or is trampled to.
	moisture [maxMoisture + 1]uint32
	dirt     uint32
	tills    map[uint32]Till
	// grassKit is what bone meal on grass, short grass and ferns needs.
	grassKit *grassKit
	// plantKit holds the plants of plants.go.
	plantKit *plantKit
	// bambooKit holds bamboo and kelp.
	bambooKit *bambooKit
}

// The crops BDS builds with CropBlock, in the order their definitions are kept.
var cropNames = [...]string{"minecraft:wheat", "minecraft:carrots", "minecraft:potatoes", "minecraft:beetroot", torchflowerCro}

// NewTicker returns farming's ticker, with next for every other block. fall breaks crops and
// stems that lose their footing.
func NewTicker(reg *registry.Registry, fall FallFunc, next world.BlockTicker) *Ticker {
	b := reg.Blocks
	t := &Ticker{BlockTicker: next, blocks: b, fall: fall, info: make([]stateInfo, b.Count())}
	t.dirt, _ = b.Block(dirt)
	if _, ok := b.Block(Farmland); ok {
		for m := range int32(maxMoisture + 1) {
			if id, ok := b.StateID(Farmland, map[string]any{stateMoisture: m}); ok {
				t.moisture[m] = id
				t.info[id] = stateInfo{kind: kindFarmland, age: int8(m)}
			}
		}
	}
	defs := make([]Crop, 0, len(cropNames))
	for _, name := range cropNames {
		def := DefaultCrop()
		def.TwoStep = name == torchflowerCro
		k := cropKind{def: def}
		for age := range int32(def.MaxAge + 1) {
			id, ok := b.StateID(name, map[string]any{stateGrowth: age})
			if !ok {
				break
			}
			k.stages = append(k.stages, id)
			t.info[id] = stateInfo{kind: kindCrop, age: int8(age), crop: uint8(len(t.crops)), keepsSoil: true} //nolint:gosec // few crops
		}
		if len(k.stages) == 0 {
			continue
		}
		t.crops = append(t.crops, k)
		defs = append(defs, def)
	}
	t.defs.Store(&defs)
	for _, s := range [...]struct{ stem, fruit string }{{melonStem, melon}, {pumpkinStem, pumpkin}} {
		fruit, ok := b.Block(s.fruit)
		if !ok {
			continue
		}
		k := stemKind{fruit: fruit}
		found := true
		for age := range int32(maxStemAge + 1) {
			for f := range int32(6) {
				id, ok := b.StateID(s.stem, map[string]any{stateGrowth: age, stateFacing: f})
				if !ok {
					found = false
					continue
				}
				k.stages[age][f] = id
				t.info[id] = stateInfo{kind: kindStem, age: int8(age), facing: int8(f), crop: uint8(len(t.stems)), keepsSoil: true} //nolint:gosec // few stems
			}
		}
		if found {
			t.stems = append(t.stems, k)
		}
	}
	for id := range uint32(len(t.info)) { //nolint:gosec // the registry's block count fits uint32
		for _, tag := range b.Tags(id) {
			switch tag {
			case tagCrop:
				t.info[id].keepsSoil = true
			case tagDirt:
				t.info[id].fruitSoil = true
			}
		}
		if b.Name(id) == torchflower {
			t.info[id].keepsSoil = true
		}
	}
	t.tills = t.tillTable()
	t.grassKit = newGrassKit(reg)
	t.plantKit = t.newPlantKit()
	t.bambooKit = t.newBambooKit()
	return t
}

// SetClock sets the clock whose time of day darkens the sky crops read. Without one the sky
// is never darkened. Runs: any goroutine.
func (t *Ticker) SetClock(c *daytime.Clock) { t.clock.Store(c) }

// Crops returns the crop definitions, by the order of the vanilla crops: wheat, carrots,
// potatoes, beetroot, torchflower. Runs: any goroutine.
func (t *Ticker) Crops() []Crop { return append([]Crop(nil), *t.defs.Load()...) }

// SetCrops replaces the crop definitions, in the order Crops returns them; extra ones are
// ignored. A MaxAge past the block's last stage is held to it. Runs: any goroutine.
func (t *Ticker) SetCrops(defs []Crop) {
	n := append([]Crop(nil), *t.defs.Load()...)
	copy(n, defs)
	t.defs.Store(&n)
}

func (t *Ticker) state(block uint32) stateInfo {
	if int(block) < len(t.info) {
		return t.info[block]
	}
	return stateInfo{}
}

// crop returns the definition of a crop kind, its MaxAge held to the stages the block has.
func (t *Ticker) crop(i uint8) Crop {
	defs := *t.defs.Load()
	c := defs[i]
	c.MaxAge = min(max(c.MaxAge, 0), len(t.crops[i].stages)-1)
	return c
}

// SetWeather sets the weather that dims the sky and wets farmland. Call it before any player
// joins.
func (t *Ticker) SetWeather(w *weather.Weather, chunks *weather.Ticker) {
	t.weather.Store(w)
	t.rain.Store(chunks)
}

// raining reports whether it rains at pos.
func (t *Ticker) raining(r *world.Region, pos cube.Pos) bool {
	c := t.rain.Load()
	return c != nil && c.RainingAt(r, pos)
}

// skyDarken is how much the time of day and the weather take off sky light now.
func (t *Ticker) skyDarken() uint8 {
	c, w := t.clock.Load(), t.weather.Load()
	if c == nil {
		return 0
	}
	return uint8(min(max(daytime.SkyDarken(c.Time(), c.Cycle(), w.Rain(), w.Thunder()), 0), world.MaxLight)) //nolint:gosec // 0 to 15
}

// RandomTicks takes farmland's, crops' and stems' random ticks and the next ticker's.
func (t *Ticker) RandomTicks(block uint32, layer uint8) bool {
	k := t.state(block).kind
	return layer == 0 && k != kindNone && k != kindKelp || t.BlockTicker.RandomTicks(block, layer)
}

// RandomTick runs farmland's, a crop's or a stem's random tick, and the next ticker's for
// other blocks.
func (t *Ticker) RandomTick(r *world.Region, pos cube.Pos, block uint32, layer uint8) {
	if layer == 0 {
		switch s := t.state(block); s.kind {
		case kindFarmland:
			t.farmlandTick(r, pos, s)
		case kindCrop:
			t.cropTick(r, pos, block, s)
		case kindStem:
			t.stemTick(r, pos, block, s)
		default:
			if s.kind.plant() {
				t.plantTick(r, pos, s)
			}
		}
	}
	if t.BlockTicker.RandomTicks(block, layer) {
		t.BlockTicker.RandomTick(r, pos, block, layer)
	}
}

// QueuedTick turns farmland under something solid to dirt, and breaks crops and stems that
// lost their footing.
func (t *Ticker) QueuedTick(r *world.Region, pos cube.Pos, block uint32) {
	switch t.state(block).kind {
	case kindFarmland:
		if now, ok := r.Block(pos, 0); ok && t.state(now).kind == kindFarmland && !t.farmlandSurvives(r, pos) {
			t.toDirt(r, pos)
		}
	case kindCrop, kindStem:
		if now, ok := r.Block(pos, 0); ok && t.state(now).kind == t.state(block).kind && !t.survives(r, pos, now) && t.fall != nil {
			t.fall(r, pos, now)
		}
	case kindKelp:
		if now, ok := r.Block(pos, 0); ok && t.state(now).kind == kindKelp {
			t.kelpTick(r, pos, t.state(now))
		}
	default:
		if s := t.state(block); s.kind.plant() {
			if now, ok := r.Block(pos, 0); ok && t.state(now).kind == s.kind && !t.plantSurvives(r, pos, t.state(now)) && t.fall != nil {
				t.fall(r, pos, now)
			}
			return
		}
		t.BlockTicker.QueuedTick(r, pos, block)
	}
}

// NeighbourChanged is farmland's, a crop's and a stem's neighborChanged: what can no longer
// stay goes on its next tick, and a stem whose fruit's place changed lets go of it.
func (t *Ticker) NeighbourChanged(r *world.Region, pos, from cube.Pos, block uint32, layer uint8) {
	if layer == 0 {
		switch s := t.state(block); s.kind {
		case kindFarmland:
			if !t.farmlandSurvives(r, pos) {
				r.ScheduleTick(pos, block, surviveDelay, 0)
			}
		case kindCrop:
			// BDS's crop asks only for its soil here; its tick asks for light too.
			if !t.onSoil(r, pos) {
				r.ScheduleTick(pos, block, surviveDelay, 0)
			}
		case kindStem:
			if !t.onSoil(r, pos) {
				r.ScheduleTick(pos, block, surviveDelay, 0)
			}
			t.letGoOfFruit(r, pos, from, s)
		default:
			if s.kind.plant() && !t.plantSurvives(r, pos, s) {
				r.ScheduleTick(pos, block, surviveDelay, 0)
			}
		}
	}
	t.BlockTicker.NeighbourChanged(r, pos, from, block, layer)
}

// MayPlace reports whether block may be placed at pos as far as farming says: a crop where it
// survives, a stem on farmland, farmland under nothing solid. Other blocks pass. Runs: owner
// of the region.
func (t *Ticker) MayPlace(r *world.Region, pos cube.Pos, block uint32) bool {
	switch t.state(block).kind {
	case kindFarmland:
		return t.farmlandSurvives(r, pos)
	case kindCrop, kindStem:
		return t.survives(r, pos, block)
	}
	if s := t.state(block); s.kind.plant() {
		return t.plantSurvives(r, pos, s)
	}
	return true
}

// rng is the region's random source.
func (t *Ticker) rng(r *world.Region) *rand.Rand {
	return r.Local(t, func() any {
		return rand.New(rand.NewPCG(r.ID(), uint64(r.Dimension().CurrentTick()))) //nolint:gosec // gameplay randomness
	}).(*rand.Rand)
}

func (t *Ticker) set(r *world.Region, pos cube.Pos, block uint32) {
	if r.SetBlock(pos, 0, block) {
		r.UpdateNeighbours(pos)
	}
}

// below and isSoil look at farmland.
func (t *Ticker) onSoil(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos.Side(cube.FaceDown), 0)
	return ok && t.state(b).kind == kindFarmland
}
