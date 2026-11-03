package main

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// testModeNames are the names /gamemode takes, as BDS's command accepts
// them, in the order the client lists them.
var testModeNames = []string{"survival", "creative", "adventure", "default", "spectator", "s", "c", "a", "d", "0", "1", "2", "5", "6"}

// testModes maps each name to its game mode.
var testModes = map[string]int32{
	"s": player.GameModeSurvival, "survival": player.GameModeSurvival, "0": player.GameModeSurvival,
	"c": player.GameModeCreative, "creative": player.GameModeCreative, "1": player.GameModeCreative,
	"a": player.GameModeAdventure, "adventure": player.GameModeAdventure, "2": player.GameModeAdventure,
	"d": player.GameModeDefault, "default": player.GameModeDefault, "5": player.GameModeDefault,
	"spectator": player.GameModeSpectator, "6": player.GameModeSpectator,
}

// testCommandList is the AvailableCommands packet that makes the client
// show and complete /gamemode and /gm.
func testCommandList() *packet.AvailableCommands {
	idx := make([]uint32, len(testModeNames))
	for i := range idx {
		idx[i] = uint32(i)
	}
	chestIdx := make([]uint32, len(chestTestActions))
	for i := range chestIdx {
		chestIdx[i] = uint32(len(testModeNames) + len(itemTestActions) + i)
	}
	smithIdx := make([]uint32, len(smithTestActions))
	for i := range smithIdx {
		smithIdx[i] = uint32(len(testModeNames) + len(itemTestActions) + len(chestTestActions) + i)
	}
	recipeIdx := make([]uint32, len(recipeTestActions))
	for i := range recipeIdx {
		recipeIdx[i] = uint32(len(testModeNames) + len(itemTestActions) + len(chestTestActions) + len(smithTestActions) + i)
	}
	itemIdx := make([]uint32, len(itemTestActions))
	for i := range itemIdx {
		itemIdx[i] = uint32(len(testModeNames) + i)
	}
	overload := protocol.CommandOverload{Parameters: []protocol.CommandParameter{{
		Name: "gameMode", Type: protocol.CommandArgValid | protocol.CommandArgEnum,
	}}}
	itemOverload := protocol.CommandOverload{Parameters: []protocol.CommandParameter{
		{Name: "action", Type: protocol.CommandArgValid | protocol.CommandArgEnum | 1},
		{Name: "seconds", Type: protocol.CommandArgValid | protocol.CommandArgTypeString, Optional: true},
	}}
	cmd := func(name string) protocol.Command {
		return protocol.Command{
			Name: name, Description: "Sets your game mode (test stand-in).",
			PermissionLevel: protocol.CommandPermissionLevelAny, AliasesOffset: ^uint32(0),
			Overloads: []protocol.CommandOverload{overload},
		}
	}
	item := protocol.Command{
		Name: itemTestCommand, Description: "Tests the item API: private, life, server, timer, off.",
		PermissionLevel: protocol.CommandPermissionLevelAny, AliasesOffset: ^uint32(0),
		Overloads: []protocol.CommandOverload{itemOverload},
	}
	chest := protocol.Command{
		Name: chestTestCommand, Description: "Tests the chest API: refuse, rewrite, name, off.",
		PermissionLevel: protocol.CommandPermissionLevelAny, AliasesOffset: ^uint32(0),
		Overloads: []protocol.CommandOverload{{Parameters: []protocol.CommandParameter{
			{Name: "action", Type: protocol.CommandArgValid | protocol.CommandArgEnum | 2},
		}}},
	}
	smith := protocol.Command{
		Name: smithTestCommand, Description: "Tests the smithing API: clear, remove, change, reset.",
		PermissionLevel: protocol.CommandPermissionLevelAny, AliasesOffset: ^uint32(0),
		Overloads: []protocol.CommandOverload{{Parameters: []protocol.CommandParameter{
			{Name: "action", Type: protocol.CommandArgValid | protocol.CommandArgEnum | 3},
		}}},
	}
	recipe := protocol.Command{
		Name: recipeTestCommand, Description: "Tests the recipe API: clear, table, remove [item], change, add, reset, hide [item], hideall, show.",
		PermissionLevel: protocol.CommandPermissionLevelAny, AliasesOffset: ^uint32(0),
		Overloads: []protocol.CommandOverload{{Parameters: []protocol.CommandParameter{
			{Name: "action", Type: protocol.CommandArgValid | protocol.CommandArgEnum | 4},
			{Name: "item", Type: protocol.CommandArgValid | protocol.CommandArgTypeString, Optional: true},
		}}},
	}
	tune := protocol.Command{
		Name: tuneTestCommand, Description: "Tests the number API: <name> <value>, clear [name], server <name> <value>, serverclear [name].",
		PermissionLevel: protocol.CommandPermissionLevelAny, AliasesOffset: ^uint32(0),
		Overloads: []protocol.CommandOverload{{Parameters: []protocol.CommandParameter{
			{Name: "action", Type: protocol.CommandArgValid | protocol.CommandArgTypeString},
			{Name: "name", Type: protocol.CommandArgValid | protocol.CommandArgTypeString, Optional: true},
			{Name: "value", Type: protocol.CommandArgValid | protocol.CommandArgTypeString, Optional: true},
		}}},
	}
	return &packet.AvailableCommands{
		EnumValues: slices.Concat(testModeNames, itemTestActions, chestTestActions, smithTestActions, recipeTestActions),
		Enums: []protocol.CommandEnum{
			{Type: "GameMode", ValueIndices: idx}, {Type: "ItemTestAction", ValueIndices: itemIdx}, {Type: "ChestTestAction", ValueIndices: chestIdx},
			{Type: "SmithTestAction", ValueIndices: smithIdx}, {Type: "RecipeTestAction", ValueIndices: recipeIdx},
		},
		Commands: []protocol.Command{cmd("gamemode"), cmd("gm"), item, chest, smith, recipe, tune},
	}
}

