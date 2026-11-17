package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/effect"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
)

// Mob effects. The list is the mob's (mob.Mob.Effects); this file gives, ticks and shows
// them with the same registered effects players have, through each effect's mob hooks
// (MobTick, MobApply). BDS 1.26.60 sends MobEffect packets only for players
// (onEffectUpdated 0xB3CC950 sends it only when the actor's category has the player bit);
// those who see a mob learn its effects from the visible effects actor data (131), and
// Invisibility sets its invisible flag (0xCABBC70).

// familyUndead is the family whose heal and harm swap. BDS asks the actor type's undead
// bit (0xB3CBBA0); Helix's mobs know only their families, which vanilla gives every undead
// type.
const familyUndead = "undead"

// effectDef is the registered effect with the ID.
func (o *Mob) effectDef(id int32) (*Effect, bool) {
	def, ok := o.s.game.effects[id]
	return def, ok
}

// EffectID implements mob.EffectHooks.
func (o *Mob) EffectID(name string) (int32, bool) {
	e, ok := o.s.EffectNamed(name)
	return e.ID, ok
}

// GiveEffect implements mob.EffectHooks: a player target through Player.GiveEffect, a mob
// through giveEffect.
func (o *Mob) GiveEffect(_ *mob.Mob, target behavior.ID, e effect.Instance) {
	switch t := o.effectTarget(target).(type) {
	case *Player:
		t.GiveEffect(e)
	case *Mob:
		t.giveEffect(e)
	}
}

// RemoveEffect implements mob.EffectHooks.
func (o *Mob) RemoveEffect(_ *mob.Mob, target behavior.ID, id int32) {
	switch t := o.effectTarget(target).(type) {
	case *Player:
		t.RemoveEffect(id)
	case *Mob:
		t.removeEffect(id)
	}
}

// ClearEffects implements mob.EffectHooks.
func (o *Mob) ClearEffects(*mob.Mob) {
	for len(*o.m.Effects()) > 0 {
		o.removeEffect((*o.m.Effects())[0].ID)
	}
}

// EffectTicks implements mob.EffectHooks.
func (o *Mob) EffectTicks(_ *mob.Mob, target behavior.ID, id int32) int32 {
	var l effect.List
	switch t := o.effectTarget(target).(type) {
	case *Player:
		l = t.effects
	case *Mob:
		l = *t.m.Effects()
	}
	if e, ok := l.Get(id); ok {
		return e.Duration
	}
	return 0
}

// CanReceiveEffect implements mob.EffectHooks: a living player not in creative or
// spectator, or a living mob. Actor::canReceiveMobEffectsFromGameplay and the
// invulnerability check are not read from BDS yet.
func (o *Mob) CanReceiveEffect(_ *mob.Mob, target, _ behavior.ID) bool {
	switch t := o.effectTarget(target).(type) {
	case *Player:
		return !t.dead() && !t.creative() && t.gameMode != GameModeSpectator
	case *Mob:
		return !t.m.Dead()
	}
	return false
}

// effectTarget is the player or mob with the unique ID in the mob's region.
func (o *Mob) effectTarget(id behavior.ID) any {
	if o.entity == nil {
		return nil
	}
	if id == behavior.ID(o.entity.UniqueID()) {
		return o
	}
	r := o.entity.Region()
	if r == nil {
		return nil
	}
	return o.s.entityByID(r, id)
}

// giveEffect gives the mob an effect, as Actor::addEffect: an instant one works at once,
// others are merged into its list and shown.
func (o *Mob) giveEffect(e effect.Instance) {
	if o.m.Dead() {
		return
	}
	def, ok := o.effectDef(e.ID)
	if ok && o.m.EffectImmune(def.Name) {
		return
	}
	if ok && def.Instant {
		if def.MobApply != nil {
			def.MobApply(o, e.Amplifier, 1)
		}
		return
	}
	if ok && def.NoParticles {
		e.Hidden = true
	}
	o.m.Effects().Give(e)
	o.effectsChanged()
}

