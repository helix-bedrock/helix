package player

import (
	"math"
	"slices"

	"github.com/helix-bedrock/protocol/packet"
)

// A Hit is one hurt of a player as the engine works it out, from any source: a melee
// hit, an arrow, a fall, an explosion, fire, the void. It is made of parts so plugins
// can turn off or scale one piece (armour, one enchantment, Resistance, Thorns...)
// without redoing the rest. The engine builds the parts vanilla would apply, fires
// one HitEvent, then resolves the parts still on in BDS 1.26.60's order and caps
// (BDS 0xBCEC360 attack, 0xBFDB910 attack damage, 0xBC46230 the health attribute's
// new value: armour 0xBFE8020, enchantments 0xC4A2AA0, Resistance, hurt time, then
// absorption). The maths happens last, so rules that sum and cap (protection) or act
// on the final hit (Thorns) stay right however the parts were changed.
type Hit struct {
	// Target is the player hurt.
	Target *Player
	// Source is where the hurt comes from; Source.Attacker is the player behind it.
	Source DamageSource
	// Base is the damage before every part: for a melee hit the attack damage attribute
	// plus the weapon's damage, else the damage the source deals.
	Base float32
	// Bonuses add to Base: melee enchantments (summed and floored first, as BDS),
	// Strength and Weakness, a critical hit, the attacker's damage scale.
	Bonuses []Part
	// Reductions take off what the bonuses left, in order: the target's damage scale,
	// a helmet against falling blocks, armour (with toughness and the attacker's
	// Breach), protection enchantments (summed and capped together), effects such as
	// Resistance, then the hurt time and absorption.
	Reductions []Part
	// After are what follows a hit that hurt: knockback, Thorns and other worn
	// enchantments, armour and weapon wear, exhaustion.
	After []Part
}

// PartKind is what a Part of a Hit does.
type PartKind uint8

// The kinds of part vanilla builds. Scale is 1 for vanilla, 0 does nothing, 2 does
// twice as much.
const (
	// PartMelee is a melee enchantment on the attacker's held item (ID, Level).
	// Value is the damage its Melee hook gave; scaled, then all are summed and floored.
	PartMelee PartKind = iota + 1
	// PartAttackEffect is an attacker's effect with an Attack hook, such as Strength or
	// Weakness (ID, Level = amplifier). Scale scales the change it makes.
	PartAttackEffect
	// PartCritical is a critical hit; Value is the multiplier. Scale scales what it adds.
	PartCritical
	// PartDealtScale is the attacker's TuneDamageDealtScale (Value).
	PartDealtScale
	// PartTakenScale is the target's TuneDamageTakenScale (Value).
	PartTakenScale
	// PartHelmet is a worn helmet against falling blocks and anvils; Value is what is left.
	PartHelmet
	// PartArmor is the target's armour points (Value) with TuneArmor. Turning it off turns
	// off the whole armour share, toughness and Breach with it.
	PartArmor
	// PartToughness is the target's armour toughness (Value) with TuneToughness.
	PartToughness
	// PartBreach is a Breach-like enchantment on the attacker's held item (ID, Level);
	// Scale scales how much it changes armour's share.
	PartBreach
	// PartProtection is a protection enchantment on a worn piece (ID, Level, Slot); Value
	// is the points it counts for. All are summed, then capped, as BDS does.
	PartProtection
	// PartResistEffect is a target's effect with a Resist hook, such as Resistance (ID,
	// Level = amplifier). Scale scales the share it takes off.
	PartResistEffect
	// PartHurtTime lets only damage above the last hurt through while the hurt time
	// runs. Off lets every hit through in full; Scale is not read.
	PartHurtTime
	// PartAbsorption takes damage off absorption before health. Off goes straight to
	// health; Scale is not read.
	PartAbsorption
	// PartKnockback pushes the target away from the attacker; Value is the extra
	// knockback (Knockback enchantment, sprinting), which Scale scales. Off is no push.
	PartKnockback
	// PartAfterHurt is a worn enchantment's AfterHurt, such as Thorns (ID, Level, Slot).
	// Only Off is read.
	PartAfterHurt
	// PartArmorWear wears each armour piece by a quarter of the damage, at least one.
	// Scale scales the wear.
	PartArmorWear
	// PartWeaponWear wears the attacker's held item by Value; Scale scales it.
	PartWeaponWear
	// PartTargetExhaust and PartAttackerExhaust are the exhaustion the hurt and the hit
	// cost (Value); Scale scales them.
	PartTargetExhaust
	PartAttackerExhaust
	// PartCustom is a plugin's own part: Value is added as a bonus or taken off as a
	// reduction, at its place in the list, times Scale. It does nothing in After.
	PartCustom
)

