package network

import (
	"fmt"
	"strings"

	"github.com/helix-bedrock/protocol"
)

// Status is what the server list shows.
type Status struct {
	// Name is the first line, the MOTD.
	Name string
	// LevelName is the second line on LAN.
	LevelName string
	// Players and MaxPlayers are the player counts.
	Players, MaxPlayers int
	// GameMode is the game mode name ("Survival", "Creative", ...).
	GameMode string
	// GameModeID is the game mode's number.
	GameModeID int
	// ServerID identifies the server to clients that ping it.
	ServerID int64
	// Port and PortV6 are the IPv4 and IPv6 ports.
	Port, PortV6 uint16
}

// pong is the status in the RakNet pong format, which NetherNet's status
// endpoint reads too. ';' would end a field, so it is removed from names.
func (s Status) pong() []byte {
	clean := func(v string) string { return strings.ReplaceAll(v, ";", "") }
	return fmt.Appendf(nil, "MCPE;%s;%d;%s;%d;%d;%d;%s;%s;%d;%d;%d;0;",
		clean(s.Name), protocol.CurrentProtocol, protocol.CurrentVersion, s.Players, s.MaxPlayers,
		s.ServerID, clean(s.LevelName), clean(s.GameMode), s.GameModeID, s.Port, s.PortV6)
}
