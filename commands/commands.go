// Package commands holds Helix's built-in commands, the ones BDS has, one file each. They
// use only the public player and command APIs, as a plugin would, and Register puts them
// on a Setup. A plugin turns one off with player.Commands(setup).Remove("give"), or
// replaces it by registering its own command of the same name.
package commands

import (
	"strconv"

	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/player"
)

// Owner is who the built-in commands are registered for.
const Owner = "minecraft"

// all are the built-in commands.
var all = []command.Spec{
	give,
	clearSpec,
	teleport,
	weatherSpec,
	toggleDownfall,
	fog,
	summon,
	effectSpec,
}

// Register puts every built-in command on a Setup.
func Register(s *player.Setup) error {
	for _, spec := range all {
		if err := player.Commands(s).Add(Owner, spec); err != nil {
			return err
		}
	}
	return nil
}

// inRange checks a number against BDS's limits, failing with its message.
func inRange(out *command.Output, v, lo, hi int32) bool {
	switch {
	case v < lo:
		out.Fail("commands.generic.num.tooSmall", strconv.Itoa(int(v)), strconv.Itoa(int(lo)))
		return false
	case v > hi:
		out.Fail("commands.generic.num.tooBig", strconv.Itoa(int(v)), strconv.Itoa(int(hi)))
		return false
	}
	return true
}
