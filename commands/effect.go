package commands

import (
	"strconv"

	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/player"
)

// BDS 1.26.60's /effect (EffectCommand, setup 0x9FA2660): effect <player> clear [effect],
// effect <player> <effect> [seconds] [amplifier] [hideParticles], and effect <player>
// <effect> infinite [amplifier] [hideParticles]. Read from BDS: the three overloads and
// their argument names. The defaults (30 seconds, level I) and limits are vanilla's
// documented ones, not read from the execute code yet.

const (
	// effectSeconds is how long an effect lasts when no seconds are given.
	effectSeconds = 30
	// effectMaxSeconds and effectMaxAmplifier are the most the arguments take.
	effectMaxSeconds   = 1000000
	effectMaxAmplifier = 255
	ticksPerSecond     = 20
)

var effectSpec = command.Spec{
	Name: "effect", Description: "commands.effect.description", Permission: command.GameDirectors,
	Overloads: []command.Runnable{effectClear{}, effectGive{}, effectInfinite{}},
}

// effectClear is /effect <player> clear [effect].
type effectClear struct {
	Player command.Targets
	Clear  command.Literal `cmd:"clear"`
	Effect command.Optional[command.Effect]
}

// effectGive is /effect <player> <effect> [seconds] [amplifier] [hideParticles].
type effectGive struct {
	Player        command.Targets
	Effect        command.Effect
	Seconds       command.Optional[int32]
	Amplifier     command.Optional[int32]
	HideParticles command.Optional[bool] `cmd:"hideParticles"`
}

// effectInfinite is /effect <player> <effect> infinite [amplifier] [hideParticles].
type effectInfinite struct {
	Player        command.Targets
	Effect        command.Effect
	Infinite      command.Literal `cmd:"infinite"`
	Amplifier     command.Optional[int32]
	HideParticles command.Optional[bool] `cmd:"hideParticles"`
}

// Run takes the effect, or every effect, from each target.
func (c effectClear) Run(src command.Source, out *command.Output) {
	var id int32
	var desc string
	if c.Effect.Set {
		e, ok := effectOf(c.Player, string(c.Effect.Value), out)
		if !ok {
			return
		}
		id, desc = e.ID, "%"+e.Description
	}
	onEach(src, out, c.Player, func(p *player.Player) (string, []string, bool) {
		if id == 0 {
			if len(p.Effects()) == 0 {
				return "commands.effect.failure.notActive.all", []string{p.Name()}, false
			}
			p.ClearEffects()
			return "commands.effect.success.removed.all", []string{p.Name()}, true
		}
		if _, ok := p.Effect(id); !ok {
			return "commands.effect.failure.notActive", []string{desc, p.Name()}, false
		}
		p.RemoveEffect(id)
		return "commands.effect.success.removed", []string{desc, p.Name()}, true
	})
}

// Run gives each target the effect for the seconds.
func (c effectGive) Run(src command.Source, out *command.Output) {
	seconds := int32(effectSeconds)
	if c.Seconds.Set {
		seconds = c.Seconds.Value
	}
	if !inRange(out, seconds, 0, effectMaxSeconds) {
		return
	}
	giveEffect(src, out, c.Player, string(c.Effect), seconds*ticksPerSecond, c.Amplifier, c.HideParticles, "commands.effect.success", strconv.Itoa(int(seconds)))
}

// Run gives each target the effect for good.
func (c effectInfinite) Run(src command.Source, out *command.Output) {
	giveEffect(src, out, c.Player, string(c.Effect), player.EffectInfinite, c.Amplifier, c.HideParticles, "commands.effect.success.infinite", "")
}

// giveEffect is the work of both giving overloads. Seconds of 0 takes the effect away, as
// vanilla's /effect does.
func giveEffect(src command.Source, out *command.Output, targets command.Targets, name string, ticks int32,
	amplifier command.Optional[int32], hide command.Optional[bool], key, seconds string) {
	amp := int32(0)
	if amplifier.Set {
		amp = amplifier.Value
	}
	if !inRange(out, amp, 0, effectMaxAmplifier) {
		return
	}
	e, ok := effectOf(targets, name, out)
	if !ok {
		return
	}
	desc, level := "%"+e.Description, strconv.Itoa(int(amp))
	onEach(src, out, targets, func(p *player.Player) (string, []string, bool) {
		if ticks == 0 {
			if _, ok := p.Effect(e.ID); !ok {
				return "commands.effect.failure.notActive", []string{desc, p.Name()}, false
			}
			p.RemoveEffect(e.ID)
			return "commands.effect.success.removed", []string{desc, p.Name()}, true
		}
		p.GiveEffect(player.ActiveEffect{ID: e.ID, Amplifier: amp, Duration: ticks, Hidden: hide.Set && hide.Value})
		params := []string{desc, level, p.Name()}
		if seconds != "" {
			params = append(params, seconds)
		}
		return key, params, true
	})
}

// effectOf finds the effect by its enum name through the first target's host.
func effectOf(targets command.Targets, name string, out *command.Output) (player.Effect, bool) {
	for _, t := range targets.List {
		if p, ok := t.(*player.Player); ok {
			if e, ok := p.Host().EffectNamed(name); ok {
				return e, true
			}
			break
		}
	}
	out.Fail("commands.effect.notFound", name)
	return player.Effect{}, false
}

// onEach runs fn on every target player, as /clear does: at once for one on the runner's
// own region, answered in the output; on its own region otherwise, the runner told then.
func onEach(src command.Source, out *command.Output, targets command.Targets, fn func(p *player.Player) (string, []string, bool)) {
	runner, isPlayer := player.SourcePlayer(src)
	here := isPlayer && runner.Entity() != nil
	n := 0
	for _, t := range targets.List {
		p, ok := t.(*player.Player)
		if !ok {
			continue
		}
		n++
		if here && p.State().Region == runner.Entity().Region() {
			key, params, ok := fn(p)
			if ok {
				out.Success(key, params...)
			} else {
				out.Fail(key, params...)
			}
			continue
		}
		p.Run(func(p *player.Player) {
			key, params, _ := fn(p)
			if isPlayer {
				runner.Run(func(r *player.Player) { r.MessageTranslated(key, params...) })
			}
		})
	}
	if n == 0 {
		out.Fail("commands.generic.noTargetMatch")
	}
}
