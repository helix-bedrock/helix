package main

import (
	"strings"

	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/protocol/packet"
)

// testModes are the names /gamemode takes, as BDS's command accepts them.
var testModes = map[string]int32{
	"s": player.GameModeSurvival, "survival": player.GameModeSurvival, "0": player.GameModeSurvival,
	"c": player.GameModeCreative, "creative": player.GameModeCreative, "1": player.GameModeCreative,
	"a": player.GameModeAdventure, "adventure": player.GameModeAdventure, "2": player.GameModeAdventure,
	"d": player.GameModeDefault, "default": player.GameModeDefault, "5": player.GameModeDefault,
	"spectator": player.GameModeSpectator, "6": player.GameModeSpectator,
}

// testCommands is a stand-in until commands are designed: /gamemode for
// anyone, so break and place can be tested in game. Only with -testcmds.
func testCommands(s *helix.Setup) error {
	return player.Handle(s.As("testcmds").Player, func(p *player.Player, pk *packet.CommandRequest) {
		args := strings.Fields(strings.TrimPrefix(pk.CommandLine, "/"))
		if len(args) == 2 && (args[0] == "gamemode" || args[0] == "gm") {
			if mode, ok := testModes[strings.ToLower(args[1])]; ok {
				p.SetGameMode(mode)
			}
		}
	})
}
