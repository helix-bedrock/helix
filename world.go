package helix

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/helix-bedrock/helix/world"
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

// level holds the level.dat values the server reads, copied once when the
// world opens so player goroutines never touch the DB's level.dat.
type level struct {
	name       string
	seed       int64
	gameMode   int32
	difficulty int32
	spawn      [3]int32
	generator  int32
	// surface is the Y a player stands on in a flat world, used while the
	// spawn has no height yet; 0 when unknown.
	surface int32
}

// Generator types level.dat stores, as BDS numbers them.
const (
	generatorOverworld int32 = 1
	generatorFlat      int32 = 2
	generatorVoid      int32 = 5
)

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
	}
	s.db, s.worldStore, s.playerStore = db, mcdb.WorldStore(db), mcdb.PlayerStore(db)
	if st := s.conf.Storage.World; st != nil {
		s.worldStore = st
	}
	if st := s.conf.Storage.Players; st != nil {
		s.playerStore = st
	}
	s.level = level{
		name: ldat.LevelName, seed: ldat.RandomSeed, gameMode: ldat.GameType, difficulty: ldat.Difficulty,
		spawn: [3]int32{ldat.SpawnX, ldat.SpawnY, ldat.SpawnZ}, generator: ldat.Generator,
	}
	if err := s.openOverworld(ldat.Generator, ldat.FlatWorldLayers); err != nil {
		_ = db.Close()
		return fmt.Errorf("helix: open world %q: %w", dir, err)
	}
	s.log.Info("World opened", "dir", dir, "name", s.level.name, "new", isNew)
	return nil
}

// openOverworld starts the overworld with the generator level.dat names.
// Vanilla terrain generation is not hooked up yet: until it is, chunks an
// infinite world has not saved are empty and never saved, so the world
// still gets real terrain once the generator is there.
func (s *Server) openOverworld(generator int32, flatLayers string) error {
	conf := world.DefaultConfig()
	conf.Log, conf.Dim, conf.Store = s.log.With("part", "world"), mcdb.Overworld, s.worldStore
	conf.Blocks, conf.Biomes, conf.SimulationDistance = s.reg.Blocks, s.reg.Biomes, s.conf.TickDistance
	r, _ := mcdb.Overworld.Range()
	switch {
	case s.conf.Generator != nil:
		conf.Generator = s.conf.Generator
	case generator == generatorFlat:
		flat, err := world.ParseFlat(flatLayers, s.reg.Blocks, s.reg.Biomes, r)
		if err != nil {
			return err
		}
		conf.Generator = flat
		top := int32(flat.Bottom)
		for _, l := range flat.Layers {
			top += int32(l.Count) //nolint:gosec // layer counts are small
		}
		s.level.surface = top
	case generator == generatorVoid:
		conf.Generator = world.Empty{}
	default:
		conf.Generator, conf.DiscardGenerated = world.Empty{}, true
		s.log.Warn("vanilla world generation is not hooked up yet: chunks this world has not saved are empty and are not saved", "generator", generator)
	}
	d, err := world.New(conf)
	if err != nil {
		return err
	}
	s.overworld = d
	return nil
}

// closeWorld saves and closes the stores. Replaced stores are closed too:
// the server owns every store it was given.
func (s *Server) closeWorld() error {
	var errs []error
	if s.overworld != nil {
		errs = append(errs, s.overworld.Close())
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

// playerIDs are the IDs BDS files a player's data under: the Xbox identity
// (derived from the XUID, so only signed-in players have it) and the UUID
// the client signs itself.
func playerIDs(identity, selfSigned, xuid string) mcdb.PlayerIDs {
	ids := mcdb.PlayerIDs{SelfSignedID: selfSigned}
	if xuid != "" {
		ids.MsaID = identity
	}
	return ids
}
