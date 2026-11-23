package commands

import (
	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/protocol/packet"
)

// msgSpec is the /msg command spec.
var msgSpec = command.Spec{
	Name:        "msg",
	Description: "commands.msg.description",
	Overloads: []command.Runnable{
		msgCommand{},
	},
}

// msgCommand is /msg <player> <message>
type msgCommand struct {
	Player  command.Target
	Message string
}

// Run sends a private message to a player.
func (c msgCommand) Run(src command.Source, out *command.Output) {
	p, ok := player.SourcePlayer(src)
	if !ok {
		out.Error("commands.generic.runsFromConsoleAndChat")
		return
	}

	target, ok := c.Player.(*player.Player)
	if !ok {
		out.Error("commands.generic.player.notFound")
		return
	}

	if c.Message == "" {
		out.Error("commands.msg.noMessage")
		return
	}

	// Send private message to target
	privateMsg := &packet.Text{
		TextType:        packet.TextTypeWhisper,
		NeedsTranslation: false,
		SourceName:      p.Name(),
		Message:         c.Message,
	}
	target.Send(privateMsg)

	// Send confirmation to sender
	confirmMsg := &packet.Text{
		TextType:        packet.TextTypeWhisper,
		NeedsTranslation: false,
		SourceName:      target.Name(),
		Message:         c.Message,
	}
	p.Send(confirmMsg)

	// Track last whisper sender for /reply
	target.SetLastWhisperFrom(p)

	out.Success("commands.msg.success", target.Name())
}
