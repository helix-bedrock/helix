package helix

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/attach"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/circuit"
	"github.com/helix-bedrock/helix/connect"
	"github.com/helix-bedrock/helix/daytime"
	"github.com/helix-bedrock/helix/farm"
	"github.com/helix-bedrock/helix/fall"
	"github.com/helix-bedrock/helix/fire"
	"github.com/helix-bedrock/helix/generator"
	"github.com/helix-bedrock/helix/liquid"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/helix/portal"
	"github.com/helix-bedrock/helix/weather"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// StorageConfig replaces where the world and its players are kept. Every
// field is optional: by default both live in the world folder's LevelDB,
// with BDS's keys and format, so the folder opens in BDS as well.
type StorageConfig struct {
	// KV replaces the LevelDB under the default stores while keeping BDS's
	// keys and values, for a database other than LevelDB.
	KV mcdb.KV
	// World replaces the default world store (chunks and level keys)
	// completely. level.dat is still read from the world folder.
	World mcdb.WorldStore
	// Players replaces the default player store, for example with one
	// database shared by every server of a network.
	Players mcdb.PlayerStore
}

// openWorld opens the world folder WorldsDir/LevelName, as BDS keeps its
// worlds, and the stores. A new world gets LevelName as its name.
func (s *Server) openWorld() error {
	dir := filepath.Join(s.conf.WorldsDir, s.conf.LevelName)
	_, err := os.Stat(filepath.Join(dir, "level.dat"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("helix: open world %q: %w", dir, err)
	}
	isNew := err != nil
	db, err := mcdb.Config{Log: s.log.With("part", "storage"), KV: s.conf.Storage.KV, Blocks: s.reg.Blocks}.Open(dir)
	if err != nil {
		return fmt.Errorf("helix: open world %q: %w", dir, err)
	}
	ldat := db.LevelDat()
	if isNew {
		ldat.LevelName = s.conf.LevelName
		if s.conf.Seed != nil {
			ldat.RandomSeed = *s.conf.Seed
		}
	}
	if isNew {
		ldat.RainTime, ldat.LightningTime = weather.NewTimes(uint64(ldat.RandomSeed)) //nolint:gosec // a seed
	}
	s.weather = weather.New(weather.Levels{Rain: ldat.RainLevel, Thunder: ldat.LightningLevel, RainTime: ldat.RainTime, ThunderTime: ldat.LightningTime},
		ldat.DoWeatherCycle, uint64(ldat.RandomSeed)^uint64(time.Now().UnixNano()), s.broadcastLevelEvent) //nolint:gosec // a seed
	worldType, gt, err := s.worldType(ldat.Generator, isNew)
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("helix: open world %q: %w", dir, err)
	}
	ldat.Generator = gt.Level
	s.db, s.worldStore, s.playerStore = db, mcdb.WorldStore(db), mcdb.PlayerStore(db)
	if st := s.conf.Storage.World; st != nil {
		s.worldStore = st
	}
	if st := s.conf.Storage.Players; st != nil {
		s.playerStore = st
	}
	s.level = player.Level{
		Name: ldat.LevelName, Seed: ldat.RandomSeed, GameMode: ldat.GameType, Difficulty: ldat.Difficulty,
		Spawn: [3]int32{ldat.SpawnX, ldat.SpawnY, ldat.SpawnZ}, Generator: ldat.Generator,
		KeepInventory: ldat.KeepInventory, ShowDeathMessages: ldat.ShowDeathMessages,
		PVP: ldat.PVP, NaturalRegeneration: ldat.NaturalRegeneration,
		TNTExplodes: ldat.TNTExplodes, TNTExplosionDropDecay: ldat.TNTExplosionDropDecay, DoTileDrops: ldat.DoTileDrops, GameRules: gameRules(ldat),
		RespawnBlocksExplode: ldat.RespawnBlocksExplode, DoDayLightCycle: ldat.DoDayLightCycle, DoEntityDrops: ldat.DoEntityDrops, MobGriefing: ldat.MobGriefing,
		DoMobSpawning: ldat.DoMobSpawning,
		PlayersSleepingPercentage: ldat.PlayersSleepingPercentage,
	}
	s.ticker = world.NewScheduler(s.conf.TickCores)
	s.ticker.SetPanicHandler(s.panicked)
	s.ticker.SetCurrentTick(ldat.CurrentTick)
	s.clock = daytime.NewClock(s.ticker.CurrentTick, int32(min(ldat.Time, math.MaxInt32)), ldat.DoDayLightCycle) //nolint:gosec // capped
	if err := s.openOverworld(worldType, gt, ldat.RandomSeed, ldat.FlatWorldLayers, ldat.RandomTickSpeed, ldat.DoFireTick); err != nil {
		s.ticker.Close()
		_ = db.Close()
		return fmt.Errorf("helix: open world %q: %w", dir, err)
	}
	if s.circuits != nil {
		s.circuits.SetClock(s.clock)
	}
	if s.farm != nil {
		s.farm.SetClock(s.clock)
	}
	s.log.Info("World opened", "dir", dir, "name", s.level.Name, "new", isNew, "type", worldType, "seed", ldat.RandomSeed)
	return nil
}

