package player

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"strconv"
	"strings"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	files "github.com/helix-bedrock/helix-data/data/files"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/protocol/packet"
)

// Villager trading as BDS 1.26.60's EconomyTradeableComponent: offers made from the trade
// table (_generateTrades), priced when a player opens the window (0x734AAD0, price 0xCEF2620),
// traded through the item stack request's craft (CraftHandlerTrade), the experience and tiers
// of notifyTrade, the daily restock (resupplyTrades), and the save (0x7241CD0, MerchantRecipe
// 0xCEF2A50). The offers belong to the economy_trade_table component: a villager that loses its
// profession loses the component, and so its offers, which is how breaking a job site rerolls an
// unlocked villager's trades.

// BDS's trading numbers.
const (
	// tradeWindowType is UpdateTrade's window type.
	tradeWindowType = 15
	// tradeOpenSound is the level sound the villager plays when the window opens (121), and
	// tradeSound / tradeSoundAlone the one after a trade, with and without a trading player.
	tradeOpenSound  = 121
	tradeSound      = 122
	tradeSoundAlone = 124
	// heroEffect is Hero of the Village; heroBase and heroPerLevel its discount share.
	heroEffect   = 29
	heroBase     = 0.3
	heroPerLevel = 0.0625
	// tradeOrbsBase and tradeOrbsRandom are the experience a trade gives (3 + random(4));
	// levelUpOrbs is added on a level up.
	tradeOrbsBase   = 3
	tradeOrbsRandom = 4
	levelUpOrbs     = 5
	// levelUpTicks is the wait before a level up's reward.
	levelUpTicks = 10
	// tradeSlotA and tradeSlotB are the trade window's two ingredient slots in the UI.
	tradeSlotA = 4
	tradeSlotB = 5
	// itemEmerald is what riches count.
	itemEmerald = "minecraft:emerald"
	// tradesPath is where the trade tables are in helix-data's vanilla pack.
	tradesPath = "vanilla/trading"
)

// The saved keys (EconomyTradeableComponent and Actor).
const (
	tradeKeyOffers    = "Offers"
	tradeKeyRecipes   = "Recipes"
	tradeKeyTierExp   = "TierExpRequirements"
	tradeKeyRiches    = "Riches"
	tradeKeyTablePath = "TradeTablePath"
	tradeKeyTier      = "TradeTier"
	tradeKeyExp       = "TradeExperience"
)

// tradeTable is a trade table file.
type tradeTable struct {
	Tiers []tradeTier `json:"tiers"`
}

// tradeTier is one tier: its trades in groups, or one list taken whole.
type tradeTier struct {
	TotalExpRequired int32        `json:"total_exp_required"`
	Groups           []tradeGroup `json:"groups"`
	Trades           []tradeEntry `json:"trades"`
}

// tradeGroup is a group the tier picks num_to_select trades from; 0 takes them all.
type tradeGroup struct {
	NumToSelect int32        `json:"num_to_select"`
	Trades      []tradeEntry `json:"trades"`
}

// tradeEntry is one trade of a table. Every vanilla table sets trader_exp, max_uses and
// reward_exp; the defaults for a table that leaves them out were not read from BDS.
type tradeEntry struct {
	Wants     []tradeItem `json:"wants"`
	Gives     []tradeItem `json:"gives"`
	TraderExp *int32      `json:"trader_exp"`
	MaxUses   *int32      `json:"max_uses"`
	RewardExp *bool       `json:"reward_exp"`
}

// tradeItem is a want or give: an item, or a choice of items, each with its filters.
type tradeItem struct {
	Item            string            `json:"item"`
	Quantity        json.RawMessage   `json:"quantity"`
	PriceMultiplier float32           `json:"price_multiplier"`
	Functions       []json.RawMessage `json:"functions"`
	Filters         json.RawMessage   `json:"filters"`
	Choice          []tradeItem       `json:"choice"`
	filter          *definition.Filter
}

