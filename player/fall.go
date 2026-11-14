package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// DamageCause is why a player is hurt, as BDS's ActorDamageCause numbers
// it.
type DamageCause int32

// The damage causes Helix deals so far, and those BDS's damage rules name.
const (
	DamageNone         DamageCause = 0
	DamageEntityAttack DamageCause = 2
	DamageFall         DamageCause = 5
	DamageVoid         DamageCause = 12
	DamageSelfDestruct DamageCause = 13
	DamageStarve       DamageCause = 16
)

// DamageSource is where a hurt comes from.
type DamageSource struct {
	Cause DamageCause
	// Attacker is the player behind it, if any.
	Attacker *Player
	// Knockback pushes the player away from Attacker; Extra adds to the push.
	Knockback bool
	Extra     float32
	// Block is the name of the block that hurt, for contact damage.
	Block string
}

// fallBigDamage is the damage from which BDS plays the big fall sound; fallFree is
// how far a fall costs nothing.
const (
	// gameRuleFallDamage turns all fall damage off: landings and ender pearls, for
	// players and mobs alike.
	gameRuleFallDamage = "falldamage"
	fallBigDamage      = 5
	fallFree           = 3
)

// vanillaFall is BDS's fall distance (Actor::checkFallDamage, with the
// resets of climbing, gliding and slow falling that BDS's movement does
// before it): in water it is cleared; on landing the FallDamage system
// runs with the distance fallen, then it is cleared; it grows while the
// player moves down and is cleared when it does not.
func vanillaFall(p *Player, m *MoveInfo) {
	if p.vehicle != nil {
		p.move.fall = 0
		return
	}
	dy := m.To[1] - m.From[1]
	_, slow := p.Effect(EffectSlowFalling)
	if m.Climbing || p.Moving(MoveGliding) || slow && dy < 0 {
		p.move.fall = 0
	}
	switch {
	case m.InWater:
		p.move.fall = 0
	case m.OnGround:
		if p.move.fall > 0 {
			p.s.game.sys.FallDamage(p, p.move.fall)
		}
		p.move.fall = 0
	case dy < 0:
		p.move.fall -= dy
	default:
		p.move.fall = 0
	}
}

// fallHurts reports whether the falldamage game rule lets falls hurt; a level without
// the rule keeps fall damage on.
func (s *Host) fallHurts() bool {
	on, ok := s.GameRule(gameRuleFallDamage)
	return on || !ok
}

// vanillaFallDamage is BDS's landing (the block's fall-on, then the
// player's causeFallDamage): damage ceil(distance - 3 - jump boost level),
// a big or small fall sound when it hurts, then the fall or land sound of
// the block below. Blocks that change the fall (slime, hay, beds) are not
// ported yet.
func vanillaFallDamage(p *Player, distance float32) {
	r := p.entity.Region()
	if r == nil {
		return
	}
	var boost float32
	if amp, ok := p.Effect(EffectJumpBoost); ok {
		boost = float32(amp + 1)
	}
	damage := float32(math.Ceil(float64(distance-p.Tuning(TuneFallFree)-boost))) * p.Tuning(TuneFallDamageScale)
	if !p.s.fallHurts() {
		damage = 0
	}
	feet := p.entity.Position().Sub(mgl32.Vec3{0, eyeHeight, 0})
	pos := vecPos(feet)
	p.trample(r, feet, distance)
	if damage > 0 && !p.invulnerable() {
		sound := packet.SoundEventFallSmall
		if damage >= fallBigDamage {
			sound = packet.SoundEventFallBig
		}
		playSound(r, pos, soundEvent{name: sound, pos: feet, extra: -1, entity: "minecraft:player"}, p.viewer)
		p.s.game.sys.Hurt(p, damage, DamageSource{Cause: DamageFall})
	}
	sound := packet.SoundEventLand
	if damage > 0 {
		sound = packet.SoundEventFall
	}
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok {
		return
	}
	playSound(r, pos, p.s.blockSound(sound, feet, below).by("minecraft:player"), p.viewer)
}