// Part is one piece of a Hit.
type Part struct {
	Kind PartKind
	// ID is the enchantment or effect ID, for the kinds that have one.
	ID int32
	// Level is the enchantment's level or the effect's amplifier.
	Level int32
	// Slot is the armour slot (0 helmet to 3 boots) of a worn enchantment, else -1.
	Slot int8
	// Value is the number vanilla worked out for the part, as its kind says.
	Value float32
	// Scale scales what the part does: 1 is vanilla.
	Scale float32
	// Off turns the part off.
	Off bool
}

// part is a vanilla part of kind with its value, at full scale.
func part(kind PartKind, value float32) Part {
	return Part{Kind: kind, Value: value, Scale: 1, Slot: -1}
}

// on returns how much the part counts: 0 when off.
func (p *Part) on() float32 {
	if p.Off {
		return 0
	}
	return p.Scale
}

// Part returns the first part of kind with id in any list, or nil. id is ignored for
// kinds without one.
func (h *Hit) Part(kind PartKind, id int32) *Part {
	for _, list := range [...][]Part{h.Bonuses, h.Reductions, h.After} {
		for i := range list {
			if list[i].Kind == kind && list[i].ID == id {
				return &list[i]
			}
		}
	}
	return nil
}

// Off turns off every part of kind with id; it reports whether any was found.
func (h *Hit) Off(kind PartKind, id int32) bool {
	return h.each(kind, id, func(p *Part) { p.Off = true })
}

// Scale scales every part of kind with id; it reports whether any was found.
func (h *Hit) Scale(kind PartKind, id int32, scale float32) bool {
	return h.each(kind, id, func(p *Part) { p.Scale = scale })
}

func (h *Hit) each(kind PartKind, id int32, fn func(p *Part)) bool {
	found := false
	for _, list := range [...][]Part{h.Bonuses, h.Reductions, h.After} {
		for i := range list {
			if list[i].Kind == kind && list[i].ID == id {
				fn(&list[i])
				found = true
			}
		}
	}
	return found
}

// Critical reports whether the hit is a critical hit that is still on.
func (h *Hit) Critical() bool {
	p := h.Part(PartCritical, 0)
	return p != nil && !p.Off && p.Scale > 0
}

// Damage is what the hit deals with its parts as they are now, before the hurt time
// and absorption.
func (h *Hit) Damage() float32 {
	v := h.Base
	v = h.addBonuses(v)
	return h.reduce(v)
}

// addBonuses is BDS's attack damage after the base: enchantments summed and floored,
// then the other bonuses in order.
func (h *Hit) addBonuses(v float32) float32 {
	var melee float32
	for i := range h.Bonuses {
		if b := &h.Bonuses[i]; b.Kind == PartMelee {
			melee += b.Value * b.on()
		}
	}
	v += float32(math.Floor(float64(melee)))
	for i := range h.Bonuses {
		b := &h.Bonuses[i]
		s := b.on()
		if s == 0 {
			continue
		}
		switch b.Kind {
		case PartAttackEffect:
			if a := h.Source.Attacker; a != nil {
				if e, ok := a.s.game.effects[b.ID]; ok && e.Attack != nil {
					v += (e.Attack(b.Level, v) - v) * s
				}
			}
		case PartCritical, PartDealtScale:
			v *= 1 + (b.Value-1)*s
		case PartCustom:
			v += b.Value * s
		}
	}
	return v
}

// reduce takes the reductions off v in order, up to the hurt time.
func (h *Hit) reduce(v float32) float32 {
	protected := false
	for i := range h.Reductions {
		r := &h.Reductions[i]
		s := r.on()
		if s == 0 {
			continue
		}
		switch r.Kind {
		case PartTakenScale, PartHelmet:
			v *= 1 + (r.Value-1)*s
		case PartArmor:
			v = h.afterArmor(v, r)
		case PartProtection:
			if !protected {
				protected = true
				v = h.afterProtection(v)
			}
		case PartResistEffect:
			if e, ok := h.Target.s.game.effects[r.ID]; ok && e.Resist != nil {
				v += (e.Resist(r.Level, v) - v) * s
			}
		case PartCustom:
			v = max(v-r.Value*s, 0)
		}
	}
	return v
}

