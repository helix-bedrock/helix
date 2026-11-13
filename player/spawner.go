package player

import (
	"maps"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/spawn"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Monster spawners and spawn eggs, as BDS 1.26.60.
//
// The spawner is BDS's BaseMobSpawner (vtable 0xFBC9728; constructor 0xCEAA280, tick
// 0xCEAAB00, load 0xCEAB450, save 0xCEAC7E0, delay 0xCEAA8D0). Each tick of its chunk:
//
//   - Nothing happens unless a player who is not in creative or spectator (Player slot 37,
//     0xBCFFF70) is within RequiredPlayerRange of the block's centre (a negative range
//     reaches everywhere), measured to the player's position (its eyes, as BDS keeps it),
//     and the spawner has a mob (EntityIdentifier not empty).
//   - Delay counts down. At 0 it tries SpawnCount times: a spot (x + (r1-r2)*SpawnRange +
//     0.5, y + randInt(3) - 1, z + (r3-r4)*SpawnRange + 0.5) whose block is free and whose
//     block distance (|dx|+|dy|+|dz|) from the spawner is at most SpawnRange. When
//     MaxNearbyEntities mobs of the type are already in the box SpawnRange*2 to the sides
//     and 4 up and down of the spawner, the spawner only resets its delay. Else the mob is
//     made with its natural spawn and surface flags (Spawner::spawnMob 0xCD8F8C0), which
//     runs its own spawn check (spawn.CheckSpawnRules: monsters need block light 0 and
//     darkness, animals a walkable spot, as for natural spawning), added, and shown with
//     LevelEvent 2004 at the spawner (data: ceil(width) + ceil(height)<<8, 0xCD594A0).
//   - After a spawn, or when the box was full, the delay resets (MinSpawnDelay plus
//     randInt(MaxSpawnDelay-MinSpawnDelay)) and BlockEvent 1 tells clients.
//
// A spawn egg used on a spawner sets its mob (ActorPlacerItem::_useOn 0xC9C6D20 ->
// MobSpawnerBlockActor::setMob) in any game mode, sends the block entity and uses up one
// egg outside creative. Elsewhere it spawns its mob on the clicked face, turned toward the
// player.
//
// Not done: SpawnPotentials and SpawnData's properties are kept in the save but not used
// (the spawner always makes EntityIdentifier), the client's spinning (Spin is the client's
// to draw), the half-block raise on fences, a check BDS makes through a Level shared pointer
// (ServerLevel slot 118) whose meaning is unknown, and the spot's block test, which BDS
// makes from two BlockType property bits Helix does not have: here a spot is free unless
// its block is solid-blocking.

// BaseMobSpawner's defaults (constructor 0xCEAA280).
const (
	spawnerDelay        = 20
	spawnerMinDelay     = 200
	spawnerMaxDelay     = 800
	spawnerCount        = 4
	spawnerMaxNearby    = 6
	spawnerPlayerRange  = 16
	spawnerSpawnRange   = 4
	spawnerHeightRange  = 4
	spawnerDisplayW     = 0.8
	spawnerDisplayH     = 1.8
	spawnerDisplayScale = 1
	// spawnerEntityID is the block entity's saved id.
	spawnerEntityID = "MobSpawner"
	// mobSpawnerBlock is the spawner's block.
	mobSpawnerBlock = "minecraft:mob_spawner"
	// levelEventSpawnerSpawn shows a spawn's flames at the spawner.
	levelEventSpawnerSpawn = 2004
	// blockEventSpawnerDelay is the BlockEvent a delay reset sends.
	blockEventSpawnerDelay = 1
)

// MobSpawner is a monster spawner's block entity. Runs: owner of its chunk.
type MobSpawner struct {
	s     *Host
	chunk world.ChunkHandle
	// The saved fields, by their BDS names.
	delay, minDelay, maxDelay, count, maxNearby, playerRange, spawnRange int32
	entity                                                               string
	width, height, scale                                                 float64
	// saved is the data it was loaded with, written back for the fields not run here.
	saved map[string]any
}

func (s *Host) newSpawner() *MobSpawner {
	return &MobSpawner{
		s: s, delay: spawnerDelay, minDelay: spawnerMinDelay, maxDelay: spawnerMaxDelay, count: spawnerCount,
		maxNearby: spawnerMaxNearby, playerRange: spawnerPlayerRange, spawnRange: spawnerSpawnRange,
		width: spawnerDisplayW, height: spawnerDisplayH, scale: spawnerDisplayScale,
	}
}

// loadSpawner reads a spawner as BaseMobSpawner::load does; a missing field keeps its default.
func (s *Host) loadSpawner(saved map[string]any) *MobSpawner {
	m := s.newSpawner()
	m.saved = maps.Clone(saved)
	short := func(key string, dst *int32) {
		if v, ok := saved[key].(int16); ok {
			*dst = int32(v)
		}
	}
	short("Delay", &m.delay)
	short("MinSpawnDelay", &m.minDelay)
	short("MaxSpawnDelay", &m.maxDelay)
	short("SpawnCount", &m.count)
	short("MaxNearbyEntities", &m.maxNearby)
	short("RequiredPlayerRange", &m.playerRange)
	short("SpawnRange", &m.spawnRange)
	f := func(key string, dst *float64) {
		if v, ok := saved[key].(float32); ok {
			*dst = float64(v)
		}
	}
	f("DisplayEntityWidth", &m.width)
	f("DisplayEntityHeight", &m.height)
	f("DisplayEntityScale", &m.scale)
	m.entity, _ = saved["EntityIdentifier"].(string)
	return m
}

// Entity returns the type the spawner makes, "" for none.
func (m *MobSpawner) Entity() string { return m.entity }

// SetEntity sets the type the spawner makes and shows it to the players who have its chunk,
// as MobSpawnerBlockActor::setMob. Runs: owner of its chunk.
func (m *MobSpawner) SetEntity(r *world.Region, pos cube.Pos, typ string) {
	m.entity = typ
	r.SendBlockEntity(pos)
}

// BindBlockEntity keeps the chunk handle for saves.
func (m *MobSpawner) BindBlockEntity(h world.ChunkHandle) { m.chunk = h }

// SaveData is the spawner as BaseMobSpawner::save writes it, with the block entity fields.
func (m *MobSpawner) SaveData(pos cube.Pos) map[string]any {
	data := maps.Clone(m.saved)
	if data == nil {
		data = map[string]any{"isMovable": uint8(1)}
	}
	data["id"] = spawnerEntityID
	data["x"], data["y"], data["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	data["EntityIdentifier"] = m.entity
	data["Delay"] = int16(m.delay)                     //nolint:gosec // BDS saves shorts
	data["MinSpawnDelay"] = int16(m.minDelay)          //nolint:gosec // BDS saves shorts
	data["MaxSpawnDelay"] = int16(m.maxDelay)          //nolint:gosec // BDS saves shorts
	data["SpawnCount"] = int16(m.count)                //nolint:gosec // BDS saves shorts
	data["MaxNearbyEntities"] = int16(m.maxNearby)     //nolint:gosec // BDS saves shorts
	data["RequiredPlayerRange"] = int16(m.playerRange) //nolint:gosec // BDS saves shorts
	data["SpawnRange"] = int16(m.spawnRange)           //nolint:gosec // BDS saves shorts
	data["DisplayEntityWidth"] = float32(m.width)
	data["DisplayEntityHeight"] = float32(m.height)
	data["DisplayEntityScale"] = float32(m.scale)
	return data
}

// NetworkData is what the client gets: the same, so it draws the mob inside.
func (m *MobSpawner) NetworkData(pos cube.Pos) map[string]any { return m.SaveData(pos) }

// TickBlockEntity is BaseMobSpawner::tick (0xCEAAB00).
func (m *MobSpawner) TickBlockEntity(r *world.Region, pos cube.Pos) {
	if m.entity == "" || !m.playerNear(r, pos) {
		return
	}
	if m.delay > 0 {
		m.delay--
		return
	}
	if m.delay == -1 {
		m.resetDelay(r, pos)
		return
	}
	spawned := false
	for range m.count {
		at, ok := m.spot(r, pos)
		if !ok {
			continue
		}
		if m.nearbyCount(r, pos) >= m.maxNearby {
			m.resetDelay(r, pos)
			return
		}
		if m.spawnAt(r, pos, at) {
			spawned = true
		}
	}
	if spawned {
		m.resetDelay(r, pos)
	}
}

// playerNear is BDS's player search: a survival or adventure player within range of the
// block's centre.
func (m *MobSpawner) playerNear(r *world.Region, pos cube.Pos) bool {
	centre := blockVec(pos).Add(mgl64.Vec3{0.5, 0.5, 0.5})
	limit := math.MaxFloat64
	if m.playerRange >= 0 {
		limit = float64(m.playerRange) * float64(m.playerRange)
	}
	sc := m.s.scratch()
	defer sc.done()
	sc.list = r.AppendEntities(sc.list)
	for _, e := range sc.list {
		p, ok := e.Owner().(*Player)
		if !ok || p.entity == nil || p.dead() || p.creative() || p.spectating() {
			continue
		}
		if e.Position().Sub(centre).LenSqr() < limit {
			return true
		}
	}
	return false
}

// spot draws one spawn spot, false when its block is taken or it is too far by blocks.
func (m *MobSpawner) spot(r *world.Region, pos cube.Pos) (mgl64.Vec3, bool) {
	rng := float64(m.spawnRange)
	x := float64(pos[0]) + float64(rand.Float32()-rand.Float32())*rng + 0.5 //nolint:gosec // gameplay randomness
	y := float64(pos[1] + rand.IntN(3) - 1)                                 //nolint:gosec // gameplay randomness
	z := float64(pos[2]) + float64(rand.Float32()-rand.Float32())*rng + 0.5 //nolint:gosec // gameplay randomness
	at := mgl64.Vec3{x, y, z}
	b := feetBlock(at)
	if sg := m.s.spawning; sg != nil {
		if id, ok := r.Block(b, 0); !ok || sg.blocks.Block(id).Flags&spawn.FlagSolidBlocking != 0 {
			return at, false
		}
	}
	d := absInt(b[0]-pos[0]) + absInt(b[1]-pos[1]) + absInt(b[2]-pos[2])
	return at, d <= int(m.spawnRange)
}

// nearbyCount counts the spawner's type in BDS's box around it.
func (m *MobSpawner) nearbyCount(r *world.Region, pos cube.Pos) int32 {
	side := float64(m.spawnRange) * 2
	px, py, pz := float64(pos[0]), float64(pos[1]), float64(pos[2])
	typ := spawnerType(m.entity)
	sc := m.s.scratch()
	defer sc.done()
	sc.list = r.AppendEntitiesIn(sc.list, float64(px-side), float64(pz-side), float64(px+side), float64(pz+side))
	var n int32
	for _, e := range sc.list {
		o, ok := e.Owner().(*Mob)
		if !ok || o.gone || o.m.Dead() || o.m.Type() != typ {
			continue
		}
		b := o.m.Box()
		if b.Max.X > px-side && b.Min.X < px+side && b.Max.Y > py-spawnerHeightRange && b.Min.Y < py+spawnerHeightRange &&
			b.Max.Z > pz-side && b.Min.Z < pz+side {
			n++
		}
	}
	return n
}

// spawnerType is the mob type of an identifier that may carry a spawn event.
func spawnerType(id string) string {
	if i := strings.IndexByte(id, '<'); i > 0 {
		return id[:i]
	}
	return id
}

// spawnAt makes the spawner's mob at the spot, if its own spawn check lets it.
func (m *MobSpawner) spawnAt(r *world.Region, pos cube.Pos, at mgl64.Vec3) bool {
	rs := m.s.regionSpawnState(r)
	if rs == nil {
		return false
	}
	defer rs.done()
	sm := rs.w.NewMob(m.entity, path.Vec3{X: float64(at[0]), Y: float64(at[1]), Z: float64(at[2])}, true)
	if sm == nil {
		return false
	}
	if !spawn.CheckSpawnRules(&rs.w, &rs.lv, &rs.rnd, spawnerType(m.entity), sm) {
		return false
	}
	sm.Add()
	if o, ok := sm.(*spawnedMob); ok {
		w, h := o.o.m.Size()
		data := int32(math.Ceil(float64(w))) + int32(math.Ceil(float64(h)))<<8
		levelEvent(r, pos, blockVec(pos), levelEventSpawnerSpawn, data)
	}
	return true
}

// resetDelay is BaseMobSpawner::delay (0xCEAA8D0).
func (m *MobSpawner) resetDelay(r *world.Region, pos cube.Pos) {
	m.delay = m.minDelay
	if m.maxDelay > m.minDelay {
		m.delay += rand.Int32N(m.maxDelay - m.minDelay) //nolint:gosec // gameplay randomness
	}
	m.chunk.Changed()
	r.Broadcast(pos, &packet.BlockEvent{Position: blockPos(pos), EventType: blockEventSpawnerDelay}, nil)
}

// spawnEggMob is the mob a spawn egg makes, "" for an item that is not one: its name before
// _spawn_egg, except the eggs whose mob has another name.
func spawnEggMob(item string) string {
	name, ok := strings.CutSuffix(item, "_spawn_egg")
	if !ok {
		return ""
	}
	switch name {
	case "minecraft:evoker":
		return "minecraft:evocation_illager"
	case "minecraft:tropical_fish":
		return "minecraft:tropicalfish"
	case "minecraft:villager":
		return "minecraft:villager_v2"
	case "minecraft:zombie_villager":
		return "minecraft:zombie_villager_v2"
	}
	return name
}

// useSpawnEgg is a spawn egg used on a block: it sets a spawner's mob, else spawns its mob on
// the clicked face. False if nothing happened.
func (p *Player) useSpawnEgg(r *world.Region, clicked cube.Pos, face int32, held ItemStack) bool {
	typ := spawnEggMob(held.Name)
	if typ == "" || !p.s.CanSpawnMob(typ) {
		return false
	}
	if b, ok := r.Block(clicked, 0); ok && p.s.reg.Blocks.Name(b) == mobSpawnerBlock {
		be, ok := r.BlockEntity(clicked)
		sp, isSpawner := be.(*MobSpawner)
		if !ok || !isSpawner {
			return false
		}
		sp.SetEntity(r, clicked, typ)
		p.useUpHeld()
		return true
	}
	pos := clicked.Side(cube.Face(face))
	at := blockVec(pos).Add(mgl64.Vec3{0.5, 0, 0.5})
	d := p.entity.Position().Sub(at)
	var yaw float64
	if d[0]*d[0]+d[2]*d[2] > 1e-7 {
		yaw = float64(math.Atan2(float64(d[2]), float64(d[0])))*180/math.Pi - 90
	}
	o, ok := p.s.newMob(typ, at, yaw, definition.InitSpawned, "")
	if !ok {
		return false
	}
	if name := held.CustomName(); name != "" {
		o.m.SetNameTag(name)
	}
	o.add(r.Dimension())
	p.useUpHeld()
	return true
}
