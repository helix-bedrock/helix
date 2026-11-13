package mob

import (
	"encoding/json"
	"maps"
	"math"
	"slices"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/component"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/mcdb"
)

// Mob state: the definition's state components (ageable, breedable, angry, healable,
// breathable, burns_in_daylight, hurt_on_condition, despawn and the rest), run by the entity
// library's state package once per tick while the mob thinks. What they do to the mob itself
// happens at once; hurting it waits for Apply. A despawned mob is removed as BDS's
// Actor::despawn: no death, no drops. Burning down is the engine's, shared with players
// (FireTicks, SetFireTicks).

// readStates builds the state components from the definition. A component whose JSON did not
// change keeps its state.
func (m *Mob) readStates(c component.Context, comps definition.Group) {
	if m.stateEnv.Self == nil {
		m.stateEnv = state.Env{
			World: goalWorld{&m.v}, Self: stateMob{m}, Events: m.inst,
			Tester: func(self, other behavior.Actor) definition.Tester {
				return tester{m: m, self: self, other: other}
			},
			HasFamily:    func(a behavior.Actor, f string) bool { return slices.Contains(families(a), f) },
			BlockMatches: m.blockMatches,
		}
	}
	s, errs := component.States(c, &m.stateEnv, m.states, m.stateComps, comps)
	for _, err := range errs {
		m.rt.Log.Debug("mob state", "type", m.typ, "err", err)
	}
	m.states = s
	m.stateComps = maps.Clone(comps)
	m.applySaved()
}

// stateTick runs the state components and burning, and reports whether the mob despawned.
// Runs: think.
func (m *Mob) stateTick() bool {
	if m.despawned {
		return true
	}
	if m.geneEvents && m.states != nil && m.states.Genetics != nil {
		// GeneticsComponent's initialize: a new mob's random genes run their variants' birth
		// events (a goat's born_default adds its milking interaction).
		m.geneEvents = false
		m.states.Genetics.FireEvents(&m.stateEnv)
	}
	if m.states != nil {
		// A transformation that comes due is not run yet: making the new entity is the
		// engine's and not hooked up.
		m.states.Tick(&m.stateEnv, state.TickInput{Chunks: despawnChunks{m}, LeashChunks: despawnChunks{m}})
	}
	return m.despawned
}

// FireTicks is how long the mob still burns.
func (m *Mob) FireTicks() int32 { return m.fireTicks }

// SetFireTicks sets how long the mob burns, and its on-fire flag with it. Runs: Apply, or
// the engine's tick of the mob.
func (m *Mob) SetFireTicks(n int32) {
	m.fireTicks = max(n, 0)
	m.SetFlag(flagOnFire, m.fireTicks > 0)
}

// FireImmune reports minecraft:fire_immune.
func (m *Mob) FireImmune() bool { return m.flags.Has(behavior.FlagFireImmune) }

// Wet is in water, or in the rain under the open sky: what puts a mob's fire out.
func (m *Mob) Wet() bool { return stateMob{m}.InWaterOrRain() }

// Despawned reports whether the mob despawned, to be removed without dying.
func (m *Mob) Despawned() bool { return m.despawned }

// blockMatches reports the block at p named by one of the descriptors.
func (m *Mob) blockMatches(p behavior.Pos, blocks []string) bool {
	if m.v.r == nil {
		return false
	}
	name := m.rt.Blocks.reg.Name(m.v.block(p, 0))
	return slices.ContainsFunc(blocks, func(d string) bool { return ns(d) == name })
}

// canSeeSky is BDS's canSeeSky: at or above the height map.
func (m *Mob) canSeeSky(p behavior.Vec3) bool {
	if m.v.r == nil {
		return false
	}
	h, ok := m.v.r.Height(int(math.Floor(float64(p.X))), int(math.Floor(float64(p.Z))))
	return ok && int(math.Floor(float64(p.Y))) >= h
}

// despawnChunks answers the despawn rules about the chunks around the mob.
type despawnChunks struct{ m *Mob }

func (c despawnChunks) region() *world.Region { return c.m.v.r }

// TickRange is the dimension's simulation distance.
func (c despawnChunks) TickRange() int32 {
	r := c.region()
	if r == nil {
		return 0
	}
	return r.Dimension().SimulationDistance()
}

// Loaded reports every chunk within n chunks of p held by the mob's region.
func (c despawnChunks) Loaded(p behavior.Pos, n int32) bool {
	r := c.region()
	if r == nil {
		return false
	}
	cx, cz := p.X>>4, p.Z>>4
	for x := cx - n; x <= cx+n; x++ {
		for z := cz - n; z <= cz+n; z++ {
			if _, ok := r.ChunkTicked(mcdb.ChunkPos{x, z}); !ok {
				return false
			}
		}
	}
	return true
}

// UntickedNeighbour reports a chunk within n chunks of p that did not tick this tick or the
// one before: the edge of every simulation zone.
func (c despawnChunks) UntickedNeighbour(p behavior.Pos, n int32) bool {
	r := c.region()
	if r == nil {
		return false
	}
	cx, cz := p.X>>4, p.Z>>4
	for x := cx - n; x <= cx+n; x++ {
		for z := cz - n; z <= cz+n; z++ {
			if recent, _ := r.ChunkTicked(mcdb.ChunkPos{x, z}); !recent {
				return true
			}
		}
	}
	return false
}

// stateMob is the mob as the library's state components see it. state.Mob.Hurt clashes with
// mob.Mob.Hurt, so it is a separate type. Equipment, effects, leashes, riding, items and
// particles are not hooked up yet and answer as absent.
type stateMob struct{ *Mob }

