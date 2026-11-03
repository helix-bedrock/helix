package player

import (
	"math"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Tune names one number of a player's hunger, health and combat. Its value comes
// from, in order: the player's own (Player.SetTuning), the server's while running
// (Host.SetTuning), then the default: vanilla's, or Setup.SetTuning's. Clearing a
// value drops back to the next one.
//
// A vanilla system reads these numbers; a Systems hook that replaces it decides for
// itself whether to.
type Tune int

// The numbers. Those named Every are in ticks, and 0 or less turns the thing off.
// Those named Scale multiply vanilla's result, so 1 changes nothing.
const (
	// What each action costs in exhaustion.
	TuneExhaustHeal Tune = iota
	TuneExhaustJump
	TuneExhaustSprintJump
	TuneExhaustMine
	TuneExhaustAttack
	TuneExhaustDamage
	TuneExhaustWalk
	TuneExhaustSprint
	TuneExhaustSwim
	TuneExhaustLunge
	// TuneExhaustScale multiplies all exhaustion: how fast hunger drains.
	TuneExhaustScale
	// TuneExhaustStep is the exhaustion that burns food; it can build up to 20 at
	// most, the attribute's limit, so a step above that never burns.
	TuneExhaustStep
	// TuneBurnSaturation and TuneBurnHunger are what one step burns.
	TuneBurnSaturation
	TuneBurnHunger

	// TuneMaxHealth and TuneMaxHunger are the most the player can have, at least 1.
	// A respawn fills them.
	TuneMaxHealth
	TuneMaxHunger

	// TuneRegenHunger is the hunger from which health comes back.
	TuneRegenEvery
	TuneRegenAmount
	TuneRegenHunger
	// TuneStarveFloorEasy and TuneStarveFloorNormal are the health starving stops
	// at on those difficulties.
	TuneStarveEvery
	TuneStarveDamage
	TuneStarveFloorEasy
	TuneStarveFloorNormal
	// What the peaceful difficulty gives back.
	TunePeacefulHungerEvery
	TunePeacefulHungerAmount
	TunePeacefulHealthEvery
	TunePeacefulHealthAmount
	// What food gives.
	TuneFoodNutritionScale
	TuneFoodSaturationScale

	// TuneDamageTakenScale multiplies damage before armour.
	TuneDamageTakenScale
	// TuneHurtTicks is how long only harder hits count after a hurt.
	TuneHurtTicks
	// TuneArmor, TuneToughness and TuneProtection are added to what the player
	// wears; below 0 they take it away, down to none. TuneProtection counts in the
	// points of armour enchantments.
	TuneArmor
	TuneToughness
	TuneProtection
	// TuneArmorMaxShare is the most of a hit armour can take away.
	TuneArmorMaxShare
	// TuneProtectionLimit is the most points of protection that count, and
	// TuneProtectionPoint the share of a hit each takes away.
	TuneProtectionLimit
	TuneProtectionPoint
	// TuneFallFree is how many blocks a fall costs nothing.
	TuneFallFree
	TuneFallDamageScale

	// What the player's hits do, read from the attacker. TuneAttackDamage is added
	// to its melee damage, TuneDamageDealtScale multiplies the whole hit.
	TuneAttackDamage
	TuneDamageDealtScale
	TuneCritMultiplier
	// TuneKnockbackDealtScale multiplies every push of the player's hits;
	// TuneKnockbackPerLevel and TuneKnockbackSprint make up their extra knockback.
	TuneKnockbackDealtScale
	TuneKnockbackPerLevel
	TuneKnockbackSprint

	// How the player is pushed, read from the one being hit. Push and Lift are the
	// first push of any hit, ExtraPush and ExtraLift what each point of extra
	// knockback adds. Retain is how much of its speed it keeps, LiftScale multiplies
	// each lift, and Cap is the most upward speed a push leaves.
	TuneKnockbackPush
	TuneKnockbackLift
	TuneKnockbackExtraPush
	TuneKnockbackExtraLift
	TuneKnockbackRetain
	TuneKnockbackLiftScale
	TuneKnockbackCap
	// TuneKnockbackTakenScale multiplies every push the player gets.
	TuneKnockbackTakenScale
	// TuneShieldRaise is the ticks a shield must be up before it blocks,
	// TuneShieldCooldown the ticks an axe's hit keeps it down, and TuneShieldPush and
	// TuneShieldLift how hard a blocked hit pushes the attacker.
	TuneShieldRaise
	TuneShieldCooldown
	TuneShieldPush
	TuneShieldLift
	// TuneTotemHealth is the health a totem leaves.
	TuneTotemHealth
	// TuneEnchantMaxShelves is the most bookshelves that count around a table.
	TuneEnchantMaxShelves
	// TuneAnvilTooExpensive is the price a player out of creative is refused at,
	// TuneAnvilNameLimit what a rename alone is held to, and TuneAnvilStackCost what
	// merging enchantments onto a stack costs.
	TuneAnvilTooExpensive
	TuneAnvilNameLimit
	TuneAnvilStackCost
	// TuneAnvilMergeBonus and TuneAnvilRepairableBonus are the percent of durability merging
	// two items gives on top, plain and through the repairable component;
	// TuneAnvilMaterialShare is the part of it one material restores (4 is a quarter), and
	// TuneAnvilMergeCost what a merge that repairs costs.
	TuneAnvilMergeBonus
	TuneAnvilRepairableBonus
	TuneAnvilMaterialShare
	TuneAnvilMergeCost
	// TuneAnvilWearChance is the chance a use out of creative wears the anvil a step.
	TuneAnvilWearChance
	// TuneGrindstoneBonusShare is the part of durability two merged items get on top (20
	// is a twentieth), TuneGrindstoneXPScale multiplies the experience given back.
	TuneGrindstoneBonusShare
	TuneGrindstoneXPScale

	tuneCount
)

// tuneSpec is one Tune's name and vanilla value (BDS 1.26.60).
type tuneSpec struct {
	name    string
	vanilla float32
}

// tuneSpecs are the Tunes. The exhaustion costs are the player definition's, read at
// boot; the ones here are only what a Host without a Setup sees.
var tuneSpecs = [tuneCount]tuneSpec{
	TuneExhaustHeal:          {name: "exhaust_heal"},
	TuneExhaustJump:          {name: "exhaust_jump"},
	TuneExhaustSprintJump:    {name: "exhaust_sprint_jump"},
	TuneExhaustMine:          {name: "exhaust_mine"},
	TuneExhaustAttack:        {name: "exhaust_attack"},
	TuneExhaustDamage:        {name: "exhaust_damage"},
	TuneExhaustWalk:          {name: "exhaust_walk"},
	TuneExhaustSprint:        {name: "exhaust_sprint"},
	TuneExhaustSwim:          {name: "exhaust_swim"},
	TuneExhaustLunge:         {name: "exhaust_lunge"},
	TuneExhaustScale:         {"exhaust_scale", 1},
	TuneExhaustStep:          {"exhaust_step", exhaustionStep},
	TuneBurnSaturation:       {"burn_saturation", 1},
	TuneBurnHunger:           {"burn_hunger", 1},
	TuneMaxHealth:            {"max_health", 20},
	TuneMaxHunger:            {"max_hunger", 20},
	TuneRegenEvery:           {"regen_every", foodTimerEnd},
	TuneRegenAmount:          {"regen_amount", 1},
	TuneRegenHunger:          {"regen_hunger", regenHunger},
	TuneStarveEvery:          {"starve_every", foodTimerEnd},
	TuneStarveDamage:         {"starve_damage", starveDamage},
	TuneStarveFloorEasy:      {"starve_floor_easy", starveEasyFloor},
	TuneStarveFloorNormal:    {"starve_floor_normal", starveNormalFloor},
	TunePeacefulHungerEvery:  {"peaceful_hunger_every", peacefulHungerEvery},
	TunePeacefulHungerAmount: {"peaceful_hunger_amount", 1},
	TunePeacefulHealthEvery:  {"peaceful_health_every", peacefulHealthEvery},
	TunePeacefulHealthAmount: {"peaceful_health_amount", 1},
	TuneFoodNutritionScale:   {"food_nutrition_scale", 1},
	TuneFoodSaturationScale:  {"food_saturation_scale", 1},
	TuneDamageTakenScale:     {"damage_taken_scale", 1},
	TuneHurtTicks:            {"hurt_ticks", hurtTicks},
	TuneArmor:                {name: "armor"},
	TuneToughness:            {name: "toughness"},
	TuneProtection:           {name: "protection"},
	TuneArmorMaxShare:        {"armor_max_share", armorMaxShare},
	TuneProtectionLimit:      {"protection_limit", protectionLimit},
	TuneProtectionPoint:      {"protection_point", protectionPoint},
	TuneFallFree:             {"fall_free", fallFree},
	TuneFallDamageScale:      {"fall_damage_scale", 1},
	TuneAttackDamage:         {name: "attack_damage"},
	TuneDamageDealtScale:     {"damage_dealt_scale", 1},
	TuneCritMultiplier:       {"crit_multiplier", critMultiplier},
	TuneKnockbackDealtScale:  {"knockback_dealt_scale", 1},
	TuneKnockbackPerLevel:    {"knockback_per_level", knockbackPerLevel},
	TuneKnockbackSprint:      {"knockback_sprint", sprintKnockback},
	TuneKnockbackPush:        {"knockback_push", knockbackPush},
	TuneKnockbackLift:        {"knockback_lift", knockbackPush},
	TuneKnockbackExtraPush:   {"knockback_extra_push", knockbackExtraPush},
	TuneKnockbackExtraLift:   {"knockback_extra_lift", knockbackExtraLift},
	TuneKnockbackRetain:      {"knockback_retain", knockbackRetain},
	TuneKnockbackLiftScale:   {"knockback_lift_scale", knockbackLift},
	TuneKnockbackCap:         {"knockback_cap", knockbackCap},
	TuneKnockbackTakenScale:  {"knockback_taken_scale", 1},
	TuneShieldRaise:          {"shield_raise", shieldRaiseTicks},
	TuneShieldCooldown:       {"shield_cooldown", shieldCooldownTicks},
	TuneShieldPush:           {"shield_push", shieldPush},
	TuneShieldLift:           {"shield_lift", shieldLift},
	TuneTotemHealth:          {"totem_health", totemHealth},
	TuneEnchantMaxShelves:    {"enchant_max_shelves", enchantMaxShelves},
	TuneAnvilTooExpensive:    {"anvil_too_expensive", anvilTooExpensive},
	TuneAnvilNameLimit:       {"anvil_name_limit", anvilNameOnly},
	TuneAnvilStackCost:       {"anvil_stack_cost", anvilStackCost},
	TuneAnvilMergeBonus:      {"anvil_merge_bonus", anvilMergeBonus},
	TuneAnvilRepairableBonus: {"anvil_repairable_bonus", anvilRepairableBonus},
	TuneAnvilMaterialShare:   {"anvil_material_share", anvilMaterialShare},
	TuneAnvilMergeCost:       {"anvil_merge_cost", anvilMergeCost},
	TuneAnvilWearChance:      {"anvil_wear_chance", anvilWearChance},
	TuneGrindstoneBonusShare: {"grindstone_bonus_share", grindstoneBonusShare},
	TuneGrindstoneXPScale:    {"grindstone_xp_scale", 1},
}

// tuneTable is one value per Tune.
type tuneTable [tuneCount]float32

// vanillaTuning is every Tune's vanilla value, with the exhaustion costs ex.
func vanillaTuning(ex exhaustionValues) tuneTable {
	var t tuneTable
	for i := range tuneSpecs {
		t[i] = tuneSpecs[i].vanilla
	}
	t[TuneExhaustHeal], t[TuneExhaustJump], t[TuneExhaustSprintJump] = ex.Heal, ex.Jump, ex.SprintJump
	t[TuneExhaustMine], t[TuneExhaustAttack], t[TuneExhaustDamage] = ex.Mine, ex.Attack, ex.Damage
	t[TuneExhaustWalk], t[TuneExhaustSprint], t[TuneExhaustSwim], t[TuneExhaustLunge] = ex.Walk, ex.Sprint, ex.Swim, ex.Lunge
	return t
}

// String returns the Tune's name, as TuneByName takes it.
func (t Tune) String() string {
	if !t.valid() {
		return ""
	}
	return tuneSpecs[t].name
}

// TuneByName finds a Tune by its name.
func TuneByName(name string) (Tune, bool) {
	for i := range tuneSpecs {
		if tuneSpecs[i].name == name {
			return Tune(i), true
		}
	}
	return 0, false
}

func (t Tune) valid() bool { return t >= 0 && t < tuneCount }

// tunable reports whether v may be set as t's value: not a number or infinity would
// spread into health and packets.
func tunable(t Tune, v float32) bool {
	return t.valid() && !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0)
}

