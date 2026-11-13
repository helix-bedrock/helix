package mob

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/world/mcdb"
)

// Breeding, growing up and leashes: the entity library's breedable, ageable and leashable
// components decide; this file gives them the mob and hands the engine what it must do
// (the baby, the experience, the lead item) through Hooks.
//
// BDS 1.26.60: a player's use on a mob tries the interaction components in a fixed order,
// interact, ageable, breedable, leashable, nameable (the provider table at 0xF70DB30 and
// on); the first that takes it wins. Mating (BreedableComponent 0x7371850) makes each baby
// with createBornActor (0xBFF2400, which always adds the persistent marker), then spawns
// seven heart particles and experience_reward's on_bred, each expression rounded (0x73710C0).
// The heart particles are the client's and are not sent.

// dataLeashHolder is the actor data key with the leash holder's unique ID, -1 for none.
const dataLeashHolder = 37

// SavedState is the age and breeding state a mob saves, BDS's AgeableComponent ("Age",
// "GrowthPaused", load 0x7444D80) and BreedableComponent ("InLove", "LoveCause",
// "BreedCooldown", load 0x7372E40) fields. Has* are false when the component was not there.
type SavedState struct {
	HasAge       bool
	Age          int32
	GrowthPaused bool
	HasBreed     bool
	InLove       int32
	Cooldown     int32
	LoveCause    int64
}

// SaveState is what the mob's age and breeding components hold now.
func (m *Mob) SaveState() SavedState {
	var s SavedState
	if m.states == nil {
		if m.saved != nil {
			return *m.saved
		}
		return s
	}
	if a := m.states.Ageable; a != nil {
		s.HasAge, s.Age = true, a.Age()
	}
	if b := m.states.Breedable; b != nil {
		s.HasBreed, s.InLove, s.Cooldown, s.LoveCause = true, b.Love(), b.Cooldown(), int64(b.LoveCause())
	}
	return s
}

// RestoreState keeps a save's age and breeding state to put back once the definition's
// components are built. Runs: before the mob's first tick.
func (m *Mob) RestoreState(s SavedState) { m.saved = &s }

// applySaved puts the saved state into the components just built.
func (m *Mob) applySaved() {
	s := m.saved
	if s == nil || m.states == nil {
		return
	}
	m.saved = nil
	if a := m.states.Ageable; a != nil && s.HasAge {
		a.SetAge(s.Age)
		m.SetFlag(behavior.FlagBaby, a.Age() < 0)
	}
	if b := m.states.Breedable; b != nil && s.HasBreed {
		b.SetState(s.InLove, s.Cooldown, behavior.ID(s.LoveCause))
	}
}

// CanMate reports that the mob can breed with partner, seen as its last-tick snapshot.
// Runs: the mob's think.
func (m *Mob) CanMate(partner behavior.Actor) bool {
	if m.states == nil || m.states.Breedable == nil {
		return false
	}
	ready := false
	if s, ok := partner.(*Snapshot); ok {
		ready = s.breedReady
	}
	return m.states.Breedable.CanMateWith(m, partner, ready)
}

// Breed is the breed goal mating the mob with partner; it waits for Apply.
func (m *Mob) Breed(partner behavior.Actor) {
	if partner != nil {
		m.do(op{kind: opBreed, id: partner.ID()})
	}
}

// mate is BreedableComponent's mate in Apply: the partner is checked again, both leave
// love, and each baby is made, followed by its experience.
func (m *Mob) mate(id behavior.ID) {
	if m.states == nil || m.states.Breedable == nil || m.hooks == nil || m.v.r == nil {
		return
	}
	pm := m.rt.Env.Mob(m.v.r, id)
	if pm == nil || pm.Dead() || pm.states == nil || pm.states.Breedable == nil {
		return
	}
	b := m.states.Breedable
	if !b.CanMate(m, pm, pm.states.Breedable) {
		return
	}
	m.births = b.Mate(&m.stateEnv, pm, pm.states.Breedable, m.births[:0])
	for _, birth := range m.births {
		if baby := m.hooks.Born(m, birth); baby != nil {
			b.Born(&m.stateEnv, birth, baby)
		}
		if xp := m.BredExperience(); xp > 0 {
			m.hooks.Experience(m, xp)
		}
	}
	clear(m.births)
	pm.snapshot()
}

