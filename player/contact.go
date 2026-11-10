package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
)

// Cactus and sweet berry bush damage, from BDS 1.26.60. Each tick BDS's EntityInside system
// walks the blocks inside an entity's box (shrunk by 0.001, as for fire) and records the block
// that touched it; the actor's base tick then asks that block whether it hurts (a Block
// virtual) and, if so, deals 1 contact damage from it (an ActorDamageByBlockSource):
//
//   - Cactus always hurts. Its box is 1/16 inside its cell, so walking into one counts.
//   - A sweet berry bush hurts only mobs (players too) when it is past its first growth stage
//     and the mob moved more than 0.003 blocks along x or z that tick.
//
// Dropped items run the same base tick: their health is 5 and they have no hurt cooldown, so a
// cactus destroys an item in five ticks. Berry bushes leave items alone (not mobs).
// Jumping on a berry bush does nothing more in BDS; only farmland is trampled.

// DamageContact is BDS's ActorDamageCause for touching a cactus or berry bush.
const DamageContact DamageCause = 1

const (
	nameCactus     = "minecraft:cactus"
	nameBerryBush  = "minecraft:sweet_berry_bush"
	stateGrowth    = "growth"
	contactDamage  = 1
	berryMoveLimit = 0.003
)

// contactBlock is what a box touches that hurts by contact.
type contactBlock uint8

const (
	contactNone contactBlock = iota
	contactCactus
	contactBerry
)

// contactOf reports whether the block b hurts by contact: a cactus, or a berry bush past its
// first stage.
func (s *Host) contactOf(b uint32) contactBlock {
	switch s.reg.Blocks.Name(b) {
	case nameCactus:
		return contactCactus
	case nameBerryBush:
		if g, ok := s.reg.Blocks.States(b)[stateGrowth].(int32); ok && g > 0 {
			return contactBerry
		}
	}
	return contactNone
}

// tickContact deals the player its contact damage for what its box touches this tick. For a
// player BDS's berry bush check (0xD4E0C70) reads PostTickPositionDeltaComponent, how far
// its last movement took it, not its velocity, which the client keeps at 0 while stuck in
// the bush. The client plays the bush's scratch sound (block.sweet_berry_bush.hurt) itself on
// each hurt; BDS sends no sound for it. Runs: the player's region, once a tick.
func (p *Player) tickContact(c burnContact) {
	p.move.inBerry = c.contact == contactBerry
	if p.invulnerable() {
		return
	}
	switch c.contact {
	case contactCactus:
	case contactBerry:
		v := p.move.moved
		if abs32(v[0]) <= berryMoveLimit && abs32(v[2]) <= berryMoveLimit {
			return
		}
	default:
		return
	}
	block := nameCactus
	if c.contact == contactBerry {
		block = nameBerryBush
	}
	p.s.game.sys.Hurt(p, contactDamage, DamageSource{Cause: DamageContact, Block: block})
}

// tickContact is a dropped item's contact damage: a cactus takes 1 from its health each tick.
func (i *ItemActor) tickContact(e *world.Entity, c burnContact) {
	if c.contact == contactCactus && !i.taken {
		i.loseHealth(e, contactDamage)
	}
}

// playerBox is a player's box at its eye position at.
func playerBox(at mgl32.Vec3) (lo, hi mgl32.Vec3) {
	feet := at[1] - eyeHeight
	lo = mgl32.Vec3{at[0] - playerHalfWidth, feet, at[2] - playerHalfWidth}
	hi = mgl32.Vec3{at[0] + playerHalfWidth, feet + playerHeight, at[2] + playerHalfWidth}
	return lo, hi
}
