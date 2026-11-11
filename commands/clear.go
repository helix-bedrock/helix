package commands

import (
	"strconv"

	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/player"
)

// BDS 1.26.60's /clear (ClearCommand: setup 0xA02BD20, execute 0xA02C2B0): clear [player]
// [itemName] [data] [maxCount]. With no player it clears whoever runs it. data -1 matches any
// aux value, maxCount -1 takes every match and 0 only counts them (commands.clear.testing).
// It takes from the inventory, armour, offhand and the UI slots (cursor, crafting grid).
// Read from BDS: the keys, the four arguments and the lower limit of -1; the order the slots
// are emptied in is Helix's.

var clearSpec = command.Spec{
	Name: "clear", Description: "commands.clear.description", Permission: command.GameDirectors,
	Overloads: []command.Runnable{clearOverload{}},
}

// clearOverload is /clear's one overload, every argument optional.
type clearOverload struct {
	Player   command.Optional[command.Targets]
	ItemName command.Optional[command.Item] `cmd:"itemName"`
	Data     command.Optional[int32]
	MaxCount command.Optional[int32] `cmd:"maxCount"`
}

// clearRule is what one /clear takes.
type clearRule struct {
	// item is the item's name, "" for any; data its aux value, -1 for any.
	item string
	data int32
	// max is how many to take, -1 for all, 0 to only count.
	max int32
}

// Run clears every target. One on the runner's own region is cleared at once and answered
// in the output; one elsewhere is cleared on its own region's next tick, and a player who ran
// the command is told the result then (another source gets no line for it).
func (c clearOverload) Run(src command.Source, out *command.Output) {
	rule := clearRule{data: -1, max: -1}
	if c.Data.Set {
		rule.data = c.Data.Value
	}
	if c.MaxCount.Set {
		rule.max = c.MaxCount.Value
	}
	if !inRange(out, rule.data, -1, giveMax) || !inRange(out, rule.max, -1, 1<<31-1) {
		return
	}
	var targets []*player.Player
	if c.Player.Set {
		for _, t := range c.Player.Value.List {
			if p, ok := t.(*player.Player); ok {
				targets = append(targets, p)
			}
		}
	} else if p, ok := player.SourcePlayer(src); ok {
		targets = append(targets, p)
	}
	if len(targets) == 0 {
		out.Fail("commands.generic.noTargetMatch")
		return
	}
	if c.ItemName.Set {
		item, ok := targets[0].Host().NewItem(string(c.ItemName.Value), 1, 0)
		if !ok {
			out.Fail("commands.give.item.notFound", string(c.ItemName.Value))
			return
		}
		rule.item = item.Name
	}
	runner, isPlayer := player.SourcePlayer(src)
	here := isPlayer && runner.Entity() != nil
	for _, p := range targets {
		if here && p.State().Region == runner.Entity().Region() {
			key, params := clearPlayer(p, rule)
			if key == "commands.clear.failure.no.items" {
				out.Fail(key, params...)
			} else {
				out.Success(key, params...)
			}
			continue
		}
		p.Run(func(p *player.Player) {
			key, params := clearPlayer(p, rule)
			if isPlayer {
				runner.Run(func(r *player.Player) { r.MessageTranslated(key, params...) })
			}
		})
	}
}

// clearPlayer takes what the rule matches from a player and returns the message for it.
// Runs: the player's region.
func clearPlayer(p *player.Player, rule clearRule) (string, []string) {
	left := rule.max
	n := 0
	for _, inv := range [...]*player.Inventory{p.Inventory(), p.Armour(), p.Offhand(), p.UI()} {
		for i, size := 0, inv.Size(); i < size; i++ {
			s := inv.Item(i)
			if s.Empty() || rule.item != "" && s.Name != rule.item || rule.data != -1 && int32(s.Meta) != rule.data {
				continue
			}
			take := int32(s.Count)
			if rule.max >= 0 {
				if rule.max > 0 {
					take = min(take, left)
					left -= take
				}
			}
			n += int(take)
			if rule.max != 0 && take > 0 {
				s.Count -= uint8(take) //nolint:gosec // at most the stack's count
				if s.Count == 0 {
					s = player.ItemStack{}
				}
				inv.SetItem(i, s)
			}
			if rule.max > 0 && left == 0 {
				break
			}
		}
		if rule.max > 0 && left == 0 {
			break
		}
	}
	count := strconv.Itoa(n)
	switch {
	case n == 0:
		return "commands.clear.failure.no.items", []string{p.Name()}
	case rule.max == 0:
		return "commands.clear.testing", []string{p.Name(), count}
	}
	return "commands.clear.success", []string{p.Name(), count}
}