// tradeFunction is one function on a give.
type tradeFunction struct {
	Function           string     `json:"function"`
	Levels             tradeRange `json:"levels"`
	Values             tradeRange `json:"values"`
	Treasure           bool       `json:"treasure"`
	ID                 string     `json:"id"`
	BaseCost           *int32     `json:"base_cost"`
	BaseRandomCost     *int32     `json:"base_random_cost"`
	PerLevelCost       *int32     `json:"per_level_cost"`
	PerLevelRandomCost *int32     `json:"per_level_random_cost"`
}

// tradeRange is a number or {min, max}.
type tradeRange struct{ Min, Max int32 }

func (r *tradeRange) UnmarshalJSON(b []byte) error {
	var n float64
	if json.Unmarshal(b, &n) == nil {
		r.Min, r.Max = int32(n), int32(n)
		return nil
	}
	var o struct{ Min, Max float64 }
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	r.Min, r.Max = int32(o.Min), int32(o.Max)
	return nil
}

// tradeOffer is one offer (MerchantRecipe).
type tradeOffer struct {
	buyA, buyB, sell ItemStack
	// baseA and baseB are the counts before prices (buyCountA, buyCountB).
	baseA, baseB                        int32
	uses, maxUses, tier, traderExp, dem int32
	rewardExp                           bool
	multA, multB                        float32
	netID                               int32
}

// villagerTrades is a villager's EconomyTradeableComponent.
type villagerTrades struct {
	offers  []tradeOffer
	tierExp []int32
	riches  int32
	path    string
	// resupplied is set once trade_resupply restocked; it clears when the component goes.
	resupplied bool
	// levelTimer counts down a level up's reward for levelPlayer.
	levelTimer  int32
	levelPlayer behavior.ID
	// player is the player in the window, nil for none.
	player *Player
}

// loadTradeTables reads every trade table in helix-data's vanilla pack, by the path the
// components name ("trading/economy_trades/farmer_trades.json"). Runs: boot.
func (s *Host) loadTradeTables() error {
	s.tradeTables = map[string]*tradeTable{}
	return fs.WalkDir(files.Files(), tradesPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		raw, err := fs.ReadFile(files.Files(), path)
		if err != nil {
			return fmt.Errorf("player: trade table %s: %w", path, err)
		}
		var t tradeTable
		if err := json.Unmarshal(raw, &t); err != nil {
			s.log.Debug("trade table not read", "path", path, "err", err)
			return nil
		}
		s.compileTradeFilters(&t)
		s.tradeTables[strings.TrimPrefix(path, "vanilla/")] = &t
		return nil
	})
}

// compileTradeFilters reads every item's filters once.
func (s *Host) compileTradeFilters(t *tradeTable) {
	var compile func(items []tradeItem)
	compile = func(items []tradeItem) {
		for i := range items {
			it := &items[i]
			if len(it.Filters) != 0 {
				f, err := s.mobs.ParseFilter(it.Filters)
				if err != nil {
					s.log.Debug("trade filter not read", "item", it.Item, "err", err)
				}
				it.filter = f
			}
			compile(it.Choice)
		}
	}
	for ti := range t.Tiers {
		tier := &t.Tiers[ti]
		for _, e := range tier.Trades {
			compile(e.Wants)
			compile(e.Gives)
		}
		for _, g := range tier.Groups {
			for _, e := range g.Trades {
				compile(e.Wants)
				compile(e.Gives)
			}
		}
	}
}

// tickTrades keeps a villager's offers in step with its trade component: none without it
// (a reroll), generated when it appears or its table changes, restocked when
// trade_resupply appears, and a level up's reward after its wait. Runs: the mob's apply.
func (o *Mob) tickTrades(r *world.Region) {
	cfg := o.m.TradeTable()
	if cfg == nil {
		if o.trades != nil {
			o.closeTrade()
			o.trades = nil
			o.m.SetTradeWants(nil)
		}
		return
	}
	if o.trades == nil || o.trades.path != cfg.Table && len(o.trades.offers) == 0 {
		o.generateTrades(cfg.Table)
	} else if o.trades.path != cfg.Table {
		// BDS keeps offers whose saved table path is empty, and regenerates for another table.
		if o.trades.path == "" {
			o.trades.path = cfg.Table
		} else {
			o.generateTrades(cfg.Table)
		}
	}
	t := o.trades
	switch on := o.m.Resupplying(); {
	case on && !t.resupplied:
		o.resupplyTrades()
		t.resupplied = true
	case !on:
		t.resupplied = false
	}
	if t.player == nil && t.levelTimer > 0 {
		if t.levelTimer--; t.levelTimer == 0 {
			// The reward is Regeneration for 200 ticks and a village standing; mobs have no
			// effects and villages no standings yet.
			t.levelPlayer = behavior.NoID
		}
	}
	if p := t.player; p != nil && (p.entity == nil || p.entity.Region() != r || p.window.kind != ContainerTrade || p.window.trader != o) {
		o.endTrade()
	}
}

