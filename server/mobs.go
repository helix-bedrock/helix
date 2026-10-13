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
