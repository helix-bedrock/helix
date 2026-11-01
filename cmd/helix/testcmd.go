package main

import (
	"strings"

	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/protocol/packet"
)

// testModes are the names /gamemode takes, as BDS's command accepts them.
var testModes = map[string]int32{
	"s": helix.GameModeSurvival, "survival": helix.GameModeSurvival, "0": helix.GameModeSurvival,
	"c": helix.GameModeCreative, "creative": helix.GameModeCreative, "1": helix.GameModeCreative,
	"a": helix.GameModeAdventure, "adventure": helix.GameModeAdventure, "2": helix.GameModeAdventure,
	"d": helix.GameModeDefault, "default": helix.GameModeDefault, "5": helix.GameModeDefault,
	"spectator": helix.GameModeSpectator, "6": helix.GameModeSpectator,
}

// testCommands is a stand-in until commands are designed: /gamemode for
// anyone, so break and place can be tested in game. Only with -testcmds.
func testCommands(s *helix.Setup) error {
	return helix.Handle(s.As("testcmds"), func(p *helix.Player, pk *packet.CommandRequest) {
		args := strings.Fields(strings.TrimPrefix(pk.CommandLine, "/"))
		if len(args) == 2 && (args[0] == "gamemode" || args[0] == "gm") {
			if mode, ok := testModes[strings.ToLower(args[1])]; ok {
				p.SetGameMode(mode)
			}
		}
	})
}
