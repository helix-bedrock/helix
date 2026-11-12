package main

import (
	"fmt"

	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/player"
)

// tpsCommand is the test command that shows the tick rates.
const tpsCommand = "tps"

// tpsRun shows the server's TPS, the TPS of the player's region and of the player's client.
func tpsRun(srv *helix.Server, p *player.Player) string {
	return fmt.Sprintf("Server %.1f TPS, your region %.1f TPS, your client %.1f TPS", srv.TPS(), p.RegionTPS(), p.TPS())
}
