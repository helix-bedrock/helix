package player

import (
	"fmt"
	"io/fs"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/spawn"
	files "github.com/helix-bedrock/helix-data/data/files"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"

	"github.com/helix-bedrock/helix/world"
)

// Natural mob spawning: the entity library's spawn package is BDS's BedrockSpawner, and this
// file answers its questions about a region. The Host is the world.ChunkSpawner the server
// gives every dimension, so each ticking chunk gets one spawner tick after its block ticks.
//
// Not done yet (the spawner skips or refuses these): structure spawn areas (witch huts,
// monuments, fortresses, outposts) and ancient cities, villages (player_in_village_filter
// never blocks), /mobevent (every mob event is on, BDS's default), custom spawn rules (only
// vanilla's spawn_rules.json is read). Players in other regions are not
// seen by the distance filter; regions are far enough apart that this rarely matters.

// spawning is the spawner shared by every region, with the tables it reads.
type spawning struct {
	sp     *spawn.Spawner
	blocks *mob.SpawnBlocks
	// biomes maps a registry biome ID to the spawner's biome index, -1 for none.
	biomes map[int16]uint32
	// tags are each registry biome's tags, for the mob filter tests.
	tags map[int16][]string
}

// setUpSpawning loads vanilla's spawn rules and biome tags. Without them mobs only come
// from /summon.
func (s *Host) setUpSpawning() error {
	raw, err := fs.ReadFile(files.Files(), "spawn_rules.json")
	if err != nil {
		return fmt.Errorf("player: read spawn rules: %w", err)
	}
	rules, err := spawn.Load(raw)
	if err != nil {
		return fmt.Errorf("player: spawn rules: %w", err)
	}
	vanilla, err := spawn.LoadBiomes(files.Files(), "vanilla/biomes")
	if err != nil {
		return fmt.Errorf("player: spawn biomes: %w", err)
	}
	blocks, err := mob.NewSpawnBlocks(s.reg.Blocks)
	if err != nil {
		return err
	}
	sg := &spawning{blocks: blocks, biomes: map[int16]uint32{}, tags: map[int16][]string{}}
	all := s.reg.Biomes.All()
	infos := make([]spawn.BiomeInfo, 0, len(all))
	for _, b := range all {
		info, ok := vanilla[strings.TrimPrefix(b.Name, "minecraft:")]
		if !ok {
			// A biome of a plugin: its tags from the registry, never snow covered.
			info = spawn.BiomeInfo{Tags: b.Tags}
		}
		sg.biomes[b.ID] = uint32(len(infos)) //nolint:gosec // a biome count
		sg.tags[b.ID] = info.Tags
		infos = append(infos, info)
	}
	sg.sp = spawn.New(rules, infos)
	s.spawning = sg
	return nil
}

// regionSpawn is a region's own spawner state, kept with Region.Local.
type regionSpawn struct {
	st  *spawn.State
	w   spawnWorld
	rnd spawnRand
	lv  spawn.Level
	// tick is the world tick lv was filled for.
	tick int64
}

type regionSpawnKey struct{}

// SpawnTick runs the natural spawner on a ticking chunk, BDS's spawner tick for one chunk.
// Runs: owner of the region.
func (s *Host) SpawnTick(r *world.Region, pos mcdb.ChunkPos) {
	if rs := s.regionSpawn(r); rs != nil {
		rs.st.TickChunk(&rs.w, &rs.lv, &rs.rnd, pos[0], pos[1])
		rs.done()
	}
}

// PopulationSpawn gives a newly generated chunk its first herd, BDS's post-process spawn.
// Runs: owner of the region.
func (s *Host) PopulationSpawn(r *world.Region, pos mcdb.ChunkPos) {
	s.spawnStructureMobs(r, pos)
	if rs := s.regionSpawn(r); rs != nil {
		rs.st.PostProcess(&rs.w, &rs.lv, &rs.rnd, pos[0], pos[1])
		rs.done()
	}
}

// regionSpawn readies the region's spawner state for one call this tick, nil when nothing
// may spawn.
func (s *Host) regionSpawn(r *world.Region) *regionSpawn {
	rs := s.regionSpawnState(r)
	if rs == nil || !rs.lv.MobSpawning {
		return nil
	}
	return rs
}

