package player

import (
	"maps"
	"strings"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Candles, candle cakes and cakes, from BDS 1.26.60.
//
// Candles (CandleBlock vtable 0xFB9D380, CandleBlockItem 0xFB5D3B8): the item's useOn
// (0xC9A3560) adds one to the same candle when it holds fewer than four, else places a new
// one, on a block whose top holds it (canProvideSupport up). Lit, a candle gives 3 light per
// candle (0xCCF36E0; helix-data's light emission). Clicking one (0xCB45550): flint and steel,
// a fire charge or a Fire Aspect item lights it and wears the item by one (BDS's hurtAndBreak,
// so a fire charge is not used up); an empty hand puts a lit one out; any other item goes to
// the item. Lighting and going out are the fire package's.
//
// Cakes (CakeBlock 0xFBA6F50, use 0xCB88A90): a candle on a whole cake makes the candle cake of
// its colour (sound cake.add_candle at the corner, one candle used outside creative); else a
// player who may eat (creative, peaceful or not full) eats a slice (0xCB89090): nutrition 2,
// saturation modifier 0.1, one more bite, the last bite removes the cake, and a burp at the
// head. Comparators read 14 - 2 * bites (circuit package).
//
// Candle cakes (CandleCakeBlock 0xFB9D7F8, use 0xCB47140): a fire starter lights it; an empty
// hand puts a lit one out; anything else eats from it as from a whole cake, throwing out its
// candle first (as a block's drop, when blocks drop). Cakes and candle cakes need a solid block
// below (attach package).
//
// Not done: the item candle cakes refuse (a name compare at 0xCB47140, FEE1680, not read), the
// air-only check of the candle item (Helix lets a candle replace what any block item replaces),
// and the cake's gameEvent.

// Candle and cake states and names.
const (
	candleCount    = "candles"
	candleLitState = "lit"
	cakeBite       = "bite_counter"
	nameCake       = "minecraft:cake"
	candleCakeTail = "_cake"
)

// Candle and cake numbers from BDS.
const (
	// candleMax is the candles state of a full candle block (four candles).
	candleMax = 3
	// cakeLastBite is the bite a cake is gone after.
	cakeLastBite = 6
	// cakeNutrition and cakeSaturation are a slice's food.
	cakeNutrition  = 2
	cakeSaturation = 0.1
)

// isCandleBlock reports whether name is a candle block of any colour.
func isCandleBlock(name string) bool { return strings.HasSuffix(name, "candle") }

// isCandleCake reports whether name is a candle cake of any colour.
func isCandleCake(name string) bool { return strings.HasSuffix(name, "candle"+candleCakeTail) }

// candleBuiltOver is the candle item's stacking: the same candle with fewer than four.
func candleBuiltOver(blocks *registry.Blocks, b, placed uint32) bool {
	if blocks.Name(b) != blocks.Name(placed) {
		return false
	}
	n, _ := blocks.States(b)[candleCount].(int32)
	return n < candleMax
}

// candleState is the state the candle item places: one more on the candle already there,
// keeping whether it burns, else a new unlit candle.
func candleState(pl *Placement) (uint32, bool) {
	blocks := pl.Player.s.reg.Blocks
	name := blocks.Name(pl.Block)
	if r := pl.Player.entity.Region(); r != nil {
		if b, ok := r.Block(pl.Pos, 0); ok && blocks.Name(b) == name {
			states := maps.Clone(blocks.States(b))
			n, _ := states[candleCount].(int32)
			states[candleCount] = n + 1
			return blocks.StateID(name, states)
		}
	}
	return pl.Block, true
}

// ignitesCandle reports whether an item lights a candle: flint and steel, a fire charge, or
// anything with Fire Aspect (BDS's sub_BD04470).
func ignitesCandle(s ItemStack) bool { return s.Name == nameFireCharge || ignitesCampfire(s) }

// lightCandleWith lights the candle or candle cake at pos with the held fire starter, wearing it
// by one outside creative when it lit.
func (p *Player) lightCandleWith(r *world.Region, pos cube.Pos) {
	if f := p.s.fire; f != nil && f.LightCandle(r, pos) && !p.creative() {
		p.hurtHeld(1)
	}
}

// useCandle is a click on a candle.
func (p *Player) useCandle(r *world.Region, u *BlockUse) bool {
	switch {
	case ignitesCandle(u.Item):
		p.lightCandleWith(r, u.Pos)
		return true
	case !u.Item.Empty():
		return false
	}
	f := p.s.fire
	return f != nil && f.ExtinguishCandle(r, u.Pos)
}

// useCandleCake is a click on a candle cake.
func (p *Player) useCandleCake(r *world.Region, u *BlockUse) bool {
	if ignitesCandle(u.Item) {
		p.lightCandleWith(r, u.Pos)
		return true
	}
	if f := p.s.fire; f != nil && u.Item.Empty() && f.ExtinguishCandle(r, u.Pos) {
		return true
	}
	if !p.mayEat(FoodItem{}) {
		return false
	}
	blocks := p.s.reg.Blocks
	if p.s.level.DoTileDrops {
		candle := strings.TrimSuffix(blocks.Name(u.Block), candleCakeTail)
		d := NewBlockDrop(u.Pos, ItemStack{Name: candle, Count: 1})
		p.s.spawnItem(r.Dimension(), d.Stack, d.Pos, d.Velocity, world.Rotation{}, d.PickupDelay, 0)
	}
	if cake, ok := blocks.Block(nameCake); ok {
		p.eatCake(r, u.Pos, cake)
	}
	return true
}

// useCake is a click on a cake: a candle on a whole one, else a slice eaten.
func (p *Player) useCake(r *world.Region, u *BlockUse) bool {
	blocks := p.s.reg.Blocks
	bite, _ := blocks.States(u.Block)[cakeBite].(int32)
	if bite == 0 && u.Item.Count > 0 && isCandleBlock(u.Item.Name) {
		cc, ok := blocks.Block(u.Item.Name + candleCakeTail)
		if !ok {
			return true
		}
		playSound(r, u.Pos, soundEvent{name: packet.SoundEventCakeAddCandle, pos: blockVec(u.Pos), extra: -1}, nil)
		if r.SetBlock(u.Pos, 0, cc) {
			r.UpdateNeighbours(u.Pos)
		}
		if !p.creative() {
			p.useHeld()
		}
		return true
	}
	if p.mayEat(FoodItem{}) {
		p.eatCake(r, u.Pos, u.Block)
	}
	return true
}

// eatCake is BDS's CakeBlock eat: a slice's food, one more bite on cake (the last removes it),
// then a burp at the head.
func (p *Player) eatCake(r *world.Region, pos cube.Pos, cake uint32) {
	p.feed(cakeNutrition, cakeSaturation)
	blocks := p.s.reg.Blocks
	next := blocks.Air()
	if bite, _ := blocks.States(cake)[cakeBite].(int32); bite < cakeLastBite {
		states := maps.Clone(blocks.States(cake))
		states[cakeBite] = bite + 1
		if id, ok := blocks.StateID(blocks.Name(cake), states); ok {
			next = id
		}
	}
	if r.SetBlock(pos, 0, next) {
		r.UpdateNeighbours(pos)
	}
	playSound(r, pos, soundEvent{name: packet.SoundEventBurp, pos: p.AttachPos(AttachHead), extra: -1}, nil)
}