// generateTrades makes a new offer list from the table (_generateTrades): every tier at
// once, each group's num_to_select trades shuffled out of it.
func (o *Mob) generateTrades(path string) {
	s := o.s
	t := &villagerTrades{path: path, levelPlayer: behavior.NoID}
	if old := o.trades; old != nil {
		t.riches, t.player = old.riches, old.player
	}
	o.trades = t
	table := s.tradeTables[path]
	if table == nil {
		s.log.Debug("trade table not found", "type", o.m.Type(), "table", path)
		o.syncTradeWants()
		return
	}
	rnd := o.m.Rand()
	for ti, tier := range table.Tiers {
		groups := tier.Groups
		if len(tier.Trades) != 0 {
			groups = append([]tradeGroup{{Trades: tier.Trades}}, groups...)
		}
		for _, g := range groups {
			picks := g.Trades
			if g.NumToSelect > 0 && int(g.NumToSelect) < len(picks) {
				order := make([]int, len(picks))
				for i := range order {
					order[i] = i
				}
				for i := len(order) - 1; i > 0; i-- {
					j := int(rnd.NextInt(int32(i + 1))) //nolint:gosec // small
					order[i], order[j] = order[j], order[i]
				}
				chosen := make([]tradeEntry, 0, g.NumToSelect)
				for _, k := range order[:g.NumToSelect] {
					chosen = append(chosen, picks[k])
				}
				picks = chosen
			}
			for _, e := range picks {
				if off, ok := o.makeOffer(e, int32(ti)); ok { //nolint:gosec // few tiers
					t.offers = append(t.offers, off)
				}
			}
		}
		t.tierExp = append(t.tierExp, tier.TotalExpRequired)
	}
	maxTier := int32(max(len(table.Tiers)-1, 0)) //nolint:gosec // few tiers
	o.m.SetMaxTradeTier(maxTier)
	if o.m.TradeTier() > maxTier {
		o.m.SetTradeTier(maxTier)
	}
	o.syncTradeWants()
}

// makeOffer turns a table trade into an offer: its wants and give after filters, a random
// choice and quantity for each, and the give's functions.
func (o *Mob) makeOffer(e tradeEntry, tier int32) (tradeOffer, bool) {
	var wants []ItemStack
	var mults []float32
	for _, w := range e.Wants {
		it, mult, _, ok := o.tradeStack(w)
		if ok {
			wants, mults = append(wants, it), append(mults, mult)
		}
	}
	if len(wants) == 0 || len(e.Gives) == 0 {
		return tradeOffer{}, false
	}
	sell, _, cost, ok := o.tradeStack(e.Gives[0])
	if !ok {
		return tradeOffer{}, false
	}
	if cost > 0 {
		wants[0].Count = uint8(min(cost, 255)) //nolint:gosec // bounded
	}
	off := tradeOffer{buyA: wants[0], sell: sell, baseA: int32(wants[0].Count), multA: mults[0], tier: tier,
		traderExp: 1, maxUses: 7, rewardExp: true}
	if len(wants) > 1 {
		off.buyB, off.baseB, off.multB = wants[1], int32(wants[1].Count), mults[1]
	}
	if e.TraderExp != nil {
		off.traderExp = *e.TraderExp
	}
	if e.MaxUses != nil {
		off.maxUses = *e.MaxUses
	}
	if e.RewardExp != nil {
		off.rewardExp = *e.RewardExp
	}
	off.netID = o.s.tradeIDs.Add(1)
	return off, true
}