// ticksPerSecond is the server's tick rate.
const ticksPerSecond = 20

// showItemTimer shows how long an item has left, as minutes and seconds, then
// the name of the player looking, so the tag proves each player gets their own.
// A player who turned the timer off gets no tag, on every item, within a second.
// Only with -testcmds. Items with a view never merge, so stacks do not join
// even for a player with the timer off.
func showItemTimer(i *player.ItemActor, settings func(*player.Player) *itemTestDrops) {
	i.SetView(func(p *player.Player, v *player.ItemView) {
		if settings(p).timerOff.Load() {
			return
		}
		if i.TicksLeft() == player.ItemLifetimeForever {
			v.Name = "never " + p.Name()
			return
		}
		left := i.TicksLeft() / ticksPerSecond
		v.Name = fmt.Sprintf("%d:%02d %s", left/60, left%60, p.Name())
	})
}

// tuneTestCommand is the command that tries the number API.
const tuneTestCommand = "tune"

// tuneTest runs /tune on the player who ran it, or on the server, and says what the
// player now runs on: <name> <value>, clear [name], server <name> <value>, serverclear
// [name]. Without a name, clear drops all.
func tuneTest(p *player.Player, args []string) string {
	host := p.Host()
	if len(args) == 1 && args[0] == "clear" {
		p.ClearAllTuning()
		return "Your numbers cleared."
	}
	if len(args) == 1 && args[0] == "serverclear" {
		host.ClearAllTuning()
		return "Server numbers cleared."
	}
	if len(args) == 2 && (args[0] == "clear" || args[0] == "serverclear") {
		t, ok := player.TuneByName(args[1])
		if !ok {
			return "No number called " + args[1]
		}
		if args[0] == "clear" {
			p.ClearTuning(t)
		} else {
			host.ClearTuning(t)
		}
		return fmt.Sprintf("%s is now %v.", t, p.Tuning(t))
	}
	server := len(args) == 3 && args[0] == "server"
	if server {
		args = args[1:]
	}
	if len(args) != 2 {
		return "/tune <name> <value>, clear [name], server <name> <value>, serverclear [name]"
	}
	t, ok := player.TuneByName(args[0])
	if !ok {
		return "No number called " + args[0]
	}
	v, err := strconv.ParseFloat(args[1], 32)
	if err != nil {
		return "Not a number: " + args[1]
	}
	if server {
		ok = host.SetTuning(t, float32(v))
	} else {
		ok = p.SetTuning(t, float32(v))
	}
	if !ok {
		return "Refused: " + args[1]
	}
	return fmt.Sprintf("%s is now %v.", t, p.Tuning(t))
}

// smithTestCommand is the command that tries the smithing API.
const smithTestCommand = "smithtest"

// smithTestSword is the recipe smithTestCommand removes and changes.
const smithTestSword = "minecraft:netherite_sword"

// smithTestActions are what smithTestCommand takes: clear every recipe, remove the
// netherite sword's, change it to make a golden sword, reset to what the server
// started with.
var smithTestActions = []string{"clear", "remove", "change", "reset"}

