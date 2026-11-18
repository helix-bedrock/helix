package player

import (
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// The player's side of farming (the rules are in package farm): hoes tilling, bone meal and
// trampling, as BDS 1.26.60 runs them.
const (
	// tagHoe is the item tag BDS's dirt blocks look for (minecraft:is_hoe).
	tagHoe = "minecraft:is_hoe"
	// nameBoneMeal is the bone meal item.
	nameBoneMeal = "minecraft:bone_meal"
	// hoeWear is the damage a hoe takes from tilling (on_tool_used's hurtAndBreak).
	hoeWear = 1
	// trampleDrop is how far below the feet the farmland walked on is looked for: Helix's
	// choice, so that the block under the feet is found on farmland, which is lower than a
	// full block, and on a full block alike.
	trampleDrop = 0.2
	// playerVolume is the player's box, width times height times depth, which the trample
	// odds read.
	playerVolume = playerHalfWidth * 2 * playerHeight * playerHalfWidth * 2
)

// holdsHoe reports whether s is an item tagged as a hoe.
func (p *Player) holdsHoe(s ItemStack) bool {
	if s.Empty() {
		return false
	}
	t, ok := p.s.reg.Items.ByName(s.Name)
	return ok && slices.Contains(t.Tags, tagHoe)
}

// tillWithHoe is a dirt block's use with a hoe (BDS and the others): the block is
// tilled, and unless it was rooted dirt the hoe's on_tool_used plays the use-on
// sound with the new block and wears the hoe a point; rooted dirt drops hanging roots. False
// if the block is not tilled.
func (p *Player) tillWithHoe(r *world.Region, u *BlockUse) bool {
	f := p.s.farm
	if f == nil || !p.holdsHoe(u.Item) {
		return false
	}
	k, ok := f.TillAt(r, u.Pos)
	if !ok {
		return false
	}
	if k.Drop != "" {
		d := NewBlockDrop(u.Pos, ItemStack{Name: k.Drop, Count: 1})
		p.s.spawnItem(r.Dimension(), d.Stack, d.Pos, d.Velocity, world.Rotation{}, d.PickupDelay, 0)
	}
	if k.ToolUsed {
		playSound(r, u.Pos, p.s.blockSound(packet.SoundEventItemUseOn, blockCenter(u.Pos), k.Block), nil)
		if !p.creative() {
			p.hurtHeld(hoeWear)
		}
	}
	p.flush()
	return true
}

// useBoneMeal is BoneMealItem's useOn: on a crop or stem that grows, the growth
// particles where the block was clicked and one bone meal used up. True if bone meal was used
// on something that takes it, grown or not.
func (p *Player) useBoneMeal(r *world.Region, clicked cube.Pos, click mgl32.Vec3, held ItemStack) bool {
	f := p.s.farm
	if f == nil || held.Name != nameBoneMeal {
		return false
	}
	if !f.Fertilize(r, clicked, p.creative(), false) {
		b, ok := r.Block(clicked, 0)
		return ok && f.Fertilizable(b)
	}
	levelEvent(r, clicked, blockVec(clicked).Add(click), packet.LevelEventParticleCropGrowth, 0)
	p.useUpHeld()
	p.flush()
	return true
}

// dispenseBoneMeal is BoneMealItem's dispense: the block in front is fertilized
// as by a player in survival; if it grew, a click, the growth particles and one bone meal
// used up, else a failed click and nothing used. Either way the dispenser smokes, as BDS's
// does when an item's dispense returns true, which bone meal's always does.
func (s *Host) dispenseBoneMeal(r *world.Region, c *Chest, slot int, front cube.Pos, at mgl32.Vec3) {
	f := s.farm
	if f == nil || !f.Fertilize(r, front, false, false) {
		playSound(r, front, soundEvent{name: packet.SoundEventBlockClickFail, pos: at, extra: dispenseBoneMealFail}, nil)
		return
	}
	playSound(r, front, soundEvent{name: packet.SoundEventBlockClick, pos: at, extra: -1}, nil)
	levelEvent(r, front, blockVec(front), packet.LevelEventParticleCropGrowth, 0)
	it := c.items[slot]
	it.Count--
	c.Inventory(r).SetItem(slot, it)
}

// dispenseBoneMealFail is the data of BDS's failed click when bone meal grows nothing.
const dispenseBoneMealFail = 1000

// pickBerries is a sweet berry bush's use: from growth 2 the
// bush drops its drops (the berries by its growth), plays the pick sound and goes back to
// growth 1. With bone meal in hand a bush that is not fully grown takes the bone meal instead.
// False if the click was not taken.
func (p *Player) pickBerries(r *world.Region, u *BlockUse) bool {
	f := p.s.farm
	if f == nil {
		return false
	}
	bush, grown := f.BerryBush(u.Block)
	if !bush || u.Item.Name == nameBoneMeal && !grown {
		return false
	}
	return f.PickBerries(r, u.Pos, func(block uint32) {
		p.s.dropPicked(r, u.Pos, block, packet.SoundEventSweetBerryBushPick)
	})
}

// pickGlowBerries is a cave vine with berries' use: it drops its drops (the
// glow berries), plays the pick sound and goes back to plain cave vines. False if the click
// was not taken.
func (p *Player) pickGlowBerries(r *world.Region, u *BlockUse) bool {
	f := p.s.farm
	if f == nil {
		return false
	}
	return f.PickGlowBerries(r, u.Pos, func(block uint32) {
		p.s.dropPicked(r, u.Pos, block, packet.SoundEventCaveVinesPickBerries)
	})
}

// dropPicked drops a picked plant's drops (its berries, by the block as it was) at pos and
// plays the pick sound: the shared part of picking by hand and of raid_garden.
func (s *Host) dropPicked(r *world.Region, pos cube.Pos, block uint32, sound string) {
	items, _ := s.game.sys.Drops(s, block, &loot.Context{})
	for _, it := range items {
		d := NewBlockDrop(pos, it)
		s.spawnItem(r.Dimension(), d.Stack, d.Pos, d.Velocity, world.Rotation{}, d.PickupDelay, 0)
	}
	playSound(r, pos, soundEvent{name: sound, pos: blockCenter(pos), extra: -1}, nil)
}

// shearPlantTip is shears on the tip of weeping, twisting or cave vines: it stops growing and
// the shears wear by 1. False if the click was not taken.
func (p *Player) shearPlantTip(r *world.Region, u *BlockUse) bool {
	f := p.s.farm
	if f == nil || !f.ShearPlantTip(r, u.Pos, u.Item.Name) {
		return false
	}
	p.hurtHeld(1)
	playSound(r, u.Pos, soundEvent{name: packet.SoundEventShear, pos: blockCenter(u.Pos), extra: -1}, nil)
	return true
}

// standOnDripleaf tips a big dripleaf a player, mob or item with its feet at feet stands on.
// Runs: the region's owner, once a tick for each.
func (s *Host) standOnDripleaf(r *world.Region, feet mgl32.Vec3) {
	f := s.farm
	if f == nil {
		return
	}
	f.StandOnDripleaf(r, vecPos(feet), feet[1])
	f.StandOnDripleaf(r, vecPos(feet.Sub(mgl32.Vec3{0, standBelow, 0})), feet[1])
}

// standBelow is how far under the feet the block stood on is looked for.
const standBelow = 0.01

// trample is FarmBlock's fall-on for the player landing after falling distance: the
// farmland it lands on may turn to dirt. BDS lets a player trample with the mine ability.
func (p *Player) trample(r *world.Region, feet mgl32.Vec3, distance float32) {
	f := p.s.farm
	if f == nil {
		return
	}
	pos := vecPos(feet.Sub(mgl32.Vec3{0, trampleDrop, 0}))
	f.Trample(r, pos, distance, playerVolume, p.abilities.Flag(AbilityMine))
}

// useComposter is ComposterBlock's use: a full composter gives a bone meal
// (thrown out as a block's drop) and empties; one at 7 does not take the click;
// otherwise one of the held item is composted (farm.Ticker.Compost), and if it was used the
// growth particles show a little above the composter's corner and one is used up outside
// creative. False if the click was not taken, so the item is used instead.
func (p *Player) useComposter(r *world.Region, u *BlockUse) bool {
	f := p.s.farm
	if f == nil {
		return false
	}
	lvl, ok := f.ComposterLevel(u.Block)
	if !ok {
		return false
	}
	if f.EmptyComposter(r, u.Pos) {
		p.s.dropComposterBoneMeal(r, u.Pos)
		return true
	}
	if lvl >= composterReady || u.Item.Empty() || f.Compost(r, u.Pos, u.Item.Name, 1) != 1 {
		return false
	}
	levelEvent(r, u.Pos, blockVec(u.Pos).Add(mgl32.Vec3{0, composterParticleUp, 0}), packet.LevelEventParticleCropGrowth, 0)
	p.useUpHeld()
	p.flush()
	return true
}

// The composter's numbers the player side reads.
const (
	composterReady = 7
	// composterParticleUp is how far above the block's corner BDS shows the particles.
	composterParticleUp = 0.75
)

// dropComposterBoneMeal throws out the bone meal of a full composter at pos, as BDS's
// composter spawns it (0.15 to 0.85 into the block on each axis, picked up after 10 ticks,
// as a block's drop).
func (s *Host) dropComposterBoneMeal(r *world.Region, pos cube.Pos) {
	d := NewBlockDrop(pos, ItemStack{Name: nameBoneMeal, Count: 1})
	s.spawnItem(r.Dimension(), d.Stack, d.Pos, d.Velocity, world.Rotation{}, d.PickupDelay, 0)
}

// composterDrops adds a full composter's bone meal to what breaking it drops (BDS's
// composter gives it when the block goes).
func (s *Host) composterDrops(r *world.Region, pos cube.Pos, items []ItemStack) []ItemStack {
	f := s.farm
	if f == nil {
		return items
	}
	if b, ok := r.Block(pos, 0); ok {
		if lvl, ok := f.ComposterLevel(b); ok && lvl == composterFull {
			items = append(items, ItemStack{Name: nameBoneMeal, Count: 1})
		}
	}
	return items
}

// composterFull is a composter holding bone meal.
const composterFull = 8
