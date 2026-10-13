package server

import (
	"log/slog"

	"github.com/helix-bedrock/helix/server/entity/mob"
	"github.com/helix-bedrock/helix/server/world"
)

// withMobs adds every vanilla mob from helix-data to reg (Helix). If the
// data cannot be read, reg is returned without mobs and the error logged.
func withMobs(reg world.EntityRegistry, log *slog.Logger) world.EntityRegistry {
	out, err := mob.Registry(reg)
	if err != nil {
		log.Error("load mob definitions; mobs are disabled", "err", err)
	}
	return out
}

// mobSpawner returns the region tick that spawns and despawns mobs, or nil
// if natural spawning is off or the rules cannot be read.
func mobSpawner(conf Config) func(tx *world.Tx, tick int64) {
	if conf.DisableMobSpawning {
		return nil
	}
	sp, err := mob.NewSpawner(conf.Entities)
	if err != nil {
		conf.Log.Error("load spawn rules; mobs do not spawn naturally", "err", err)
		return nil
	}
	return sp.Tick
}
