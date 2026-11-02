package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/liquid"
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

// pourBucket is BDS's bucket emptying for water and lava: the liquid goes
// in the clicked block if it can be built over, else in its neighbour on
// the clicked face if that can. There, water goes in the second layer of a
// block that can hold it; otherwise the liquid replaces air or a block
// whose material is not solid. Neighbours are told, the liquid is ticked
// after its delay, the empty sound plays and the bucket becomes an empty
// bucket outside creative. Not yet read from BDS: how the block is picked
// (the built-over rule above is Helix's), picking liquids up, items a
// replaced block drops, the nether's rule for water, and buckets of fish
// and powder snow. False if nothing was poured.
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
	if b, ok := r.Block(clicked, 0); !ok || !p.s.game.replaceable(p, b, block) {
		pos = clicked.Side(cube.Face(face))
		if b, ok := r.Block(pos, 0); !ok || !p.s.game.replaceable(p, b, block) {
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
	}
	if !r.SetBlock(pos, layer, block) {
		return false
	}
	r.UpdateNeighbours(pos)
	r.ScheduleTick(pos, block, liquid.TickDelay(liq.kind, false), 0)
	r.Broadcast(pos, &packet.LevelSoundEvent{SoundType: liq.sound, Position: blockVec(pos).Add(mgl32.Vec3{0.5, 0.5, 0.5}), ExtraData: -1}, nil)
	if !p.creative() {
		inv := &p.inventory
		inv.main[inv.held] = ItemStack{Name: emptyBucket, Count: 1, netID: p.s.stackIDs.Add(1)}
		p.sendHeld()
	}
	return true
}
