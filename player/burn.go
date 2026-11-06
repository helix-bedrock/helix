package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Burning, from BDS 1.26.60: fire and soul fire blocks and lava hurt what is inside them and
// set it on fire (their entityInside), the player's minecraft:hurt_on_condition hurts it in
// lava, and the on-fire timer hurts it once a second until it runs out or water puts it out
// (BDS's OnFire system). Dropped items burn the same way; fire-resistant items take nothing.
// The research note is private.

// Damage causes of fire, as BDS's ActorDamageCause numbers them.
const (
	DamageFire     DamageCause = 6
	DamageFireTick DamageCause = 7
	DamageLava     DamageCause = 8
)

// Numbers of BDS's burning.
const (
	// fireBlockDamage and soulFireDamage are what standing in fire or soul fire deals each
	// tick; lava deals fireBlockDamage too, besides lavaDamage.
	fireBlockDamage = 1
	soulFireDamage  = 2
	// lavaDamage is the player's minecraft:hurt_on_condition in lava, each tick.
	lavaDamage = 4
	// burnSeconds is how long touching fire or lava sets something on fire.
	burnSeconds = 8
	ticksPerSec = 20
	// fireProtectionCut is how much of the fire time each level of Fire Protection worn
	// takes off.
	fireProtectionCut = 0.15
	// burnEvery is how often the fire hurts, in ticks; burnDamage is how much.
	burnEvery  = 20
	burnDamage = 1
	// immuneBurnOut is how fast fire goes out on something fire does not hurt.
	immuneBurnOut = 4
	// insideShrink is how far BDS shrinks a box before looking at the blocks inside it.
	insideShrink = 0.001
	// liquidLevels is how many heights a liquid has; a source stands one short of a full
	// block.
	liquidLevels = 9
	// fizzHeightShift is where the height sits in the fizz particles' data, above the width.
	fizzHeightShift = 8
)

// burnContact is what an entity's box touches that burns.
type burnContact struct {
	// fire is the damage of the fire it stands in (fire or soul fire), 0 for none.
	fire float32
	// lava is set when it is in lava; lavaBurn when it is deep enough in lava for lava's
	// entityInside, which needs the lava's surface above the bottom of the box.
	lava, lavaBurn bool
}

// burning reports whether c touches anything that sets on fire.
func (c burnContact) burning() bool { return c.fire > 0 || c.lavaBurn }

// touching finds what the box lo..hi touches that burns, as BDS's checkInsideBlocks walks the
// blocks inside the box.
func (s *Host) touching(r *world.Region, lo, hi mgl32.Vec3) burnContact {
	var c burnContact
	shrink := mgl32.Vec3{insideShrink, insideShrink, insideShrink}
	lo, hi = lo.Add(shrink), hi.Sub(shrink)
	from, to := floorPos(lo), floorPos(hi)
	blocks := s.reg.Blocks
	for x := from[0]; x <= to[0]; x++ {
		for y := from[1]; y <= to[1]; y++ {
			for z := from[2]; z <= to[2]; z++ {
				pos := cube.Pos{x, y, z}
				b, ok := r.Block(pos, 0)
				if !ok {
					continue
				}
				switch blocks.Name(b) {
				case nameFire:
					c.fire = max(c.fire, fireBlockDamage)
					continue
				case nameSoulFire:
					c.fire = max(c.fire, soulFireDamage)
					continue
				}
				if m, _ := liquidAt(s.reg, r, pos); m == registry.MaterialLava {
					c.lava = true
					if s.liquidTop(r, pos) > lo[1] {
						c.lavaBurn = true
					}
				}
			}
		}
	}
	return c
}

// liquidTop is the height of the surface of the liquid at pos: one ninth below the top of the
// block per level of depth, and a source one ninth below it.
func (s *Host) liquidTop(r *world.Region, pos cube.Pos) float32 {
	depth := int32(0)
	for layer := uint8(0); layer < 2; layer++ {
		if b, ok := r.Block(pos, layer); ok {
			if d, ok := s.reg.Blocks.States(b)["liquid_depth"].(int32); ok {
				depth = d
				break
			}
		}
	}
	if depth >= liquidFalling {
		depth = 0
	}
	return float32(pos[1]) + 1 - float32(depth+1)/liquidLevels
}

// burnState is a player's on-fire timer, BDS's OnFire component.
type burnState struct {
	ticks int32
	shown bool
}

// OnFire returns how many ticks the player has left on fire. Runs: the player's region.
func (p *Player) OnFire() int32 { return p.burn.ticks }

// SetOnFire sets the player on fire for seconds, as BDS's setOnFire: each level of Fire
// Protection worn takes 15% off, and a longer fire already burning is kept. Runs: the
// player's region.
func (p *Player) SetOnFire(seconds int32) {
	if seconds <= 0 || p.dead() {
		return
	}
	ticks := seconds * ticksPerSec
	if prot := p.fireProtection(); prot > 0 {
		share := min(max(1-float64(prot)*fireProtectionCut, 0), 1)
		ticks = int32(math.Round(float64(ticks) * share))
	}
	p.burn.ticks = max(p.burn.ticks, ticks)
	p.showBurning()
}

// Extinguish puts the player's fire out. Runs: the player's region.
func (p *Player) Extinguish() {
	p.burn.ticks = 0
	p.showBurning()
}

// fireProtection is the sum of the Fire Protection levels of the armour worn.
func (p *Player) fireProtection() int32 {
	var n int32
	for _, s := range p.inventory.armor {
		enchantLevels(s, func(id, lvl int16) {
			if id == EnchantFireProtection {
				n += int32(lvl)
			}
		})
	}
	return n
}

