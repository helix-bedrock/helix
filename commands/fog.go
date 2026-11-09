package commands

import (
	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/player"
)

// BDS 1.26.60's /fog (FogCommand: push 0x9FB0490, pop 0x9FB0A40, remove 0x9FB1010): fog
// <victim> push <fogId> [userProvidedId], fog <victim> pop|remove [userProvidedId]. Fog IDs
// are not checked against the resource packs' fog definitions, which BDS does.

var fog = command.Spec{
	Name: "fog", Description: "commands.fog.description", Permission: command.GameDirectors,
	Overloads: []command.Runnable{fogPush{}, fogPop{}, fogRemove{}},
}

// fogPush is /fog <victim> push <fogId> [userProvidedId].
type fogPush struct {
	Victim command.Targets
	Push   command.Literal          `cmd:"push"`
	FogID  string                   `cmd:"fogId"`
	User   command.Optional[string] `cmd:"userProvidedId"`
}

// fogPop is /fog <victim> pop [userProvidedId].
type fogPop struct {
	Victim command.Targets
	Pop    command.Literal          `cmd:"pop"`
	User   command.Optional[string] `cmd:"userProvidedId"`
}

// fogRemove is /fog <victim> remove [userProvidedId].
type fogRemove struct {
	Victim command.Targets
	Remove command.Literal          `cmd:"remove"`
	User   command.Optional[string] `cmd:"userProvidedId"`
}

// eachVictim runs fn on each player target on its own region. The runner is told of success
// at once; a victim that refuses (full stack, no such user ID) is reported to a player runner
// when its region gets to it.
func eachVictim(src command.Source, t command.Targets, out *command.Output, fn func(p *player.Player) bool, done, refused, param string) {
	runner, _ := player.SourcePlayer(src)
	found := false
	for _, v := range t.List {
		p, ok := v.(*player.Player)
		if !ok {
			continue
		}
		found = true
		name := p.Name()
		p.Run(func(p *player.Player) {
			if !fn(p) && runner != nil {
				runner.Run(func(r *player.Player) { r.MessageTranslated(refused, param, name) })
			}
		})
	}
	if !found {
		out.Fail("commands.generic.noTargetMatch")
		return
	}
	out.Success(done, param)
}

// Run pushes the fog on each victim.
func (c fogPush) Run(src command.Source, out *command.Output) {
	user := c.User.Value
	eachVictim(src, c.Victim, out, func(p *player.Player) bool { return p.PushFog(c.FogID, user) },
		"commands.fog.success.push", "commands.fog.limitReached", c.FogID)
}

// Run pops a fog off each victim.
func (c fogPop) Run(src command.Source, out *command.Output) {
	user := c.User.Value
	eachVictim(src, c.Victim, out, func(p *player.Player) bool { return p.PopFog(user) },
		"commands.fog.success.pop", "commands.fog.invalidUserId", user)
}

// Run removes fogs from each victim.
func (c fogRemove) Run(src command.Source, out *command.Output) {
	user := c.User.Value
	eachVictim(src, c.Victim, out, func(p *player.Player) bool { return p.RemoveFogs(user) },
		"commands.fog.success.remove", "commands.fog.invalidUserId", user)
}