// smithTest runs the smithing test on the live recipes and says what happened.
func smithTest(reg *registry.Registry, start *packet.CraftingData, action string) string {
	if action == "reset" {
		reg.Crafting.Update(func(p *packet.CraftingData) {
			p.SmithingTransformRecipes, p.SmithingTrimRecipes = start.SmithingTransformRecipes, start.SmithingTrimRecipes
		})
		return "Smithing recipes reset."
	}
	var msg string
	err := reg.EditSmithing(func(s *registry.Smithing) error {
		switch action {
		case "clear":
			s.Clear()
			msg = "Smithing recipes cleared."
		case "remove":
			msg = fmt.Sprintf("Removed %d recipes for the netherite sword.", s.RemoveFor(smithTestSword))
		case "change":
			msg = "No netherite sword recipe to change."
			for _, r := range s.For(smithTestSword) {
				r.Result = "minecraft:golden_sword"
				msg = "The netherite sword recipe now makes a golden sword."
				if err := s.Set(r); err != nil {
					return err
				}
			}
		default:
			msg = "Use clear, remove, change or reset."
		}
		return nil
	})
	if err != nil {
		return err.Error()
	}
	return msg
}

// recipeTestCommand is the command that tries the recipe API.
const recipeTestCommand = "recipetest"

// recipeTestStick is the item recipeTestCommand changes, and removes when not told
// another.
const recipeTestStick = "minecraft:stick"

// recipeTestActions are what recipeTestCommand takes: clear every recipe, table clears
// the crafting table's, remove the recipes of an item, change the stick to make apples,
// add a recipe making a diamond from a stick and an apple, reset to what the server
// started with.
//
// hide, hideall and show act on the player who runs the command only: hide takes away
// an item's recipes, hideall every recipe, show gives them back.
var recipeTestActions = []string{"clear", "table", "remove", "change", "add", "reset", "hide", "hideall", "show"}

// recipeTestPlayer runs the actions on the player's own recipes. It reports false for
// any other action.
func recipeTestPlayer(p *player.Player, action, item string) (string, bool) {
	switch action {
	case "hide":
		p.EditRecipes(func(r *player.PlayerRecipes) { r.RemoveFor(item) })
		return "Took " + item + " recipes from you only.", true
	case "hideall":
		p.EditRecipes(func(r *player.PlayerRecipes) { r.Clear() })
		return "Took every recipe from you only.", true
	case "show":
		p.EditRecipes(func(r *player.PlayerRecipes) { r.Reset() })
		return "You have the server's recipes again.", true
	}
	return "", false
}

// recipeTestItem is the item a command names, with or without "minecraft:".
func recipeTestItem(args []string) string {
	if len(args) == 0 {
		return recipeTestStick
	}
	if strings.Contains(args[0], ":") {
		return args[0]
	}
	return "minecraft:" + args[0]
}

// recipeTestRemove removes every recipe that makes an item, smithing ones too.
func recipeTestRemove(reg *registry.Registry, item string) string {
	removed := 0
	err := reg.EditRecipes(func(s *registry.Recipes) error {
		removed += s.RemoveFor(item)
		return nil
	})
	if err == nil {
		err = reg.EditSmithing(func(s *registry.Smithing) error {
			removed += s.RemoveFor(item)
			return nil
		})
	}
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("Removed %d recipes for %s.", removed, item)
}

// recipeTest runs the recipe test on the live recipes and says what happened. Only
// remove reads the item.
func recipeTest(reg *registry.Registry, start *packet.CraftingData, action, item string) string {
	if action == "reset" {
		reg.Crafting.Update(func(p *packet.CraftingData) { *p = *start })
		return "Recipes reset."
	}
	if action == "remove" {
		return recipeTestRemove(reg, item)
	}
	const stick = recipeTestStick
	var msg string
	err := reg.EditRecipes(func(s *registry.Recipes) error {
		switch action {
		case "clear":
			s.Clear()
			msg = "All recipes cleared."
		case "table":
			msg = fmt.Sprintf("Removed %d crafting table recipes.", s.ClearBlock("crafting_table"))
		case "change":
			r, ok := s.Get(stick)
			if !ok {
				msg = "No stick recipe to change."
				return nil
			}
			r.Output = []registry.RecipeItem{{Name: "minecraft:apple", Count: 4}}
			msg = "Two planks now make 4 apples."
			return s.Set(r)
		case "add":
			msg = "A stick and an apple now make a diamond."
			return s.Set(registry.Recipe{
				ID: "test:diamond", Input: []crafting.Ingredient{crafting.AnyVariant(stick), crafting.AnyVariant("minecraft:apple")},
				Output: []registry.RecipeItem{{Name: "minecraft:diamond"}},
			})
		default:
			msg = "Use clear, table, remove, change, add or reset."
		}
		return nil
	})
	if err != nil {
		return err.Error()
	}
	return msg
}