// removeEffect takes effect id away from the mob.
func (o *Mob) removeEffect(id int32) {
	if o.m.Effects().Remove(id) {
		o.effectsChanged()
	}
}

// tickEffects runs each of the mob's effects' MobTick and counts it down, as the player's
// tickEffects. Runs: the mob's apply.
func (o *Mob) tickEffects() {
	l := o.m.Effects()
	for i := 0; i < len(*l); {
		e := &(*l)[i]
		id := e.ID
		left := e.Left()
		if def, ok := o.effectDef(id); ok && def.MobTick != nil {
			def.MobTick(o, e.Amplifier, left)
		}
		// A tick may have killed the mob and cleared its effects.
		if i >= len(*l) || (*l)[i].ID != id {
			continue
		}
		switch (*l)[i].Countdown() {
		case effect.Gone:
			o.removeEffect(id)
			continue
		case effect.Restored:
			o.effectsChanged()
		}
		i++
	}
}

// effectsChanged puts the effects' movement modifiers and Invisibility on the mob and
// shows its particles to those who see it.
func (o *Mob) effectsChanged() {
	l := *o.m.Effects()
	for _, def := range o.s.game.effects {
		amount := mobSpeedModifier(def, l)
		if amount != 0 || hasSpeedModifier(def) {
			o.m.SetEffectSpeed(def.Name, amount)
		}
	}
	_, invisible := l.Get(EffectInvisibility)
	o.m.SetFlag(protocol.EntityDataFlagInvisible, invisible)
	o.FlagsChanged(o.m)
}

// hasSpeedModifier reports whether the effect changes movement.
func hasSpeedModifier(def *Effect) bool {
	for _, mod := range def.Modifiers {
		if mod.Attribute == attrMovementName && !mod.OnMax {
			return true
		}
	}
	return false
}

// mobSpeedModifier is the effect's multiply total movement modifier while the mob has
// it, 0 when it does not. Other operations and attributes are not applied to mobs yet.
func mobSpeedModifier(def *Effect, l effect.List) float32 {
	amp, ok := l.Amplifier(def.ID)
	if !ok {
		return 0
	}
	var v float32
	for _, mod := range def.Modifiers {
		if mod.Attribute == attrMovementName && !mod.OnMax && mod.Operation == ModifierMultiplyTotal {
			v += mod.Amount * float32(amp+1)
		}
	}
	return v
}

// visibleEffects is the mob's visible effects actor data.
func (o *Mob) visibleEffects() int64 { return o.m.Effects().Visible() }

// saveEffects and loadEffects keep the mob's effects as BDS saves an actor's.
func (o *Mob) saveEffects(data map[string]any) { o.m.Effects().Save(data) }
func (o *Mob) loadEffects(data map[string]any) {
	*o.m.Effects() = effect.Load(data)
	l := *o.m.Effects()
	for _, def := range o.s.game.effects {
		if amount := mobSpeedModifier(def, l); amount != 0 {
			o.m.SetEffectSpeed(def.Name, amount)
		}
	}
	if _, ok := l.Get(EffectInvisibility); ok {
		o.m.SetFlag(protocol.EntityDataFlagInvisible, true)
	}
}

// --- what effects do to mobs ---

// mobHeal heals the mob, or hurts an undead one with magic, as BDS's health delegate swaps
// the heal and harm buffs for undead (0xBC44C80).
func mobHeal(o *Mob, amount float32) {
	if o.m.Dead() {
		return
	}
	if o.undead() {
		o.hurtBy(amount, behavior.NoID, DamageMagic)
		return
	}
	o.m.SetHealth(o.m.HealthValue() + amount)
}

// mobHarm is the opposite of mobHeal.
func mobHarm(o *Mob, amount float32) {
	if o.m.Dead() {
		return
	}
	if o.undead() {
		o.m.SetHealth(o.m.HealthValue() + amount)
		return
	}
	o.hurtBy(amount, behavior.NoID, DamageMagic)
}

// undead reports whether heal and harm swap on the mob.
func (o *Mob) undead() bool {
	for _, f := range o.m.Families() {
		if f == familyUndead {
			return true
		}
	}
	return false
}

