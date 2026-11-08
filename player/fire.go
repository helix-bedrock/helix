package player

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// nameSoulFire is soul fire, which a player puts out like fire.
const nameSoulFire = "minecraft:soul_fire"

// lightFire is BDS's FlintAndSteelItem: always on the block beside the clicked face. If that
// block is air it plays the ignite sound and sets fire there (the fire turns to soul fire on
// soul sand or soul soil by itself). Either way the item wears by one, as BDS's code does.
// Fire lit inside an empty obsidian frame makes a nether portal instead (trySpawnPortal).
func (p *Player) lightFire(r *world.Region, clicked cube.Pos, face int32, held ItemStack) bool {
	if face < 0 || face > int32(cube.FaceEast) || !p.inReach(clicked) {
		return false
	}
	pos := clicked.Side(cube.Face(face))
	blocks := p.s.reg.Blocks
	if b, ok := r.Block(pos, 0); ok && b == blocks.Air() {
		playSound(r, pos, soundEvent{name: packet.SoundEventIgnite, pos: blockCenter(pos), extra: -1}, nil)
		if !p.lightPortal(r, pos) {
			if fire, ok := blocks.Block(nameFire); ok && p.mayPlaceFire(pos, fire) && r.SetBlock(pos, 0, fire) {
				r.UpdateNeighbours(pos)
			}
		}
	}
	if !p.creative() {
		p.hurtHeld(1)
	}
	return true
}

// mayPlaceFire runs BlockPlaceEvent for fire set by the player; false if a listener stopped
// it.
func (p *Player) mayPlaceFire(pos cube.Pos, fire uint32) bool {
	g := p.s.game
	if !g.placeEv.Has() {
		return true
	}
	e := BlockPlaceEvent{Player: p, Pos: pos, Block: fire}
	g.placeEv.fire(p.s.log, &e)
	return !e.Cancelled()
}

// putOutFire is BDS's extinguishFire, run when a player starts breaking a block: fire or soul
// fire in the block beside the clicked face goes out with the extinguish sound. True if it
// did; the block clicked is then not broken.
func (p *Player) putOutFire(r *world.Region, clicked cube.Pos, face int32) bool {
	if face < 0 || face > int32(cube.FaceEast) {
		return false
	}
	pos := clicked.Side(cube.Face(face))
	b, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	if name := p.s.reg.Blocks.Name(b); name != nameFire && name != nameSoulFire {
		return false
	}
	playSound(r, pos, soundEvent{name: packet.SoundEventExtinguishFire, pos: blockCenter(pos), extra: -1}, nil)
	if r.SetBlock(pos, 0, p.s.reg.Blocks.Air()) {
		r.UpdateNeighbours(pos)
	}
	return true
}