// tradeStack makes one want or give: a random choice among those the filters let through,
// its item and quantity, and its functions. cost is what enchant_book_for_trading asks.
func (o *Mob) tradeStack(it tradeItem) (s ItemStack, mult float32, cost int32, ok bool) {
	rnd := o.m.Rand()
	if len(it.Choice) != 0 {
		var pass []tradeItem
		for _, c := range it.Choice {
			if o.m.Test(c.filter) {
				pass = append(pass, c)
			}
		}
		if len(pass) == 0 {
			return ItemStack{}, 0, 0, false
		}
		it = pass[rnd.NextInt(int32(len(pass)))] //nolint:gosec // small
	} else if !o.m.Test(it.filter) {
		return ItemStack{}, 0, 0, false
	}
	name, aux := tradeItemName(it.Item)
	count := int32(1)
	if len(it.Quantity) != 0 {
		var q tradeRange
		if json.Unmarshal(it.Quantity, &q) == nil {
			count = q.Min
			if q.Max > q.Min {
				count += rnd.NextInt(q.Max - q.Min)
			}
		}
	}
	st, made := o.s.tradeItem(name, aux, count)
	if !made {
		o.s.log.Debug("trade item not known", "item", it.Item)
		return ItemStack{}, 0, 0, false
	}
	for _, raw := range it.Functions {
		var f tradeFunction
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		if c := o.tradeFunction(&st, f); c > 0 {
			cost = c
		}
	}
	return st, it.PriceMultiplier, cost, true
}

// tradeAliases are the old item names the vanilla trade tables still use, as BDS's item
// registry aliases them: the name, or the name and aux value, to the item of today.
var tradeAliases = map[string]string{
	"minecraft:muttonraw":          "minecraft:mutton",
	"minecraft:map":                "minecraft:filled_map",
	"minecraft:turtle_shell_piece": "minecraft:turtle_scute",
	"minecraft:speckled_melon":     "minecraft:glistering_melon_slice",
	"minecraft:fish":               "minecraft:cod",
	"minecraft:cooked_fish":        "minecraft:cooked_cod",
	"minecraft:clownfish":          "minecraft:tropical_fish",
	"minecraft:horsearmorleather":  "minecraft:leather_horse_armor",
	"minecraft:dye:0":              "minecraft:ink_sac",
	"minecraft:dye:4":              "minecraft:lapis_lazuli",
	"minecraft:bucket:2":           "minecraft:milk_bucket",
	"minecraft:bucket:10":          "minecraft:lava_bucket",
}

// tradeItemName splits "minecraft:coal:0" into the item and its aux value, after the
// aliases.
func tradeItemName(s string) (string, int16) {
	if !strings.Contains(s, ":") {
		s = "minecraft:" + s
	}
	if a, ok := tradeAliases[s]; ok {
		return a, 0
	}
	if i := strings.LastIndexByte(s, ':'); i > 0 && strings.Count(s, ":") >= 2 {
		if n, err := strconv.Atoi(s[i+1:]); err == nil {
			name := s[:i]
			if a, ok := tradeAliases[name]; ok {
				name = a
			}
			return name, int16(n) //nolint:gosec // aux values are small
		}
	}
	return s, 0
}

// tradeItem makes a stack of a trade's item, a block item with its block.
func (s *Host) tradeItem(name string, aux int16, count int32) (ItemStack, bool) {
	if _, ok := s.reg.Items.ByName(name); !ok || count <= 0 {
		return ItemStack{}, false
	}
	it := ItemStack{Name: name, Meta: aux, Count: uint8(min(count, 255))} //nolint:gosec // bounded
	if id, ok := s.reg.Blocks.Block(name); ok {
		blocks := s.reg.Blocks
		it.Block = map[string]any{"name": blocks.Name(id), "states": blocks.States(id), "version": blocks.Version(id)}
	}
	return it, true
}

