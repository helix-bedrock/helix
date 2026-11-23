package player

import (
	"github.com/helix-bedrock/protocol/packet"
)

// handleText handles incoming chat messages from clients.
func handleText(p *Player, pk *packet.Text) {
	// Only handle chat messages (TextTypeChat is what clients send for chat)
	if pk.TextType != packet.TextTypeChat {
		return
	}
	if pk.Message == "" {
		return
	}

	// Broadcast message to all players
	msg := &packet.Text{
		TextType:        packet.TextTypeChat,
		NeedsTranslation: false,
		SourceName:      p.Name(),
		Message:         pk.Message,
		XUID:            p.XUID(),
	}

	// Send to everyone online
	for _, online := range p.s.Online() {
		online.send(msg)
	}
}