// fireImmune is BDS's isFireImmune for a player: creative and spectator, whom nothing but the
// void hurts. Assumed from the game modes, not read.
func (p *Player) fireImmune() bool { return p.invulnerable() }

// burnHurt deals fire damage, which Fire Resistance stops.
func (p *Player) burnHurt(damage float32, cause DamageCause) {
	if _, ok := p.Effect(EffectFireResistance); ok {
		return
	}
	p.s.game.sys.Hurt(p, damage, DamageSource{Cause: cause})
}

// tickBurning is a player's tick of fire: what it stands in hurts it and sets it on fire, lava
// hurts it, and the fire it is on burns down, hurting once a second, or goes out in water.
// Runs: the player's region, once a tick.
func (p *Player) tickBurning() {
	if p.entity == nil || p.dead() {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	at := p.entity.Position()
	feet := at[1] - eyeHeight
	lo := mgl32.Vec3{at[0] - playerHalfWidth, feet, at[2] - playerHalfWidth}
	hi := mgl32.Vec3{at[0] + playerHalfWidth, feet + playerHeight, at[2] + playerHalfWidth}
	c := p.s.touching(r, lo, hi)
	immune := p.fireImmune()
	if !immune {
		if c.fire > 0 {
			p.burnHurt(c.fire, DamageFire)
			p.SetOnFire(burnSeconds)
		}
		if c.lavaBurn {
			p.burnHurt(fireBlockDamage, DamageFire)
			p.SetOnFire(burnSeconds)
		}
		if c.lava {
			p.burnHurt(lavaDamage, DamageLava)
		}
	}
	if p.burn.ticks <= 0 || p.dead() {
		return
	}
	if !immune && p.move.inWater && !c.lava {
		p.Extinguish()
		fizzOut(r, lo, hi)
		return
	}
	if immune {
		p.burn.ticks -= immuneBurnOut
	} else {
		if p.burn.ticks%burnEvery == 0 {
			p.burnHurt(burnDamage, DamageFireTick)
		}
		p.burn.ticks--
	}
	p.showBurning()
}

// showBurning tells everyone, the player too, when the fire flag changes.
func (p *Player) showBurning() {
	on := p.burn.ticks > 0
	if on == p.burn.shown || p.entity == nil {
		return
	}
	p.burn.shown = on
	pk := actorDataAt(p.runtimeID, protocol.EntityMetadata{protocol.EntityDataKeyFlags: p.actorFlags()}, p.entity.Tick())
	p.entity.Send(pk)
	p.send(pk)
}

// tickBurning is a dropped item's tick of fire: fire and lava hurt it and set it on fire, and
// the fire burns down, hurting once a second. Fire-resistant items take nothing and their fire
// goes out four times as fast. Water puts it out unless it is in lava. Its health is 5, so an
// item in fire or lava is gone in five ticks.
func (i *ItemActor) tickBurning(r *world.Region, e *world.Entity, inWater bool) {
	at := e.Position()
	half := mgl32.Vec3{itemHalfSize, itemHalfSize, itemHalfSize}
	c := i.s.touching(r, at.Sub(half), at.Add(half))
	immune := i.s.game != nil && i.s.game.fireResistant[i.stack.Name]
	if c.lava && !immune {
		playSound(r, floorPos(at), soundEvent{name: packet.SoundEventFizz, pos: at}, nil)
	}
	if !immune && c.burning() {
		if c.fire > 0 {
			i.burnHurt(e, c.fire)
		}
		if !i.taken && c.lavaBurn {
			i.burnHurt(e, fireBlockDamage)
		}
		if !i.taken {
			i.burnTicks = max(i.burnTicks, burnSeconds*ticksPerSec)
		}
	}
	if i.taken || i.burnTicks <= 0 {
		i.showBurning(e)
		return
	}
	switch {
	case inWater && !c.lava:
		i.burnTicks = 0
		fizzOut(r, at.Sub(half), at.Add(half))
	case immune:
		i.burnTicks -= immuneBurnOut
	default:
		if i.burnTicks%burnEvery == 0 {
			i.burnHurt(e, burnDamage)
		}
		i.burnTicks--
	}
	if !i.taken {
		i.showBurning(e)
	}
}

// burnHurt takes fire damage from the item's health; it is gone at none left.
func (i *ItemActor) burnHurt(e *world.Entity, damage float32) {
	i.health -= int32(damage)
	if i.health <= 0 {
		i.taken = true
		e.Remove()
	}
}

// showBurning tells those who see the item when its fire flag changes.
func (i *ItemActor) showBurning(e *world.Entity) {
	on := i.burnTicks > 0 && !i.taken
	if on == i.burnShown {
		return
	}
	i.burnShown = on
	i.sendFlags(e)
}

// fizzOut is BDS's sound and smoke of a fire put out by water, for the box lo..hi: the fizz
// at its middle and the fizz particles from its corner, sized by its width and height.
func fizzOut(r *world.Region, lo, hi mgl32.Vec3) {
	mid := lo.Add(hi).Mul(0.5)
	at := floorPos(mid)
	playSound(r, at, soundEvent{name: packet.SoundEventFizz, pos: mid, extra: -1}, nil)
	size := hi.Sub(lo)
	width := int32(math.Ceil(float64(size[0])))
	height := int32(math.Ceil(float64(size[1])))
	levelEvent(r, at, lo, packet.LevelEventParticlesFizzEffect, width+height<<fizzHeightShift)
}