// tradeFunction runs one give function and returns the price enchant_book_for_trading
// sets, 0 for none. random_dye and exploration_map are not built: their item stays plain.
func (o *Mob) tradeFunction(st *ItemStack, f tradeFunction) int32 {
	rnd := o.m.Rand()
	switch strings.TrimPrefix(f.Function, "minecraft:") {
	case "enchant_with_levels":
		*st = o.s.EnchantItem(*st, f.Levels.Min+rnd.NextInt(max(f.Levels.Max-f.Levels.Min, 0)+1), 0, f.Treasure)
	case "random_aux_value":
		st.Meta = int16(f.Values.Min + rnd.NextInt(max(f.Values.Max-f.Values.Min, 0)+1)) //nolint:gosec // aux values are small
	case "set_potion":
		for id := int32(0); ; id++ {
			p, ok := o.s.Potion(id)
			if !ok {
				break
			}
			if p.Name == f.ID {
				st.Meta = int16(id) //nolint:gosec // potion IDs are small
				if st.Name == "minecraft:arrow" {
					st.Meta++
				}
				break
			}
		}
	case "enchant_book_for_trading":
		return o.enchantBookForTrading(st, f)
	}
	return 0
}

// enchantBookForTrading is EnchantBookForTradingFunction (1.26.60 0xD6BD400): a random
// enchantment books may have (Lootable) at a random level, and its price: base_cost +
// random(base_random_cost + level * per_level_random_cost) + level * per_level_cost, doubled
// for a treasure enchantment and then capped at the book's stack size.
func (o *Mob) enchantBookForTrading(st *ItemStack, f tradeFunction) int32 {
	val := func(p *int32, def int32) int32 {
		if p != nil {
			return *p
		}
		return def
	}
	base, baseRand := val(f.BaseCost, 2), val(f.BaseRandomCost, 5)
	perRand, per := val(f.PerLevelRandomCost, 10), val(f.PerLevelCost, 3)
	var pool []Enchantment
	for _, e := range o.s.Enchantments() {
		if e.Lootable && !e.Disabled {
			pool = append(pool, e)
		}
	}
	if len(pool) == 0 {
		return 0
	}
	rnd := o.m.Rand()
	e := pool[rnd.NextInt(int32(len(pool)))] //nolint:gosec // small
	level := e.MinLevel + rnd.NextInt(max(e.MaxLevel-e.MinLevel, 0)+1)
	cost := rnd.NextInt(max(baseRand+level*perRand, 1)) + base + level*per
	if e.Treasure {
		cost = min(cost*2, int32(st.maxStack(o.s.reg)))
	}
	if st.Name == itemBook {
		st.Name = itemEnchantedBook
	}
	*st = st.WithEnchant(e.ID, int16(level)) //nolint:gosec // enchantment levels are small
	return cost
}

// syncTradeWants tells the mob what its offers want, for trade_interest.
func (o *Mob) syncTradeWants() {
	var w []mob.TradeWant
	if o.trades != nil {
		w = make([]mob.TradeWant, 0, len(o.trades.offers))
		for _, off := range o.trades.offers {
			w = append(w, mob.TradeWant{BuyA: behaviorItem(off.buyA), BuyB: behaviorItem(off.buyB), Sell: behaviorItem(off.sell), Tier: off.tier})
		}
	}
	o.m.SetTradeWants(w)
}

func behaviorItem(s ItemStack) behavior.Item {
	if s.Empty() {
		return behavior.Item{}
	}
	return behavior.Item{Name: s.Name, Aux: s.Meta, Count: int32(s.Count)}
}

// resupplyTrades restocks (resupplyTrades): demand follows how much of each offer was used,
// uses go back to 0 and the price follows the demand.
func (o *Mob) resupplyTrades() {
	for i := range o.trades.offers {
		off := &o.trades.offers[i]
		off.dem = max(0, off.dem+off.uses-(off.maxUses-off.uses))
		off.uses = 0
		off.buyA.Count = uint8(min(max(off.baseA+max(0, int32(math.Floor(float64(float32(off.baseA)*float32(off.dem)*off.multA)))), 1), 255)) //nolint:gosec // bounded
	}
}

// price is an offer's buyA count for the player (0xCEF2620): demand, the cured discounts
// (none yet: curing is not built) and Hero of the Village, at least 1 and at most a stack.
func (o *Mob) price(p *Player, off *tradeOffer) uint8 {
	base := float32(off.baseA)
	hero := int32(0)
	if amp, ok := p.Effect(heroEffect); ok {
		hero = -max(1, int32(math.Floor(float64(base*(heroBase+heroPerLevel*float32(amp))))))
	}
	price := max(0, int32(math.Floor(float64(base*float32(off.dem)*off.multA)))) + off.baseA + hero
	return uint8(min(max(price, 1), int32(off.buyA.maxStack(o.s.reg)))) //nolint:gosec // bounded
}