// worldType picks the world type: the configured one, else the one level.dat
// names, else vanilla for a new world. Its level.dat number is filled in.
func (s *Server) worldType(level int32, isNew bool) (string, generator.Type, error) {
	types := s.conf.Generators
	if types == nil {
		types = generator.Default()
	}
	name := s.conf.WorldType
	switch {
	case name != "":
	case isNew:
		name = generator.NameVanilla
	default:
		name = generator.ForLevel(level)
	}
	t, ok := types[name]
	if !ok || t.New == nil {
		return "", generator.Type{}, fmt.Errorf("unknown world type %q (have %s)", name, strings.Join(types.Names(), ", "))
	}
	if t.Level == 0 {
		t.Level = generator.LevelInfinite
	}
	if !isNew && s.conf.WorldType != "" && t.Level != level {
		s.log.Warn("the world was made as another type: chunks it has not saved yet are made as the configured type", "type", name, "level.dat", generator.ForLevel(level))
	}
	return name, t, nil
}

// openOverworld starts the overworld with the world type's generator.
func (s *Server) openOverworld(typeName string, gt generator.Type, seed int64, flatLayers string, randomTickSpeed int32, doFireTick bool) error {
	conf := world.DefaultConfig()
	conf.Log, conf.Dim, conf.Store = s.log.With("part", "world"), mcdb.Overworld, s.worldStore
	conf.Blocks, conf.Biomes, conf.SimulationDistance = s.reg.Blocks, s.reg.Biomes, s.conf.TickDistance
	conf.EntityViewRadius2 = s.conf.EntityViewRadius2
	conf.Scheduler, conf.RandomTickSpeed = s.ticker, randomTickSpeed
	if err := s.openPortals(); err != nil {
		return err
	}
	conf.Ticker = s.conf.BlockTicker
	if conf.Ticker == nil {
		table, err := connect.New(s.reg.Blocks)
		if err != nil {
			return err
		}
		attached, err := attach.New(s.reg.Blocks)
		if err != nil {
			return err
		}
		rules := fire.Rules{DoFireTick: doFireTick, TNTExplodes: s.level.TNTExplodes, Difficulty: s.level.Difficulty}
		liquids := liquid.New(s.reg.Blocks)
		liquids.SetDrops(s.popBlock)
		s.farm = farm.NewTicker(s.reg, s.fallBlock, liquids)
		s.farm.SetSupport(attached.Supports)
		s.fire = fire.NewTicker(s.reg, attached, rules, s.primeTNT, s.farm)
		falls, err := fall.New(s.reg.Blocks, attached.Supports)
		if err != nil {
			return err
		}
		s.falling = falls
		circuits, err := circuit.NewTicker(s.reg, attached, s.fallBlock, fall.NewTicker(falls, s.startFalling, s.destroySnow, attach.NewTicker(attached, s.fallBlock, blockuse.NewTicker(s.reg.Blocks, connect.NewTicker(table, portal.NewTicker(s.portals, s.dimensionOf, s.fire))))))
		if err != nil {
			return err
		}
		circuits.SetSettings(s.redstone)
		conf.Ticker = circuits
		s.circuits = circuits
	}
	r, _ := mcdb.Overworld.Range()
	conf.Generator = s.conf.Generator
	if conf.Generator == nil {
		g, err := gt.New(generator.Context{
			Registry: s.reg, Seed: seed, Dimension: mcdb.Overworld, Range: r,
			Options: s.conf.WorldTypeOptions, FlatLayers: flatLayers, Log: s.log,
		})
		if err != nil {
			return fmt.Errorf("world type %s: %w", typeName, err)
		}
		conf.Generator = g
	}
	switch g := conf.Generator.(type) {
	case world.Flat:
		top := int32(g.Bottom)
		for _, l := range g.Layers {
			top += int32(l.Count) //nolint:gosec // layer counts are small
		}
		s.level.Surface = top
	case world.Spawner:
		// The spawn is out of range until BDS's spawn search places it, which is not
		// ported: players start on the ground at the spawn column instead.
		if sp := s.level.Spawn; sp[1] < int32(r[0]) || sp[1] > int32(r[1]) {
			s.level.Surface = g.SpawnHeight(sp[0], sp[2])
		}
	}
	if s.farm != nil {
		if v, ok := conf.Generator.(*generator.Vanilla); ok {
			s.farm.SetTrees(v)
		} else {
			s.farm.SetTrees(generator.NewBackgroundTrees(s.reg, seed, s.log))
		}
	}
	climate, _ := conf.Generator.(weather.Climate)
	s.chunkWeather = weather.NewTicker(weather.Config{Registry: s.reg, Weather: s.weather, Climate: climate, Strike: s.strike, Mobs: s.mobsIn})
	conf.Weather = s.chunkWeather
	conf.Spawner = mobSpawner{s}
	if s.fire != nil {
		s.fire.SetWeather(s.chunkWeather)
	}
	if s.farm != nil {
		s.farm.SetWeather(s.weather, s.chunkWeather)
	}
	if s.circuits != nil {
		s.circuits.SetWeather(s.weather)
	}
	d, err := world.New(conf)
	if err != nil {
		return err
	}
	s.overworld = d
	conf.Weather = nil
	return s.openOtherDimensions(conf, typeName, seed)
}

