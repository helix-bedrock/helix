package helix

import (
	"github.com/helix-bedrock/helix/server/cmd"
	"github.com/helix-bedrock/helix/server/player"
	"github.com/helix-bedrock/helix/server/world"
)

// gameModeRunnable is /gamemode: it changes the game mode of the player
// running it, or of the players targeted.
type gameModeRunnable struct {
	Mode    gameModeName               `cmd:"gameMode"`
	Targets cmd.Optional[[]cmd.Target] `cmd:"player"`
}

// gameModeName is a game mode as /gamemode takes it: a name, its short
// form or its number, as in vanilla.
type gameModeName string

func (gameModeName) Type() string { return "GameMode" }
func (gameModeName) Options(cmd.Source) []string {
	return []string{"survival", "creative", "adventure", "spectator", "s", "c", "a", "sp", "0", "1", "2", "6"}
}

var gameModes = map[gameModeName]struct {
	mode world.GameMode
	name string
}{
	"survival": {world.GameModeSurvival, "Survival"}, "s": {world.GameModeSurvival, "Survival"}, "0": {world.GameModeSurvival, "Survival"},
	"creative": {world.GameModeCreative, "Creative"}, "c": {world.GameModeCreative, "Creative"}, "1": {world.GameModeCreative, "Creative"},
	"adventure": {world.GameModeAdventure, "Adventure"}, "a": {world.GameModeAdventure, "Adventure"}, "2": {world.GameModeAdventure, "Adventure"},
	"spectator": {world.GameModeSpectator, "Spectator"}, "sp": {world.GameModeSpectator, "Spectator"}, "6": {world.GameModeSpectator, "Spectator"},
}

func (r gameModeRunnable) Run(src cmd.Source, o *cmd.Output, _ *world.Tx) {
	gm, ok := gameModes[r.Mode]
	if !ok {
		o.Errorf("Unknown game mode %q.", r.Mode)
		return
	}
	targets, given := r.Targets.Load()
	if !given {
		self, ok := src.(cmd.Target)
		if !ok {
			o.Error("Name a player when running /gamemode from the console.")
			return
		}
		targets = []cmd.Target{self}
	}
	for _, t := range targets {
		p, ok := t.(*player.Player)
		if !ok {
			continue
		}
		p.SetGameMode(gm.mode)
		p.Messagef("Your game mode has been set to %s.", gm.name)
		if p != src {
			o.Printf("Set %s's game mode to %s.", p.Name(), gm.name)
		}
	}
}
