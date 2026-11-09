package main

import (
	"strconv"

	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/player"
)

// lightningCommand is a test command for lightning: strike the player who runs it, or change
// how often it strikes in a thunderstorm. Only with -testcmds.
const lightningCommand = "lightning"

// lightning runs /lightning [odds <n>|reset]: no words strike the player; odds n makes a
// stormy chunk get lightning one tick in n; reset goes back to BDS's one in 100000.
func lightning(srv *helix.Server, p *player.Player, args []string) string {
	if len(args) == 0 {
		if !p.Host().StrikePlayer(p) {
			return "Could not strike you."
		}
		return "Striking you."
	}
	t := srv.ChunkWeather()
	if t == nil {
		return "There is no weather yet."
	}
	switch {
	case args[0] == "reset" && len(args) == 1:
		t.SetLightningOdds(0)
		return "Lightning is back to BDS's one in 100000 ticks per stormy chunk."
	case args[0] == "odds" && len(args) == 2:
		n, err := strconv.ParseInt(args[1], 10, 32)
		if err != nil || n < 1 {
			return "The odds must be a whole number, 1 or more."
		}
		t.SetLightningOdds(int32(n))
		return "Lightning strikes a stormy chunk one tick in " + args[1] + " until the server stops (needs /weather thunder)."
	}
	return "Use: /lightning (strikes you), /lightning odds <n>, or /lightning reset"
}
