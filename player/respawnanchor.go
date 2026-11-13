package player

import (
	"math"
	"sync"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Respawn anchors as BDS 1.26.60 (Linux, IDA): RespawnAnchorBlock's use listener
// 0xD53B9F0, the charge change 0xD53CB90, Player::setSpawnPoint 0xBCF6300, the respawn
// (0xBCED800) with its spawn check 0xBCD4290, the charge taken (slot 97) and the place
// search 0xCD8EDB0.
const (
	nameRespawnAnchor  = "minecraft:respawn_anchor"
	stateAnchorCharge  = "respawn_anchor_charge"
	nameGlowstone      = "minecraft:glowstone"
	anchorMaxCharge    = 4
	anchorBlast        = 5
	anchorWaterToCalm  = 2
	anchorSearchFrom   = -1
	anchorSearchTo     = 2
	msgAnchorSpawnSet  = "tile.respawn_anchor.respawnSet"
	msgAnchorNotValid  = "tile.respawn_anchor.notValid"
	nameWaterBlock     = "minecraft:water"
	nameFlowingWaterBl = "minecraft:flowing_water"
)

// respawnAnchorBlock is the respawn anchor's behaviour. solid is helix-data's
// bds/block_solid.json (BDS's Block solid flag), read once: the place search stands players
// on these blocks.
type respawnAnchorBlock struct {
	once  sync.Once
	solid map[string]bool
}

// Use is the use listener: glowstone charges an anchor that is not full; a charged anchor
// sets the player's spawn in the Nether and explodes anywhere else (when respawn blocks
// explode); an empty one leaves the click to the item.
func (a *respawnAnchorBlock) Use(u *BlockUse) bool {
	p := u.Player
	r := p.entity.Region()
	if r == nil {
		return false
	}
	charge := anchorCharge(p, u.Block)
	if u.Item.Name == nameGlowstone && charge < anchorMaxCharge {
		if !p.creative() {
			p.useHeld()
		}
		p.s.setAnchorCharge(r, u.Pos, u.Block, charge+1)
		return true
	}
	if charge <= 0 {
		return false
	}
	if p.dim == p.s.nether {
		p.setAnchorSpawn(u.Pos)
		return true
	}
	if p.s.level.RespawnBlocksExplode {
		p.s.explodeAnchor(r, u.Pos, u.Block)
	}
	return true
}

// anchorCharge is the charge of an anchor state, 0 to 4.
func anchorCharge(p *Player, block uint32) int32 {
	c, _ := p.s.reg.Blocks.States(block)[stateAnchorCharge].(int32)
	return c
}

// setAnchorCharge sets the anchor at pos to charge, clamped to 0..4, with the charge sound
// when it goes up and the deplete sound when it goes down.
func (s *Host) setAnchorCharge(r *world.Region, pos cube.Pos, block uint32, charge int32) {
	blocks := s.reg.Blocks
	old, _ := blocks.States(block)[stateAnchorCharge].(int32)
	charge = min(max(charge, 0), anchorMaxCharge)
	next, ok := blocks.StateID(nameRespawnAnchor, map[string]any{stateAnchorCharge: charge})
	if !ok {
		return
	}
	if r.SetBlock(pos, 0, next) {
		r.UpdateNeighbours(pos)
	}
	name := packet.SoundEventRespawnAnchorDeplete
	if charge > old {
		name = packet.SoundEventRespawnAnchorCharge
	}
	playSound(r, pos, soundEvent{name: name, pos: blockCenter(pos), extra: -1}, nil)
}

// setAnchorSpawn makes the anchor at pos the player's spawn in the Nether, unless it already
// is: the block, where the player stands (floored, feet rounded up) and the dimension, as
// setSpawnPoint keeps them, the SetSpawnPosition packet, the set_spawn sound and message.
func (p *Player) setAnchorSpawn(pos cube.Pos) {
	if p.spawn.set && p.spawn.dim == spawnNether && p.spawn.pos == pos {
		return
	}
	feet := p.entity.Position().Sub(mgl32.Vec3{0, playerEyeHeight, 0})
	stand := cube.Pos{int(math.Floor(float64(feet[0]))), int(math.Ceil(float64(feet[1]))), int(math.Floor(float64(feet[2])))}
	p.spawn = spawnPointState{set: true, pos: pos, stand: stand, dim: spawnNether}
	p.send(&packet.SetSpawnPosition{SpawnType: packet.SpawnTypePlayer, Position: blockPos(stand), Dimension: spawnNether, SpawnPosition: blockPos(pos)})
	if r := p.entity.Region(); r != nil {
		playSound(r, pos, soundEvent{name: packet.SoundEventRespawnAnchorSetSpawn, pos: blockCenter(pos), extra: -1}, nil)
	}
	p.tell(msgAnchorSpawnSet)
}

// explodeAnchor is the anchor used outside the Nether: it goes, then a power 5 explosion at
// its centre with fire, as BDS's: no actor behind it, and under water when two or more of
// its six neighbours are water, which keeps the blocks around it.
func (s *Host) explodeAnchor(r *world.Region, pos cube.Pos, block uint32) {
	under := s.anchorWater(r, pos) >= anchorWaterToCalm
	if r.SetBlock(pos, 0, s.reg.Blocks.Air()) {
		r.UpdateNeighbours(pos)
	}
	x := NewExplosion(blockCenter(pos), anchorBlast)
	x.Fire, x.MaxResistance, x.Underwater = true, math.MaxFloat32, &under
	x.Kind, x.FromBlock, x.Block, x.BlockPos = ExplosionRespawnAnchor, true, block, pos
	s.Explode(r, x)
}

// anchorWater counts the anchor's neighbours, below, above, north, south, west and east,
// that are water on the first layer.
func (s *Host) anchorWater(r *world.Region, pos cube.Pos) int {
	n := 0
	for _, f := range [...]cube.Face{cube.FaceDown, cube.FaceUp, cube.FaceNorth, cube.FaceSouth, cube.FaceWest, cube.FaceEast} {
		if b, ok := r.Block(pos.Side(f), 0); ok {
			if name := s.reg.Blocks.Name(b); name == nameWaterBlock || name == nameFlowingWaterBl {
				n++
			}
		}
	}
	return n
}

// anchorBlock returns the respawn anchor's behaviour, nil if a plugin replaced it.
func (s *Host) anchorBlock() *respawnAnchorBlock {
	a, _ := s.game.blocks[nameRespawnAnchor].(*respawnAnchorBlock)
	return a
}

// isSolid is BDS's Block solid flag of b.
func (a *respawnAnchorBlock) isSolid(s *Host, b uint32) bool {
	a.once.Do(func() {
		a.solid = map[string]bool{}
		names, err := data.SolidBlocks()
		if err != nil {
			return
		}
		for _, n := range names {
			a.solid[n] = true
		}
	})
	return a.solid[s.reg.Blocks.Name(b)]
}

// anchorSpot is where a player respawns at its anchor in r, or false if the anchor is gone or
// empty, or there is no room. take uses up a charge, as BDS does when it respawns the player.
func (p *Player) anchorSpot(r *world.Region, take bool) (mgl32.Vec3, bool) {
	if r == nil || !p.spawn.set || p.spawn.dim != spawnNether {
		return mgl32.Vec3{}, false
	}
	b, ok := r.Block(p.spawn.pos, 0)
	if !ok || p.s.reg.Blocks.Name(b) != nameRespawnAnchor || anchorCharge(p, b) <= 0 {
		return mgl32.Vec3{}, false
	}
	a := p.s.anchorBlock()
	if a == nil {
		return mgl32.Vec3{}, false
	}
	at, ok := a.standSpot(p.s, r, p.spawn.stand)
	if !ok {
		return mgl32.Vec3{}, false
	}
	if take {
		p.s.setAnchorCharge(r, p.spawn.pos, b, anchorCharge(p, b)-1)
	}
	return mgl32.Vec3{float32(at[0]) + 0.5, float32(at[1]) + playerEyeHeight, float32(at[2]) + 0.5}, true
}

// standSpot is BDS's place search (0xCD8EDB0): where the player stood when it set the
// spawn, if it can stand there still (a solid block below, it and the one above not solid);
// else the first such place from x-1..x+2, z-1..z+2 at the same height, z changing fastest.
func (a *respawnAnchorBlock) standSpot(s *Host, r *world.Region, from cube.Pos) (cube.Pos, bool) {
	if a.canStand(s, r, from) {
		return from, true
	}
	for dx := anchorSearchFrom; dx <= anchorSearchTo; dx++ {
		for dz := anchorSearchFrom; dz <= anchorSearchTo; dz++ {
			at := from.Add(cube.Pos{dx, 0, dz})
			if a.canStand(s, r, at) {
				return at, true
			}
		}
	}
	return cube.Pos{}, false
}

// canStand reports whether a player can stand at pos: solid below, not in or above.
func (a *respawnAnchorBlock) canStand(s *Host, r *world.Region, pos cube.Pos) bool {
	below, ok1 := r.Block(pos.Side(cube.FaceDown), 0)
	at, ok2 := r.Block(pos, 0)
	above, ok3 := r.Block(pos.Side(cube.FaceUp), 0)
	return ok1 && ok2 && ok3 && a.isSolid(s, below) && !a.isSolid(s, at) && !a.isSolid(s, above)
}

// respawnAtAnchor respawns a player who died in the Nether at its anchor there, taking a
// charge. BDS 1.26.60's Respawn (0xBCED800) checks the spawn against the dimension the
// player died in (0xBCD4290), so an anchor never pulls a player back from another
// dimension: one who died elsewhere respawns as without it. False if the player is not in
// the Nether or its spawn is no anchor; an anchor that is gone, empty or blocked is
// forgotten with BDS's message, and the caller respawns the player as without one.
func (p *Player) respawnAtAnchor() bool {
	if !p.spawn.set || p.spawn.dim != spawnNether || p.s.nether == nil || p.dim != p.s.nether {
		return false
	}
	at, ok := p.anchorSpot(p.entity.Region(), true)
	if !ok {
		p.forgetAnchor()
		return false
	}
	p.s.game.sys.Respawn(p, at)
	return true
}

// forgetAnchor clears a spawn whose anchor no longer works, with BDS's message.
func (p *Player) forgetAnchor() {
	p.spawn = spawnPointState{}
	p.tell(msgAnchorNotValid)
}
