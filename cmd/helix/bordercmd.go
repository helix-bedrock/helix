package main

import "github.com/helix-bedrock/helix"

// borderBlocksCommand turns the walls of border blocks on or off (a Helix switch, not in
// BDS). Only with -testcmds.
const borderBlocksCommand = "borderblocks"

// borderBlocks runs /borderblocks <on|off>.
func borderBlocks(srv *helix.Server, args []string) string {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return "Use: /borderblocks <on|off>"
	}
	srv.SetBorderBlocks(args[0] == "on")
	return "Border blocks are " + args[0] + " until the server stops."
}