// openTrade opens the villager's trade window for the player (the interaction 0x734AAD0):
// it records the player, prices the offers for it, sends UpdateTrade and plays the haggle
// sound. False when the villager does not trade now: no table, a baby, or another player
// trading.
func (p *Player) openTrade(r *world.Region, o *Mob) bool {
	if !o.m.HasTradeTable() || o.m.Baby() || o.m.Dead() {
		return false
	}
	if o.trades == nil {
		o.tickTrades(r)
	}
	t := o.trades
	if t == nil || t.player != nil && t.player != p || len(t.offers) == 0 {
		return false
	}
	if p.window.open {
		p.leaveWindow(r, true)
	}
	w := &p.window
	w.nextID()
	w.open, w.kind, w.inv, w.trader = true, ContainerTrade, nil, o
	w.dim = r.Dimension()
	t.player = p
	o.m.SetTradingPlayer(behavior.ID(p.entity.UniqueID()))
	for i := range t.offers {
		t.offers[i].buyA.Count = o.price(p, &t.offers[i])
	}
	p.sendTrade(o)
	o.Sound(o.m, tradeOpenSound, o.m.Eyes())
	p.flush()
	return true
}

// sendTrade sends the villager's offers to the player.
func (p *Player) sendTrade(o *Mob) {
	t := o.trades
	data, err := nbt.MarshalEncoding(o.offersTag(true), nbt.NetworkLittleEndian)
	if err != nil {
		p.s.log.Debug("trade offers not encoded", "err", err)
		return
	}
	name := ""
	if cfg := o.m.TradeTable(); cfg != nil {
		name = cfg.DisplayName
	}
	p.send(&packet.UpdateTrade{
		WindowID: p.window.id, WindowType: tradeWindowType, Size: int32(len(t.offers)), //nolint:gosec // few offers
		TradeTier: o.m.TradeTier(), VillagerUniqueID: o.entity.UniqueID(), EntityUniqueID: p.entity.UniqueID(),
		DisplayName: name, NewTradeUI: true, DemandBasedPrices: true, SerialisedOffers: data,
	})
}

// offersTag is MerchantRecipeList's tag: the recipes and each tier's experience.
func (o *Mob) offersTag(network bool) map[string]any {
	t := o.trades
	recipes := make([]any, 0, len(t.offers))
	for _, off := range t.offers {
		m := map[string]any{
			"buyA": off.buyA.nbt(0, false), "sell": off.sell.nbt(0, false),
			"tier": off.tier, "uses": off.uses, "maxUses": off.maxUses, "traderExp": off.traderExp,
			"rewardExp": boolByte(off.rewardExp), "demand": off.dem, "buyCountA": off.baseA, "buyCountB": off.baseB,
			"priceMultiplierA": off.multA, "priceMultiplierB": off.multB,
		}
		if !off.buyB.Empty() {
			m["buyB"] = off.buyB.nbt(0, false)
		}
		if network {
			m["netId"] = off.netID
		}
		recipes = append(recipes, m)
	}
	tiers := make([]any, 0, len(t.tierExp))
	for i, e := range t.tierExp {
		tiers = append(tiers, map[string]any{strconv.Itoa(i): e})
	}
	return map[string]any{tradeKeyRecipes: recipes, tradeKeyTierExp: tiers}
}

// endTrade forgets the player in the villager's window.
func (o *Mob) endTrade() {
	if o.trades == nil {
		return
	}
	o.trades.player = nil
	o.m.SetTradingPlayer(behavior.NoID)
}

// closeTrade closes the window of the player trading with the villager.
func (o *Mob) closeTrade() {
	if o.trades == nil || o.trades.player == nil {
		return
	}
	p := o.trades.player
	o.endTrade()
	if p.window.kind == ContainerTrade && p.window.trader == o {
		p.CloseWindow()
	}
}

