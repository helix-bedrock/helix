package main

import (
	"fmt"

	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/player"
)

// tpsCommand is the test command that shows the tick rates.
const tpsCommand = "tps"

// tpsRun shows the server's TPS, the TPS of the player's region and of the player's client.
// With "live" it turns a readout above the hotbar on or off that keeps them up to date.
func tpsRun(srv *helix.Server, p *player.Player, args []string) string {
	if len(args) > 0 && args[0] == "live" {
		if p.ShowingTPS() {
			p.ShowTPS(nil)
			return "Live TPS off."
		}
		p.ShowTPS(srv.TPS)
		return "Live TPS on; /tps live again turns it off."
	}
	return fmt.Sprintf("Server %.1f TPS, your region %.1f TPS, your client %.1f TPS", srv.TPS(), p.RegionTPS(), p.TPS())
}
