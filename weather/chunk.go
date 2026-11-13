package weather

import (
	"math/rand/v2"
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// BDS's numbers for the weather on a chunk.
const (
	// stormLevel is how much rain times thunder a chunk needs for lightning, which then
	// strikes one tick in lightningOdds.
	stormLevel    = 0.9
	lightningOdds = 100000
	// noLightningBelow is the biome temperature below which lightning never strikes.
	noLightningBelow = 0.2
	// freezeOdds is the one in freezeOdds chance a try freezes the water under the top.
	freezeOdds = 16
	// FreezeTemperature is the temperature at or below which water freezes and snow falls.
	FreezeTemperature = 0.15
	// snowBase and snowScale give the one in snowBase + rain * snowScale (at least
	// snowBase + snowFloor) chance a try lays snow.
	snowBase  = 256
	snowScale = -512
	snowFloor = -240
	// freezeLight and snowLight are the block light at and above which water does not
	// freeze and snow does not settle.
	freezeLight = 10
	snowLight   = 12
	// maxSnowHeight is a full snow layer's height state; layersPerBlock how many layers a
	// snow block counts as.
	maxSnowHeight  = 7
	layersPerBlock = 8
	// snowPerLayer is how much snow_accumulation one layer takes.
	snowPerLayer = 0.125
	// lcgMul and lcgAdd are BDS's position generator, x = 3x + 1013904223.
	lcgMul, lcgAdd = 3, 1013904223
	// coolingStart, coolingRate and coolingDivisor are BDS's cooling above sea level + 17,
	// used without a Climate.
	coolingStart   = 17
	coolingRate    = -0.05
	coolingDivisor = 40
	seaLevel       = 63
)

// BDS's material types the snow rule reads (bds/block_materials.json).
const (
	materialAir    = 0
	materialDirt   = 1
	materialWater  = 5
	materialLeaves = 7
	materialIce    = 13
)

// The blocks weather sets.
const (
	nameIce       = "minecraft:ice"
	nameSnowLayer = "minecraft:snow_layer"
	nameSnow      = "minecraft:snow"
	nameWater     = "minecraft:water"
	nameFlowing   = "minecraft:flowing_water"
	stateHeight   = "height"
	stateCovered  = "covered_bit"
	stateDepth    = "liquid_depth"
)

// Climate gives BDS's biome temperature at a block; a world generator that knows its
// noise gives it (the vanilla one does).
type Climate interface {
	Temperature(biome uint32, x, y, z int32) float32
}

// StrikeFunc makes a lightning bolt at pos. Runs: owner of the region.
type StrikeFunc func(r *world.Region, pos mgl32.Vec3)

// MobsFunc appends the feet of the mobs and players in the chunk at pos to dst, the ones
// lightning may strike. Runs: owner of the region.
type MobsFunc func(r *world.Region, pos mcdb.ChunkPos, dst []mgl32.Vec3) []mgl32.Vec3

// Config is what the chunk weather needs.
type Config struct {
	Registry *registry.Registry
	Weather  *Weather
	// Climate gives temperatures. Nil uses each biome's own with BDS's cooling above sea
	// level, without the per column noise.
	Climate Climate
	// Strike makes lightning; nil makes none. Mobs finds what it may hit; nil hits only the
	// ground.
	Strike StrikeFunc
	Mobs   MobsFunc
}

// Ticker runs the weather on every ticking chunk: lightning, then water freezing and snow
// settling, randomtickspeed tries. It is the world's WeatherTicker.
type Ticker struct {
	conf    Config
	blocks  *registry.Blocks
	biomes  *registry.Biomes
	air     uint32
	ice     uint32
	snowBlk uint32
	// snow are the snow layers by height, covered[h] the snowlogged ones.
	snow, covered [maxSnowHeight + 1]uint32
	// info is per state: what the weather reads of it.
	info []blockInfo
	// odds replaces lightningOdds when above 0 (SetLightningOdds).
	odds atomic.Int32
	// rods marks the lightning rod states (rod.go).
	rods []bool
}

// SetLightningOdds makes lightning strike a stormy chunk one tick in n instead of BDS's one in
// 100000; 0 or less goes back to BDS's. For testing. Safe from any goroutine.
func (t *Ticker) SetLightningOdds(n int32) { t.odds.Store(n) }

// blockInfo is what the weather reads of a state.
type blockInfo struct {
	precip data.Precipitation
	// material is BDS's material type; solid whether it is a solid material.
	material uint8
	solid    bool
	// snowHeight is a snow layer's height + 1, 0 for any other block.
	snowHeight int8
	// covered is a snow layer's covered_bit.
	covered bool
	// still is source water.
	still bool
}

// regionState is a region's own random numbers.
type regionState struct {
	lcg  uint32
	rng  *rand.Rand
	mobs []mgl32.Vec3
	rods []rodHit
}

type regionKey struct{}

// NewTicker returns the chunk weather.
func NewTicker(conf Config) *Ticker {
	b := conf.Registry.Blocks
	t := &Ticker{conf: conf, blocks: b, biomes: conf.Registry.Biomes, air: b.Air(), info: make([]blockInfo, b.Count())}
	t.ice, _ = b.Block(nameIce)
	t.snowBlk, _ = b.Block(nameSnow)
	t.rods = rodTable(b.Name, b.Count())
	for id := range uint32(b.Count()) { //nolint:gosec // block counts fit
		p := b.Properties(id)
		t.info[id] = blockInfo{precip: p.Precipitation, material: p.Material, solid: p.SolidMaterial}
	}
	for h := range int32(maxSnowHeight + 1) {
		for c, dst := range [2]*[maxSnowHeight + 1]uint32{&t.snow, &t.covered} {
			if id, ok := b.StateID(nameSnowLayer, map[string]any{stateHeight: h, stateCovered: uint8(c)}); ok { //nolint:gosec // 0 or 1
				dst[h] = id
				t.info[id].snowHeight, t.info[id].covered = int8(h+1), c == 1 //nolint:gosec // 1 to 8
			}
		}
	}
	for _, n := range [...]string{nameWater, nameFlowing} {
		if id, ok := b.StateID(n, map[string]any{stateDepth: int32(0)}); ok {
			t.info[id].still = true
		}
	}
	return t
}

func (t *Ticker) state(b uint32) blockInfo {
	if int(b) < len(t.info) {
		return t.info[b]
	}
	return blockInfo{}
}

func (t *Ticker) region(r *world.Region) *regionState {
	s, _ := r.Local(regionKey{}, func() any {
		seed := r.ID()
		return &regionState{lcg: uint32(seed), rng: rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))} //nolint:gosec // weather, not a secret
	}).(*regionState)
	return s
}

