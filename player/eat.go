package player

import (
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Numbers of BDS 1.26.60's item use.
const (
	// eatEffectStart and eatEffectEvery are when the eating effects show: every 4
	// ticks once fewer than 26 are left.
	eatEffectStart = 26
	eatEffectEvery = 4
	// foodEffectTicks turns a food effect's seconds into ticks.
	foodEffectTicks = 20
)

// FoodEffect is an effect a food may give.
type FoodEffect struct {
	ID, Amplifier int32
	// Duration is in ticks.
	Duration int32
	// Chance is 0 to 1.
	Chance float32
}

// FoodItem is what eating an item does, from the item's own data.
type FoodItem struct {
	Nutrition          int32
	SaturationModifier float32
	CanAlwaysEat       bool
	// Duration is how many ticks eating takes.
	Duration int32
	// Becomes names the item left over, such as a bowl.
	Becomes string
	Effects []FoodEffect
	// RemoveEffects are the effect IDs eating it takes away (remove_effects: the honey
	// bottle's poison).
	RemoveEffects []int32
}

// usingState is the item the player is using, as BDS keeps a copy of it.
type usingState struct {
	active bool
	slot   uint8
	item   ItemStack
	food   FoodItem
	left   int32
	// bow is a bow being drawn, which ends when the player lets go.
	bow bool
	// milk is a milk bucket being drunk, and potion a potion.
	milk, potion bool
	// crossbow is a crossbow being loaded: it loads when the draw is done or let go after.
	crossbow bool
}

// ItemUseEvent fires before a player starts using the item it holds in the air.
type ItemUseEvent struct {
	Cancellable
	Player *Player
	Item   ItemStack
}

// ItemEatEvent fires when a player finishes eating, before the food does anything.
// FoodItem may be changed.
type ItemEatEvent struct {
	Cancellable
	Player *Player
	Item   ItemStack
	Food   FoodItem
}

// OnItemUse listens to players using items in the air.
func OnItemUse(s *Setup, fn func(e *ItemUseEvent), p Priority) error {
	return addListener(s, &s.h.game.itemUseEv, fn, p)
}

// OnItemEat listens to players finishing a food.
func OnItemEat(s *Setup, fn func(e *ItemEatEvent), p Priority) error {
	return addListener(s, &s.h.game.itemEatEv, fn, p)
}

// UseItemFunc is the player clicking the air with held; false if nothing happened.
type UseItemFunc func(p *Player, held ItemStack) bool

// EatFunc finishes eating: the food's effects on the player and the item used up.
type EatFunc func(p *Player, held ItemStack, food FoodItem)

// FoodOfFunc returns what eating an item does; false if it is not eaten.
type FoodOfFunc func(p *Player, item ItemStack) (FoodItem, bool)

// foodTable reads every item's food from its item data.
func foodTable(items *registry.Items) map[string]FoodItem {
	foods := map[string]FoodItem{}
	items.Each(func(t registry.ItemType) {
		if f, ok := foodFromComponents(t.Components); ok {
			foods[t.Name] = f
		}
	})
	return foods
}

// foodFromComponents reads a food from an item's component data.
func foodFromComponents(data map[string]any) (FoodItem, bool) {
	comps, _ := data["components"].(map[string]any)
	m, ok := comps["minecraft:food"].(map[string]any)
	if !ok {
		return FoodItem{}, false
	}
	f := FoodItem{Nutrition: nbtInt(m["nutrition"]), SaturationModifier: nbtFloat(m["saturation_modifier"]), CanAlwaysEat: nbtInt(m["can_always_eat"]) != 0}
	f.Becomes, _ = m["using_converts_to"].(string)
	if v, ok := comps["minecraft:use_duration"]; ok {
		f.Duration = nbtInt(v)
	} else if props, ok := comps["item_properties"].(map[string]any); ok {
		f.Duration = nbtInt(props["use_duration"])
	}
	f.RemoveEffects = nbtInts(m["remove_effects"])
	if list, ok := m["effects"].([]any); ok {
		for _, e := range list {
			if em, ok := e.(map[string]any); ok {
				f.Effects = append(f.Effects, FoodEffect{
					ID: nbtInt(em["id"]), Amplifier: nbtInt(em["amplifier"]),
					Duration: nbtInt(em["duration"]) * foodEffectTicks, Chance: nbtFloat(em["chance"]),
				})
			}
		}
	}
	return f, true
}

// nbtInt reads any NBT whole number.
func nbtInt(v any) int32 {
	switch n := v.(type) {
	case uint8:
		return int32(n)
	case int16:
		return int32(n)
	case int32:
		return n
	case int64:
		return int32(n) //nolint:gosec // item data values are small
	}
	return 0
}

// nbtInts reads an NBT list of whole numbers.
func nbtInts(v any) []int32 {
	switch l := v.(type) {
	case []int32:
		return l
	case []any:
		out := make([]int32, 0, len(l))
		for _, x := range l {
			out = append(out, nbtInt(x))
		}
		return out
	}
	return nil
}

// nbtFloat reads an NBT float.
func nbtFloat(v any) float32 {
	switch n := v.(type) {
	case float32:
		return n
	case float64:
		return float32(n)
	}
	return float32(nbtInt(v))
}

// vanillaFoodOf is the item's own food data.
func vanillaFoodOf(p *Player, item ItemStack) (FoodItem, bool) {
	f, ok := p.s.foods[item.Name]
	return f, ok
}

// handleUseAir is BDS's click in the air: the slot and item are checked as for a
// click on a block, then GameMode's useItem runs.
func (p *Player) handleUseAir(d *protocol.UseItemTransactionData) {
	if !p.selectUsed(d) {
		p.sendHeld()
		p.flush()
		return
	}
	if held := p.inventory.Held(); !held.Empty() {
		p.useRepeat = d.TriggerType == protocol.TriggerTypeSimulationTick
		p.s.game.sys.UseItem(p, held)
		p.useRepeat = false
	}
}

// vanillaUseItem is BDS's useItem as far as built: armour goes on, snowballs, eggs and
// pearls are thrown, a fishing rod casts or reels in, a bow starts drawing, and foods start being eaten when the player may
// eat. Potions are drunk as milk is.
func vanillaUseItem(p *Player, held ItemStack) bool {
	g := p.s.game
	if g.itemUseEv.Has() {
		e := ItemUseEvent{Player: p, Item: held}
		g.itemUseEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
	}
	if g.sys.EquipArmor(p, held) {
		return true
	}
	if p.throwItem(held) {
		return true
	}
	if p.useEmptyMap(held) {
		return true
	}
	if held.Name == nameFireworkRocket {
		return p.useFirework(held)
	}
	if held.Name == nameGoatHorn {
		return p.useGoatHorn(held)
	}
	if held.Name == nameFishingRod {
		return p.useFishingRod()
	}
	if held.Name == nameBow {
		if !p.mayDrawBow(held) {
			return false
		}
		p.startDrawing(held)
		return true
	}
	if held.Name == nameTrident {
		p.startTrident(held)
		return true
	}
	if held.Name == nameCrossbow {
		return p.useCrossbow(held)
	}
	if held.Name == nameMilkBucket {
		p.startMilk(held)
		return true
	}
	if held.Name == namePotion {
		p.startPotion(held)
		return true
	}
	food, ok := g.sys.FoodOf(p, held)
	if !ok || !p.mayEat(food) {
		return false
	}
	p.startUsing(held, food)
	return true
}

// mayEat is BDS's food use rule: creative, peaceful, not full, or a food that can
// always be eaten.
func (p *Player) mayEat(food FoodItem) bool {
	return p.creative() || p.s.level.Difficulty == difficultyPeaceful || food.CanAlwaysEat ||
		p.attributes.get(attrHunger) < p.attributes.maximum(attrHunger)
}

// startUsing is BDS's startUsingItem: nothing changes while the same item in the same
// slot is in use, else the player starts over and those who see it are told.
func (p *Player) startUsing(held ItemStack, food FoodItem) {
	u := &p.using
	slot := p.inventory.held
	if u.active && u.slot == slot && u.item.Name == held.Name && u.item.Meta == held.Meta {
		return
	}
	*u = usingState{active: true, slot: slot, item: held, food: food, left: food.Duration}
	p.sendActorFlags()
}

// startMilk starts drinking milk, which any player may.
func (p *Player) startMilk(held ItemStack) {
	u := &p.using
	slot := p.inventory.held
	if u.active && u.slot == slot && u.item.Name == held.Name {
		return
	}
	*u = usingState{active: true, slot: slot, item: held, left: bucketUseTicks, milk: true}
	p.sendActorFlags()
}

// startDrawing starts drawing a bow.
func (p *Player) startDrawing(held ItemStack) {
	u := &p.using
	slot := p.inventory.held
	if u.active && u.slot == slot && u.item.Name == held.Name {
		return
	}
	*u = usingState{active: true, slot: slot, item: held, left: bowUseTicks, bow: true}
	p.sendActorFlags()
}

// letGo is the client saying it let go of what it was using: a drawn bow shoots.
func (p *Player) letGo() {
	u := p.using
	if u.active && u.crossbow && p.inventory.held == u.slot {
		if held := p.inventory.Held(); held.Name == u.item.Name {
			p.loadCrossbow(held, crossbowLoadTime(u.item)-u.left)
		}
	}
	if u.active && u.bow && p.inventory.held == u.slot {
		if held := p.inventory.Held(); held.Name == u.item.Name {
			if held.Name == nameTrident {
				p.releaseTrident(held, bowUseTicks-u.left)
			} else {
				p.releaseBow(held, bowUseTicks-u.left)
			}
		}
	}
	p.stopUsing()
}

// stopUsing is BDS's stopUsingItem.
func (p *Player) stopUsing() {
	if !p.using.active {
		return
	}
	p.using = usingState{}
	p.sendActorFlags()
}

// tickUsing moves the item in use on, once a tick: it must stay in its slot, the
// eating effects show, and at the end the item is eaten.
func (p *Player) tickUsing() {
	u := &p.using
	if !u.active {
		return
	}
	held := p.inventory.Held()
	if p.dead() || p.inventory.held != u.slot || held.Empty() || held.Name != u.item.Name || held.Meta != u.item.Meta {
		p.stopUsing()
		return
	}
	u.left--
	if u.crossbow {
		// BDS's useTimeDepleted (0xC9B0E70) lets go by itself once the draw is done. Helix
		// waits for the client to let go instead: stopping the use here clears the player's
		// using flag while the button is still down, and the client then starts a new use
		// that shoots the crossbow it just loaded.
		u.left = max(u.left, 0)
		return
	}
	if u.bow {
		if u.left <= 0 {
			p.stopUsing()
		}
		return
	}
	if u.left > 0 {
		if u.left < eatEffectStart && u.left%eatEffectEvery == 0 {
			p.showEating(held)
		}
		return
	}
	food, milk, potion := u.food, u.milk, u.potion
	p.stopUsing()
	if milk {
		p.drinkMilk()
		return
	}
	if potion {
		p.drinkPotion(held)
		return
	}
	p.s.game.sys.Eat(p, held, food)
}

// showEating sends those who see the player the eating event, with the item.
func (p *Player) showEating(held ItemStack) {
	t, ok := p.s.reg.Items.ByName(held.Name)
	if !ok || p.entity == nil {
		return
	}
	p.entity.Send(actorEventData(p.runtimeID, packet.ActorEventFeed, int32(t.ID)<<16|int32(uint16(held.Meta))))
}

// vanillaEat is BDS's finishing a food: hunger and saturation, the burp, one item
// gone, what it leaves behind, and its effects.
func vanillaEat(p *Player, held ItemStack, food FoodItem) {
	g := p.s.game
	if g.itemEatEv.Has() {
		e := ItemEatEvent{Player: p, Item: held, Food: food}
		g.itemEatEv.fire(p.s.log, &e)
		if e.Cancelled() {
			p.sendHeld()
			p.flush()
			return
		}
		food = e.Food
	}
	p.feed(food.Nutrition, food.SaturationModifier)
	if r := p.entity.Region(); r != nil {
		playSound(r, p.blockPos(), soundEvent{name: packet.SoundEventBurp, pos: p.entity.Position(), extra: -1, entity: "minecraft:player"}, nil)
	}
	if !p.creative() {
		p.useHeld()
		if food.Becomes != "" {
			left := ItemStack{Name: "minecraft:" + food.Becomes, Count: 1, netID: p.s.stackIDs.Add(1)}
			if rest := p.Give(left); !rest.Empty() {
				g.sys.DropItem(p, rest, false)
			}
		}
	}
	for _, id := range food.RemoveEffects {
		p.RemoveEffect(id)
	}
	for _, e := range food.Effects {
		if p.rng.Float32() < e.Chance {
			p.AddEffect(e.ID, e.Amplifier, e.Duration)
		}
	}
	p.flush()
}

// feed is BDS's eat: hunger goes up, saturation may not pass hunger, and saturation
// goes up by twice the nutrition times the modifier.
func (p *Player) feed(nutrition int32, modifier float32) {
	f := p.Food()
	f.Hunger = min(f.Hunger+float32(nutrition)*p.Tuning(TuneFoodNutritionScale), p.attributes.maximum(attrHunger))
	gain := modifier * float32(nutrition) * p.Tuning(TuneFoodSaturationScale)
	f.Saturation = min(f.Saturation+gain+gain, f.Hunger)
	p.SetFood(f)
}