// afterArmor is BDS's armour share (0xBFE8020): v = dmg/(tough*0.25+2) + armour, the
// share max(v, armour*0.2)/25 up to 20 points, else the tuned maximum; the attacker's
// Breach then changes the share.
func (h *Hit) afterArmor(v float32, armor *Part) float32 {
	t := h.Target
	a := max(armor.Value*armor.Scale, 0)
	var tough float32
	for i := range h.Reductions {
		if p := &h.Reductions[i]; p.Kind == PartToughness {
			tough += p.Value * p.on()
		}
	}
	tough = max(tough, 0)
	x := v/(tough*toughnessShare+toughnessBase) + a
	share := t.Tuning(TuneArmorMaxShare)
	if x <= armorFullPoints {
		share = max(x, a*armorMinShare) / armorPerPoint
	}
	if at := h.Source.Attacker; at != nil {
		for i := range h.Reductions {
			p := &h.Reductions[i]
			s := p.on()
			if p.Kind != PartBreach || s == 0 {
				continue
			}
			if e, ok := at.hitEnchant(int16(p.ID)); ok && e.Breach != nil { //nolint:gosec // enchantment IDs fit int16
				share += (e.Breach(int16(p.Level), share) - share) * s //nolint:gosec // levels fit int16
			}
		}
	}
	return v * (1 - share)
}

// afterProtection is BDS's armour enchantments (0xC4A2AA0): the points summed, the
// count stopping at 26, then capped and each worth a share.
func (h *Hit) afterProtection(v float32) float32 {
	t := h.Target
	var points float32
	for i := range h.Reductions {
		p := &h.Reductions[i]
		if p.Kind == PartProtection && points < maxProtectionCount {
			points += p.Value * p.on()
		}
	}
	counted := max(min(points+t.Tuning(TuneProtection), t.Tuning(TuneProtectionLimit)), 0)
	return v * (counted*-t.Tuning(TuneProtectionPoint) + 1)
}

// HitEvent fires once for every hit on a player, after the engine has built its
// parts and before they are resolved. Turn parts off, scale them, add PartCustom
// parts or change Base; Cancel stops the hurt. It does not fire for a hit a shield
// stops.
type HitEvent struct {
	Cancellable
	Hit *Hit
}

// OnHit listens to every hit on a player.
func OnHit(s *Setup, fn func(e *HitEvent), p Priority) error {
	return addListener(s, &s.h.game.hitEv, fn, p)
}

// TakeHitFunc hurts the player with h; false if it was not hurt.
type TakeHitFunc func(h *Hit) bool

// HitPartsFunc adds to h the parts the target and the cause bring: reductions and
// what follows the hurt. The attacker's parts are already in h.
type HitPartsFunc func(h *Hit)

// AttackPartsFunc builds a melee hit of p on target: its base and bonuses and what
// follows a hit from the attacker's side.
type AttackPartsFunc func(p, target *Player) *Hit

// NewHit is a hit of damage on p from src with no parts yet.
func NewHit(p *Player, damage float32, src DamageSource) *Hit {
	return &Hit{Target: p, Source: src, Base: damage}
}

