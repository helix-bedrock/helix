package helix

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	// The block registry is not built yet, so the DB opens without one:
	// level keys, players and level.dat work, chunks wait for it.
	db, err := mcdb.Config{Log: s.log.With("part", "storage"), KV: s.conf.Storage.KV}.Open(dir)
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
		spawn: [3]int32{ldat.SpawnX, ldat.SpawnY, ldat.SpawnZ},
	}
	s.log.Info("World opened", "dir", dir, "name", s.level.name, "new", isNew)
	return nil
}

// closeWorld saves and closes the stores. Replaced stores are closed too:
// the server owns every store it was given.
func (s *Server) closeWorld() error {
	var errs []error
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
