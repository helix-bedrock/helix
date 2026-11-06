package player

import (
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Commands is the server's command manager, for registering commands at boot. Vanilla
// and plugins share it; RegisterCommand records who registered what.
func Commands(s *Setup) *command.Manager { return s.h.commands }

// RegisterCommand defines and registers a command for the Setup's owner.
func RegisterCommand(s *Setup, spec command.Spec) error { return s.h.commands.Add(s.owner, spec) }

// Commands is the server's command manager. Commands may be added, replaced and removed
// while running; online players get the new list at once. Runs: anywhere.
func (h *Host) Commands() *command.Manager { return h.commands }

// CommandSource is a player running a command. Commands get the player back with
// SourcePlayer.
type CommandSource struct{ p *Player }

// SourcePlayer is the player behind a command's source, if a player runs it.
func SourcePlayer(src command.Source) (*Player, bool) {
	if s, ok := src.(*CommandSource); ok && s.p != nil {
		return s.p, true
	}
	return nil, false
}

// Name is the player's name.
func (s *CommandSource) Name() string { return s.p.name }

// Permission is the player's command permission level.
func (s *CommandSource) Permission() command.Permission {
	return command.Permission(s.p.abilities.CommandPermission)
}

// Position is the player's feet.
func (s *CommandSource) Position() mgl32.Vec3 { return s.p.State().Position }

// Rotation is where the player looks.
func (s *CommandSource) Rotation() (yaw, pitch float32) {
	r := s.p.State().Rotation
	return r.Yaw, r.Pitch
}

// Targets resolves a selector over every player online, in any region.
func (s *CommandSource) Targets(sel command.Selector) []command.Target {
	return s.p.s.selectPlayers(s.p, s.Position(), sel)
}

// commandGameModes are the game mode names and letters a selector's m takes.
var commandGameModes = map[string]int32{
	"0": GameModeSurvival, "s": GameModeSurvival, "survival": GameModeSurvival,
	"1": GameModeCreative, "c": GameModeCreative, "creative": GameModeCreative,
	"2": GameModeAdventure, "a": GameModeAdventure, "adventure": GameModeAdventure,
	"5": GameModeDefault, "d": GameModeDefault, "default": GameModeDefault,
	"6": GameModeSpectator, "spectator": GameModeSpectator,
}

// selectPlayers resolves a selector to players: a name, @s and @initiator (the runner),
// @a and @e (everyone, until there are other entities), @p (nearest) and @r (random),
// with the arguments name, r, rm, x, y, z, m, l, lm, type and c. Players are read
// through State, so every region's are seen. self may be nil (the console).
func (h *Host) selectPlayers(self *Player, from mgl32.Vec3, sel command.Selector) []command.Target {
	if sel.Kind == "" {
		for _, o := range h.Online() {
			if strings.EqualFold(o.name, sel.Name) {
				return []command.Target{o}
			}
		}
		return nil
	}
	type found struct {
		p    *Player
		dist float32
	}
	var list []found
	if sel.Kind == "s" || sel.Kind == "initiator" {
		if self != nil {
			list = append(list, found{p: self})
		}
	} else {
		for _, o := range h.Online() {
			list = append(list, found{p: o})
		}
	}
	at := from
	for _, axis := range []struct {
		key string
		i   int
	}{{"x", 0}, {"y", 1}, {"z", 2}} {
		if a, ok := sel.Arg(axis.key); ok {
			if v, err := strconv.ParseFloat(strings.TrimPrefix(a.Value, "~"), 32); err == nil {
				if strings.HasPrefix(a.Value, "~") {
					at[axis.i] += float32(v)
				} else {
					at[axis.i] = float32(v)
				}
			}
		}
	}
	list = slices.DeleteFunc(list, func(f found) bool { return !matchesSelector(f.p, at, sel) })
	for i := range list {
		list[i].dist = list[i].p.State().Position.Sub(at).Len()
	}
	count := 0
	if a, ok := sel.Arg("c"); ok {
		count, _ = strconv.Atoi(a.Value)
	}
	switch sel.Kind {
	case "p":
		if count == 0 {
			count = 1
		}
	case "r":
		rand.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
		if count == 0 {
			count = 1
		}
	}
	if sel.Kind != "r" {
		slices.SortStableFunc(list, func(a, b found) int {
			switch {
			case a.dist < b.dist:
				return -1
			case a.dist > b.dist:
				return 1
			}
			return 0
		})
	}
	if count < 0 {
		slices.Reverse(list)
		count = -count
	}
	if count > 0 && count < len(list) {
		list = list[:count]
	}
	out := make([]command.Target, len(list))
	for i, f := range list {
		out[i] = f.p
	}
	return out
}

// matchesSelector checks a player against a selector's filters.
func matchesSelector(p *Player, at mgl32.Vec3, sel command.Selector) bool {
	st := p.State()
	dist := st.Position.Sub(at).Len()
	for _, a := range sel.Args {
		ok := true
		switch a.Key {
		case "name":
			ok = strings.EqualFold(p.name, a.Value)
		case "type":
			ok = a.Value == "player" || a.Value == "minecraft:player"
		case "m":
			mode, known := commandGameModes[strings.ToLower(a.Value)]
			ok = known && mode == st.GameMode
		case "r", "rm", "l", "lm":
			v, err := strconv.ParseFloat(a.Value, 32)
			if err != nil {
				return false
			}
			switch a.Key {
			case "r":
				ok = dist <= float32(v)
			case "rm":
				ok = dist >= float32(v)
			case "l":
				ok = float64(st.Level) <= v
			case "lm":
				ok = float64(st.Level) >= v
			}
		default:
			continue
		}
		if ok == a.Negated {
			return false
		}
	}
	return true
}

// setUpCommands fills the item and block lists and tells online players when commands
// change.
func (h *Host) setUpCommands() {
	m := h.commands
	var items []string
	h.reg.Items.Each(func(t registry.ItemType) { items = appendCommandNames(items, t.Name) })
	m.SetEnumOptions(command.EnumItem, items)
	var blocks []string
	seen := map[string]bool{}
	for id := range uint32(h.reg.Blocks.Count()) { //nolint:gosec // block counts are small
		if name := h.reg.Blocks.Name(id); !seen[name] {
			seen[name] = true
			blocks = appendCommandNames(blocks, name)
		}
	}
	m.SetEnumOptions(command.EnumBlock, blocks)
	// BDS's EntityType list has the short name first, its Biome list only full names.
	var entities []string
	for _, e := range h.reg.EntityTypes.All() {
		if short, ok := strings.CutPrefix(e.Name, "minecraft:"); ok {
			entities = append(entities, short)
		}
		entities = append(entities, e.Name)
	}
	m.SetEnumOptions(command.EnumEntityType, entities)
	var biomes []string
	for _, b := range h.reg.Biomes.All() {
		biomes = append(biomes, b.Name)
	}
	m.SetEnumOptions(command.EnumBiome, biomes)
	m.OnChange(func() {
		for _, p := range h.Online() {
			p.sendNow(m.Packet(&CommandSource{p: p}))
		}
	})
	m.OnEnumChange(func(enumType string) {
		for _, p := range h.Online() {
			p.sendNow(m.SoftEnumUpdate(enumType, &CommandSource{p: p}))
		}
	})
}

// appendCommandNames adds a name as BDS's Item and Block lists have it: the
// full name, then for the minecraft namespace the short one.
func appendCommandNames(list []string, name string) []string {
	list = append(list, name)
	if short, ok := strings.CutPrefix(name, "minecraft:"); ok {
		list = append(list, short)
	}
	return list
}

// commandsPacket is the command list the player is sent.
func (p *Player) commandsPacket() *packet.AvailableCommands {
	return p.s.commands.Packet(&CommandSource{p: p})
}

// handleCommandRequest runs a command a player typed and sends what it said back as BDS
// does, in a CommandOutput.
func handleCommandRequest(p *Player, pk *packet.CommandRequest) {
	out := p.s.commands.Execute(&CommandSource{p: p}, pk.CommandLine)
	msgs := out.Messages()
	if len(msgs) == 0 {
		return
	}
	res := &packet.CommandOutput{CommandOrigin: pk.CommandOrigin, OutputType: packet.CommandOutputTypeAllOutput, SuccessCount: out.Successes()}
	for _, m := range msgs {
		res.OutputMessages = append(res.OutputMessages, protocol.CommandOutputMessage{Success: m.Success, Message: m.Text, Parameters: m.Params})
	}
	p.send(res)
	p.flush()
}
