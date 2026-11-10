package mob

import (
	"math"
	"math/rand/v2"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/physics"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Env is what the mob runtime asks the rest of the engine: the parts of the world that are
// not blocks (other entities, players, game rules, the clock) and the actions a goal takes in
// it. The player package's Host implements it. Every call runs on the region that owns r.
type Env interface {
	// Boxes are the collision boxes of block b at pos, inside the block (doors and the other
	// computed shapes included).
	Boxes(r *world.Region, pos cube.Pos, b uint32) []registry.Box
	// Actor is the entity with the unique ID, or nil when it is gone.
	Actor(r *world.Region, id behavior.ID) behavior.Actor
	// Entities appends the entities whose boxes touch b, self left out.
	Entities(r *world.Region, dst []behavior.Actor, self behavior.ID, b behavior.Box) []behavior.Actor
	// Players appends every player in r's dimension.
	Players(r *world.Region, dst []behavior.Actor) []behavior.Actor
	// DimensionID is the dimension's BDS number.
	DimensionID(d *world.Dimension) int32
	// DefaultGameMode, Difficulty and MobGriefing are the level's settings.
	DefaultGameMode() int
	Difficulty() int32
	MobGriefing() bool
	// SkyDarken is BDS's sky darkening in r's dimension, 0 to 11.
	SkyDarken(r *world.Region) int32
	// Rain and Thunder are the dimension's weather levels, 0 to 1.
	Rain(r *world.Region) float32
	Thunder(r *world.Region) float32
	// LevelEvent sends the level event at p with the block name as data.
	LevelEvent(r *world.Region, id int32, p behavior.Vec3, block string)
	// BreakBlock breaks the block at p as broken by the entity.
	BreakBlock(r *world.Region, p behavior.Pos, by behavior.ID)
	// SetBlock places a block a goal chose.
	SetBlock(r *world.Region, p behavior.Pos, b behavior.PlacedBlock, by behavior.ID)
	// Particle spawns a named particle effect.
	Particle(r *world.Region, name string, p behavior.Vec3)
}

// view is one region seen by the mob runtime. It is filled for each mob tick and never kept.
type view struct {
	r      *world.Region
	blocks *Blocks
	env    Env
	rnd    *Random
	// boxes is a scratch list for collision queries.
	boxes []registry.Box
}

func (v *view) block(p path.Pos, layer uint8) uint32 {
	b, ok := v.r.Block(cube.Pos{int(p.X), int(p.Y), int(p.Z)}, layer)
	if !ok {
		// An unloaded block reads as air; the world refuses moves into unloaded chunks.
		return v.blocks.reg.Air()
	}
	return b
}

func (v *view) loaded(p path.Pos) bool {
	_, ok := v.r.Block(cube.Pos{int(p.X), int(p.Y), int(p.Z)}, 0)
	return ok
}

// liquid is the state in the liquid layer, or the block itself when that layer is air and
// the block is a liquid.
func (v *view) liquid(p path.Pos) uint32 {
	b := v.block(p, 1)
	if b == v.blocks.reg.Air() {
		return v.block(p, 0)
	}
	return b
}

// boxesAt appends the world-space collision boxes of the block at p.
func (v *view) boxesAt(dst []path.Box, p path.Pos) []path.Box {
	b := v.block(p, 0)
	for _, bx := range v.env.Boxes(v.r, cube.Pos{int(p.X), int(p.Y), int(p.Z)}, b) {
		dst = append(dst, path.Box{
			Min: path.Vec3{X: float32(p.X) + bx[0], Y: float32(p.Y) + bx[1], Z: float32(p.Z) + bx[2]},
			Max: path.Vec3{X: float32(p.X) + bx[3], Y: float32(p.Y) + bx[4], Z: float32(p.Z) + bx[5]},
		})
	}
	return dst
}

func overlap(a, b path.Box) bool {
	return a.Min.X < b.Max.X && a.Max.X > b.Min.X && a.Min.Y < b.Max.Y && a.Max.Y > b.Min.Y && a.Min.Z < b.Max.Z && a.Max.Z > b.Min.Z
}

func floor32(f float32) int32 { return int32(math.Floor(float64(f))) }

// cells calls fn for each block cell b touches.
func cells(b path.Box, fn func(p path.Pos) bool) {
	for x := floor32(b.Min.X); x <= floor32(b.Max.X); x++ {
		for y := floor32(b.Min.Y); y <= floor32(b.Max.Y); y++ {
			for z := floor32(b.Min.Z); z <= floor32(b.Max.Z); z++ {
				if !fn(path.Pos{X: x, Y: y, Z: z}) {
					return
				}
			}
		}
	}
}

// --- path.World and nav.World ---

// pathWorld answers the pathfinder and navigation.
type pathWorld struct{ *view }

func (w pathWorld) Block(p path.Pos) path.Block  { return path.Block(w.block(p, 0)) }
func (w pathWorld) Liquid(p path.Pos) path.Block { return path.Block(w.block(p, 1)) }
func (w pathWorld) Info(b path.Block) *path.BlockInfo {
	return w.blocks.Path(uint32(b))
}

func (w pathWorld) CollisionBox(b path.Block, p path.Pos) path.Box {
	boxes := w.env.Boxes(w.r, cube.Pos{int(p.X), int(p.Y), int(p.Z)}, uint32(b))
	if len(boxes) == 0 {
		return path.Box{}
	}
	out := registry.Box{1, 1, 1, 0, 0, 0}
	for _, bx := range boxes {
		for i := range 3 {
			out[i] = min(out[i], bx[i])
			out[i+3] = max(out[i+3], bx[i+3])
		}
	}
	return path.Box{
		Min: path.Vec3{X: float32(p.X) + out[0], Y: float32(p.Y) + out[1], Z: float32(p.Z) + out[2]},
		Max: path.Vec3{X: float32(p.X) + out[3], Y: float32(p.Y) + out[4], Z: float32(p.Z) + out[5]},
	}
}

func (w pathWorld) MinY() int32 { return int32(w.r.Dimension().Range()[0]) } //nolint:gosec // world heights fit

// Pathable is canBePathedThrough's default: the material does not block motion. Doors,
// slabs, snow and liquids have their own rules in the pathfinder.
func (w pathWorld) Pathable(b path.Block, _, _ path.Pos) bool {
	return !w.blocks.Goal(uint32(b)).BlocksMotion
}

func (w pathWorld) Door(b path.Block) (facing int32, open, hinge bool) {
	s := w.blocks.reg.States(uint32(b))
	if d, ok := s["direction"].(int32); ok {
		facing = d
	} else if d, ok := s["minecraft:cardinal_direction"].(string); ok {
		facing = map[string]int32{"south": 0, "west": 1, "north": 2, "east": 3}[d]
	}
	return facing, stateTrue(s, "open_bit"), stateTrue(s, "door_hinge_bit")
}

func (w pathWorld) HeightRange() (lo, hi int32) {
	rg := w.r.Dimension().Range()
	return int32(rg[0]), int32(rg[1]) + 1 //nolint:gosec // world heights fit
}

func (w pathWorld) Height(x, z int32) (int32, bool) {
	h, ok := w.r.Height(int(x), int(z))
	return int32(h) - int32(w.r.Dimension().Range()[0]), ok //nolint:gosec // world heights fit
}

func (w pathWorld) IsDay() bool { return w.env.SkyDarken(w.r) < 4 }

func (w pathWorld) BorderBlock(x, z int32) (border, loaded bool) {
	return false, w.loaded(path.Pos{X: x, Y: int32(w.r.Dimension().Range()[0]), Z: z}) //nolint:gosec // world heights fit
}

func (w pathWorld) BlocksMotion(b path.Block) bool { return w.blocks.Goal(uint32(b)).BlocksMotion }

func (w pathWorld) SwimClear(a, b path.Vec3) bool { _, hit := w.clip(a, b); return !hit }

// clip walks the line from a to b block by block and returns the first point inside a
// block's collision box. A simple DDA, not yet BDS's clip.
func (v *view) clip(a, b path.Vec3) (path.Vec3, bool) {
	d := path.Vec3{X: b.X - a.X, Y: b.Y - a.Y, Z: b.Z - a.Z}
	l := float32(math.Sqrt(float64(d.X*d.X + d.Y*d.Y + d.Z*d.Z)))
	if l == 0 {
		return a, false
	}
	steps := int(min(l*4, 800)) + 1
	var buf [8]path.Box
	for i := 0; i <= steps; i++ {
		t := float32(i) / float32(steps)
		p := path.Vec3{X: a.X + d.X*t, Y: a.Y + d.Y*t, Z: a.Z + d.Z*t}
		cell := path.Pos{X: floor32(p.X), Y: floor32(p.Y), Z: floor32(p.Z)}
		for _, bx := range v.boxesAt(buf[:0], cell) {
			if p.X >= bx.Min.X && p.X <= bx.Max.X && p.Y >= bx.Min.Y && p.Y <= bx.Max.Y && p.Z >= bx.Min.Z && p.Z <= bx.Max.Z {
				return p, true
			}
		}
	}
	return b, false
}

// --- physics.World ---

type physWorld struct{ *view }

func (w physWorld) Block(p path.Pos) physics.Block { return w.blocks.Physics(w.block(p, 0)) }

func (w physWorld) MaterialAt(p path.Pos) path.Material {
	m := w.blocks.Path(w.block(p, 0)).Material
	if m == materialAir {
		return w.blocks.Path(w.block(p, 1)).Material
	}
	return m
}

func (w physWorld) CollisionBoxes(dst []path.Box, b path.Box) []path.Box {
	cells(path.Box{Min: path.Vec3{X: b.Min.X, Y: b.Min.Y - 1, Z: b.Min.Z}, Max: b.Max}, func(p path.Pos) bool {
		n := len(dst)
		dst = w.boxesAt(dst, p)
		// Keep only those that touch b.
		keep := dst[:n]
		for _, bx := range dst[n:] {
			if overlap(bx, b) {
				keep = append(keep, bx)
			}
		}
		dst = keep
		return true
	})
	return dst
}

func (w physWorld) ContainsMaterial(b path.Box, m path.Material) bool {
	found := false
	cells(b, func(p path.Pos) bool {
		found = w.MaterialAt(p) == m
		return !found
	})
	return found
}

func (w physWorld) ContainsLiquid(b path.Box) bool {
	found := false
	cells(b, func(p path.Pos) bool {
		found = w.blocks.Goal(w.liquid(p)).Liquid
		return !found
	})
	return found
}

// --- behavior.World ---

type goalWorld struct{ *view }

func (w goalWorld) Random() behavior.Random       { return w.rnd }
func (w goalWorld) IRandom() behavior.Random      { return w.rnd }
func (w goalWorld) ThreadRandom() behavior.Random { return w.rnd }
func (w goalWorld) CurrentTick() uint64           { return uint64(max(w.r.CurrentTick(), 0)) } //nolint:gosec // clamped
func (w goalWorld) Actor(id behavior.ID) behavior.Actor {
	if id == behavior.NoID {
		return nil
	}
	return w.env.Actor(w.r, id)
}

func (w goalWorld) Entities(dst []behavior.Actor, self behavior.ID, b behavior.Box) []behavior.Actor {
	return w.env.Entities(w.r, dst, self, b)
}
func (w goalWorld) Players(dst []behavior.Actor) []behavior.Actor { return w.env.Players(w.r, dst) }
func (w goalWorld) DefaultGameMode() int                          { return w.env.DefaultGameMode() }
func (w goalWorld) Peaceful() bool                                { return w.env.Difficulty() == 0 }

func (w goalWorld) Brightness(p behavior.Pos) float32 {
	return float32(w.r.Brightness(cube.Pos{int(p.X), int(p.Y), int(p.Z)}, uint8(min(max(w.env.SkyDarken(w.r), 0), 15)))) / 15 //nolint:gosec // clamped
}

func (w goalWorld) Block(p behavior.Pos) behavior.Block { return w.blocks.Goal(w.block(p, 0)) }
func (w goalWorld) LiquidMaterial(p behavior.Pos) path.Material {
	return w.blocks.Path(w.block(p, 1)).Material
}
func (w goalWorld) ClearLine(a, b behavior.Vec3) bool { _, hit := w.clip(a, b); return !hit }
func (w goalWorld) Clip(a, b behavior.Vec3) (behavior.Vec3, bool) {
	return w.clip(a, b)
}
func (w goalWorld) IsDay() bool        { return w.env.SkyDarken(w.r) < 4 }
func (w goalWorld) RainLevel() float32 { return w.env.Rain(w.r) }

func (w goalWorld) AboveTopSolid(x, z int32) int32 {
	rg := w.r.Dimension().Range()
	for y := int32(rg[1]); y >= int32(rg[0]); y-- { //nolint:gosec // world heights fit
		if w.blocks.Goal(w.block(path.Pos{X: x, Y: y, Z: z}, 0)).Solid {
			return y + 1
		}
	}
	return int32(rg[0]) //nolint:gosec // world heights fit
}

func (w goalWorld) MaxHeight() int32 { return int32(w.r.Dimension().Range()[1]) + 1 } //nolint:gosec // world heights fit

func (w goalWorld) SkyVisible(p behavior.Pos) bool { return p.Y >= w.HeightMap(p.X, p.Z) }

func (w goalWorld) LiquidBlock(p behavior.Pos) behavior.Block { return w.blocks.Goal(w.liquid(p)) }

func (w goalWorld) BoxHasLiquid(b behavior.Box) bool { return physWorld(w).ContainsLiquid(b) }

func (w goalWorld) BoxCollides(b behavior.Box) bool {
	var buf [16]path.Box
	return len(physWorld(w).CollisionBoxes(buf[:0], b)) > 0
}

func (w goalWorld) HeightMap(x, z int32) int32 {
	h, ok := w.r.Height(int(x), int(z))
	if !ok {
		return w.MinHeight()
	}
	return int32(h) //nolint:gosec // world heights fit
}

func (w goalWorld) MinHeight() int32  { return int32(w.r.Dimension().Range()[0]) } //nolint:gosec // world heights fit
func (w goalWorld) MobGriefing() bool { return w.env.MobGriefing() }
func (w goalWorld) LevelEvent(id int32, p behavior.Vec3, block string) {
	w.env.LevelEvent(w.r, id, p, block)
}
func (w goalWorld) BreakBlock(p behavior.Pos, by behavior.ID) { w.env.BreakBlock(w.r, p, by) }
func (w goalWorld) Passable(p behavior.Pos, _ behavior.ID) bool {
	return !w.blocks.Goal(w.block(p, 0)).BlocksMotion
}
func (w goalWorld) Particle(name string, p behavior.Vec3) { w.env.Particle(w.r, name, p) }
func (w goalWorld) SeaLevel() int32                       { return 63 }
func (w goalWorld) BuiltOver(p behavior.Pos) bool {
	g := w.blocks.Goal(w.block(p, 0))
	return g.Material == materialAir || g.Liquid
}

func (w goalWorld) BlockState(p behavior.Pos, state string) (int32, bool) {
	switch v := w.blocks.reg.States(w.block(p, 0))[state].(type) {
	case int32:
		return v, true
	case uint8:
		return int32(v), true
	}
	return 0, false
}

func (w goalWorld) ChestOpen(behavior.Pos) bool { return false }
func (w goalWorld) ThunderLevel() float32       { return w.env.Rain(w.r) * w.env.Thunder(w.r) }
func (w goalWorld) SetBlock(p behavior.Pos, b behavior.PlacedBlock, by behavior.ID) {
	w.env.SetBlock(w.r, p, b, by)
}
func (w goalWorld) GriefAllowed(behavior.Pos, behavior.ID) bool { return w.env.MobGriefing() }
func (w goalWorld) BaseGameAtLeast(int, int, int) bool          { return true }

// Random is a region's random for the mob runtime: Java's nextGaussian pairing over a
// seeded generator. BDS draws from MT19937 streams; the numbers differ, the distributions
// match.
type Random struct {
	r        *rand.Rand
	gauss    float32
	hasGauss bool
}

// NewRandom returns a random seeded with seed.
func NewRandom(seed uint64) *Random {
	return &Random{r: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))} //nolint:gosec // gameplay randomness
}

// NextInt returns a number in [0, n).
func (r *Random) NextInt(n int32) int32 {
	if n <= 0 {
		return 0
	}
	return r.r.Int32N(n)
}

// NextFloat returns a number in [0, 1).
func (r *Random) NextFloat() float32 { return r.r.Float32() }

// NextBool returns true half the time.
func (r *Random) NextBool() bool { return r.r.IntN(2) == 1 }

// NextGaussian returns a normal number, keeping the second of each pair.
func (r *Random) NextGaussian() float32 {
	if r.hasGauss {
		r.hasGauss = false
		return r.gauss
	}
	for {
		a, b := 2*r.r.Float64()-1, 2*r.r.Float64()-1
		s := a*a + b*b
		if s < 1 && s != 0 {
			m := math.Sqrt(-2 * math.Log(s) / s)
			r.gauss, r.hasGauss = float32(b*m), true
			return float32(a * m)
		}
	}
}
