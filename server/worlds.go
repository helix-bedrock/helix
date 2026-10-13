package server

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/helix-bedrock/helix/server/player"
	"github.com/helix-bedrock/helix/server/world"
)

// WorldConfig describes a world loaded with Server.LoadWorld. Fields left
// zero take the server's settings.
type WorldConfig struct {
	// Dim is the world's dimension. Overworld if nil.
	Dim world.Dimension
	// Provider stores the world, for example a mcdb.DB opened on its own
	// folder. Nothing is stored if nil.
	Provider world.Provider
	// Generator generates new chunks. If nil, the server's generator for Dim.
	Generator world.Generator
	// ReadOnly stops the world from being saved.
	ReadOnly bool
}

// ErrWorldExists and ErrNoWorld are returned by LoadWorld and UnloadWorld.
var (
	ErrWorldExists = errors.New("server: a world with that name is loaded")
	ErrNoWorld     = errors.New("server: no world with that name is loaded")
)

// LoadWorld opens a world while the server runs and makes it known by name
// (Helix). It shares the server's events, pool and debug monitor. Players
// get there by being added to it, for example with Player.Respawn to it or
// through a plugin. Safe to call from anywhere but a transaction.
func (srv *Server) LoadWorld(name string, c WorldConfig) (*world.World, error) {
	if name == "" {
		return nil, errors.New("server: world name is empty")
	}
	if c.Dim == nil {
		c.Dim = world.Overworld
	}
	if c.Generator == nil {
		c.Generator = srv.conf.Generator(c.Dim)
	}
	srv.wmu.Lock()
	defer srv.wmu.Unlock()
	if _, ok := srv.extra[name]; ok || srv.builtIn(name) != nil {
		return nil, fmt.Errorf("%w: %q", ErrWorldExists, name)
	}
	logger := srv.conf.Log.With("world", name)
	w := world.Config{
		Log:                 logger,
		Dim:                 c.Dim,
		Provider:            c.Provider,
		Generator:           c.Generator,
		RandomTickSpeed:     srv.conf.RandomTickSpeed,
		ReadOnly:            c.ReadOnly,
		SaveInterval:        srv.conf.SaveInterval,
		ChunkUnloadInterval: srv.conf.ChunkUnloadInterval,
		ChunkLoadWorkers:    srv.conf.ChunkLoadWorkers,
		Entities:            srv.conf.Entities,
		Blocks:              srv.conf.Blocks,
		Events:              srv.conf.WorldEvents,
		Debug:               srv.conf.Debug,
		Pool:                srv.pool,
		SectionSize:         srv.conf.RegionSectionSize,
	}.New()
	srv.extra[name] = w
	logger.Info("Loaded world.")
	return w, nil
}

// UnloadWorld saves and closes a world loaded with LoadWorld. Its players
// are first moved to the overworld's spawn. The overworld, nether and end
// cannot be unloaded. Safe to call from anywhere but a transaction; it
// returns once the world is closed.
func (srv *Server) UnloadWorld(name string) error {
	srv.wmu.Lock()
	w, ok := srv.extra[name]
	delete(srv.extra, name)
	srv.wmu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrNoWorld, name)
	}
	srv.pmu.RLock()
	handles := make([]*world.EntityHandle, 0, len(srv.p))
	for _, p := range srv.p {
		handles = append(handles, p.handle)
	}
	srv.pmu.RUnlock()
	to, spawn := srv.world, srv.world.Spawn().Vec3Middle()
	for _, h := range handles {
		// A player whose transaction fails has left already.
		_, _ = player.Call(context.Background(), h, func(tx *world.Tx, p *player.Player) (struct{}, error) {
			if tx.World() == w {
				moved := tx.RemoveEntity(p)
				to.Do(func(tx *world.Tx) { tx.AddEntityAt(moved, spawn) })
			}
			return struct{}{}, nil
		})
	}
	err := w.Close()
	srv.conf.Log.Info("Unloaded world.", "world", name)
	return err
}

// WorldByName returns a loaded world: "overworld", "nether", "end" or the
// name a world was loaded with.
func (srv *Server) WorldByName(name string) (*world.World, bool) {
	srv.wmu.Lock()
	defer srv.wmu.Unlock()
	if w := srv.builtIn(name); w != nil {
		return w, true
	}
	w, ok := srv.extra[name]
	return w, ok
}

// Worlds returns every loaded world by name.
func (srv *Server) Worlds() map[string]*world.World {
	srv.wmu.Lock()
	defer srv.wmu.Unlock()
	out := maps.Clone(srv.extra)
	if out == nil {
		out = map[string]*world.World{}
	}
	out["overworld"], out["nether"], out["end"] = srv.world, srv.nether, srv.end
	return out
}

func (srv *Server) builtIn(name string) *world.World {
	switch name {
	case "overworld":
		return srv.world
	case "nether":
		return srv.nether
	case "end":
		return srv.end
	}
	return nil
}
