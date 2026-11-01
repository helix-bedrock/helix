package helix

import (
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/helix/registry"
)

// Setup is the one boot builder (API_DESIGN.md, "Registry"): the registry
// (blocks, items, biomes, entity types) and the player part (packet
// handlers, movement, gameplay: player.Handle, player.OnBlockBreak, ...).
// Vanilla registers through it first, then Config.Setup: there is no back
// door. Everything is frozen before a transport opens.
type Setup struct {
	*registry.Setup
	// Player is the player part of the Setup.
	Player *player.Setup
}

// As returns the same Setup acting for owner, a plugin's name in the log.
func (s *Setup) As(owner string) *Setup {
	return &Setup{Setup: s.Setup.As(owner), Player: s.Player.As(owner)}
}