// ChunkWeather is BDS's weather part of the chunk tick. Only a dimension with weather (the
// overworld) gets a Ticker.
func (t *Ticker) ChunkWeather(r *world.Region, pos mcdb.ChunkPos, speed int32) {
	w := t.conf.Weather
	if w == nil {
		return
	}
	s := t.region(r)
	odds := int(t.odds.Load())
	if odds <= 0 {
		odds = lightningOdds
	}
	if w.Rain()*w.Thunder() > stormLevel && s.rng.IntN(odds) == 0 {
		t.lightning(r, s, pos)
	}
	for range speed {
		t.precipitate(r, s, pos)
	}
}

// next draws a column of the chunk from the region's generator.
func (s *regionState) next(pos mcdb.ChunkPos) (int, int) {
	s.lcg = lcgMul*s.lcg + lcgAdd
	return int(pos[0])<<4 | int(s.lcg>>2&15), int(pos[1])<<4 | int(s.lcg>>10&15)
}

// lightning strikes the nearest lightning rod that draws it, else a mob or player out in the
// open in the chunk if there is one, else the top of a random column, where it rains.
func (t *Ticker) lightning(r *world.Region, s *regionState, pos mcdb.ChunkPos) {
	x, z := s.next(pos)
	top, ok := t.RainTop(r, x, z)
	if !ok {
		return
	}
	at := mgl32.Vec3{float32(x), float32(top), float32(z)}
	if rod, ok := t.lightningRod(r, s, cube.Pos{x, top, z}); ok {
		at = rodStrike(rod)
	} else if t.conf.Mobs != nil {
		s.mobs = t.conf.Mobs(r, pos, s.mobs[:0])
		open := s.mobs[:0]
		for _, m := range s.mobs {
			if h, ok := r.Height(int(floor(m[0])), int(floor(m[2]))); ok && int(floor(m[1])) >= h {
				open = append(open, m)
			}
		}
		if len(open) > 0 {
			at = open[s.rng.IntN(len(open))]
		}
	}
	bp := cube.Pos{int(floor(at[0])), int(floor(at[1])), int(floor(at[2]))}
	b, ok := t.biome(r, bp)
	if !ok || b.Downfall <= 0 || b.Temperature < noLightningBelow {
		return
	}
	if t.conf.Strike != nil {
		t.conf.Strike(r, at)
	}
}

func floor(f float32) float32 {
	i := float32(int32(f))
	if i > f {
		i--
	}
	return i
}