// BredExperience is experience_reward's on_bred: each expression rounded, summed.
func (m *Mob) BredExperience() int32 {
	if m.states == nil || m.states.ExperienceReward == nil {
		return 0
	}
	var sum int32
	for _, src := range m.states.ExperienceReward.OnBred {
		if e, err := parseMolang(src); err == nil {
			sum += int32(math.Round(float64(e.eval(m))))
		}
	}
	return max(sum, 0)
}

// Feed is a player using the held item on the mob for its ageable, then its breedable
// component, in BDS's order: a baby grows, an adult falls in love. It reports whether the
// mob took the item and what the item turns into ("" when it is used up). Runs: the mob's
// region, outside its think.
func (m *Mob) Feed(player behavior.Actor, h state.Hand) (result string, ok bool) {
	if m.states == nil || m.stateEnv.Self == nil || player == nil || m.Dead() {
		return "", false
	}
	if a := m.states.Ageable; a != nil {
		if i := a.Feed(&m.stateEnv, player, h); i >= 0 {
			m.SetFlag(behavior.FlagBaby, a.Age() < 0)
			return orResult(a.Config().FeedItems[i].Result, a.Config().TransformToItem), true
		}
	}
	if b := m.states.Breedable; b != nil {
		if i := b.Feed(&m.stateEnv, player, h, m.environmentMet(b.Config().Environment)); i >= 0 {
			return orResult(b.Config().BreedItems[i].Result, b.Config().TransformToItem), true
		}
	}
	return "", false
}

func orResult(item, def string) string {
	if item != "" {
		return item
	}
	return def
}

// environmentMet is breedable's environment_requirements: for each, at least count of its
// blocks within radius of the mob. The cube BDS searches was not read; a cube of the radius
// is used.
func (m *Mob) environmentMet(reqs []state.EnvironmentRequirement) bool {
	if m.v.r == nil {
		return len(reqs) == 0
	}
	at := blockOf(m.body.Pos)
	for _, rq := range reqs {
		n, rad := int32(0), int32(math.Ceil(float64(rq.Radius)))
		for x := at.X - rad; x <= at.X+rad && n < rq.Count; x++ {
			for y := at.Y - rad; y <= at.Y+rad && n < rq.Count; y++ {
				for z := at.Z - rad; z <= at.Z+rad && n < rq.Count; z++ {
					if m.blockMatches(behavior.Pos{X: x, Y: y, Z: z}, rq.Blocks) {
						n++
					}
				}
			}
		}
		if n < rq.Count {
			return false
		}
	}
	return true
}

// HeldItem is an item a player holds as the state components match it (state.Hand).
type HeldItem struct{ Name string }

// Matches reports that the item is the one a descriptor names: a name with or without its
// namespace and aux, an object with "item" or "name", or one of the old names vanilla's
// files still use.
func (h HeldItem) Matches(desc string) bool {
	if h.Name == "" {
		return false
	}
	desc = strings.TrimSpace(desc)
	if strings.HasPrefix(desc, "{") {
		var o struct{ Item, Name string }
		if json.Unmarshal([]byte(desc), &o) != nil {
			return false
		}
		desc = o.Item
		if desc == "" {
			desc = o.Name
		}
	}
	name, ok := itemWithoutAux(desc)
	if !ok {
		return false
	}
	name = strings.ToLower(name)
	if alias, found := oldItemNames[strings.TrimPrefix(name, "minecraft:")]; found {
		name = alias
	}
	return ns(name) == h.Name
}

// oldItemNames are the old item names vanilla's entity files still use, by their current
// names (BDS's item aliases).
var oldItemNames = map[string]string{
	"muttonraw":      "minecraft:mutton",
	"muttoncooked":   "minecraft:cooked_mutton",
	"appleenchanted": "minecraft:enchanted_golden_apple",
	"fish":           "minecraft:cod",
	"clownfish":      "minecraft:tropical_fish",
	"cooked_fish":    "minecraft:cooked_cod",
}

// --- leashes ---

// LeashHolder is the entity holding the mob's leash, NoID for none.
func (m *Mob) LeashHolder() behavior.ID { return m.leashHolder }

// SetLeashHolder ties the leash to an entity: the leashed flag and the holder actor data
// the client draws the lead with. Runs: the mob's region (its think included).
func (m *Mob) SetLeashHolder(id behavior.ID) {
	m.leashHolder = id
	m.SetFlag(behavior.FlagLeashed, id != behavior.NoID)
	m.setData(dataLeashHolder, int64(id))
}

