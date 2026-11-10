package mob

import (
	"math"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/component"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/mcdb"
)

// Despawning: the definition's minecraft:despawn, run by the entity library's state.Despawn
// (BDS 1.26.60's despawn tick) once per tick while the mob thinks. A despawned mob is removed
// as BDS's Actor::despawn: no death, no drops.

// readDespawn builds the despawn component from the definition, or drops it when the
// definition has none.
func (m *Mob) readDespawn(c component.Context, comps definition.Group) {
	raw, ok := comps[component.DespawnName]
	if !ok {
		m.despawn = nil
		return
	}
	obj, err := component.ParseObject(raw)
	if err != nil {
		m.rt.Log.Debug("mob despawn", "type", m.typ, "err", err)
	}
	cfg := c.DespawnConfig(obj)
	if m.despawn == nil {
		m.despawn = state.NewDespawn(cfg)
	} else {
		m.despawn.SetConfig(cfg)
	}
	if m.stateEnv.Self == nil {
		m.stateEnv = state.Env{
			World: goalWorld{&m.v}, Self: stateMob{m},
			Tester: func(_, other behavior.Actor) definition.Tester { return m.tester(other) },
		}
	}
}

// despawnTick runs the despawn rules and reports whether the mob despawned. Runs: think.
func (m *Mob) despawnTick() bool {
	if m.despawn == nil || m.despawned {
		return m.despawned
	}
	m.despawn.Tick(&m.stateEnv, despawnChunks{m})
	return m.despawned
}

// Despawned reports whether the mob despawned, to be removed without dying.
func (m *Mob) Despawned() bool { return m.despawned }

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
			if _, ok := r.ChunkTickedAt(mcdb.ChunkPos{x, z}); !ok {
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
	now := r.CurrentTick()
	cx, cz := p.X>>4, p.Z>>4
	for x := cx - n; x <= cx+n; x++ {
		for z := cz - n; z <= cz+n; z++ {
			if at, ok := r.ChunkTickedAt(mcdb.ChunkPos{x, z}); !ok || at < now-1 {
				return true
			}
		}
	}
	return false
}

// stateMob is the mob as the library's state components see it. Only minecraft:despawn runs
// so far, so the parts it never asks for answer as absent and do nothing.
type stateMob struct{ *Mob }

func (s stateMob) Health() (health, maxHealth int32) {
	return int32(math.Ceil(float64(s.health))), int32(math.Ceil(float64(s.maxHealth)))
}
func (s stateMob) ResetNoActionTime() { s.noActionTime = 0 }

// Despawn marks the mob despawned; its entity is removed when its tick is applied.
func (s stateMob) Despawn() { s.despawned = true }

func (stateMob) Heal(int32)                                   {}
func (stateMob) SetOwner(behavior.ID)                         {}
func (stateMob) ActorEvent(int)                               {}
func (stateMob) DropItem(string) bool                         { return false }
func (stateMob) AddEffect(string, int32, int32)               {}
func (stateMob) CanAttack(behavior.Actor, bool) bool          { return false }
func (stateMob) Particles(int, int)                           {}
func (stateMob) MakeMad()                                     {}
func (stateMob) Hurt(int, float32)                            {}
func (stateMob) SetOnFire(int32)                              {}
func (stateMob) CanSeeDaylight() bool                         { return false }
func (stateMob) InWaterOrRain() bool                          { return false }
func (stateMob) InPowderSnow() bool                           { return false }
func (stateMob) SlotEmpty(string) bool                        { return true }
func (stateMob) Breath([]string, []string) state.Medium       { return state.MediumAir }
func (stateMob) Respiration() int32                           { return 0 }
func (stateMob) SetAirSupply(int16, int16)                    {}
func (stateMob) LeashHolder() behavior.ID                     { return behavior.NoID }
func (stateMob) SetLeashHolder(behavior.ID)                   {}
func (stateMob) DropLeash(bool, bool)                         {}
func (stateMob) StopRiding()                                  {}
func (stateMob) GameEvent(string, behavior.ID, behavior.Vec3) {}

var _ state.Mob = stateMob{}
