package commands

import (
	"strconv"
	"strings"

	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/player"
)

// BDS 1.26.60's /give (GiveCommand: setup 0x9FC1530, execute 0x9FC1C30): give <player>
// <itemName> [amount] [data]. The components argument is not built yet.

// giveMax is the most amount and data take, as BDS checks them.
const giveMax = 0x7fff

var give = command.Spec{
	Name: "give", Description: "commands.give.description", Permission: command.GameDirectors,
	Overloads: []command.Runnable{giveOverload{}},
}

// giveOverload is /give's one overload.
type giveOverload struct {
	Player   command.Targets
	ItemName command.Item `cmd:"itemName"`
	Amount   command.Optional[int32]
	Data     command.Optional[int32]
}

// Run gives every target the items, each on its own region: whole stacks into the
// inventory, what does not fit dropped at the player.
func (c giveOverload) Run(_ command.Source, out *command.Output) {
	amount, data := int32(1), int32(0)
	if c.Amount.Set {
		amount = c.Amount.Value
	}
	if c.Data.Set {
		data = c.Data.Value
	}
	if !inRange(out, amount, 1, giveMax) || !inRange(out, data, 0, giveMax) {
		return
	}
	var names []string
	for _, t := range c.Player.List {
		p, ok := t.(*player.Player)
		if !ok {
			continue
		}
		item, ok := p.Host().NewItem(string(c.ItemName), 1, int16(data)) //nolint:gosec // checked above
		if !ok {
			out.Fail("commands.give.item.notFound", string(c.ItemName))
			return
		}
		names = append(names, p.Name())
		name := string(c.ItemName)
		p.Run(func(p *player.Player) { giveItems(p, item, amount, name) })
	}
	if len(names) == 0 {
		out.Fail("commands.generic.noTargetMatch")
		return
	}
	out.Success("commands.give.success", string(c.ItemName), strconv.Itoa(int(amount)), strings.Join(names, ", "))
}

// giveItems puts amount of an item on a player in whole stacks, drops the rest and tells
// the player. Runs: the player's region.
func giveItems(p *player.Player, item player.ItemStack, amount int32, name string) {
	most := int32(max(p.Host().MaxStack(item.Name), 1)) //nolint:gosec // stack sizes are small
	for left := amount; left > 0; {
		s := item
		s.Count = uint8(min(left, most)) //nolint:gosec // at most a max stack
		left -= int32(s.Count)
		if rest := p.Give(s); !rest.Empty() {
			p.Drop(rest)
		}
	}
	p.MessageTranslated("commands.give.successRecipient", name, strconv.Itoa(int(amount)))
}