// LeashInteract is a player leashing the mob with a lead, or taking off a lead it holds (the
// leashable component's interaction):
// false when the mob cannot be leashed by that player now. Runs: outside the mob's think.
func (m *Mob) LeashInteract(player behavior.Actor, h state.Hand) (state.LeashAction, bool) {
	if m.states == nil || m.states.Leashable == nil || m.stateEnv.Self == nil || player == nil || m.Dead() {
		return state.LeashNone, false
	}
	l := m.states.Leashable
	a, _ := l.Interaction(&m.stateEnv, player, h)
	if a == state.LeashNone {
		return a, false
	}
	l.Run(&m.stateEnv, a, player)
	return a, true
}

// LeashToHolder moves the mob's leash to another holder (a fence's knot), as
// LeashFenceKnotActor's binding does with Actor::setLeashHolder.
func (m *Mob) LeashToHolder(id behavior.ID) {
	if m.leashHolder != behavior.NoID {
		m.SetLeashHolder(id)
	}
}

// LeashMaxDistance is the distance at which the leash breaks for a holder, from the preset
// it would use (10 without one).
func (m *Mob) LeashMaxDistance(holder behavior.Actor) float64 {
	if m.states == nil || m.states.Leashable == nil || m.stateEnv.Self == nil {
		return state.DefaultLeashPreset().MaxDistance
	}
	return m.states.Leashable.Preset(&m.stateEnv, holder).MaxDistance
}

// Leashable reports minecraft:leashable.
func (m *Mob) Leashable() bool { return m.states != nil && m.states.Leashable != nil }

// Unleash takes the leash off as Actor::dropLeash, dropping the lead when asked. Runs: the
// mob's region.
func (s stateMob) DropLeash(dropItem, _ bool) {
	if s.leashHolder == behavior.NoID {
		return
	}
	s.SetLeashHolder(behavior.NoID)
	n := int32(0)
	if dropItem {
		n = 1
	}
	s.do(op{kind: opDropLeash, n: n})
}

// Unleash drops the mob's leash, the lead with it when dropLead is set.
func (m *Mob) Unleash(dropLead bool) { stateMob{m}.DropLeash(dropLead, true) }

func (s stateMob) LeashHolder() behavior.ID      { return s.leashHolder }
func (s stateMob) SetLeashHolder(id behavior.ID) { s.Mob.SetLeashHolder(id) }

// Leash is the holder's box centre and the leash's maximum distance (behavior.Mob).
func (m *Mob) Leash() (behavior.Vec3, float64, bool) {
	h := m.leashActor()
	if h == nil {
		return behavior.Vec3{}, 0, false
	}
	b := h.Box()
	c := behavior.Vec3{X: (b.Min.X + b.Max.X) / 2, Y: (b.Min.Y + b.Max.Y) / 2, Z: (b.Min.Z + b.Max.Z) / 2}
	return c, m.LeashMaxDistance(h), true
}

// LeashStretched reports the holder past the preset's hard distance.
func (m *Mob) LeashStretched() bool {
	h := m.leashActor()
	if h == nil || m.states == nil || m.states.Leashable == nil {
		return false
	}
	p := m.states.Leashable.Preset(&m.stateEnv, h)
	hp, mp := h.Pos(), m.body.Pos
	dx, dy, dz := hp.X-mp.X, hp.Y-mp.Y, hp.Z-mp.Z
	return dx*dx+dy*dy+dz*dz > p.HardDistance*p.HardDistance
}

func (m *Mob) leashActor() behavior.Actor {
	if m.leashHolder == behavior.NoID || m.v.r == nil {
		return nil
	}
	return m.rt.Env.Actor(m.v.r, m.leashHolder)
}

// BoxState answers the leash about the chunks a missing holder could be in: ticking when
// every chunk the box overlaps ticked this tick or the last.
func (c despawnChunks) BoxState(b behavior.Box) state.ChunkState {
	r := c.region()
	if r == nil {
		return state.ChunksMissing
	}
	out := state.ChunksTicking
	for x := floor32(b.Min.X) >> 4; x <= floor32(b.Max.X)>>4; x++ {
		for z := floor32(b.Min.Z) >> 4; z <= floor32(b.Max.Z)>>4; z++ {
			recent, ok := r.ChunkTicked(mcdb.ChunkPos{x, z})
			if !ok {
				return state.ChunksMissing
			}
			if !recent {
				out = state.ChunksLoaded
			}
		}
	}
	return out
}

var (
	_ behavior.Breeder  = (*Mob)(nil)
	_ state.Hand        = HeldItem{}
	_ state.LeashChunks = despawnChunks{}
)