// tradeCraft is a trade under way in a stack request: what the offer still wants
// consumed, A then B.
type tradeCraft struct {
	index int
	want  [2]ItemStack
	left  [2]int32
	auto  bool
}

// craftTrade starts a trade from the window (CraftHandlerTrade): the offer named by its
// network ID, unlocked and not used up; then the request must consume its price and take
// its item.
func (p *Player) craftTrade(netID uint32, times uint8, auto bool) uint8 {
	c := &p.requests.craft
	if c.active {
		return protocol.ItemStackResponseStatusRequestAlreadyInProgress
	}
	o := p.window.trader
	if o == nil || o.trades == nil || o.gone || o.m.Dead() {
		return protocol.ItemStackResponseStatusInvalidCraftRequest
	}
	times = max(times, 1)
	t := o.trades
	idx := -1
	for i := range t.offers {
		if uint32(t.offers[i].netID) == netID { //nolint:gosec // net IDs are positive
			idx = i
			break
		}
	}
	if idx < 0 {
		return protocol.ItemStackResponseStatusFailedToFindRecipeByNetId
	}
	off := &t.offers[idx]
	if off.tier > o.m.TradeTier() || off.uses+int32(times) > off.maxUses {
		return protocol.ItemStackResponseStatusInvalidCraftRequest
	}
	tc := &tradeCraft{index: idx, auto: auto}
	tc.want[0], tc.left[0] = off.buyA, int32(off.buyA.Count)*int32(times)
	if !off.buyB.Empty() {
		tc.want[1], tc.left[1] = off.buyB, int32(off.buyB.Count)*int32(times)
	}
	c.active, c.auto, c.times, c.trade = true, false, times, tc
	sell := p.s.freshStack(off.sell)
	c.results = append(c.results, craftResult{stack: sell, left: int32(off.sell.Count) * int32(times)})
	c.after.trade, c.after.tradeTimes = idx+1, times
	return protocol.ItemStackResponseStatusOK
}

// consumedTrade checks items taken for a trade: from the trade slots, or from the
// inventory for a trade the client fills itself, of an item the offer wants.
func (p *Player) consumedTrade(src slotRef, used ItemStack) uint8 {
	tc := p.requests.craft.trade
	switch src.kind {
	case protocol.ContainerTradeTwoIngredientOne, protocol.ContainerTradeTwoIngredientTwo:
	case containerCombined:
		if !tc.auto {
			return protocol.ItemStackResponseStatusConsumedItemNotAllowed
		}
	default:
		return protocol.ItemStackResponseStatusConsumedItemNotAllowed
	}
	for i := range tc.want {
		w := tc.want[i]
		if w.Empty() || tc.left[i] <= 0 || w.Name != used.Name || w.Meta != used.Meta {
			continue
		}
		if int32(used.Count) > tc.left[i] {
			return protocol.ItemStackResponseStatusConsumedTooMuchFromSlot
		}
		tc.left[i] -= int32(used.Count)
		return protocol.ItemStackResponseStatusOK
	}
	return protocol.ItemStackResponseStatusMismatchSlotExpectedConsumedItem
}

// tradeDone reports every wanted item consumed.
func (tc *tradeCraft) done() bool { return tc.left[0] <= 0 && tc.left[1] <= 0 }