// SetTuning sets the default of t, which clearing goes back to; false if t is not a
// Tune or v is not a finite number.
func (s *Setup) SetTuning(t Tune, v float32) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	if tunable(t, v) {
		s.h.game.tuning[t] = v
	}
	return nil
}

// Tuning returns the default of t so far.
func (s *Setup) Tuning(t Tune) float32 {
	if !t.valid() {
		return 0
	}
	return s.h.game.tuning[t]
}

// Tuning returns the server's value of t, what players without their own use. Runs:
// anywhere.
func (s *Host) Tuning(t Tune) float32 {
	if !t.valid() {
		return 0
	}
	if live := s.tuning.Load(); live != nil {
		return live[t]
	}
	return tuneSpecs[t].vanilla
}

// SetTuning changes t for every player without its own value, at once; false if t is
// not a Tune or v is not a finite number. Runs: anywhere.
func (s *Host) SetTuning(t Tune, v float32) bool {
	if !tunable(t, v) {
		return false
	}
	s.swapTuning(func(table *tuneTable) { table[t] = v })
	return true
}

// ClearTuning puts t back to its default. Runs: anywhere.
func (s *Host) ClearTuning(t Tune) {
	if t.valid() {
		s.swapTuning(func(table *tuneTable) { table[t] = s.game.tuning[t] })
	}
}

