package main

import (
	"strings"
	"sync"

	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// testModeNames are the names /gamemode takes, as BDS's command accepts
// them, in the order the client lists them.
var testModeNames = []string{"survival", "creative", "adventure", "default", "spectator", "s", "c", "a", "d", "0", "1", "2", "5", "6"}

// testModes maps each name to its game mode.
var testModes = map[string]int32{
	"s": player.GameModeSurvival, "survival": player.GameModeSurvival, "0": player.GameModeSurvival,
	"c": player.GameModeCreative, "creative": player.GameModeCreative, "1": player.GameModeCreative,
	"a": player.GameModeAdventure, "adventure": player.GameModeAdventure, "2": player.GameModeAdventure,
	"d": player.GameModeDefault, "default": player.GameModeDefault, "5": player.GameModeDefault,
	"spectator": player.GameModeSpectator, "6": player.GameModeSpectator,
}

// testCommandList is the AvailableCommands packet that makes the client
// show and complete /gamemode and /gm.
func testCommandList() *packet.AvailableCommands {
	idx := make([]uint32, len(testModeNames))
	for i := range idx {
		idx[i] = uint32(i)
	}
	overload := protocol.CommandOverload{Parameters: []protocol.CommandParameter{{
		Name: "gameMode", Type: protocol.CommandArgValid | protocol.CommandArgEnum,
	}}}
	cmd := func(name string) protocol.Command {
		return protocol.Command{
			Name: name, Description: "Sets your game mode (test stand-in).",
			PermissionLevel: protocol.CommandPermissionLevelAny, AliasesOffset: ^uint32(0),
			Overloads: []protocol.CommandOverload{overload},
		}
	}
	return &packet.AvailableCommands{
		EnumValues: testModeNames,
		Enums:      []protocol.CommandEnum{{Type: "GameMode", ValueIndices: idx}},
		Commands:   []protocol.Command{cmd("gamemode"), cmd("gm")},
	}
}

// testCommands is a stand-in until commands are designed: /gamemode for
// anyone, so break and place can be tested in game. Only with -testcmds.
// The command list goes to each player with its first input.
func testCommands(s *helix.Setup) error {
	setup := s.As("testcmds").Player
	list := testCommandList()
	var sent sync.Map // *player.Player -> struct{}, so a rejoin gets it again
	if err := player.WrapHandle(setup, func(p *player.Player, pk *packet.PlayerAuthInput, next player.PacketHandler[*packet.PlayerAuthInput]) {
		if _, done := sent.LoadOrStore(p, struct{}{}); !done {
			// A failed write means the connection is closing; the read loop
			// ends the session, so there is nothing more to do here.
			_ = p.Conn().WritePacket(list)
		}
		next(p, pk)
	}); err != nil {
		return err
	}
	return player.Handle(setup, func(p *player.Player, pk *packet.CommandRequest) {
		args := strings.Fields(strings.TrimPrefix(pk.CommandLine, "/"))
		if len(args) == 2 && (args[0] == "gamemode" || args[0] == "gm") {
			if mode, ok := testModes[strings.ToLower(args[1])]; ok {
				p.SetGameMode(mode)
			}
		}
	})
}