// dimensionOf is BDS's number of an open dimension. Runs: anywhere once the world is
// open.
func (s *Server) dimensionOf(d *world.Dimension) mcdb.Dimension {
	switch {
	case d != nil && d == s.nether:
		return mcdb.Nether
	case d != nil && d == s.end:
		return mcdb.End
	}
	return mcdb.Overworld
}

// openPortals loads the portal records from the world's portals key.
func (s *Server) openPortals() error {
	blocks, err := portal.NewBlocks(s.reg.Blocks)
	if err != nil {
		return err
	}
	value, err := s.worldStore.LoadLevel(mcdb.LevelKeyPortals)
	if err != nil && !errors.Is(err, mcdb.ErrNotFound) {
		return fmt.Errorf("helix: load portals: %w", err)
	}
	store := s.worldStore
	records, err := portal.LoadRecords(value, func(v []byte) {
		if err := store.StoreLevel(mcdb.LevelKeyPortals, v); err != nil {
			s.log.Error("save portal records", "err", err)
		}
	})
	if err != nil {
		s.log.Warn("portal records unreadable; starting without them", "err", err)
	}
	s.portals = &portal.Portals{Blocks: blocks, Records: records}
	return nil
}

// openOtherDimensions opens the Nether and the End beside the overworld, with the
// overworld's settings. They get vanilla terrain whatever the overworld's type, as a
// BDS flat world still has a normal Nether and End; a void world's are void too.
func (s *Server) openOtherDimensions(conf world.Config, typeName string, seed int64) error {
	for _, dim := range []mcdb.Dimension{mcdb.Nether, mcdb.End} {
		r, _ := dim.Range()
		var gen world.Generator = world.Empty{}
		if typeName != generator.NameVoid {
			g, err := generator.Default()[generator.NameVanilla].New(generator.Context{
				Registry: s.reg, Seed: seed, Dimension: dim, Range: r, Log: s.log,
			})
			if err != nil {
				return err
			}
			gen = g
		}
		conf.Dim, conf.Generator = dim, gen
		conf.Log = s.log.With("part", "world", "dimension", dim.String())
		d, err := world.New(conf)
		if err != nil {
			return err
		}
		if dim == mcdb.Nether {
			s.nether = d
		} else {
			s.end, s.endGen = d, gen
		}
	}
	return nil
}

// closeWorld saves and closes the stores. Replaced stores are closed too:
// the server owns every store it was given.
func (s *Server) closeWorld() error {
	var errs []error
	for _, d := range []*world.Dimension{s.overworld, s.nether, s.end} {
		if d != nil {
			errs = append(errs, d.Close())
		}
	}
	if s.ticker != nil {
		// level.dat keeps the world's tick, which saved tick times count in.
		if s.db != nil {
			s.db.LevelDat().CurrentTick = s.ticker.CurrentTick()
			errs = append(errs, s.saveClock())
		}
		s.ticker.Close()
	}
	if st := s.conf.Storage.World; st != nil {
		errs = append(errs, st.Close())
	}
	if st, ok := s.conf.Storage.Players.(interface{ Close() error }); ok {
		errs = append(errs, st.Close())
	}
	if s.db != nil {
		errs = append(errs, s.db.Close())
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("helix: close world: %w", err)
	}
	return nil
}

// WorldStore is the store of the world's chunks and level keys. It is set
// while Run runs.
func (s *Server) WorldStore() mcdb.WorldStore { return s.worldStore }

// PlayerStore is the store of the players' saved data. It is set while Run
// runs.
func (s *Server) PlayerStore() mcdb.PlayerStore { return s.playerStore }