// regionSpawnState is regionSpawn whatever the doMobSpawning game rule says, for monster
// spawners, which BDS runs without it.
func (s *Host) regionSpawnState(r *world.Region) *regionSpawn {
	sg := s.spawning
	if sg == nil || s.mobs == nil {
		return nil
	}
	rs, ok := r.Local(regionSpawnKey{}, func() any {
		seed := uint64(s.level.Seed) ^ r.ID() //nolint:gosec // seed bits
		return &regionSpawn{st: sg.sp.NewState(), rnd: spawnRand{rand.New(rand.NewPCG(seed, r.ID()))}, tick: -1}
	}).(*regionSpawn)
	if !ok {
		return nil
	}
	now := r.CurrentTick()
	if rs.tick != now {
		rs.tick = now
		s.fillSpawnLevel(r, &rs.lv)
	}
	rg := r.Dimension().Range()
	rs.w = spawnWorld{s: s, sg: sg, r: r, d: r.Dimension(), minY: int32(rg[0]), maxY: int32(rg[1]) + 1, rnd: &rs.rnd, list: rs.w.list}
	return rs
}

// done drops the entity pointers the call collected.
func (rs *regionSpawn) done() {
	clear(rs.w.list)
	rs.w.list = rs.w.list[:0]
}

// fillSpawnLevel reads the level state the spawn rules test, once per region tick.
func (s *Host) fillSpawnLevel(r *world.Region, lv *spawn.Level) {
	*lv = spawn.Level{
		MobSpawning: s.level.DoMobSpawning,
		Difficulty:  uint8(min(max(s.level.Difficulty, 0), 3)), //nolint:gosec // clamped
		Time:        r.CurrentTick(),
		SkyDarken:   uint8(min(max(s.SkyDarken(r), 0), 15)), //nolint:gosec // clamped
		Rain:        s.Rain(r), Thunder: s.Thunder(r),
		TickRange: r.Dimension().SimulationDistance(),
	}
	if s.clock != nil {
		lv.DayTime = int64(s.clock.Time())
	}
	switch mcdb.Dimension(s.dimensionID(r.Dimension())) {
	case mcdb.Nether:
		lv.Dimension = spawn.Nether
	case mcdb.End:
		lv.Dimension = spawn.End
	default:
		lv.Dimension = spawn.Overworld
	}
}

// spawnRand is the region's random for the spawner (BDS uses the level's).
type spawnRand struct{ r *rand.Rand }

func (s *spawnRand) Int(n int32) int32 {
	if n <= 0 {
		return 0
	}
	return s.r.Int32N(n)
}

func (s *spawnRand) Float() float32 { return s.r.Float32() }

// spawnWorld answers the spawner about one region during one chunk's spawner tick.
type spawnWorld struct {
	s          *Host
	sg         *spawning
	r          *world.Region
	d          *world.Dimension
	minY, maxY int32
	rnd        *spawnRand
	list       []*world.Entity
}

func cubePos(p path.Pos) cube.Pos { return cube.Pos{int(p.X), int(p.Y), int(p.Z)} }

func (w *spawnWorld) Block(p path.Pos) spawn.Block {
	id, _ := w.r.Block(cubePos(p), 0)
	return w.sg.blocks.Block(id)
}

func (w *spawnWorld) Biome(p path.Pos) uint32 {
	id, ok := w.r.Biome(cubePos(p))
	if !ok {
		return math.MaxUint32
	}
	if i, ok := w.sg.biomes[id]; ok {
		return i
	}
	return math.MaxUint32
}

func (w *spawnWorld) HeightRange() (minY, maxY int32) { return w.minY, w.maxY }

func (w *spawnWorld) Light(p path.Pos) (sky, block uint8) {
	sky, block, _ = w.r.Light(cubePos(p))
	return sky, block
}

func (w *spawnWorld) Loaded(p path.Pos) bool {
	_, ok := w.r.ChunkTicked(mcdb.ChunkPos{p.X >> 4, p.Z >> 4})
	return ok
}

func (w *spawnWorld) TickedRecently(cx, cz int32) bool {
	recent, _ := w.r.ChunkTicked(mcdb.ChunkPos{cx, cz})
	return recent
}