// afterTrade is notifyTrade for each trade the request made: uses, the haggle sound, then
// for an adult the experience and tier, riches for emeralds and the experience orbs.
func (p *Player) afterTrade(index int, times uint8) {
	o := p.window.trader
	if o == nil || o.trades == nil || index < 0 || index >= len(o.trades.offers) || p.entity == nil {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	t := o.trades
	tierBefore := o.m.TradeTier()
	for range times {
		off := &t.offers[index]
		off.uses++
		sound := tradeSoundAlone
		if t.player != nil {
			sound = tradeSound
		}
		o.Sound(o.m, sound, o.m.Eyes())
		if o.m.Baby() {
			continue
		}
		orbs := tradeOrbsBase + o.m.Rand().NextInt(tradeOrbsRandom)
		if tier := o.m.TradeTier(); tier < o.m.MaxTradeTier() {
			exp := o.m.TradeExperience() + off.traderExp
			o.m.SetTradeExperience(exp)
			if next := t.tierFor(exp); next > tier {
				t.levelTimer, t.levelPlayer = levelUpTicks, behavior.ID(p.entity.UniqueID())
				orbs += levelUpOrbs
				o.m.SetTradeTier(next)
			}
		}
		if off.buyA.Name == itemEmerald {
			t.riches += int32(off.buyA.Count)
		}
		if off.rewardExp {
			at := o.entity.Position()
			at[1] += 0.5
			p.s.SpawnExperience(r, at, orbs)
		}
	}
	if o.m.TradeTier() != tierBefore {
		p.sendTrade(o)
	}
}

// tierFor is the last tier whose experience the villager has reached.
func (t *villagerTrades) tierFor(exp int32) int32 {
	tier := int32(0)
	for i, need := range t.tierExp {
		if exp >= need {
			tier = int32(i) //nolint:gosec // few tiers
		}
	}
	return tier
}

// saveTrades writes the trade component and the trade data as BDS saves them.
func (o *Mob) saveTrades(data map[string]any) {
	if tier := o.m.TradeTier(); tier != 0 || o.trades != nil {
		data[tradeKeyTier] = tier
		data[tradeKeyExp] = o.m.TradeExperience()
	}
	if o.trades == nil {
		return
	}
	data[tradeKeyOffers] = o.offersTag(false)
	data[tradeKeyRiches] = o.trades.riches
	data[tradeKeyTablePath] = o.trades.path
}

// loadTrades reads them back.
func (o *Mob) loadTrades(data map[string]any) {
	if v, ok := data[tradeKeyTier].(int32); ok {
		o.m.SetTradeTier(v)
	}
	if v, ok := data[tradeKeyExp].(int32); ok {
		o.m.SetTradeExperience(v)
	}
	tag, ok := data[tradeKeyOffers].(map[string]any)
	if !ok {
		return
	}
	t := &villagerTrades{levelPlayer: behavior.NoID}
	t.riches, _ = data[tradeKeyRiches].(int32)
	t.path, _ = data[tradeKeyTablePath].(string)
	recipes, _ := tag[tradeKeyRecipes].([]any)
	for _, raw := range recipes {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		off := tradeOffer{netID: o.s.tradeIDs.Add(1)}
		off.buyA = itemFromNBT(asMap(m["buyA"]))
		off.buyB = itemFromNBT(asMap(m["buyB"]))
		off.sell = itemFromNBT(asMap(m["sell"]))
		off.tier, _ = m["tier"].(int32)
		off.uses, _ = m["uses"].(int32)
		off.maxUses, _ = m["maxUses"].(int32)
		off.traderExp, _ = m["traderExp"].(int32)
		off.dem, _ = m["demand"].(int32)
		off.baseA, _ = m["buyCountA"].(int32)
		off.baseB, _ = m["buyCountB"].(int32)
		off.multA, _ = m["priceMultiplierA"].(float32)
		off.multB, _ = m["priceMultiplierB"].(float32)
		reward, _ := m["rewardExp"].(uint8)
		off.rewardExp = reward != 0
		if off.buyA.Empty() || off.sell.Empty() {
			continue
		}
		if off.baseA == 0 {
			off.baseA = int32(off.buyA.Count)
		}
		t.offers = append(t.offers, off)
	}
	tiers, _ := tag[tradeKeyTierExp].([]any)
	for i, raw := range tiers {
		if m, ok := raw.(map[string]any); ok {
			v, _ := m[strconv.Itoa(i)].(int32)
			t.tierExp = append(t.tierExp, v)
		}
	}
	o.trades = t
	o.syncTradeWants()
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// Carried shows the item trade_interest holds up (mob.ShowHooks): only those who see the
// villager get it; the villager's real hand stays as it was.
func (o *Mob) Carried(m *mob.Mob) {
	pk := o.handPacket(mob.EquipMainhand)
	if it := m.Equipment(mob.EquipMainhand); !it.Empty() {
		if st, ok := o.s.tradeItem(it.Name, it.Aux, 1); ok {
			pk.NewItem = st.networkShown(o.s.reg)
		}
	}
	o.entity.Send(pk)
}