// ClearAllTuning puts every Tune back to its default. Runs: anywhere.
func (s *Host) ClearAllTuning() {
	s.swapTuning(func(table *tuneTable) { *table = s.game.tuning })
}

// swapTuning publishes a changed copy of the live table, so every player's read in
// a tick is one load and no lock.
func (s *Host) swapTuning(change func(*tuneTable)) {
	for {
		old := s.tuning.Load()
		next := s.game.tuning
		if old != nil {
			next = *old
		}
		change(&next)
		if s.tuning.CompareAndSwap(old, &next) {
			return
		}
	}
}

// playerTuning is the values a player has of its own.
type playerTuning struct {
	set    [tuneCount]bool
	values tuneTable
}

// Tuning returns the value of t the player runs on. Runs: the player's region.
func (p *Player) Tuning(t Tune) float32 {
	if !t.valid() {
		return 0
	}
	if o := p.tuned; o != nil && o.set[t] {
		return o.values[t]
	}
	return p.s.Tuning(t)
}

// HasTuning reports whether the player has its own value of t. Runs: the player's
// region.
func (p *Player) HasTuning(t Tune) bool {
	return t.valid() && p.tuned != nil && p.tuned.set[t]
}

// SetTuning gives the player its own value of t, over the server's; false if t is not
// a Tune or v is not a finite number. It lasts until cleared or the player leaves: it
// is not saved. Runs: the player's region.
func (p *Player) SetTuning(t Tune, v float32) bool {
	if !tunable(t, v) {
		return false
	}
	if p.tuned == nil {
		p.tuned = &playerTuning{}
	}
	p.tuned.set[t], p.tuned.values[t] = true, v
	return true
}

