package player

import (
	"math"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/world/cube"
)

// Freezing in powder snow, as BDS 1.26.60's FreezingSystem::tickSystem (sub_78663F0): while
// any block the player's box touches is powder snow, the freeze strength climbs by 1/140 a
// tick to 1, else it falls by 2/140 to 0. At full strength the player shakes (actor flag 40)
// and every 40 ticks takes 1 freezing damage (1.5 with the Freezing effect). Leather armour
// (any piece, BDS's FreezingImmuneFromEquipment, sub_B3C4130) stops it climbing, as do being
// dead or a spectator. The strength is sent as actor data 120, which the client draws as the
// frost overlay. BDS also adds a "Freeze effect" movement modifier of strength * -0.05; a
// player's movement is the client's own, so it is not sent.

// DamageFreezing is the damage cause of freezing.
const DamageFreezing DamageCause = 27

const (
	// freezeRise and freezeFall are how much the strength moves each tick.
	freezeRise = float32(1.0 / 140)
	freezeFall = float32(2.0 / 140)
	// freezeHurtEvery is how often a fully frozen player is hurt, in ticks.
	freezeHurtEvery = 40
	// freezeDamage and freezeEffectDamage are that hurt, without and with the Freezing effect.
	freezeDamage       = 1
	freezeEffectDamage = 1.5
	// freezeEpsilon is BDS's float compare for 0 and 1.
	freezeEpsilon = 1.1920929e-07
	// gameRuleFreezeDamage turns the hurt off.
	gameRuleFreezeDamage = "freezedamage"
)

// freezeImmuneWear is the armour that keeps a player from freezing.
var freezeImmuneWear = [...]string{"minecraft:leather_helmet", "minecraft:leather_chestplate", "minecraft:leather_leggings", "minecraft:leather_boots"}

// freezeState is the player's freeze strength and what clients were last told.
type freezeState struct {
	strength float32
	sent     float32
	shaking  bool
	ticks    uint32
}

// FreezeStrength is how frozen the player is, 0 to 1. Runs: the player's region.
func (p *Player) FreezeStrength() float32 { return p.freeze.strength }

// tickFreeze is BDS's freezing tick for a player. Runs: the player's region, once a tick.
func (p *Player) tickFreeze() {
	if p.entity == nil {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	f := &p.freeze
	f.ticks++
	_, effect := p.Effect(EffectFreezing)
	immune := p.freezeImmuneWorn()
	rising := false
	switch {
	case effect && !immune:
		rising = true
	case effect || immune || p.dead() || p.spectating():
	default:
		rising = p.inPowderSnow()
	}
	if f.strength == 0 && !rising {
		return
	}
	if rising {
		f.strength = min(f.strength+freezeRise, 1)
	} else {
		f.strength = max(f.strength-freezeFall, 0)
	}
	full := math.Abs(float64(f.strength-1)) <= freezeEpsilon
	if full && f.ticks%freezeHurtEvery == 0 && !p.invulnerable() {
		if on, ok := p.s.GameRule(gameRuleFreezeDamage); on || !ok {
			damage := float32(freezeDamage)
			if effect && !immune {
				damage = freezeEffectDamage
			}
			p.s.game.sys.Hurt(p, damage, DamageSource{Cause: DamageFreezing})
		}
	}
	if math.Abs(float64(f.strength)) <= freezeEpsilon {
		f.strength = 0
	}
	if f.shaking != full {
		f.shaking = full
		p.sendActorFlags()
		p.send(actorDataAt(p.runtimeID, protocol.EntityMetadata{protocol.EntityDataKeyFlags: p.actorFlags()}, p.entity.Tick()))
	}
	if f.sent != f.strength {
		f.sent = f.strength
		pk := actorDataAt(p.runtimeID, protocol.EntityMetadata{protocol.EntityDataKeyFreezingEffectStrength: f.strength}, p.entity.Tick())
		p.entity.Send(pk)
		p.send(pk)
	}
}

// freezeImmuneWorn reports leather armour in any armour slot.
func (p *Player) freezeImmuneWorn() bool {
	for _, it := range p.inventory.armor {
		for _, n := range freezeImmuneWear {
			if it.Name == n {
				return true
			}
		}
	}
	return false
}

// inPowderSnow reports powder snow in any block the player's box touches, the box shrunk by
// 0.001 on each side as BDS does.
func (p *Player) inPowderSnow() bool {
	r := p.entity.Region()
	pos := p.entity.Position()
	feet := pos[1] - playerEyeHeight
	const in = 0.001
	minX, maxX := int(floorInt(pos[0]-playerHalfWidth+in)), int(floorInt(pos[0]+playerHalfWidth-in))
	minY, maxY := int(floorInt(feet+in)), int(floorInt(feet+playerHeight-in))
	minZ, maxZ := int(floorInt(pos[2]-playerHalfWidth+in)), int(floorInt(pos[2]+playerHalfWidth-in))
	blocks := p.s.reg.Blocks
	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			for z := minZ; z <= maxZ; z++ {
				if b, ok := r.Block(cube.Pos{x, y, z}, 0); ok && blocks.Name(b) == namePowderSnow {
					return true
				}
			}
		}
	}
	return false
}