// chestTestCommand is the command that tries the chest API.
const chestTestCommand = "chesttest"

// chestTestActions are what chestTestCommand takes: refuse every move in a chest,
// rewrite turns what lands in the chest into sticks and what is taken into apples,
// name renames the chest in the player's window.
var chestTestActions = []string{"refuse", "rewrite", "name", "off"}

// The chest test modes.
const (
	chestTestOff int32 = iota
	chestTestRefuse
	chestTestRewrite
)

// chestTest runs the chest test's item rule.
func chestTest(mode int32, e *player.ChestItemEvent) {
	switch mode {
	case chestTestRefuse:
		e.Cancel()
	case chestTestRewrite:
		if !e.New.Empty() {
			e.New.Name, e.New.Block = "minecraft:stick", nil
		}
		if !e.Cursor.Empty() {
			e.Cursor.Name, e.Cursor.Block = "minecraft:apple", nil
		}
		for _, slot := range e.InventorySlots {
			if s := e.InventoryItem(slot); !s.Empty() {
				s.Name, s.Block = "minecraft:apple", nil
				e.SetInventoryItem(slot, s)
			}
		}
	}
}

// itemTestCommand is the command that tries the item API.
const itemTestCommand = "itemtest"

// itemTestActions are what itemTestCommand takes.
var itemTestActions = []string{"private", "life", "server", "timer", "off"}

// itemTestDrops is how a player's drops are set up, and whether it sees the
// timer. private and lifetime are owned by the player's region; timerOff is read
// by the regions of the items the player sees.
type itemTestDrops struct {
	private  bool
	lifetime int32
	timerOff atomic.Bool
}

// itemTest runs /itemtest: private toggles the player's drops being seen only
// by it, life sets their lifetime, server sets every item's, timer toggles the
// player seeing the timer, off undoes the player's own settings and hides the
// timer. Rules last until the item's chunk reloads.
func itemTest(p *player.Player, drops *itemTestDrops, srv *atomic.Pointer[helix.Server], args []string) string {
	if len(args) == 0 {
		return "/itemtest private | life <seconds|forever|default> | server <seconds|forever|default> | timer | off"
	}
	switch args[0] {
	case "private":
		drops.private = !drops.private
		return fmt.Sprintf("Your new drops are private: %v", drops.private)
	case "timer":
		shown := drops.timerOff.Load()
		drops.timerOff.Store(!shown)
		return fmt.Sprintf("You see item timers: %v", shown)
	case "off":
		drops.private, drops.lifetime = false, 0
		drops.timerOff.Store(true)
		return "Your new drops are normal again and you see no item timers"
	case "life", "server":
		if len(args) != 2 {
			return "Give seconds, forever or default"
		}
		ticks, ok := parseItemLifetime(args[1])
		if !ok {
			return "Give seconds, forever or default"
		}
		if args[0] == "life" {
			drops.lifetime = ticks
			return "Your new drops last: " + args[1]
		}
		if s := srv.Load(); s != nil {
			s.SetItemLifetime(ticks)
		}
		return "All items without their own lifetime last: " + args[1]
	}
	return "Unknown action " + args[0]
}

// parseItemLifetime reads seconds, "forever" or "default" as ticks.
func parseItemLifetime(s string) (int32, bool) {
	switch s {
	case "forever":
		return player.ItemLifetimeForever, true
	case "default":
		return 0, true
	}
	seconds, err := strconv.Atoi(s)
	if err != nil || seconds <= 0 || seconds > math.MaxInt32/ticksPerSecond {
		return 0, false
	}
	return int32(seconds) * ticksPerSecond, true //nolint:gosec // checked above
}

// testCommands is a stand-in until commands are designed: /gamemode for
// anyone, so break and place can be tested in game, /itemtest to try the item
// API, and a countdown over dropped items. Only with -testcmds. The command
// list goes to each player with its first input. srv holds the server once it
// is built.
func testCommands(srv *atomic.Pointer[helix.Server]) func(s *helix.Setup) error {
	return func(s *helix.Setup) error { return setUpTestCommands(s, srv) }
}

