package player

import (
	"math"
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Living is anything with health that the world hurts: players and mobs. The hazards are
// written once against it (fire, lava and burning, cactus and berry bushes, falls,
// explosions, lightning, projectiles), so each hurts both, and a new hazard written the same
// way hurts both too. Each kind keeps its own hurt rules behind TakeDamage: a player's
// armour, effects and Systems.Hurt; a mob's damage sensors and hurt time.
type Living interface {
	Blastable
	// TakeDamage deals damage through the entity's own hurt and reports whether it took any.
	TakeDamage(damage float32, src DamageSource) bool
	// Dead reports no health left.
	Dead() bool
	// FireImmune means fire and lava do not hurt it and its fire burns out four times as fast.
	FireImmune() bool
	// OnFire is how many ticks it still burns; SetFireTicks sets that and shows the flag.
	OnFire() int32
	SetFireTicks(ticks int32)
	// SetOnFire lights it for at least seconds, after its own cuts (Fire Protection).
	SetOnFire(seconds int32)
	// Wet reports water, or rain on it, which put its fire out.
	Wet() bool
	// LavaDamage is what lava deals it each tick besides lava's fire: the player's own
	// hurt_on_condition. A mob's lava damage is in its definition, so it answers 0.
	LavaDamage() float32
}

var (
	_ Living = (*Player)(nil)
	_ Living = (*Mob)(nil)
)

// tickHazards is one tick of what a living entity's box lo..hi touches: contact blocks
// (moved is how far it moved this tick, which a berry bush needs), fire and lava, then its
// own fire burning down, hurting once a second, or going out in water or rain. It returns
// what the box touched. Runs: the entity's region, once a tick.
func (s *Host) tickHazards(r *world.Region, l Living, lo, hi, moved mgl32.Vec3) burnContact {
	return s.tickHazardsFrom(r, l, lo, hi, moved, s.touching(r, lo, hi))
}

// tickHazardsFrom is tickHazards with what the box touches already found.
func (s *Host) tickHazardsFrom(r *world.Region, l Living, lo, hi, moved mgl32.Vec3, c burnContact) burnContact {
	if l.Dead() {
		return c
	}
	s.contactHurt(l, c, moved)
	if l.Dead() {
		return c
	}
	s.campfireHurt(r, l, c.campfire)
	if l.Dead() {
		return c
	}
	immune := l.FireImmune()
	if !immune {
		if c.fire > 0 {
			l.TakeDamage(c.fire, DamageSource{Cause: DamageFire})
			l.SetOnFire(burnSeconds)
		}
		if c.lavaBurn {
			l.TakeDamage(fireBlockDamage, DamageSource{Cause: DamageFire})
			l.SetOnFire(burnSeconds)
		}
		if d := l.LavaDamage(); c.lava && d > 0 {
			l.TakeDamage(d, DamageSource{Cause: DamageLava})
		}
	}
	ticks := l.OnFire()
	if ticks <= 0 || l.Dead() {
		return c
	}
	if !immune && l.Wet() && !c.lava {
		l.SetFireTicks(0)
		fizzOut(r, lo, hi)
		return c
	}
	if immune {
		l.SetFireTicks(ticks - immuneBurnOut)
		return c
	}
	if ticks%burnEvery == 0 {
		l.TakeDamage(burnDamage, DamageSource{Cause: DamageFireTick})
	}
	l.SetFireTicks(ticks - 1)
	return c
}

// contactHurt deals contact damage for what the box touches. A berry bush hurts only what
// moved more than a little this tick: for a player BDS's berry bush check (0xD4E0C70) reads
// PostTickPositionDeltaComponent, how far its last movement took it, not its velocity,
// which the client keeps at 0 while stuck in the bush; mobs are taken the same way, not
// checked. The client plays the bush's scratch sound (block.sweet_berry_bush.hurt) itself on
// each hurt; BDS sends no sound for it.
func (s *Host) contactHurt(l Living, c burnContact, moved mgl32.Vec3) {
	block := nameCactus
	switch c.contact {
	case contactCactus:
	case contactBerry:
		if abs32(moved[0]) <= berryMoveLimit && abs32(moved[2]) <= berryMoveLimit {
			return
		}
		block = nameBerryBush
	default:
		return
	}
	l.TakeDamage(contactDamage, DamageSource{Cause: DamageContact, Block: block})
}

// --- Player as Living ---

// TakeDamage hurts the player through the Hurt system. Fire Resistance stops fire and lava
// damage, as BDS's hurt does.
func (p *Player) TakeDamage(damage float32, src DamageSource) bool {
	if fireCause(src.Cause) {
		if _, ok := p.Effect(EffectFireResistance); ok {
			return false
		}
	}
	return p.s.game.sys.Hurt(p, damage, src)
}

// Dead reports the player out of health.
func (p *Player) Dead() bool { return p.dead() }

// FireImmune is BDS's isFireImmune for a player: creative and spectator, whom nothing but the
// void hurts. Assumed from the game modes, not read.
func (p *Player) FireImmune() bool { return p.fireImmune() }

// SetFireTicks sets how long the player burns. Runs: the player's region.
func (p *Player) SetFireTicks(ticks int32) {
	p.burn.ticks = max(ticks, 0)
	p.showBurning()
}

// Wet reports the player in water, or in the rain at its head.
func (p *Player) Wet() bool {
	if p.move.inWater {
		return true
	}
	r := p.entity.Region()
	if r == nil {
		return false
	}
	at := p.entity.Position()
	return p.s.inRain(r, mgl32.Vec3{at[0], at[1] - eyeHeight + playerHeight, at[2]})
}

// LavaDamage is the player's minecraft:hurt_on_condition in lava.
func (p *Player) LavaDamage() float32 { return lavaDamage }

// fireCause reports the damage causes fire and Fire Resistance are about.
func fireCause(c DamageCause) bool {
	return c == DamageFire || c == DamageFireTick || c == DamageLava
}

// --- Mob as Living ---

// TakeDamage hurts the mob through its damage sensors and hurt time.
func (m *Mob) TakeDamage(damage float32, src DamageSource) bool {
	return m.hurt(damage, src.Attacker, src.Cause) > 0
}

// Dead reports the mob out of health or gone.
func (m *Mob) Dead() bool { return m.gone || m.m.Dead() }

// FireImmune reports minecraft:fire_immune.
func (m *Mob) FireImmune() bool { return m.m.FireImmune() }

// OnFire is how many ticks the mob still burns.
func (m *Mob) OnFire() int32 { return m.m.FireTicks() }

// SetFireTicks sets how long the mob burns.
func (m *Mob) SetFireTicks(ticks int32) { m.m.SetFireTicks(ticks) }

// SetOnFire lights the mob for at least seconds. Fire Protection on mob armour is not
// counted: mobs wear nothing yet.
func (m *Mob) SetOnFire(seconds int32) {
	if seconds <= 0 || m.Dead() || m.FireImmune() {
		return
	}
	m.m.SetFireTicks(max(m.m.FireTicks(), seconds*ticksPerSec))
}

// Wet reports the mob in water or the rain.
func (m *Mob) Wet() bool { return m.m.Wet() }

// LavaDamage is 0: a mob's lava damage is its definition's hurt_on_condition.
func (m *Mob) LavaDamage() float32 { return 0 }

// BlastBox is the mob's box; a dead mob is passed by.
func (m *Mob) BlastBox() (lo, hi mgl32.Vec3, ok bool) {
	if m.Dead() {
		return lo, hi, false
	}
	b := m.m.Box()
	return mglVec(b.Min), mglVec(b.Max), true
}

// BlastHit hurts the mob.
func (m *Mob) BlastHit(damage float32, src DamageSource) { m.TakeDamage(damage, src) }

// BlastPush adds to the mob's speed and shows it.
func (m *Mob) BlastPush(push mgl32.Vec3) {
	if m.Dead() {
		return
	}
	b := m.m.Body()
	b.Vel = behavior.Vec3{X: b.Vel.X + push[0], Y: b.Vel.Y + push[1], Z: b.Vel.Z + push[2]}
	m.entity.Send(&packet.SetActorMotion{EntityRuntimeID: m.entity.RuntimeID(), Velocity: mglVec(b.Vel), Tick: m.entity.Tick()})
}

// tickHazards runs the shared hazards on the mob, then its landing. Runs: Apply.
func (m *Mob) tickHazards(r *world.Region) {
	b := m.m.Body()
	box := m.m.Box()
	moved := mglVec(b.Pos).Sub(mglVec(b.PrevPos))
	m.s.tickHazards(r, m, mglVec(box.Min), mglVec(box.Max), moved)
	if b.Landed && !m.Dead() {
		m.s.landed(r, m, mglVec(b.Pos), b.LandedFall, m.m.Type())
	}
}

// hasFamily reports the mob in the type family.
func (m *Mob) hasFamily(f string) bool { return slices.Contains(m.m.Families(), f) }

// landed is BDS's landing for an entity that is not a player (the player's goes through
// Systems.Fall and FallDamage, which share these rules): damage ceil(distance - 3), a big or
// small fall sound when it hurts, then the fall or land sound of the block below. A mob's
// damage sensor decides whether the fall hurts (chickens, cats). Jump Boost and blocks that
// change the fall are not counted: mobs have no effects yet.
func (s *Host) landed(r *world.Region, l Living, feet mgl32.Vec3, distance float32, typ string) {
	damage := float32(math.Ceil(float64(distance-s.Tuning(TuneFallFree)))) * s.Tuning(TuneFallDamageScale)
	if !s.fallHurts() {
		damage = 0
	}
	pos := floorPos(feet)
	if damage > 0 {
		sound := packet.SoundEventFallSmall
		if damage >= fallBigDamage {
			sound = packet.SoundEventFallBig
		}
		playSound(r, pos, soundEvent{name: sound, pos: feet, extra: -1, entity: typ}, nil)
		l.TakeDamage(damage, DamageSource{Cause: DamageFall})
	}
	sound := packet.SoundEventLand
	if damage > 0 {
		sound = packet.SoundEventFall
	}
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok {
		return
	}
	playSound(r, pos, s.blockSound(sound, feet, below).by(typ), nil)
}
