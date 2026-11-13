package commands

import (
	"strings"

	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/player"
)

// BDS 1.26.60's /summon, first overload only: summon <entityType> [spawnPos]. The
// rotation, spawn event and name tag overloads come with the rest of the mob work. Only
// mobs with a definition the mob runtime runs can be summoned; a player has to run it,
// since the console has no dimension yet.

var summon = command.Spec{
	Name: "summon", Description: "commands.summon.description", Permission: command.GameDirectors,
	Overloads: []command.Runnable{summonOverload{}},
}

// summonOverload is /summon <entityType> [spawnPos].
type summonOverload struct {
	EntityType command.EntityType `cmd:"entityType"`
	SpawnPos   command.Optional[command.Position]
}

// Run adds the mob at the position, the runner's feet by default, on the runner's region.
func (c summonOverload) Run(src command.Source, out *command.Output) {
	p, ok := player.SourcePlayer(src)
	if !ok {
		out.Fail("commands.generic.noTargetMatch")
		return
	}
	typ := string(c.EntityType)
	if !strings.Contains(typ, ":") {
		typ = "minecraft:" + typ
	}
	if !p.Host().CanSpawnMob(typ) {
		out.Fail("commands.summon.failed")
		return
	}
	pos := src.Position()
	if c.SpawnPos.Set {
		pos = c.SpawnPos.Value.Resolve(src)
	}
	yaw, _ := src.Rotation()
	p.Run(func(p *player.Player) {
		if d := p.Dimension(); d != nil {
			p.Host().SpawnMob(d, typ, pos, yaw)
		}
	})
	out.Success("commands.summon.success")
}
