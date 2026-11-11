// Package fire is BDS 1.26.60's fire: fire and soul fire blocks, how fire ages, burns out,
// burns the blocks around it and spreads, and lava lighting fire next to flammable blocks.
// Ported from BDS's FireBlock, SoulFireBlock and lava's random tick; the research note is
// private.
//
// Not ported: nether portals lit by fire, and bees in a burning hive.
package fire

import (
	"math/rand/v2"
	"sync/atomic"

	"github.com/helix-bedrock/helix/attach"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/weather"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Blocks fire looks for.
const (
	FireName     = "minecraft:fire"
	SoulFireName = "minecraft:soul_fire"
	soulSand     = "minecraft:soul_sand"
	soulSoil     = "minecraft:soul_soil"
	campfire     = "minecraft:campfire"
	soulCampfire = "minecraft:soul_campfire"
	tnt          = "minecraft:tnt"
	underwaterTN = "minecraft:underwater_tnt"
	netherrack   = "minecraft:netherrack"
	magma        = "minecraft:magma"
	bedrock      = "minecraft:bedrock"
)

// States fire reads.
const (
	ageState         = "age"
	infiniburnState  = "infiniburn_bit"
	extinguishedStat = "extinguished"
)

// Numbers of BDS's fire.
const (
	// maxAge is fire's oldest age.
	maxAge = 15
	// tickBase and tickSpread are fire's tick delay: tickBase plus up to tickSpread-1 more.
	tickBase, tickSpread = 30, 10
	// placedDelay is the tick fire takes after it is placed, and survivalDelay the tick that
	// removes fire that lost what held it.
	placedDelay, survivalDelay = 1, 1
	// soulDelay is the tick that turns fire on soul sand or soul soil into soul fire.
	soulDelay = 3
	// sideOdds and verticalOdds are the odds fire burns the block beside it and above or below
	// it; humidOdds is taken off both in a humid biome.
	sideOdds, verticalOdds, humidOdds = 300, 250, 50
	// spreadBase is added to the flame odds of the blocks around an empty block fire may
	// spread to; spreadPerDifficulty is added per level of difficulty, easy to hard.
	spreadBase, spreadPerDifficulty = 40, 7
	// spreadReach is how far up fire looks for blocks to spread to; spreadUnit is the odds
	// against each block, times its height above one block up.
	spreadReach, spreadUnit = 4, 100
	// oldBurnOut is the chance against (one in) fire at maxAge on a block that does not
	// burn going out.
	oldBurnOut = 4
	// stayAge is the oldest a fire with nothing to burn stays on a sturdy block, and on TNT
	// that cannot explode the age below which it stays.
	stayAge, tntStayAge = 3, 4
	// keepFire is the chance against (out of age+10) a burnt block turning to fire; at or
	// under keepFireMax it does.
	keepFire = 10
	keepMax  = 4
	// burnAgeSpread is the spread of the age a burnt block's fire gets: up to (n-1)/4 older.
	burnAgeSpread, burnAgeDiv = 5, 4
	// lavaTries is how many blocks lava's random tick tries around it.
	lavaTries = 10
)

// Materials fire reads, BDS's MaterialType.
const (
	materialWater     = 5
	materialLava      = 6
	materialExplosive = 12
)

// Difficulties.
const (
	difficultyEasy = 1
	difficultyHard = 3
)

// Rules are the game rules and level values fire reads.
type Rules struct {
	// DoFireTick is the dofiretick game rule: off, fire neither spreads nor burns out and lava
	// lights nothing.
	DoFireTick bool
	// TNTExplodes is the tntexplodes game rule: off, fire on TNT stays while young.
	TNTExplodes bool
	// Difficulty is the level's difficulty, 0 peaceful to 3 hard. Fire spreads more readily
	// the harder it is.
	Difficulty int32
}

// PrimeFunc lights the TNT block at pos that fire reached, as TNT lit by fire does. Runs:
// owner of the region.
type PrimeFunc func(r *world.Region, pos cube.Pos)

// Ticker adds fire and lava's fire to another BlockTicker. It does not change after
// NewTicker except for its rules, so one Ticker serves every dimension.
type Ticker struct {
	world.BlockTicker
	blocks  *registry.Blocks
	biomes  *registry.Biomes
	support *attach.Table
	prime   PrimeFunc
	// campfireWet throws out the food of a campfire that water reached; nil for none.
	campfireWet CampfireWetFunc
	rules       atomic.Pointer[Rules]
	// rain says where it rains; nil for never.
	rain atomic.Pointer[weather.Ticker]
	info []info
	// fire holds the default fire state (no connections) of each age, soulFire the soul fire
	// block.
	fire [maxAge + 1]uint32
	// ages holds, for each fire state, its age and the same state at every age: fire has
	// connection states besides its age.
	ages     map[uint32]*fireAges
	soulFire uint32
	hasFire  bool
}

// info is what fire knows of a block state.
type info uint8

const (
	// isFire is a fire state, isSoulFire soul fire.
	isFire info = 1 << iota
	isSoulFire
	// infiniburn is a block fire on top of never burns out on.
	infiniburn
	// soulBase turns fire on it into soul fire.
	soulBase
	isCampfire
	isTNT
	// isLava is lava, which lights fire around it on random ticks.
	isLava
)

// NewTicker returns fire's ticker, with next for every other block. prime lights TNT; nil
// leaves it.
func NewTicker(reg *registry.Registry, support *attach.Table, rules Rules, prime PrimeFunc, next world.BlockTicker) *Ticker {
	b := reg.Blocks
	t := &Ticker{BlockTicker: next, blocks: b, biomes: reg.Biomes, support: support, prime: prime, info: make([]info, b.Count())}
	t.rules.Store(&rules)
	t.ages = map[uint32]*fireAges{}
	if def, ok := b.Block(FireName); ok {
		t.fire, t.hasFire = t.agesOf(def).states, true
	}
	named := map[string]info{
		netherrack: infiniburn, magma: infiniburn, soulSand: soulBase, soulSoil: soulBase,
		campfire: isCampfire, soulCampfire: isCampfire, tnt: isTNT, underwaterTN: isTNT,
	}
	for id := range uint32(len(t.info)) { //nolint:gosec // the registry's block count fits uint32
		name := b.Name(id)
		t.info[id] |= named[name]
		switch name {
		case FireName:
			t.info[id] |= isFire
			t.ages[id] = t.agesOf(id)
		case SoulFireName:
			t.info[id] |= isSoulFire
		case bedrock:
			if v, _ := b.States(id)[infiniburnState].(uint8); v != 0 {
				t.info[id] |= infiniburn
			}
		}
		if b.Properties(id).Material == materialLava {
			t.info[id] |= isLava
		}
	}
	t.soulFire, _ = b.Block(SoulFireName)
	return t
}

// SetRules changes the rules fire reads. Runs: any goroutine.
func (t *Ticker) SetRules(r Rules) { t.rules.Store(&r) }

// SetWeather sets what says where it rains, which puts fire out and stops it spreading. Call
// it before any player joins; without it, it never rains on fire.
func (t *Ticker) SetWeather(chunks *weather.Ticker) { t.rain.Store(chunks) }

// raining reports whether it rains at pos.
func (t *Ticker) raining(r *world.Region, pos cube.Pos) bool {
	c := t.rain.Load()
	return c != nil && c.RainingAt(r, pos)
}

// rainedOn reports whether it rains at pos or on any of its four sides.
func (t *Ticker) rainedOn(r *world.Region, pos cube.Pos) bool {
	if t.raining(r, pos) {
		return true
	}
	for _, f := range [...]cube.Face{cube.FaceWest, cube.FaceEast, cube.FaceNorth, cube.FaceSouth} {
		if t.raining(r, pos.Side(f)) {
			return true
		}
	}
	return false
}

// Rules returns the rules fire reads. Runs: any goroutine.
func (t *Ticker) Rules() Rules { return *t.rules.Load() }

func (t *Ticker) is(block uint32, i info) bool {
	return int(block) < len(t.info) && t.info[block]&i != 0
}

// fireAges is one fire state's age and the same state at every age.
type fireAges struct {
	age    int
	states [maxAge + 1]uint32
}

// agesOf finds the fire state block at every age, keeping its other states. An age the
// registry does not know keeps block.
func (t *Ticker) agesOf(block uint32) *fireAges {
	states := t.blocks.States(block)
	a := &fireAges{}
	if v, ok := states[ageState].(int32); ok {
		a.age = int(v)
	}
	with := make(map[string]any, len(states))
	for k, v := range states {
		with[k] = v
	}
	name := t.blocks.Name(block)
	for age := range int32(maxAge + 1) {
		with[ageState] = age
		id, ok := t.blocks.StateID(name, with)
		if !ok {
			id = block
		}
		a.states[age] = id
	}
	return a
}

// age is the age of a fire state.
func (t *Ticker) age(block uint32) int {
	if a, ok := t.ages[block]; ok {
		return a.age
	}
	return 0
}

// aged is the fire state block at age, keeping its connections.
func (t *Ticker) aged(block uint32, age int) uint32 {
	if a, ok := t.ages[block]; ok {
		return a.states[age]
	}
	return t.fire[age]
}

// RandomTicks takes lava's random ticks and the next ticker's.
func (t *Ticker) RandomTicks(block uint32, layer uint8) bool {
	return layer == 0 && t.is(block, isLava) || t.BlockTicker.RandomTicks(block, layer)
}

// RandomTick runs lava's random tick, and the next ticker's for other blocks.
func (t *Ticker) RandomTick(r *world.Region, pos cube.Pos, block uint32, layer uint8) {
	if layer == 0 && t.is(block, isLava) {
		t.lavaTick(r, pos)
	}
	if t.BlockTicker.RandomTicks(block, layer) {
		t.BlockTicker.RandomTick(r, pos, block, layer)
	}
}

// QueuedTick runs fire's and soul fire's ticks.
func (t *Ticker) QueuedTick(r *world.Region, pos cube.Pos, block uint32) {
	switch {
	case t.is(block, isFire):
		if b, ok := r.Block(pos, 0); ok && t.is(b, isFire) {
			t.tick(r, pos, b)
		}
	case t.is(block, isSoulFire):
		if !t.soulSurvives(r, pos) {
			t.remove(r, pos)
		}
	default:
		// BDS's liquid tick tries lava's fire first, as its random tick does: lava sets fire
		// soon after it is placed or flows, not only on random ticks.
		if t.is(block, isLava) {
			t.lavaTick(r, pos)
		}
		t.BlockTicker.QueuedTick(r, pos, block)
	}
}

// Placed is fire's onPlace: fire that cannot stay goes out, fire on soul sand or soul soil
// turns to soul fire, else it ticks next tick. Soul fire off soul blocks goes out.
func (t *Ticker) Placed(r *world.Region, pos cube.Pos, block uint32, layer uint8) {
	switch {
	case layer == 0 && t.is(block, isFire) && !t.local(r).aging:
		if !t.survives(r, pos) {
			t.remove(r, pos)
		} else if !t.toSoulFire(r, pos) {
			r.ScheduleTick(pos, t.fire[0], placedDelay, 0)
		}
	case layer == 0 && t.is(block, isSoulFire):
		if !t.soulSurvives(r, pos) {
			t.remove(r, pos)
		}
	case layer == 0 && t.is(block, isCampfire), layer == 1 && t.campfireAt(r, pos):
		t.campfireChanged(r, pos)
	}
	t.BlockTicker.Placed(r, pos, block, layer)
}

// NeighbourChanged is fire's neighborChanged: fire that lost what held it goes out on its
// next tick, and fire now on soul sand or soul soil turns to soul fire a little later.
func (t *Ticker) NeighbourChanged(r *world.Region, pos, from cube.Pos, block uint32, layer uint8) {
	switch {
	case layer == 0 && t.is(block, isFire):
		if !t.survives(r, pos) {
			r.ScheduleTick(pos, t.fire[0], survivalDelay, 0)
		} else if b, ok := r.Block(pos.Side(cube.FaceDown), 0); ok && t.is(b, soulBase) {
			r.ScheduleTick(pos, t.fire[0], soulDelay, 0)
		}
	case layer == 0 && t.is(block, isSoulFire):
		if !t.soulSurvives(r, pos) {
			r.ScheduleTick(pos, t.soulFire, survivalDelay, 0)
		}
	case layer == 0 && t.is(block, isCampfire):
		t.campfireChanged(r, pos)
	}
	t.BlockTicker.NeighbourChanged(r, pos, from, block, layer)
}

// MayPlace reports whether fire may be put at pos (FireBlock's canSurvive), as lightning asks
// before setting it. Runs: owner of the region.
func (t *Ticker) MayPlace(r *world.Region, pos cube.Pos) bool { return t.survives(r, pos) }

// survives is fire's canSurvive: not on a campfire, and on a block whose top holds it or
// next to a block that burns.
func (t *Ticker) survives(r *world.Region, pos cube.Pos) bool {
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok || t.is(below, isCampfire) {
		return false
	}
	return t.support.Supports(below, cube.FaceUp) || t.flammableAround(r, pos)
}

// soulSurvives is soul fire's canSurvive: on soul sand or soul soil.
func (t *Ticker) soulSurvives(r *world.Region, pos cube.Pos) bool {
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	return ok && t.is(below, soulBase)
}

// flammableAround reports whether a block beside, above or below pos catches fire.
func (t *Ticker) flammableAround(r *world.Region, pos cube.Pos) bool {
	for _, f := range allFaces() {
		if b, ok := r.Block(pos.Side(f), 0); ok && t.blocks.Properties(b).FlameOdds != 0 {
			return true
		}
	}
	return false
}

// toSoulFire turns fire on soul sand or soul soil into soul fire; true if it did.
func (t *Ticker) toSoulFire(r *world.Region, pos cube.Pos) bool {
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok || !t.is(below, soulBase) || t.soulFire == 0 {
		return false
	}
	t.set(r, pos, t.soulFire)
	return true
}

// set puts block at pos and tells the neighbours, BDS's update flags 3.
func (t *Ticker) set(r *world.Region, pos cube.Pos, block uint32) {
	if r.SetBlock(pos, 0, block) {
		r.UpdateNeighbours(pos)
	}
}

// remove is BDS's removeBlock: air, with no drops.
func (t *Ticker) remove(r *world.Region, pos cube.Pos) { t.set(r, pos, t.blocks.Air()) }

// local is fire's per-region working state.
type local struct {
	rng *rand.Rand
	// aging is set while a fire changes to an older age: BDS runs onPlace only when the block
	// type changes, and Placed runs on any change of state.
	aging bool
}

// newLocal makes a region's working state.
func newLocal(r *world.Region) *local {
	return &local{rng: rand.New(rand.NewPCG(r.ID(), uint64(r.Dimension().CurrentTick())))} //nolint:gosec // gameplay randomness
}

func (t *Ticker) local(r *world.Region) *local {
	v, _ := r.Local(t, func() any { return newLocal(r) }).(*local)
	return v
}

func (t *Ticker) rng(r *world.Region) *rand.Rand { return t.local(r).rng }
