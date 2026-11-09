package main

import (
	"github.com/helix-bedrock/helix/daytime"
	"strings"
	"sync/atomic"

	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/protocol/packet"
)

// testOwner is who the test commands are registered for.
const testOwner = "testcmds"

// The suggestion lists of the test commands' first argument.
type (
	testModeArg   string
	itemTestArg   string
	chestTestArg  string
	smithTestArg  string
	recipeTestArg string
	timeTestArg   string
)

func (testModeArg) Type() string                    { return "TestGameMode" }
func (testModeArg) Options(command.Source) []string { return testModeNames }
func (itemTestArg) Type() string                    { return "ItemTestAction" }
func (itemTestArg) Options(command.Source) []string { return itemTestActions }
func (chestTestArg) Type() string                   { return "ChestTestAction" }
func (chestTestArg) Options(command.Source) []string {
	return chestTestActions
}
func (smithTestArg) Type() string { return "SmithTestAction" }
func (smithTestArg) Options(command.Source) []string {
	return smithTestActions
}
func (recipeTestArg) Type() string { return "RecipeTestAction" }
func (recipeTestArg) Options(command.Source) []string {
	return recipeTestActions
}
func (timeTestArg) Type() string { return "TestTimeAction" }
func (timeTestArg) Options(command.Source) []string {
	return []string{timeWordSet, timeWordAdd, timeWordQuery}
}

// testRun is a test command's body: the player and the words after the command.
type testRun func(p *player.Player, args []string) string

// testCommand is a stand-in command whose first argument suggests from first and whose
// other words are read by run.
func testCommand(name, description string, first any, run testRun) (*command.Command, error) {
	b := command.NewBuilder(name, description)
	args := []command.Arg{command.Opt("args", command.Text(""))}
	if first != nil {
		args = []command.Arg{command.Param("action", first), command.Opt("args", command.Text(""))}
	}
	b.Overload(func(src command.Source, a command.Args, out *command.Output) {
		p, ok := player.SourcePlayer(src)
		if !ok {
			out.Error("Only players can run this.")
			return
		}
		words := strings.Fields(a.String("args"))
		if first != nil {
			words = append([]string{a.String("action")}, words...)
		}
		if msg := run(p, words); msg != "" {
			out.Print(msg)
		}
	}, args...)
	return b.Build()
}

// registerTestCommands puts the stand-in commands on the command manager.
func registerTestCommands(setup *player.Setup, srv *atomic.Pointer[helix.Server], settings func(*player.Player) *itemTestDrops, chestMode, chestNames *atomic.Int32, flags *chestTestFlags) error {
	var smithStart atomic.Pointer[packet.CraftingData]
	registry := func() bool { return srv.Load() != nil }
	start := func() *packet.CraftingData {
		reg := srv.Load().Registry()
		smithStart.CompareAndSwap(nil, reg.Crafting.Packet())
		return smithStart.Load()
	}
	gameMode := func(p *player.Player, args []string) string {
		if mode, ok := testModes[strings.ToLower(args[0])]; ok {
			p.SetGameMode(mode)
			return "Game mode set."
		}
		return "Unknown game mode " + args[0]
	}
	chest := func(_ *player.Player, args []string) string {
		switch args[0] {
		case "refuse":
			chestMode.Store(chestTestRefuse)
		case "rewrite":
			chestMode.Store(chestTestRewrite)
		case "name":
			chestNames.Store(1)
		case "price":
			flags.price.Store(true)
		case "noopen":
			flags.noOpen.Store(true)
		case "nobang":
			flags.noBang.Store(true)
		case "playersonly":
			flags.playersOnly.Store(true)
		default:
			chestMode.Store(chestTestOff)
			flags.noOpen.Store(false)
			flags.noBang.Store(false)
			flags.playersOnly.Store(false)
			flags.price.Store(false)
		}
		return "/chesttest " + args[0]
	}
	type entry struct {
		name, description string
		first             any
		run               testRun
	}
	entries := []entry{
		{"gamemode", "Sets your game mode (test stand-in).", testModeArg(""), gameMode},
		{"gm", "Sets your game mode (test stand-in).", testModeArg(""), gameMode},
		{itemTestCommand, "Tests the item API: private, life, server, timer, off.", itemTestArg(""), func(p *player.Player, args []string) string {
			return itemTest(p, settings(p), srv, args)
		}},
		{chestTestCommand, "Tests the container and explosion API.", chestTestArg(""), func(p *player.Player, args []string) string {
			msg := chest(p, args)
			p.RefreshItems()
			return msg
		}},
		{smithTestCommand, "Tests the smithing API: clear, remove, change, reset.", smithTestArg(""), func(_ *player.Player, args []string) string {
			if !registry() {
				return ""
			}
			return smithTest(srv.Load().Registry(), start(), args[0])
		}},
		{recipeTestCommand, "Tests the recipe API.", recipeTestArg(""), func(p *player.Player, args []string) string {
			if msg, ok := recipeTestPlayer(p, args[0], recipeTestItem(args[1:])); ok {
				return msg
			}
			if !registry() {
				return ""
			}
			server := srv.Load()
			if args[0] == "reset" {
				for _, o := range server.Players() {
					o.EditRecipes(func(r *player.PlayerRecipes) { r.Reset() })
				}
			}
			return recipeTest(server.Registry(), start(), args[0], recipeTestItem(args[1:]))
		}},
		{tuneTestCommand, "Tests the number API: <name> <value>, clear [name], server <name> <value>, serverclear [name].", nil, tuneTest},
		{enchantTestCommand, "Tests enchanting: levels <n>, max <enchantment id> <level>.", nil, enchantTest},
		{targetTestCommand, "Hits nearby target blocks: [edge] [arrow].", nil, targetTest},
		{lightTestCommand, "Shows the server's sky and block light where you stand.", nil, func(p *player.Player, _ []string) string {
			var darken uint8
			if server := srv.Load(); server != nil && server.Clock() != nil {
				c := server.Clock()
				darken = uint8(min(max(daytime.SkyDarken(c.Time(), c.Cycle(), server.Weather().Rain(), server.Weather().Thunder()), 0), 15)) //nolint:gosec // 0 to 15
			}
			return lightTest(p, darken)
		}},
		{damageTestCommand, "Damages the item in your hand: <amount>, max, or fix.", nil, func(p *player.Player, args []string) string {
			if len(args) != 1 {
				return "Use: /damageitem <amount>, max, or fix"
			}
			return damageTest(p, args)
		}},
		{"optest", "Makes you an operator, or a member with off (test stand-in until there is an ops list).", nil, func(p *player.Player, args []string) string {
			if len(args) == 1 && args[0] == "off" {
				p.SetPermission(player.PermissionMember)
				return "You are a member now."
			}
			p.SetPermission(player.PermissionOperator)
			return "You are an operator now."
		}},
		{tickSpeedCommand, "Sets the random tick speed until the server stops (BDS's default is 1).", nil, func(_ *player.Player, args []string) string {
			if !registry() {
				return ""
			}
			return tickSpeed(srv.Load(), args)
		}},
		{timeCommand, "Changes or shows the time: set day, set 6000, add 1000, query.", timeTestArg(""), func(_ *player.Player, args []string) string {
			if !registry() {
				return ""
			}
			return timeRun(srv.Load(), args)
		}},
	}
	for _, e := range entries {
		c, err := testCommand(e.name, e.description, e.first, e.run)
		if err != nil {
			return err
		}
		player.Commands(setup).Register(testOwner, c)
	}
	return nil
}
