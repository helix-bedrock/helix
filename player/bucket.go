package player

import (
	"github.com/helix-bedrock/helix/liquid"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// bucketLiquid is what a bucket pours and the sound it makes.
type bucketLiquid struct {
	block, sound string
	kind         liquid.Kind
}

// bucketLiquids are the buckets that pour a liquid.
var bucketLiquids = map[string]bucketLiquid{
	"minecraft:water_bucket": {"minecraft:water", packet.SoundEventBucketEmptyWater, liquid.Water},
	"minecraft:lava_bucket":  {"minecraft:lava", packet.SoundEventBucketEmptyLava, liquid.Lava},
}

// emptyBucket is what a bucket becomes once poured outside creative.
const emptyBucket = "minecraft:bucket"

// bucketFills are what an empty bucket takes, by the material of the block, with the
// bucket that makes and the sound.
var bucketFills = map[uint8]bucketFill{
	registry.MaterialWater:      {"minecraft:water_bucket", packet.SoundEventBucketFillWater},
	registry.MaterialLava:       {"minecraft:lava_bucket", packet.SoundEventBucketFillLava},
	registry.MaterialPowderSnow: {"minecraft:powder_snow_bucket", packet.SoundEventBucketFillPowderSnow},
}

// bucketFill is what an empty bucket becomes and the sound of it.
type bucketFill struct{ item, sound string }

// takeLiquid removes the source at pos, the second layer first, and tells what bucket
// it fills. False if there is none: a liquid that is not a source stays (powder snow
// always is one).
func (s *Host) takeLiquid(r *world.Region, pos cube.Pos) (bucketFill, bool) {
	blocks := s.reg.Blocks
	layer := uint8(1)
	b, ok := r.Block(pos, layer)
	if !ok {
		return bucketFill{}, false
	}
	if b == blocks.Air() {
		layer = 0
		b, _ = r.Block(pos, layer)
	}
	material := blocks.Properties(b).Material
	fill, ok := bucketFills[material]
	if !ok {
		return bucketFill{}, false
	}
	if material != registry.MaterialPowderSnow {
		if depth, _ := blocks.States(b)["liquid_depth"].(int32); depth != 0 {
			return bucketFill{}, false
		}
	}
	if !r.SetBlock(pos, layer, blocks.Air()) {
		return bucketFill{}, false
	}
	r.UpdateNeighbours(pos)
	return fill, true
}

// fillBucket is BDS's empty bucket: the liquid at the clicked block, the second layer
// first, must be a source (powder snow always is). It goes, the sound plays, and the
// bucket becomes the filled one; a stack of buckets gives one up and the filled one
// goes to the inventory, or the ground when full. False if nothing was taken.
func (p *Player) fillBucket(clicked cube.Pos) bool {
	r := p.entity.Region()
	if r == nil || !p.inReach(clicked) {
		return false
	}
	fill, ok := p.s.takeLiquid(r, clicked)
	if !ok {
		return false
	}
	playSound(r, clicked, soundEvent{name: fill.sound, pos: blockCenter(clicked), extra: -1}, nil)
	if p.creative() {
		return true
	}
	filled := ItemStack{Name: fill.item, Count: 1, netID: p.s.stackIDs.Add(1)}
	inv := &p.inventory
	if inv.main[inv.held].Count <= 1 {
		inv.main[inv.held] = filled
		p.sendHeld()
		p.sendEquipment()
		return true
	}
	p.useHeld()
	if rest := p.Give(filled); !rest.Empty() {
		p.s.game.sys.DropItem(p, rest, false)
	}
	return true
}

// pourBucket is BDS's bucket emptying for water and lava: the liquid goes
// in the clicked block if it can be built over, else in its neighbour on
// the clicked face if that can. There, water goes in the second layer of a
// block that can hold it; otherwise the liquid replaces air or a block
// whose material is not solid, a block whose liquid reaction is popped
// throwing out its drops first (0xC99EC80). Neighbours are told, the liquid is ticked
// after its delay, the empty sound plays and the bucket becomes an empty
// bucket outside creative. Not yet read from BDS: how the block is picked
// (the built-over rule above is Helix's), the nether's rule for water, and buckets of fish and powder snow. False if
// nothing was poured.
func (p *Player) pourBucket(clicked cube.Pos, face int32, held ItemStack) bool {
	liq, ok := bucketLiquids[held.Name]
	r := p.entity.Region()
	if !ok || r == nil || !p.inReach(clicked) {
		return false
	}
	blocks := p.s.reg.Blocks
	block, ok := blocks.Block(liq.block)
	if !ok {
		return false
	}
	pos := clicked
	if b, ok := r.Block(clicked, 0); !ok || !p.s.game.replaceable(p, r, clicked, b, block) {
		pos = clicked.Side(cube.Face(face))
		if b, ok := r.Block(pos, 0); !ok || !p.s.game.replaceable(p, r, pos, b, block) {
			return false
		}
	}
	target, _ := r.Block(pos, 0)
	tp := blocks.Properties(target)
	layer := uint8(0)
	switch {
	case liq.kind == liquid.Water && target != blocks.Air() && tp.CanContainLiquid:
		layer = 1
	case target != blocks.Air() && tp.SolidMaterial:
		return false
	case target != blocks.Air() && tp.LiquidReaction == registry.LiquidPopped:
		p.s.PopBlock(r, pos, target)
	}
	if !r.SetBlock(pos, layer, block) {
		return false
	}
	r.UpdateNeighbours(pos)
	r.ScheduleTick(pos, block, liquid.TickDelay(liq.kind, false), 0)
	playSound(r, pos, soundEvent{name: liq.sound, pos: blockCenter(pos), extra: -1}, nil)
	if !p.creative() {
		inv := &p.inventory
		inv.main[inv.held] = ItemStack{Name: emptyBucket, Count: 1, netID: p.s.stackIDs.Add(1)}
		p.sendHeld()
	}
	return true
}