// precipitate is one try of BDS's: the water under the top of a random column may freeze,
// and where it rains or snows enough, snow may settle on top.
func (t *Ticker) precipitate(r *world.Region, s *regionState, pos mcdb.ChunkPos) {
	x, z := s.next(pos)
	top, ok := t.RainTop(r, x, z)
	if !ok {
		return
	}
	at := cube.Pos{x, top, z}
	if s.rng.IntN(freezeOdds) == 0 {
		below := at.Side(cube.FaceDown)
		if t.ShouldFreeze(r, below, true) {
			r.SetBlock(below, 0, t.ice)
			r.UpdateNeighbours(below)
		}
	}
	b, ok := t.biome(r, at)
	if !ok {
		return
	}
	amount := t.conf.Weather.OldRain() * b.Downfall
	if amount <= 0 {
		return
	}
	n := max(int(amount*snowScale), snowFloor)
	if s.rng.IntN(n+snowBase) == 0 {
		t.PlaceSnow(r, at, true, true)
	}
}

// stopsRain reports whether a block stops rain: it obstructs it, or settles snow on top.
func (t *Ticker) stopsRain(b uint32) bool {
	p := t.state(b).precip
	return p == data.PrecipitationAccumulateSnow || p == data.PrecipitationObstructRain
}

// RainTop is BDS's rain height map (LevelChunk::getTopRainBlockPos, sub_CFAD480): the Y above
// the highest block in the column that stops rain, on either layer. Runs: owner of the region.
func (t *Ticker) RainTop(r *world.Region, x, z int) (int, bool) {
	return r.ColumnTop(x, z, func(b0, b1 uint32) bool {
		return t.stopsRain(b0) || b1 != t.air && t.stopsRain(b1)
	})
}

func (t *Ticker) biome(r *world.Region, pos cube.Pos) (registry.Biome, bool) {
	id, ok := r.Biome(pos)
	if !ok {
		return registry.Biome{}, false
	}
	return t.biomes.ByID(id)
}

// Temperature is BDS's biome temperature at pos. Runs: owner of the region.
func (t *Ticker) Temperature(r *world.Region, pos cube.Pos) (float32, bool) {
	id, ok := r.Biome(pos)
	if !ok {
		return 0, false
	}
	if c := t.conf.Climate; c != nil {
		return c.Temperature(uint32(id), int32(pos[0]), int32(pos[1]), int32(pos[2])), true //nolint:gosec // biome IDs and block positions fit
	}
	b, ok := t.biomes.ByID(id)
	if !ok {
		return 0, false
	}
	const limit = seaLevel + coolingStart
	if pos[1] <= limit {
		return b.Temperature, true
	}
	return b.Temperature + float32(pos[1]-limit)*coolingRate/coolingDivisor, true
}

// Precipitating is BDS's Weather::isPrecipitatingAt (sub_CD9EC30): the biome has rain, the rain
// level is above RainingLevel, and nothing above pos stops sunlight or rain. Runs: owner of
// the region.
func (t *Ticker) Precipitating(r *world.Region, pos cube.Pos) bool {
	b, ok := t.biome(r, pos)
	if !ok || !b.Rain || t.conf.Weather == nil || t.conf.Weather.Rain() <= RainingLevel {
		return false
	}
	if h, ok := r.Height(pos[0], pos[2]); !ok || pos[1] < h {
		return false
	}
	top, ok := t.RainTop(r, pos[0], pos[2])
	return ok && pos[1] >= top
}

// RainingAt is BDS's Weather::isRainingAt (sub_CD9EE30): precipitation that is warm enough to
// be rain. Runs: owner of the region.
func (t *Ticker) RainingAt(r *world.Region, pos cube.Pos) bool {
	if !t.Precipitating(r, pos) {
		return false
	}
	temp, ok := t.Temperature(r, pos)
	return ok && temp > FreezeTemperature
}

// SnowingAt is BDS's Weather::isSnowingAt (sub_CD9EF00). Runs: owner of the region.
func (t *Ticker) SnowingAt(r *world.Region, pos cube.Pos) bool {
	if !t.Precipitating(r, pos) {
		return false
	}
	temp, ok := t.Temperature(r, pos)
	return ok && temp <= FreezeTemperature
}

// inWorld reports whether y is inside the dimension.
func inWorld(r *world.Region, y int) bool {
	rg := r.Dimension().Range()
	return y >= rg[0] && y <= rg[1]
}

// ShouldFreeze is BDS's Weather::shouldFreeze (sub_CD9FF40): source water cold enough, dark
// enough by block light and, with sides, not walled in by water on all four sides. Runs: owner
// of the region.
func (t *Ticker) ShouldFreeze(r *world.Region, pos cube.Pos, sides bool) bool {
	if temp, ok := t.Temperature(r, pos); !ok || temp > FreezeTemperature {
		return false
	}
	if !inWorld(r, pos[1]) || r.BlockLight(pos) >= freezeLight {
		return false
	}
	b, ok := r.Block(pos, 0)
	if !ok || !t.state(b).still {
		return false
	}
	if !sides {
		return true
	}
	for _, f := range [...]cube.Face{cube.FaceWest, cube.FaceEast, cube.FaceNorth, cube.FaceSouth} {
		n, ok := r.Block(pos.Side(f), 0)
		if !ok || t.state(n).material != materialWater {
			return true
		}
	}
	return false
}

