package main

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/crafting"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
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

// ticksPerSecond is the server's tick rate.
const ticksPerSecond = 20

// showItemTimer shows how long an item has left, as minutes and seconds, then
// the name of the player looking, so the tag proves each player gets their own.
// A player who turned the timer off gets no tag, on every item, within a second.
// Only with -testcmds. Items with a view still merge; the one that stays keeps
// its tag.
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

// targetTestCommand stands in for a projectile until Helix has them: it hits the target
// blocks near the player, in the middle of the top, or near a corner with "edge".
const targetTestCommand = "targettest"

// targetTestReach is how far from the player /targettest looks, each way.
const targetTestReach = 6

// targetTestOff is how far from the middle of the top an "edge" hit lands on each axis.
const targetTestOff = 0.4

// targetTest runs /targettest: [edge] [arrow].
func targetTest(p *player.Player, args []string) string {
	e := p.Entity()
	if e == nil || e.Region() == nil {
		return "Not in the world."
	}
	r := e.Region()
	edge, arrow := slices.Contains(args, "edge"), slices.Contains(args, "arrow")
	at := e.Position()
	centre := cube.Pos{int(math.Floor(float64(at[0]))), int(math.Floor(float64(at[1]))), int(math.Floor(float64(at[2])))}
	hits := 0
	for x := -targetTestReach; x <= targetTestReach; x++ {
		for y := -targetTestReach; y <= targetTestReach; y++ {
			for z := -targetTestReach; z <= targetTestReach; z++ {
				pos := cube.Pos{centre[0] + x, centre[1] + y, centre[2] + z}
				hit := mgl32.Vec3{float32(pos[0]) + 0.5, float32(pos[1]) + 1, float32(pos[2]) + 0.5}
				if edge {
					hit[0] += targetTestOff
					hit[2] += targetTestOff
				}
				if p.Host().HitTarget(r, pos, world.Vec64(hit), arrow) {
					hits++
				}
			}
		}
	}
	return fmt.Sprintf("Hit %d target blocks.", hits)
}

// lightTestCommand shows the light the server keeps where the player stands. The client
// works out its own light, so this is the only way to see the server's.
const lightTestCommand = "lighttest"

// lightTest runs /lighttest: the sky and block light in the block at the player's feet,
// and the brightness after the night's darkening (skyDarken).
func lightTest(p *player.Player, skyDarken uint8) string {
	e := p.Entity()
	if e == nil || e.Region() == nil {
		return "Not in the world."
	}
	r := e.Region()
	at := e.Position()
	pos := cube.Pos{int(math.Floor(float64(at[0]))), int(math.Floor(float64(at[1]))), int(math.Floor(float64(at[2])))}
	sky, block, ok := r.Light(pos)
	if !ok {
		return "No light here: the chunk is not loaded."
	}
	return fmt.Sprintf("At %d %d %d: sky %d, block %d, brightness now %d (night takes %d off the sky).",
		pos[0], pos[1], pos[2], sky, block, r.Brightness(pos, skyDarken), skyDarken)
}

// damageTestCommand damages the held item, to try repairs without wearing tools out.
const damageTestCommand = "damageitem"

// damageTest runs /damageitem: <amount> adds that much damage (a negative amount takes it
// off), max leaves none of the durability, fix makes the item as new. It never breaks the
// item, and refuses one that cannot be damaged.
func damageTest(p *player.Player, args []string) string {
	held := p.Held()
	if held.Empty() {
		return "You hold nothing."
	}
	most := p.MaxDamage(held.Name)
	var damage int32
	switch args[0] {
	case "max":
		damage = most
	case "fix":
		damage = 0
	default:
		n, err := strconv.ParseInt(args[0], 10, 32)
		if err != nil {
			return "Use: /damageitem <amount>, max, or fix"
		}
		damage = int32(min(max(int64(held.Damage())+n, 0), int64(most))) //nolint:gosec // clamped
	}
	if !p.SetHeldDamage(damage) {
		return held.Name + " cannot be damaged."
	}
	now := p.Held()
	return fmt.Sprintf("%s: %d damage, %d of %d durability left.", now.Name, now.Damage(), most-now.Damage(), most)
}

// enchantTestCommand is the command that gives levels and tries changing an
// enchantment's default while running.
const enchantTestCommand = "enchanttest"

// enchantTest runs /enchanttest: levels <n> sets the player's level, max <id> <level>
// changes an enchantment's maximum for the whole server.
func enchantTest(p *player.Player, args []string) string {
	if len(args) == 2 && args[0] == "levels" {
		n, err := strconv.Atoi(args[1])
		if err != nil {
			return "Not a number: " + args[1]
		}
		p.SetLevel(int32(n), 0) //nolint:gosec // a test command
		return fmt.Sprintf("Your level is now %d.", p.Level())
	}
	if len(args) == 3 && args[0] == "max" {
		id, errID := strconv.Atoi(args[1])
		level, errLevel := strconv.Atoi(args[2])
		if errID != nil || errLevel != nil {
			return "Use: max <enchantment id> <level>"
		}
		if !p.Host().EditEnchantment(int16(id), func(e *player.Enchantment) { e.MaxLevel = int32(level) }) { //nolint:gosec // a test command
			return "No enchantment " + args[1]
		}
		return fmt.Sprintf("Enchantment %d now goes to level %d.", id, level)
	}
	return "Use: /enchanttest levels <n>, or max <enchantment id> <level>"
}

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