// vanillaHitParts is BDS's reductions for the target and cause, in BDS's order, and
// what follows the hurt.
func vanillaHitParts(h *Hit) {
	p, cause := h.Target, h.Source.Cause
	g := p.s.game
	h.Reductions = append(h.Reductions, part(PartTakenScale, p.Tuning(TuneDamageTakenScale)))
	if !armorBypassed(cause) {
		inv := &p.inventory
		if !inv.armor[0].Empty() && cause >= 0 && cause < causeFallingLimit && causesFallingHit>>cause&1 != 0 {
			h.Reductions = append(h.Reductions, part(PartHelmet, helmetFalling))
		}
		var armor, tough float32
		for _, s := range inv.armor {
			if s.Empty() {
				continue
			}
			if v, ok := g.combat.armor[s.Name]; ok {
				armor += float32(v.Protection)
				tough += float32(v.Toughness)
			}
		}
		h.Reductions = append(h.Reductions,
			part(PartArmor, armor+p.Tuning(TuneArmor)),
			part(PartToughness, tough+p.Tuning(TuneToughness)))
		if a := h.Source.Attacker; a != nil {
			enchantLevels(a.inventory.Held(), func(id, level int16) {
				if e, ok := a.hitEnchant(id); ok && e.Breach != nil {
					h.Reductions = append(h.Reductions, Part{Kind: PartBreach, ID: int32(id), Level: int32(level), Slot: -1, Scale: 1})
				}
			})
		}
	}
	if cause != causeEnchantsSkip {
		for slot, s := range p.inventory.armor {
			if s.Empty() {
				continue
			}
			enchantLevels(s, func(id, level int16) {
				if e, ok := p.hitEnchant(id); ok && e.Protection != nil {
					if pts := e.Protection(level, cause); pts != 0 {
						h.Reductions = append(h.Reductions, Part{Kind: PartProtection, ID: int32(id), Level: int32(level), Slot: int8(slot), Value: float32(pts), Scale: 1}) //nolint:gosec // four armour slots
					}
				}
			})
		}
	}
	if resisted(cause) {
		for _, a := range p.effects {
			if e, ok := g.effects[a.ID]; ok && e.Resist != nil {
				h.Reductions = append(h.Reductions, Part{Kind: PartResistEffect, ID: a.ID, Level: a.Amplifier, Slot: -1, Scale: 1})
			}
		}
	}
	if cause != DamageNone && cause != DamageSelfDestruct {
		h.Reductions = append(h.Reductions, part(PartHurtTime, 0))
	}
	if cause != DamageSelfDestruct {
		h.Reductions = append(h.Reductions, part(PartAbsorption, 0))
	}
	if h.Source.Knockback && h.Source.Attacker != nil && h.Part(PartKnockback, 0) == nil {
		h.After = append(h.After, part(PartKnockback, h.Source.Extra))
	}
	if a := h.Source.Attacker; a != nil && a != p && (cause == DamageEntityAttack || cause == causeProjectile) {
		for slot, s := range p.inventory.armor {
			if s.Empty() {
				continue
			}
			enchantLevels(s, func(id, level int16) {
				if e, ok := p.hitEnchant(id); ok && e.AfterHurt != nil {
					h.After = append(h.After, Part{Kind: PartAfterHurt, ID: int32(id), Level: int32(level), Slot: int8(slot), Scale: 1}) //nolint:gosec // four armour slots
				}
			})
		}
	}
	if !armorBypassed(cause) {
		h.After = append(h.After, part(PartArmorWear, 0))
	}
	h.After = append(h.After, part(PartTargetExhaust, p.Tuning(TuneExhaustDamage)))
}

// vanillaAttackParts is BDS's melee damage (0xBFDB910): the attack damage attribute
// and the weapon, each melee enchantment, the attack effects in order, a critical hit,
// the attacker's scale; then the knockback, weapon wear and attack exhaustion.
func vanillaAttackParts(p, target *Player) *Hit {
	g, held := p.s.game, p.inventory.Held()
	h := NewHit(target, p.attributes.get(attrAttackDamage)+p.Tuning(TuneAttackDamage), DamageSource{Cause: DamageEntityAttack, Attacker: p, Knockback: true})
	if !held.Empty() {
		h.Base += float32(g.combat.damage[held.Name])
	}
	if held.Name == nameMace {
		// BDS adds the item's attack bonus to the base damage.
		h.Base += MaceBonus(p.FallDistance())
	}
	enchantLevels(held, func(id, level int16) {
		if e, ok := p.hitEnchant(id); ok && e.Melee != nil {
			h.Bonuses = append(h.Bonuses, Part{Kind: PartMelee, ID: int32(id), Level: int32(level), Slot: -1, Value: e.Melee(p, level, target), Scale: 1})
		}
	})
	for _, id := range [...]int32{EffectStrength, EffectWeakness} {
		if amp, ok := p.Effect(id); ok {
			if e, ok := g.effects[id]; ok && e.Attack != nil {
				h.Bonuses = append(h.Bonuses, Part{Kind: PartAttackEffect, ID: id, Level: amp, Slot: -1, Scale: 1})
			}
		}
	}
	if p.canCrit() {
		h.Bonuses = append(h.Bonuses, part(PartCritical, p.Tuning(TuneCritMultiplier)))
	}
	h.Bonuses = append(h.Bonuses, part(PartDealtScale, p.Tuning(TuneDamageDealtScale)))
	h.After = append(h.After,
		part(PartKnockback, p.knockbackOf()),
		part(PartWeaponWear, float32(g.hitWear(held.Name))),
		part(PartAttackerExhaust, p.Tuning(TuneExhaustAttack)))
	return h
}