// layersAt is BDS's count of snow in the column from pos down (sub_CD9F440): each snow layer
// its height, a snow block a full block's worth, at most most + 1 blocks down.
func (t *Ticker) layersAt(r *world.Region, pos cube.Pos, most int) int {
	n := 0
	for i := 0; i <= most; i++ {
		b, ok := r.Block(pos, 0)
		if !ok {
			return n
		}
		switch s := t.state(b); {
		case s.snowHeight > 0:
			n += int(s.snowHeight)
		case b == t.snowBlk:
			n += layersPerBlock
		default:
			return n
		}
		pos = pos.Side(cube.FaceDown)
	}
	return n
}

// canSnow is BDS's Weather::canPlaceTopSnow (sub_CD9EFE0): whether snow may settle at pos, and
// the layer height it would be. fromRain checks the height of the world and what is below,
// limit the biome's snow_accumulation.
func (t *Ticker) canSnow(r *world.Region, pos cube.Pos, fromRain, limit bool) (int, bool) {
	if temp, ok := t.Temperature(r, pos); !ok || temp > FreezeTemperature {
		return 0, false
	}
	if r.BlockLight(pos) >= snowLight {
		return 0, false
	}
	if fromRain && !inWorld(r, pos[1]) {
		return 0, false
	}
	b, ok := r.Block(pos, 0)
	if !ok {
		return 0, false
	}
	most := 0
	if limit {
		if bio, ok := t.biome(r, pos); ok {
			most = int(bio.SnowAccumulation * layersPerBlock)
		}
	}
	if s := t.state(b); s.snowHeight > 0 {
		if s.snowHeight-1 == maxSnowHeight {
			return 0, false
		}
		if most > 0 && t.layersAt(r, pos, most) >= most {
			return 0, false
		}
		return int(s.snowHeight) + 1, true
	}
	if b != t.air && t.state(b).precip != data.PrecipitationSnowlogging {
		return 0, false
	}
	below := pos.Side(cube.FaceDown)
	u, ok := r.Block(below, 0)
	if !ok {
		return 0, false
	}
	us := t.state(u)
	if us.snowHeight-1 == maxSnowHeight {
		if most > 0 && t.layersAt(r, pos, most) >= most {
			return 0, false
		}
		return 1, true
	}
	if fromRain && us.precip != data.PrecipitationAccumulateSnow {
		return 0, false
	}
	if us.material == materialAir || !us.solid && us.material != materialDirt && us.material != materialLeaves && us.material != materialIce {
		return 0, false
	}
	return 1, true
}

// PlaceSnow is BDS's Weather::tryToPlaceTopSnow (sub_CD9F5D0): with doweathercycle, a layer of
// snow settles at pos, or, with spread, in the column north or south of it that has less snow
// and lies at its rain top. A snowlogged plant moves to the second layer under a covered snow
// layer. Runs: owner of the region.
func (t *Ticker) PlaceSnow(r *world.Region, pos cube.Pos, fromRain, spread bool) bool {
	if t.conf.Weather == nil || !t.conf.Weather.Cycle() {
		return false
	}
	height, ok := t.canSnow(r, pos, fromRain, true)
	at := pos
	if spread {
		best, bestHeight := pos, height
		half := height / 2
		for _, f := range [...]cube.Face{cube.FaceNorth, cube.FaceSouth} {
			n := pos.Side(f)
			h, can := t.canSnow(r, n, fromRain, true)
			if !can || h >= bestHeight {
				continue
			}
			if top, ok := t.RainTop(r, n[0], n[2]); ok && top == n[1] || h < half {
				best, bestHeight = n, h
			}
		}
		if best != pos {
			t.layer(r, best)
			return true
		}
	}
	if !ok {
		return false
	}
	t.layer(r, at)
	return true
}

// layer adds a layer of snow at pos (sub_D4E8C60): one more on a snow layer, a new one on
// air, a covered one over a snowlogging plant, which moves to the second layer.
func (t *Ticker) layer(r *world.Region, pos cube.Pos) {
	b, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	s := t.state(b)
	next := t.snow[0]
	switch {
	case s.snowHeight > 0:
		h := min(int(s.snowHeight), maxSnowHeight)
		next = t.snow[h]
		if s.covered {
			next = t.covered[h]
		}
	case b != t.air && s.precip == data.PrecipitationSnowlogging:
		next = t.covered[0]
		r.SetBlock(pos, 1, b)
	}
	r.SetBlock(pos, 0, next)
	r.UpdateNeighbours(pos)
}