// chestTestActions are what chestTestCommand takes: refuse every move in a container,
// rewrite turns what lands in it into sticks and what is taken into apples, name renames
// the next chest opened, price shows each player its own price on every item, noopen
// keeps every container but the player's own inventory shut, nobang stops every
// explosion and TNT lighting, playersonly keeps every block and hurts and pushes only
// players, telling whoever set it off what exploded.
var chestTestActions = []string{"refuse", "rewrite", "name", "price", "noopen", "nobang", "playersonly", "off"}

// explosionKindNames are the explosion kinds by name, for tellExplosion.
var explosionKindNames = map[player.ExplosionKind]string{
	player.ExplosionPlugin: "a plugin", player.ExplosionTNT: "TNT", player.ExplosionTNTMinecart: "a TNT minecart",
	player.ExplosionBed: "a bed", player.ExplosionRespawnAnchor: "a respawn anchor", player.ExplosionCreeper: "a creeper",
	player.ExplosionEndCrystal: "an end crystal", player.ExplosionWither: "a wither", player.ExplosionFireball: "a fireball",
}

// tellExplosion tells the player behind an explosion what it was.
func tellExplosion(x *player.Explosion) {
	if x.Attacker == nil {
		return
	}
	msg := fmt.Sprintf("/chesttest: %s exploded", explosionKindNames[x.Kind])
	if x.FromBlock {
		msg += fmt.Sprintf(", from the block at %d %d %d", x.BlockPos[0], x.BlockPos[1], x.BlockPos[2])
	}
	// A failed write means the connection is closing; nothing to tell.
	_ = x.Attacker.Conn().WritePacket(&packet.Text{TextType: packet.TextTypeRaw, Message: msg + "; blocks kept, only players hit"})
}

// chestTestFlags are the chest test's switches that stay on until "off".
type chestTestFlags struct {
	price, noOpen, noBang, playersOnly atomic.Bool
}

// priceLore is the lore the price test shows: a price that differs per player.
func priceLore(e *player.InventoryViewEvent) {
	if e.Inventory.Kind() == player.ContainerArmour {
		return
	}
	name := e.Player.Name()
	for i, it := range e.Items {
		if it.Empty() {
			continue
		}
		price := (len(name)*7 + len(it.Name)) * int(it.Count)
		e.Items[i] = it.WithLore(fmt.Sprintf("Price for %s: $%d", name, price))
	}
}

// The chest test modes.
const (
	chestTestOff int32 = iota
	chestTestRefuse
	chestTestRewrite
)

// chestTest runs the chest test's item rule.
func chestTest(mode int32, e *player.ContainerItemEvent) {
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
	var chestMode atomic.Int32
	if err := player.OnContainerItem(setup, func(e *player.ContainerItemEvent) { chestTest(chestMode.Load(), e) }, player.Normal); err != nil {
		return err
	}
	var chestNames atomic.Int32
	var flags chestTestFlags
	if err := player.OnContainerOpen(setup, func(e *player.ContainerOpenEvent) {
		if flags.noOpen.Load() && e.Kind != player.ContainerInventory {
			e.Cancel()
			return
		}
		if e.Chest != nil && chestNames.Swap(0) != 0 {
			e.Chest.SetName(e.Region, "Renamed by /chesttest")
		}
	}, player.Normal); err != nil {
		return err
	}
	if err := player.OnInventoryView(setup, func(e *player.InventoryViewEvent) {
		if flags.price.Load() {
			priceLore(e)
		}
	}, player.Normal); err != nil {
		return err
	}
	if err := player.OnExplosionStart(setup, func(e *player.ExplosionStartEvent) {
		if flags.noBang.Load() {
			e.Cancel()
			return
		}
		if flags.playersOnly.Load() {
			e.Explosion.BreaksBlocks = false
			tellExplosion(e.Explosion)
		}
	}, player.Normal); err != nil {
		return err
	}
	if err := player.OnExplosionHit(setup, func(e *player.ExplosionHitEvent) {
		if flags.playersOnly.Load() && e.Player == nil {
			e.Cancel()
		}
	}, player.Normal); err != nil {
		return err
	}
	if err := player.OnTNTPrime(setup, func(e *player.TNTPrimeEvent) {
		if flags.noBang.Load() {
			e.Cancel()
		}
	}, player.Normal); err != nil {
		return err
	}
	return registerTestCommands(setup, srv, settings, &chestMode, &chestNames, &flags)
}
