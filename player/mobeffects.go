package player

import (
	"math/rand/v2"
	"slices"

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
// (onEffectUpdated sends it only when the actor's category has the player bit);
// those who see a mob learn its effects from the visible effects actor data (131), and
// Invisibility sets its invisible flag.

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
// spectator, or a living mob. BDS's code and the
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

// MeleeEffect implements mob.EffectHooks: a mob or a player whose shield does not stop the
// hit gets the effect.
func (o *Mob) MeleeEffect(_ *mob.Mob, target behavior.ID, e effect.Instance) {
	switch t := o.effectTarget(target).(type) {
	case *Player:
		if o.entity == nil || t.dead() {
			return
		}
		at := o.entity.Position()
		if t.blocksFrom(DamageEntityAttack, &at) {
			return
		}
		t.GiveEffect(e)
	case *Mob:
		if t != o {
			t.giveEffect(e)
		}
	}
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

// giveEffect gives the mob an effect, as BDS: an instant one works at once,
// others are merged into its list and shown.
func (o *Mob) giveEffect(e effect.Instance) {
	if o.m.Dead() {
		return
	}
	if !o.canBeAffected(e.ID) {
		return
	}
	def, ok := o.effectDef(e.ID)
	if ok && def.Instant {
		if def.MobApply != nil {
			def.MobApply(o, e.Amplifier, 1)
		}
		return
	}
	if ok && def.NoParticles {
		e.Hidden = true
	}
	now, _ := o.m.Effects().Give(e)
	o.effectAdded(now, def, ok)
}

// effectAdded runs the effect's MobAdded hook, as BDS's onEffectUpdated runs applyEffects on
// every add, merge and comeback, and shows the change.
func (o *Mob) effectAdded(e effect.Instance, def *Effect, known bool) {
	if known && def.MobAdded != nil {
		def.MobAdded(o, e.Amplifier)
	}
	o.effectsChanged()
}

// removeEffect takes effect id away from the mob.
func (o *Mob) removeEffect(id int32) {
	if !o.m.Effects().Remove(id) {
		return
	}
	if def, ok := o.effectDef(id); ok && def.MobRemoved != nil {
		def.MobRemoved(o)
	}
	o.effectsChanged()
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
			def, ok := o.effectDef(id)
			o.effectAdded((*l)[i], def, ok)
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
	o.m.SetHealthBoost(mobHealthBoost(o.s.game.effects, l))
	_, invisible := l.Get(EffectInvisibility)
	o.m.SetFlag(protocol.EntityDataFlagInvisible, invisible)
	o.FlagsChanged(o.m)
}

// mobHealthBoost is the additions the effects in l make to the health maximum (Health
// Boost), each by its amount times the level, as BDS's modifier value.
func mobHealthBoost(effects map[int32]*Effect, l effect.List) float32 {
	var v float32
	for _, e := range l {
		def, ok := effects[e.ID]
		if !ok {
			continue
		}
		for _, mod := range def.Modifiers {
			if mod.Attribute == attrHealthName && mod.OnMax && mod.Operation == ModifierAdd {
				v += mod.Amount * float32(e.Amplifier+1)
			}
		}
	}
	return v
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
	o.m.SetHealthBoost(mobHealthBoost(o.s.game.effects, l))
	if _, ok := l.Get(EffectInvisibility); ok {
		o.m.SetFlag(protocol.EntityDataFlagInvisible, true)
	}
}

// --- what effects do to mobs ---

// mobHeal heals the mob, or hurts an undead one with magic, as BDS's health delegate swaps
// the heal and harm buffs for undead.
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

// Actor types whose class overrides BDS's code, by runtime ID (the ActorType's low
// byte), from BDS 1.26.60's actor factory.
const (
	typeSpider     = 35
	typeCaveSpider = 40
	typeNpc        = 51
	typeWither     = 52
	typeDragon     = 53
	typeAgent      = 56
	// skeletonWitherVariant is the variant whose Skeleton is kept from Wither.
	skeletonWitherVariant = 1
)

// canBeAffected is BDS's code, which BDS's code asks
// first, with its class overrides: the wither takes only Instant Health and Instant Damage,
// the ender dragon, NPCs and agents nothing, spiders no Poison and
// a Skeleton of variant 1 no Wither. Otherwise the mob must be alive, of a type
// with the Mob bit, not undead by family for Regeneration and Poison, and not immune by
// minecraft:mob_effect_immunity.
func (o *Mob) canBeAffected(id int32) bool {
	t, known := o.s.reg.EntityTypes.ByName(o.m.Type())
	switch t.RuntimeID {
	case typeWither:
		return id == EffectInstantHealth || id == EffectInstantDamage
	case typeDragon, typeNpc, typeAgent:
		return false
	case typeSpider, typeCaveSpider:
		if id == EffectPoison {
			return false
		}
	case 34, 46, 48, 144, 151: // the Skeleton class: skeleton, stray, wither skeleton, bogged, parched
		if id == EffectWither && o.m.Variant() == skeletonWitherVariant {
			return false
		}
	}
	if o.m.Dead() || known && !mobActorType(t.RuntimeID) {
		return false
	}
	if (id == EffectRegeneration || id == EffectPoison) && slices.Contains(o.m.Families(), familyUndead) {
		return false
	}
	def, ok := o.effectDef(id)
	return !ok || !o.m.EffectImmune(def.Name)
}

// familyUndead is the family BDS's code keeps from Regeneration and Poison.
const familyUndead = "undead"

// mobActorType reports whether the actor type with the runtime ID has BDS 1.26.60's Mob bit
// (0x100): every type but these.
func mobActorType(id int32) bool {
	switch id {
	case 1, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89,
		90, 91, 93, 94, 95, 96, 97, 98, 99, 100, 101, 102, 103, 106, 107, 117, 119, 141, 143, 145, 154, 156, 218:
		return false
	}
	return true
}

// undead reports whether heal and harm swap on the mob: BDS asks its actor type for the Mob
// and UndeadMob bits (0x10100), not its families.
func (o *Mob) undead() bool {
	t, ok := o.s.reg.EntityTypes.ByName(o.m.Type())
	return ok && undeadType(t.RuntimeID)
}

// undeadType reports whether the actor type with the runtime ID (the ActorType's low byte)
// has the undead bits in BDS 1.26.60's ActorType enum.
func undeadType(id int32) bool {
	switch id {
	case 26, 27, // skeleton and zombie horse
		32, 34, 36, // zombie, skeleton, zombie pigman
		44, 46, 47, 48, // zombie villager, stray, husk, wither skeleton
		52, 58, // wither, phantom
		110, 116, 126, // drowned, zombie villager v2, zoglin
		144, 150, 151, 152, 155: // bogged, zombie nautilus, parched, camel husk, frostbite
		return true
	}
	return false
}

// mobAbsorptionMax is the absorption attribute's maximum BDS gives every mob.
const mobAbsorptionMax = 16

// mobAbsorptionAdded is Absorption on a mob:
// the absorption starts over at 0, then a living mob gets the effect's amount, up to the
// attribute's maximum.
func mobAbsorptionAdded(o *Mob, amplifier int32) {
	o.absorption = 0
	if !o.m.Dead() {
		o.absorption = min(float32(amplifier+1)*absorptionPerLevel, mobAbsorptionMax)
	}
}

// mobAbsorptionRemoved takes what is left of Absorption away.
func mobAbsorptionRemoved(o *Mob) { o.absorption = 0 }

// absorb takes damage of the cause off the mob's absorption first, as BDS's health delegate
// does after Resistance; self-destruct damage goes past it. It returns the
// damage left and adds what was taken to absorbed.
func (o *Mob) absorb(v float32, c DamageCause) float32 {
	if v <= 0 || o.absorption <= 0 || c == DamageSelfDestruct {
		return v
	}
	used := min(o.absorption, v)
	o.absorption -= used
	o.absorbed += used
	return v - used
}

// effectsHurt runs the Hurt hooks of the mob's effects after a hurt landed (MobEffect slot 6).
func (o *Mob) effectsHurt(c DamageCause) {
	for _, e := range *o.m.Effects() {
		if def, ok := o.effectDef(e.ID); ok && def.MobHurt != nil {
			def.MobHurt(o, e.Amplifier, c)
		}
	}
}

// effectsDied runs the Died hooks of the effects a dying mob has (MobEffect slot 5).
func (o *Mob) effectsDied() {
	for _, e := range *o.m.Effects() {
		if def, ok := o.effectDef(e.ID); ok && def.MobDied != nil {
			def.MobDied(o, e.Amplifier)
		}
	}
}

// mobRand is the random a mob's death and hurt effects roll with.
type mobRand struct{}

func (mobRand) Float32() float32 { return rand.Float32() } //nolint:gosec // gameplay randomness
func (mobRand) IntN(n int) int   { return rand.IntN(n) }   //nolint:gosec // gameplay randomness

// siteOf is the mob's effectSite: its feet, its height and the way its body faces.
func (o *Mob) siteOf() (effectSite, bool) {
	if o.entity == nil || o.entity.Region() == nil {
		return effectSite{}, false
	}
	_, h := o.m.Size()
	pitch, yaw, _, _ := o.m.Rotation()
	return effectSite{s: o.s, r: o.entity.Region(), feet: o.entity.Position(), height: h, view: viewOf(pitch, yaw), rng: mobRand{}, source: o.entity}, true
}

// mobDied runs a death effect's work at a dying mob.
func mobDied(work func(effectSite)) func(o *Mob, amplifier int32) {
	return func(o *Mob, _ int32) {
		if at, ok := o.siteOf(); ok {
			work(at)
		}
	}
}

// mobInfestedHurt is Infested on a hurt mob.
func mobInfestedHurt(o *Mob, _ int32, _ DamageCause) {
	if at, ok := o.siteOf(); ok {
		at.infest()
	}
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

// effectResist is the mob's effects' Resist hooks (Resistance) on damage of the cause, after
// armour and protection, as a player's hit takes them.
func (o *Mob) effectResist(v float32, c DamageCause) float32 {
	if !resisted(c) {
		return v
	}
	for _, e := range *o.m.Effects() {
		if def, ok := o.effectDef(e.ID); ok && def.Resist != nil {
			v = def.Resist(e.Amplifier, v)
		}
	}
	return v
}

// meleeDamage is the mob's attack damage with its effects' Attack hooks (Strength,
// Weakness), as they change a player's.
func (o *Mob) meleeDamage() float32 {
	v := o.m.MeleeDamage()
	for _, e := range *o.m.Effects() {
		if def, ok := o.effectDef(e.ID); ok && def.Attack != nil {
			v = def.Attack(e.Amplifier, v)
		}
	}
	return v
}

// --- mob.PotionHooks ---

// UseDuration implements mob.PotionHooks: a potion takes potionUseTicks to drink and a
// crossbow loads in 25 ticks less 5 per Quick Charge level.
// Other items' use durations are not known to mobs yet.
func (o *Mob) UseDuration(_ *mob.Mob, it behavior.Item) int32 {
	switch it.Name {
	case namePotion:
		return potionUseTicks
	case nameCrossbow:
		st, _ := it.Data.(ItemStack)
		return crossbowLoadTime(st)
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
	if def, ok := o.effectDef(id); ok && def.Instant && def.MobApply != nil && o.canBeAffected(id) {
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
