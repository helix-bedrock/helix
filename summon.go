package helix

import (
	"strings"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/cmd"
	"github.com/helix-bedrock/helix/server/entity/mob"
	"github.com/helix-bedrock/helix/server/world"
)

// summonRunnable is /summon: it spawns a mob where the player stands, or
// at a position.
type summonRunnable struct {
	Entity   string                   `cmd:"entityType"`
	Position cmd.Optional[mgl64.Vec3] `cmd:"spawnPos"`
}

func (r summonRunnable) Run(src cmd.Source, o *cmd.Output, tx *world.Tx) {
	if tx == nil {
		o.Error("/summon must be run in game.")
		return
	}
	name := r.Entity
	if !strings.Contains(name, ":") {
		name = "minecraft:" + name
	}
	t, ok := tx.World().EntityRegistry().Lookup(name)
	mt, isMob := t.(*mob.Type)
	if !ok || !isMob || !mt.Def().Summonable {
		o.Errorf("Unknown mob %q.", r.Entity)
		return
	}
	pos := r.Position.LoadOr(src.Position())
	yaw := 0.0
	if e, ok := src.(world.Entity); ok {
		yaw = e.Rotation().Yaw()
	}
	tx.AddEntity(mt.Spawn(pos, yaw))
	o.Printf("Summoned %s.", name)
}