func setUpTestCommands(s *helix.Setup, srv *atomic.Pointer[helix.Server]) error {
	setup := s.As("testcmds").Player
	var dropSettings sync.Map // *player.Player -> *itemTestDrops
	settings := func(p *player.Player) *itemTestDrops {
		d, _ := dropSettings.LoadOrStore(p, &itemTestDrops{})
		drops, _ := d.(*itemTestDrops)
		return drops
	}
	if err := player.OnItemDrop(setup, func(e *player.ItemDropEvent) {
		showItemTimer(e.Item, settings)
		drops := settings(e.Player)
		if drops.private {
			owner := e.Player
			e.Item.SetVisibility(func(p *player.Player) bool { return p == owner })
		}
		e.Item.SetLifetime(drops.lifetime)
	}, player.Normal); err != nil {
		return err
	}
	if err := player.OnItemLoad(setup, func(e *player.ItemLoadEvent) { showItemTimer(e.Item, settings) }, player.Normal); err != nil {
		return err
	}
	list := testCommandList()
	var sent sync.Map // *player.Player -> struct{}, so a rejoin gets it again
	if err := player.WrapHandle(setup, func(p *player.Player, pk *packet.PlayerAuthInput, next player.PacketHandler[*packet.PlayerAuthInput]) {
		if _, done := sent.LoadOrStore(p, struct{}{}); !done {
			// A failed write means the connection is closing; the read loop
			// ends the session, so there is nothing more to do here.
			_ = p.Conn().WritePacket(list)
		}
		next(p, pk)
	}); err != nil {
		return err
	}
	var chestMode atomic.Int32
	if err := player.OnChestItem(setup, func(e *player.ChestItemEvent) { chestTest(chestMode.Load(), e) }, player.Normal); err != nil {
		return err
	}
	var chestNames atomic.Int32
	if err := player.OnChestOpen(setup, func(e *player.ChestOpenEvent) {
		if chestNames.Swap(0) != 0 {
			e.Chest.SetName(e.Region, "Renamed by /chesttest")
		}
	}, player.Normal); err != nil {
		return err
	}
	var smithStart atomic.Pointer[packet.CraftingData]
	return player.Handle(setup, func(p *player.Player, pk *packet.CommandRequest) {
		args := strings.Fields(strings.TrimPrefix(pk.CommandLine, "/"))
		if len(args) > 1 && args[0] == tuneTestCommand {
			// A failed write means the connection is closing, as above.
			_ = p.Conn().WritePacket(&packet.Text{TextType: packet.TextTypeRaw, Message: tuneTest(p, args[1:])})
			return
		}
		if (len(args) == 2 || len(args) == 3) && args[0] == recipeTestCommand {
			if msg, ok := recipeTestPlayer(p, args[1], recipeTestItem(args[2:])); ok {
				// A failed write means the connection is closing, as above.
				_ = p.Conn().WritePacket(&packet.Text{TextType: packet.TextTypeRaw, Message: msg})
				return
			}
			if server := srv.Load(); server != nil {
				reg := server.Registry()
				smithStart.CompareAndSwap(nil, reg.Crafting.Packet())
				// Reset is back to how the server started, so nobody keeps a hidden recipe.
				if args[1] == "reset" {
					for _, o := range server.Players() {
						o.EditRecipes(func(r *player.PlayerRecipes) { r.Reset() })
					}
				}
				// A failed write means the connection is closing, as above.
				_ = p.Conn().WritePacket(&packet.Text{TextType: packet.TextTypeRaw, Message: recipeTest(reg, smithStart.Load(), args[1], recipeTestItem(args[2:]))})
			}
			return
		}
		if len(args) == 2 && args[0] == smithTestCommand {
			if server := srv.Load(); server != nil {
				reg := server.Registry()
				smithStart.CompareAndSwap(nil, reg.Crafting.Packet())
				// A failed write means the connection is closing, as above.
				_ = p.Conn().WritePacket(&packet.Text{TextType: packet.TextTypeRaw, Message: smithTest(reg, smithStart.Load(), args[1])})
			}
			return
		}
		if len(args) == 2 && args[0] == chestTestCommand {
			switch args[1] {
			case "refuse":
				chestMode.Store(chestTestRefuse)
			case "rewrite":
				chestMode.Store(chestTestRewrite)
			case "name":
				chestNames.Store(1)
			default:
				chestMode.Store(chestTestOff)
			}
			return
		}
		if len(args) > 0 && args[0] == itemTestCommand {
			// A failed write means the connection is closing, as above.
			_ = p.Conn().WritePacket(&packet.Text{TextType: packet.TextTypeRaw, Message: itemTest(p, settings(p), srv, args[1:])})
			return
		}
		if len(args) == 2 && (args[0] == "gamemode" || args[0] == "gm") {
			if mode, ok := testModes[strings.ToLower(args[1])]; ok {
				p.SetGameMode(mode)
			}
		}
	})
}