// vanillaTakeHit is BDS's hurt through the health attribute: a shield stops it first;
// then the parts the target brings, the HitEvent, the resolved damage, the hurt time
// letting only what is above the last hurt through, absorption first and health for
// the rest, then the hurt effects (hurt time, knockback, the hurt event), Thorns and
// other worn enchantments, armour wear and the damage exhaustion. At no health left
// the Die system runs, unless a totem saves the player. Damage sensors are not built
// yet.
func vanillaTakeHit(h *Hit) bool {
	p, src := h.Target, h.Source
	if p == nil || p.dead() || p.invulnerable() && src.Cause != DamageVoid {
		return false
	}
	g := p.s.game
	if g.sys.ShieldBlock(p, h.Damage(), src) {
		p.flush()
		return false
	}
	g.sys.HitParts(h)
	if g.hitEv.Has() {
		e := HitEvent{Hit: h}
		g.hitEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
	}
	raw := h.addBonuses(h.Base)
	damage := h.reduce(raw)
	if damage <= 0 {
		return false
	}
	if t := h.Part(PartHurtTime, 0); t != nil && !t.Off && p.hurt.ticks > 0 {
		if damage <= p.hurt.last {
			return false
		}
		damage -= p.hurt.last
	}
	abs := h.Part(PartAbsorption, 0)
	p.takeHealth(damage, src, abs != nil && !abs.Off)
	if p.hurt.ticks > 0 {
		p.hurt.last += damage
	} else {
		p.hurt.last = damage
	}
	p.hurt.ticks = int32(p.Tuning(TuneHurtTicks))
	if k := h.Part(PartKnockback, 0); k != nil && !k.Off && src.Attacker != nil {
		g.sys.Knockback(p, src.Attacker, k.Value*k.Scale)
	}
	if r := p.entity.Region(); r != nil {
		r.Broadcast(p.blockPos(), actorEvent(p.runtimeID, packet.ActorEventHurt), nil)
	}
	h.afterHurt(raw)
	p.effectsHurt(src)
	if p.dead() {
		p.effectsDied()
		g.sys.Die(p, src)
	}
	p.flush()
	return true
}

// afterHurt runs the target's after parts of a hurt: worn enchantments, armour wear
// from the damage before reductions, and the exhaustion.
func (h *Hit) afterHurt(raw float32) {
	p, g := h.Target, h.Target.s.game
	for i := range h.After {
		a := &h.After[i]
		s := a.on()
		if s == 0 {
			continue
		}
		switch a.Kind {
		case PartAfterHurt:
			if e, ok := p.hitEnchant(int16(a.ID)); ok && e.AfterHurt != nil && h.Source.Attacker != nil { //nolint:gosec // enchantment IDs fit int16
				e.AfterHurt(p, h.Source.Attacker, int16(a.Level)) //nolint:gosec // levels fit int16
			}
		case PartArmorWear:
			p.wearArmor(raw * s)
		case PartTargetExhaust:
			g.sys.Exhaust(p, a.Value*s)
		}
	}
}

// attackerAfter runs the attacker's after parts of a hit that hurt: weapon wear and
// exhaustion.
func (h *Hit) attackerAfter() {
	a := h.Source.Attacker
	if a == nil {
		return
	}
	for i := range h.After {
		x := &h.After[i]
		s := x.on()
		if s == 0 {
			continue
		}
		switch x.Kind {
		case PartWeaponWear:
			a.hurtHeld(int32(x.Value * s))
		case PartAttackerExhaust:
			a.s.game.sys.Exhaust(a, x.Value*s)
		}
	}
}

// playerEnchants is a player's own copies of enchantments, read before the server's.
type playerEnchants map[int16]*Enchantment

// hitEnchant returns the enchantment with id as this player has it: its own copy if it
// has one, else the server's. Runs: the player's region.
func (p *Player) hitEnchant(id int16) (*Enchantment, bool) {
	if e, ok := p.ownEnchants[id]; ok {
		return e, true
	}
	return p.s.game.enchant(id)
}

// EditEnchantment gives the player its own copy of enchantment id, changed by fn, read
// instead of the server's wherever a hit uses the player's enchantments (its weapon's
// and its armour's). The copy starts from the player's current one and does not follow
// later server changes. It lasts until cleared or the player leaves; false if there is
// no enchantment id. Runs: the player's region.
func (p *Player) EditEnchantment(id int16, fn func(e *Enchantment)) bool {
	cur, ok := p.hitEnchant(id)
	if !ok {
		return false
	}
	e := *cur
	fn(&e)
	e.ID = id
	if p.ownEnchants == nil {
		p.ownEnchants = playerEnchants{}
	}
	p.ownEnchants[id] = &e
	return true
}

// ClearEnchantment drops the player's own copy of enchantment id. Runs: the player's
// region.
func (p *Player) ClearEnchantment(id int16) { delete(p.ownEnchants, id) }

// ClearAllEnchantments drops every copy the player has of its own. Runs: the player's
// region.
func (p *Player) ClearAllEnchantments() { p.ownEnchants = nil }

// OwnEnchantments returns the IDs of the enchantments the player has its own copy of.
// Runs: the player's region.
func (p *Player) OwnEnchantments() []int16 {
	ids := make([]int16, 0, len(p.ownEnchants))
	for id := range p.ownEnchants {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
