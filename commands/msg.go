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

// msgCommand is /msg <targets> <message>: a private message to every player the targets name.
type msgCommand struct {
	Player  command.Targets
	Message command.Text
}

// Run sends a private message to each player named.
func (c msgCommand) Run(src command.Source, out *command.Output) {
	p, ok := player.SourcePlayer(src)
	if !ok {
		out.Error("commands.generic.runsFromConsoleAndChat")
		return
	}
	if c.Message == "" {
		out.Error("commands.msg.noMessage")
		return
	}
	sent := false
	for _, t := range c.Player.List {
		target, ok := t.(*player.Player)
		if !ok {
			continue
		}
		c.whisper(p, target)
		sent = true
	}
	if !sent {
		out.Error("commands.generic.player.notFound")
	}
}

// whisper sends the message from p to target, a copy back to p, and remembers p for /reply.
func (c msgCommand) whisper(p, target *player.Player) {
	target.Send(&packet.Text{TextType: packet.TextTypeWhisper, SourceName: p.Name(), Message: string(c.Message)})
	p.Send(&packet.Text{TextType: packet.TextTypeWhisper, SourceName: target.Name(), Message: string(c.Message)})
	target.SetLastWhisperFrom(p)
}