func (s stateMob) Health() (health, maxHealth int32) {
	return int32(math.Ceil(float64(s.health))), int32(math.Ceil(float64(s.maxHealth)))
}
func (s stateMob) ResetNoActionTime() { s.noActionTime = 0 }

// Despawn marks the mob despawned; its entity is removed when its tick is applied.
func (s stateMob) Despawn() { s.despawned = true }

// Heal adds health up to the maximum.
func (s stateMob) Heal(n int32) {
	if n > 0 && !s.Dead() {
		s.health = min(s.health+float64(n), s.maxHealth)
	}
}

// ActorEvent sends an actor event about the mob, at Apply.
func (s stateMob) ActorEvent(id int) { s.do(op{kind: opActorEvent, n: int32(id)}) } //nolint:gosec // an event number

// Hurt deals damage of a cause to the mob itself, at Apply, through the engine's hurt.
func (s stateMob) Hurt(cause int, amount float64) {
	s.do(op{kind: opHurt, n: int32(cause), amount: amount}) //nolint:gosec // a cause number
}

// SetOnFire sets the mob burning for at least that many seconds.
func (s stateMob) SetOnFire(seconds int32) {
	if s.flags.Has(behavior.FlagFireImmune) || seconds <= 0 {
		return
	}
	s.SetFireTicks(max(s.fireTicks, seconds*ticksPerSecond))
}

// CanSeeDaylight is day with the sky above the mob's eyes.
func (s stateMob) CanSeeDaylight() bool {
	return s.v.r != nil && s.rt.Env.SkyDarken(s.v.r) < 4 && s.canSeeSky(s.Eyes())
}

// InWaterOrRain is in water, or in the rain under the open sky.
func (s stateMob) InWaterOrRain() bool {
	return s.InWater() || s.v.r != nil && s.rt.Env.Rain(s.v.r) > 0 && s.canSeeSky(s.Eyes())
}

// InPowderSnow reports powder snow at the mob's feet.
func (s stateMob) InPowderSnow() bool {
	return s.v.r != nil && s.rt.Blocks.reg.Name(s.v.block(blockOf(s.body.Pos), 0)) == "minecraft:powder_snow"
}

// Breath is what is at the mob's eyes, against the breathable component's block lists.
func (s stateMob) Breath(breathe, nonBreathe []string) state.Medium {
	if s.v.r == nil {
		return state.MediumAir
	}
	p := blockOf(s.Eyes())
	id := s.v.block(p, 0)
	name := s.rt.Blocks.reg.Name(id)
	switch {
	case slices.ContainsFunc(breathe, func(d string) bool { return ns(d) == name }):
		return state.MediumBreathable
	case slices.ContainsFunc(nonBreathe, func(d string) bool { return ns(d) == name }):
		return state.MediumNotBreathable
	}
	switch s.rt.Blocks.reg.Properties(s.v.liquid(p)).Material {
	case materialWater:
		return state.MediumWater
	case materialLava:
		return state.MediumLava
	}
	if s.rt.Blocks.Full(id) && s.rt.Blocks.reg.Properties(id).SolidMaterial {
		return state.MediumSolid
	}
	return state.MediumAir
}

// SetAirSupply keeps the air supply and shows it to those who see the mob.
func (s stateMob) SetAirSupply(air, maxAir int16) {
	if s.air == air && s.maxAir == maxAir {
		return
	}
	s.air, s.maxAir = air, maxAir
	s.flagsChanged()
}

func (stateMob) SetOwner(behavior.ID)                         {}
func (stateMob) DropItem(string) bool                         { return false }
func (stateMob) AddEffect(string, int32, int32)               {}
func (stateMob) CanAttack(behavior.Actor, bool) bool          { return false }
func (stateMob) Particles(int, int)                           {}
func (stateMob) MakeMad()                                     {}
func (stateMob) SlotEmpty(string) bool                        { return true }
func (stateMob) Respiration() int32                           { return 0 }
func (stateMob) StopRiding()                                  {}
func (stateMob) GameEvent(string, behavior.ID, behavior.Vec3) {}

// blockOf is the block a position is in.
func blockOf(v behavior.Vec3) behavior.Pos {
	return behavior.Pos{X: int32(math.Floor(float64(v.X))), Y: int32(math.Floor(float64(v.Y))), Z: int32(math.Floor(float64(v.Z)))}
}

var _ state.Mob = stateMob{}

// Air is the mob's air supply and its maximum; BDS's 300 for both until breathable sets them.
func (m *Mob) Air() (air, maxAir int16) {
	if m.maxAir == 0 {
		return defaultAir, defaultAir
	}
	return m.air, m.maxAir
}

// defaultAir is BDS's air supply for a mob without breathable.
const defaultAir = 300

// Interact is a player using an item on the mob, as BDS's InteractComponent: the first of
// minecraft:interact's interactions the player may run (cooldown over, on_interact's filters
// passing with the player as other) runs, starting its cooldown and firing on_interact, and
// its JSON comes back for the engine's item work. False when none applies. Runs: the mob's
// region.
func (m *Mob) Interact(player behavior.Actor) (json.RawMessage, bool) {
	if m.states == nil || m.states.Interact == nil || m.stateEnv.Self == nil || player == nil {
		return nil, false
	}
	in := m.states.Interact
	i := in.Find(&m.stateEnv, player)
	if i < 0 {
		return nil, false
	}
	raw := in.Config().Interactions[i].Raw
	in.Run(&m.stateEnv, i, player)
	return raw, true
}