// primeTNT lights the TNT block fire reached, once players can be served.
func (s *Server) primeTNT(r *world.Region, pos cube.Pos) {
	if host := s.players.Load(); host != nil {
		host.PrimeBlock(r, pos, nil)
	}
}

// Fire is the vanilla fire ticker: fire, soul fire and lava lighting fire. Nil when the
// server was given its own BlockTicker.
func (s *Server) Fire() *fire.Ticker { return s.fire }

// Farm is the vanilla farming ticker: farmland, crops, stems, bone meal and tilling. Nil when
// the server was given its own BlockTicker.
func (s *Server) Farm() *farm.Ticker { return s.farm }

// fallBlock breaks a block that lost what held it, once players can be served.
func (s *Server) fallBlock(r *world.Region, pos cube.Pos, block uint32) {
	if host := s.players.Load(); host != nil {
		host.FallBlock(r, pos, block)
	}
}

// startFalling turns a block into a falling block.
func (s *Server) startFalling(r *world.Region, pos cube.Pos, block uint32) bool {
	if host := s.players.Load(); host != nil {
		return host.StartFalling(r, pos, block)
	}
	return false
}

// destroySnow removes a snow layer with nothing to lie on.
func (s *Server) destroySnow(r *world.Region, pos cube.Pos, block uint32) {
	if host := s.players.Load(); host != nil {
		host.DestroySnow(r, pos, block)
	}
}

// popBlock throws out the drops of a block a liquid replaced.
func (s *Server) popBlock(r *world.Region, pos cube.Pos, block uint32) {
	if host := s.players.Load(); host != nil {
		host.PopBlock(r, pos, block)
	}
}

// saveLevel writes level.dat with the world tick and the time as they are now.
func (s *Server) saveLevel() error {
	if s.db == nil {
		return nil
	}
	s.db.LevelDat().CurrentTick = s.ticker.CurrentTick()
	if s.weather != nil {
		l, ldat := s.weather.Saved(), s.db.LevelDat()
		ldat.RainLevel, ldat.LightningLevel, ldat.RainTime, ldat.LightningTime = l.Rain, l.Thunder, l.RainTime, l.ThunderTime
	}
	if err := s.saveClock(); err != nil {
		return err
	}
	if err := s.db.SaveLevelDat(); err != nil {
		return fmt.Errorf("helix: %w", err)
	}
	return nil
}

// saveClock keeps the time as BDS does: the Time in level.dat, which is what it reads back, and
// the WorldClocks key.
func (s *Server) saveClock() error {
	if s.clock == nil {
		return nil
	}
	time := s.clock.Time()
	s.db.LevelDat().Time = int64(time)
	value, err := nbt.MarshalEncoding(map[string]any{"clocks": []any{map[string]any{
		"IsPaused": uint8(0), "Name": daytime.OverworldClockName, "Time": time,
	}}}, nbt.LittleEndian)
	if err != nil {
		return fmt.Errorf("helix: encode world clocks: %w", err)
	}
	return s.db.StoreLevel(mcdb.LevelKeyWorldClocks, value)
}

// Weather returns the world's weather. Valid once the world is open. Safe from any goroutine.
func (s *Server) Weather() *weather.Weather { return s.weather }

// ChunkWeather returns the overworld's lightning, ice and snow, nil before the overworld opens.
// Safe from any goroutine once the server runs.
func (s *Server) ChunkWeather() *weather.Ticker { return s.chunkWeather }

// broadcastLevelEvent sends a level event to everyone online, once players can join.
func (s *Server) broadcastLevelEvent(event, data int32) {
	if h := s.players.Load(); h != nil {
		h.BroadcastLevelEvent(event, data)
	}
}

// strike makes lightning, once players can join.
func (s *Server) strike(r *world.Region, pos mgl32.Vec3) {
	if h := s.players.Load(); h != nil {
		h.Strike(r, pos)
	}
}

// mobSpawner runs the players' natural mob spawner on every ticking chunk of every
// dimension, once the players' Host exists.
type mobSpawner struct{ s *Server }

// SpawnTick is world.ChunkSpawner's. Runs: owner of the region.
func (m mobSpawner) SpawnTick(r *world.Region, pos mcdb.ChunkPos) {
	if h := m.s.players.Load(); h != nil {
		h.SpawnTick(r, pos)
	}
}

// mobsIn finds what lightning may strike in a chunk.
func (s *Server) mobsIn(r *world.Region, pos mcdb.ChunkPos, dst []mgl32.Vec3) []mgl32.Vec3 {
	if h := s.players.Load(); h != nil {
		return h.MobsIn(r, pos, dst)
	}
	return dst
}
