package main

import (
	"strconv"

	"github.com/helix-bedrock/helix"
)

// tickSpeedCommand is the test stand-in for /gamerule randomtickspeed, so crops, fire and the
// other random tick blocks can be watched quickly. Only with -testcmds.
const tickSpeedCommand = "tickspeed"

// tickSpeed runs /tickspeed <n>: every dimension's random tick speed becomes n.
func tickSpeed(srv *helix.Server, args []string) string {
	if len(args) != 1 {
		return "Use: /tickspeed <speed> (1 is BDS's default)"
	}
	n, err := strconv.ParseInt(args[0], 10, 32)
	if err != nil || n < 0 {
		return "The speed must be a whole number, 0 or more."
	}
	srv.SetRandomTickSpeed(int32(n))
	return "Random tick speed set to " + args[0] + " until the server stops."
}