// NearestPlayer finds the nearest player of the region, by its eye position as BDS keeps a
// player's position.
func (w *spawnWorld) NearestPlayer(p path.Vec3, maxDist float32) (path.Vec3, bool) {
	w.list = w.r.AppendEntities(w.list[:0])
	best, found := maxDist*maxDist, false
	var at path.Vec3
	for _, e := range w.list {
		pl, ok := e.Owner().(*Player)
		if !ok || pl.entity == nil {
			continue
		}
		v := e.Position()
		dx, dy, dz := v[0]-p.X, v[1]-p.Y, v[2]-p.Z
		if d := dx*dx + dy*dy + dz*dz; d <= best {
			best, found, at = d, true, path.Vec3{X: v[0], Y: v[1], Z: v[2]}
		}
	}
	return at, found
}

func (w *spawnWorld) InAncientCity(path.Pos) bool   { return false }
func (w *spawnWorld) StructureSpawns(path.Pos) bool { return false }
func (w *spawnWorld) MobEvent(string) bool          { return true }
func (w *spawnWorld) PlayerInVillage(path.Pos, int32) bool {
	return false
}

func (w *spawnWorld) Mobs(minX, minZ, maxX, maxZ int32, f func(identifier string, surface bool)) {
	w.list = w.r.AppendEntitiesIn(w.list[:0], float32(minX*16), float32(minZ*16), float32(maxX*16+15), float32(maxZ*16+15))
	for _, e := range w.list {
		if o, ok := e.Owner().(*Mob); ok && !o.gone && !o.m.Dead() {
			f(o.m.Type(), o.surface)
		}
	}
}

func (w *spawnWorld) EntitiesIn(b path.Box) int {
	w.list = w.r.AppendEntitiesIn(w.list[:0], b.Min.X-2, b.Min.Z-2, b.Max.X+2, b.Max.Z+2)
	n := 0
	for _, e := range w.list {
		var bx behavior.Box
		if a := actorOf(e); a != nil {
			bx = a.Box()
		} else {
			v := e.Position()
			bx = behavior.Box{Min: behavior.Vec3{X: v[0], Y: v[1], Z: v[2]}, Max: behavior.Vec3{X: v[0], Y: v[1], Z: v[2]}}
		}
		if bx.Min.X <= b.Max.X && bx.Max.X >= b.Min.X && bx.Min.Y <= b.Max.Y && bx.Max.Y >= b.Min.Y && bx.Min.Z <= b.Max.Z && bx.Max.Z >= b.Min.Z {
			n++
		}
	}
	return n
}

// NewMob makes a mob of the identifier, which may name the event it starts with as
// minecraft:pillager<minecraft:spawn_as_patrol_follower> does. The facing is random: not
// compared with BDS.
func (w *spawnWorld) NewMob(identifier string, pos path.Vec3, surface bool) spawn.Mob {
	typ, event := identifier, ""
	if i := strings.IndexByte(identifier, '<'); i > 0 && strings.HasSuffix(identifier, ">") {
		typ, event = identifier[:i], identifier[i+1:len(identifier)-1]
	}
	method := definition.InitSpawned
	if event != "" {
		method = definition.InitEvent
	}
	o, ok := w.s.newMob(typ, mgl32.Vec3{pos.X, pos.Y, pos.Z}, w.rnd.Float()*360, method, event)
	if !ok {
		return nil
	}
	o.surface, o.naturalSpawn = surface, true
	o.m.Ready(w.r)
	return &spawnedMob{o: o, d: w.d}
}

// spawnedMob is a mob the spawner made, not in the world until Add.
type spawnedMob struct {
	o *Mob
	d *world.Dimension
}

func (m *spawnedMob) Box() path.Box      { return m.o.m.Box() }
func (m *spawnedMob) Pos() path.Vec3     { return m.o.m.Pos() }
func (m *spawnedMob) SetPos(p path.Vec3) { m.o.m.SetPos(p) }
func (m *spawnedMob) FireImmune() bool   { return m.o.m.Flags().Has(behavior.FlagFireImmune) }
func (m *spawnedMob) Add()               { m.o.add(m.d) }
func (m *spawnedMob) Event(name string)  { m.o.m.RunEvent(name) }
func (m *spawnedMob) Persist()           { m.o.m.SetPersistent() }
