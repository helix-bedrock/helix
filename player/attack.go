package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Numbers of BDS 1.26.60's attack.
const (
	// attackFromRange is how far the position a client says it attacked from
	// may be from where the server has it.
	attackFromRange = 6
	// attackRange and attackRangeCreative are the pick range to the target.
	attackRange         = 6.7
	attackRangeCreative = 12
	// attackScan is how far around the player its target is looked for.
	attackScan = 13
	// critMultiplier is what a critical hit multiplies damage by.
	critMultiplier = 1.5
	// strengthMultiplier and strengthAdd are Strength, once per level.
	strengthMultiplier = 1.3
	strengthAdd        = 1
	// weaknessMultiplier and weaknessTake are Weakness, once per level.
	weaknessMultiplier = 0.8
	weaknessTake       = 0.5
	// sharpnessPerLevel is the damage Sharpness adds per level.
	sharpnessPerLevel = 1.25
	// knockbackPerLevel and sprintKnockback make up a hit's extra knockback.
	knockbackPerLevel = 1.15
	sprintKnockback   = 1
	// hitWearDefault and hitWearWeapon are what a hit wears the held item by.
	hitWearDefault = 2
	hitWearWeapon  = 1
	// critEventData is the data BDS sends with the critical hit animation.
	critEventData = 55
)

// Knockback BDS gives a hit: its first push, then the extra from the Knockback
// enchantment and sprinting, in the attacker's direction.
const (
	knockbackPush      = 0.4
	knockbackCap       = 0.4
	knockbackRetain    = 0.5
	knockbackLift      = 1.2
	knockbackExtraPush = 0.4
	knockbackExtraLift = 0.1
	knockbackLimit     = 128
	knockbackMinLength = 0.0001
)

// Vanilla enchantment and effect IDs a hit reads (BDS 1.26.60).
const (
	EnchantSharpness int16 = 9
	EnchantKnockback int16 = 12

	EffectStrength  int32 = 5
	EffectBlindness int32 = 15
	EffectWeakness  int32 = 18
)

// AttackFunc is a player hitting target; false if nothing happened.
type AttackFunc func(p, target *Player) bool

// KnockbackFunc pushes target away from attacker. extra is the extra knockback
// of the hit.
type KnockbackFunc func(target, attacker *Player, extra float32)

// combatTable is helix-data's bds/item_combat.json by item name.
type combatTable struct {
	damage  map[string]int32
	armor   map[string]data.ArmorValue
	wearOne map[string]bool
}

func loadCombat() (*combatTable, error) {
	c, err := data.ItemCombatData()
	if err != nil {
		return nil, err
	}
	return &combatTable{damage: c.Damage, armor: c.Armor, wearOne: set(c.HitWearOne)}, nil
}

// handleUseOnEntity is BDS's use on actor transaction, with its checks in order.
// Interacting with entities is not built yet.
func (p *Player) handleUseOnEntity(d *protocol.UseItemOnEntityTransactionData) {
	if p.dead() || p.entity == nil {
		return
	}
	if p.useOnCart(d) || p.useOnCushion(d) {
		return
	}
	if d.ActionType != protocol.UseItemOnEntityActionAttack {
		return
	}
	if !p.selectSlot(d.HotBarSlot, d.HeldItem.Stack.NetworkID) {
		p.sendHeld()
		p.flush()
		return
	}
	target := p.playerNear(d.TargetEntityRuntimeID)
	if target == nil || target == p {
		return
	}
	pos := p.entity.Position()
	if pos.Sub(d.Position).Len() > attackFromRange {
		return
	}
	limit := float32(attackRange)
	if p.creative() {
		limit = attackRangeCreative
	}
	if pos.Sub(target.entity.Position()).Len() > limit {
		return
	}
	p.s.game.sys.Attack(p, target)
}

// playerNear finds a player near this one by its runtime ID. Only entities of the
// player's own region are looked at: those are the ones it can safely change.
func (p *Player) playerNear(id uint64) *Player {
	r := p.entity.Region()
	if r == nil {
		return nil
	}
	pos := p.entity.Position()
	p.touching = r.AppendEntitiesIn(p.touching[:0], pos[0]-attackScan, pos[2]-attackScan, pos[0]+attackScan, pos[2]+attackScan)
	for _, e := range p.touching {
		if e.RuntimeID() != id {
			continue
		}
		if o, ok := e.Owner().(*Player); ok && !o.dead() {
			return o
		}
	}
	return nil
}

// vanillaAttack is BDS's player attack (0xBCEC360): the abilities and pvp rule, the
// hit's parts from the AttackParts system, the target's hurt (with the HitEvent), then
// what the hit costs the attacker and its item, and its sound. Mace and cooldowns are
// not built yet.
func vanillaAttack(p, target *Player) bool {
	if !p.abilities.Flag(AbilityAttackPlayers) || !p.s.level.PVP {
		return false
	}
	g, r := p.s.game, p.entity.Region()
	if r == nil {
		return false
	}
	h := g.sys.AttackParts(p, target)
	sound := packet.SoundEventAttackNoDamage
	if h != nil && h.addBonuses(h.Base) > 0 {
		critical := h.Critical()
		if g.sys.TakeHit(h) {
			sound = packet.SoundEventAttackStrong
			if critical {
				sound = packet.SoundEventAttackCritical
				target.entity.Send(&packet.Animate{ActionType: packet.AnimateActionCriticalHit, EntityRuntimeID: target.runtimeID, Data: critEventData})
			}
			h.attackerAfter()
		}
	}
	// The attacker's client has played the sound already.
	playSound(r, p.blockPos(), soundEvent{name: sound, pos: p.entity.Position(), extra: -1, entity: "minecraft:player"}, p.viewer)
	p.flush()
	return true
}

