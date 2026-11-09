package player

import (
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// The player's side of farming (the rules are in package farm): hoes tilling, bone meal and
// trampling, as BDS 1.26.60 runs them.
const (
	// tagHoe is the item tag BDS's dirt blocks look for (minecraft:is_hoe, 0xFD44698).
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

// tillWithHoe is a dirt block's use with a hoe (BDS 0xCB13A00 and the others): the block is
// tilled, and unless it was rooted dirt the hoe's on_tool_used (0xC981EA0) plays the use-on
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

// useBoneMeal is BoneMealItem's useOn (0xC988B20): on a crop or stem that grows, the growth
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
