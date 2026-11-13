package commands

import (
	"strings"

	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/player"
)

// BDS 1.26.60's /kill (KillCommand: names 0x9FCDF50, execute 0x9FCE090): kill [target],
// the runner without one. Every target is killed (Actor::kill) except creative players, which
// only add commands.kill.attemptKillPlayerCreative (the filter 0x9FEE2B0 -> 0xB654710).

var kill = command.Spec{
	Name: "kill", Description: "commands.kill.description", Permission: command.GameDirectors,
	Overloads: []command.Runnable{killOverload{}},
}

// killOverload is /kill [target].
type killOverload struct {
	Target command.Optional[command.Targets]
}

// Run kills the targets and names them.
func (c killOverload) Run(src command.Source, out *command.Output) {
	targets := killTargets(src, c.Target)
	var names []string
	creative := false
	for _, t := range targets {
		if p, ok := t.(*player.Player); ok && player.KillCreative(p) {
			creative = true
			continue
		}
		player.Kill(t)
		names = append(names, t.Name())
	}
	if creative {
		out.Fail("commands.kill.attemptKillPlayerCreative")
	}
	if len(names) == 0 {
		if !creative {
			out.Fail("commands.generic.noTargetMatch")
		}
		return
	}
	out.Success("commands.kill.successful", strings.Join(names, ", "))
}

// killTargets is the targets given, or the runner.
func killTargets(src command.Source, given command.Optional[command.Targets]) []command.Target {
	if given.Set {
		return given.Value.List
	}
	if p, ok := player.SourcePlayer(src); ok {
		return []command.Target{p}
	}
	return nil
}