// hitWear is what one hit wears item by.
func (g *gameplay) hitWear(item string) int32 {
	if g.combat.wearOne[item] {
		return hitWearWeapon
	}
	return hitWearDefault
}

// sharpnessDamage is BDS's Sharpness.
func sharpnessDamage(_ *Player, level int16, _ *Player) float32 {
	return float32(level) * sharpnessPerLevel
}

// strengthAttack is BDS's Strength: once per level.
func strengthAttack(amplifier int32, v float32) float32 {
	for range amplifier + 1 {
		v = v*strengthMultiplier + strengthAdd
	}
	return v
}

// weaknessAttack is BDS's Weakness: once per level, never below 0.
func weaknessAttack(amplifier int32, v float32) float32 {
	for range amplifier + 1 {
		v = max(v*weaknessMultiplier-weaknessTake, 0)
	}
	return v
}

// canCrit is BDS's rule for a critical hit: falling, off the ground, not climbing
// or in water, not blind. Riding does not exist yet.
func (p *Player) canCrit() bool {
	if _, blind := p.Effect(EffectBlindness); blind {
		return false
	}
	return p.move.fall > 0 && !p.entity.OnGround() && !p.move.climbing && !p.move.inWater
}

// knockbackOf is the extra knockback of the player's hit.
func (p *Player) knockbackOf() float32 {
	var level int16
	enchantLevels(p.inventory.Held(), func(id, l int16) {
		if e, ok := p.hitEnchant(id); ok && e.Knockback {
			level = l
		}
	})
	v := float32(level) * p.Tuning(TuneKnockbackPerLevel)
	if p.Moving(MoveSprinting) {
		v += p.Tuning(TuneKnockbackSprint)
	}
	return v
}

// vanillaKnockback is BDS's knockback: the first push away from the attacker, then
// the extra along the attacker's look. The client moves itself, so the result is
// sent to it. BDS also slows an attacker that pushed extra; a client moves itself, so
// that is not built.
func vanillaKnockback(target, attacker *Player, extra float32) {
	if target.entity == nil || attacker == nil || attacker.entity == nil {
		return
	}
	resist := max(1-target.attributes.get(attrKnockbackResistance), 0) * target.Tuning(TuneKnockbackTakenScale) * attacker.Tuning(TuneKnockbackDealtScale)
	away := target.entity.Position().Sub(attacker.entity.Position())
	vel := target.knockbackStep(target.move.velocity, away[0], away[2], target.Tuning(TuneKnockbackPush)*resist, target.Tuning(TuneKnockbackLift)*resist)
	if extra > 0 {
		yaw := direction.Units(attacker.entity.Rotation().Yaw)
		vel = target.knockbackStep(vel, -direction.Sin(yaw), direction.Cos(yaw), extra*target.Tuning(TuneKnockbackExtraPush)*resist, extra*target.Tuning(TuneKnockbackExtraLift)*resist)
	}
	target.move.velocity = vel
	target.send(&packet.SetActorMotion{EntityRuntimeID: target.runtimeID, Velocity: vel, Tick: target.entity.Tick()})
	target.flush()
}

// knockbackStep is one push of BDS's knockback on the player in the direction (x, z).
func (p *Player) knockbackStep(vel mgl32.Vec3, x, z, push, lift float32) mgl32.Vec3 {
	retain := p.Tuning(TuneKnockbackRetain)
	var dx, dz float32
	if l := float32(math.Sqrt(float64(x*x + z*z))); l >= knockbackMinLength {
		dx, dz = x/l, z/l
	}
	return mgl32.Vec3{
		clampKnockback(vel[0]*retain + dx*push),
		min(vel[1]*retain+lift*p.Tuning(TuneKnockbackLiftScale), p.Tuning(TuneKnockbackCap)),
		clampKnockback(vel[2]*retain + dz*push),
	}
}

func clampKnockback(v float32) float32 { return min(max(v, -knockbackLimit), knockbackLimit) }

// handleAnimate is vanilla's Animate handler: a player's arm swing goes to those who
// see it, always about the player itself.
func handleAnimate(p *Player, pk *packet.Animate) {
	if pk.ActionType != packet.AnimateActionSwingArm || p.entity == nil || p.dead() {
		return
	}
	p.startSwing()
	if r := p.entity.Region(); r != nil {
		r.Broadcast(p.blockPos(), &packet.Animate{ActionType: pk.ActionType, EntityRuntimeID: p.runtimeID, SwingSource: pk.SwingSource}, p.viewer)
	}
}
