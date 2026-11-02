package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// bucketLiquid is what a bucket pours and the sound it makes.
type bucketLiquid struct {
	block, sound string
}

// bucketLiquids are the buckets that pour a liquid.
var bucketLiquids = map[string]bucketLiquid{
	"minecraft:water_bucket": {"minecraft:water", packet.SoundEventBucketEmptyWater},
	"minecraft:lava_bucket":  {"minecraft:lava", packet.SoundEventBucketEmptyLava},
}

// emptyBucket is what a bucket becomes once poured outside creative.
const emptyBucket = "minecraft:bucket"

// pourBucket is a first pass of BDS's bucket useOn for water and lava: a
// source block goes in the clicked block if it can be built over, else in
// its neighbour on the clicked face if that can, with the empty sound, and
// the bucket becomes an empty bucket outside creative. Not yet read from
// BDS's bucket code: waterlogging, picking liquids up, the nether's rule
// for water, and buckets of fish and powder snow. The liquid does not flow
// yet. False if nothing was poured.
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
	if !r.SetBlock(pos, 0, block) {
		return false
	}
	r.Broadcast(pos, &packet.LevelSoundEvent{SoundType: liq.sound, Position: blockVec(pos).Add(mgl32.Vec3{0.5, 0.5, 0.5}), ExtraData: -1}, nil)
	if !p.creative() {
		inv := &p.inventory
		inv.main[inv.held] = ItemStack{Name: emptyBucket, Count: 1, netID: p.s.stackIDs.Add(1)}
		p.sendHeld()
	}
	return true
}