// mobRegenerationTick is Regeneration on a mob.
func mobRegenerationTick(o *Mob, amplifier, left int32) {
	if spaced(regenerationBase, amplifier, left) && !o.m.Dead() {
		o.m.SetHealth(o.m.HealthValue() + effectHealthStep)
	}
}

// mobPoisonTick is Poison and Fatal Poison on a mob: magic damage at each spaced tick; a
// Poison hit only lands if it leaves at least poisonFloor health.
func mobPoisonTick(fatal bool) func(o *Mob, amplifier, left int32) {
	return func(o *Mob, amplifier, left int32) {
		if !spaced(poisonBase, amplifier, left) || o.m.Dead() || !fatal && o.m.HealthValue()-effectHealthStep < poisonFloor {
			return
		}
		o.hurtBy(effectHealthStep, behavior.NoID, DamageMagic)
	}
}

// mobWitherTick is Wither on a mob.
func mobWitherTick(o *Mob, amplifier, left int32) {
	if spaced(witherBase, amplifier, left) && !o.m.Dead() {
		o.hurtBy(effectHealthStep, behavior.NoID, DamageWither)
	}
}

// mobInstantHealth and mobInstantDamage are the instant effects on a mob.
func mobInstantHealth(o *Mob, amplifier int32, scale float32) {
	mobHeal(o, instantAmount(instantHealthBase, amplifier, scale))
}

func mobInstantDamage(o *Mob, amplifier int32, scale float32) {
	mobHarm(o, instantAmount(instantDamageBase, amplifier, scale))
}

// --- mob.PotionHooks ---

// UseDuration implements mob.PotionHooks: a potion takes potionUseTicks to drink. Other
// items' use durations are not known to mobs yet.
func (o *Mob) UseDuration(_ *mob.Mob, it behavior.Item) int32 {
	if it.Name == namePotion {
		return potionUseTicks
	}
	return 0
}

// DrinkPotion implements mob.PotionHooks: every effect of the potion at its full duration,
// as a player's drink.
func (o *Mob) DrinkPotion(_ *mob.Mob, aux int16) {
	pot, ok := o.s.Potion(int32(aux))
	if !ok {
		return
	}
	for _, e := range pot.Effects {
		if e.ID != 0 {
			o.giveEffect(effect.Instance{ID: e.ID, Amplifier: e.Amplifier, Duration: e.Duration})
		}
	}
}

var (
	_ mob.EffectHooks = (*Mob)(nil)
	_ mob.PotionHooks = (*Mob)(nil)
)

// effectTaker is a player or mob a splash potion or a lingering cloud gives effects to.
type effectTaker interface {
	// canTakeEffects is false for the dead, spectators and armour stands.
	canTakeEffects() bool
	takeEffect(e ActiveEffect)
	takeInstant(id, amplifier int32, scale float32)
	BlastBox() (lo, hi mgl32.Vec3, ok bool)
}

func (p *Player) canTakeEffects() bool      { return !p.dead() && p.gameMode != GameModeSpectator }
func (p *Player) takeEffect(e ActiveEffect) { p.GiveEffect(e) }
func (p *Player) takeInstant(id, amplifier int32, scale float32) {
	p.ApplyInstantEffect(id, amplifier, scale)
}

func (o *Mob) canTakeEffects() bool      { return !o.m.Dead() && o.stand == nil && !o.gone }
func (o *Mob) takeEffect(e ActiveEffect) { o.giveEffect(e) }
func (o *Mob) takeInstant(id, amplifier int32, scale float32) {
	if def, ok := o.effectDef(id); ok && def.Instant && def.MobApply != nil && !o.m.EffectImmune(def.Name) {
		def.MobApply(o, amplifier, scale)
	}
}

// effectTakerOf is the player or mob that owns the entity, false for others.
func effectTakerOf(e *world.Entity) (effectTaker, bool) {
	t, ok := e.Owner().(effectTaker)
	if !ok || !t.canTakeEffects() {
		return nil, false
	}
	return t, true
}