// ClearTuning drops the player's own value of t. Runs: the player's region.
func (p *Player) ClearTuning(t Tune) {
	if t.valid() && p.tuned != nil {
		p.tuned.set[t] = false
	}
}

// ClearAllTuning drops all of the player's own values. Runs: the player's region.
func (p *Player) ClearAllTuning() { p.tuned = nil }

// everyTicks reports whether count is on an interval given as a Tune; 0 or less is
// never.
func everyTicks(count int32, every float32) bool {
	n := int32(every)
	return n > 0 && count%n == 0
}

// timerDone reports whether a timer has reached an interval given as a Tune; 0 or
// less is never.
func timerDone(timer int32, every float32) bool {
	return every > 0 && float32(timer) >= every
}

// tickMaxes follows the most health and hunger the player may have: a change from
// any of the three levels of Tune tells the client, and takes what no longer fits.
func (p *Player) tickMaxes() {
	h, f := max(p.Tuning(TuneMaxHealth), 1), max(p.Tuning(TuneMaxHunger), 1)
	a := &p.attributes
	if a.maximum(attrHealth) == h && a.maximum(attrHunger) == f {
		return
	}
	a[attrHealth].limit, a[attrHunger].limit = h, f
	a.set(attrHealth, a.get(attrHealth))
	a.set(attrHunger, a.get(attrHunger))
	// Sent even when the values fit: the client needs the new maximums.
	p.send(&packet.UpdateAttributes{EntityRuntimeID: p.runtimeID, Attributes: []protocol.Attribute{a.entry(attrHealth), a.entry(attrHunger)}, Tick: p.entity.Tick()})
	p.showHealth()
}
