package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/state"
)

// The creeper: BDS 1.26.60 writes it as its own class (vtable 0xFAC6800). Its tick
// (0xBD33C20) runs before the mob's own: while alive, the swell direction follows the
// ignited flag (+1 lit, -1 not), the swell moves by it and stays in 0..30, and a lit creeper
// whose swell was 0 hisses (LevelSoundEvent 51, fuse) at its head (attach point 3). The
// explosion itself is minecraft:explode, the definition's component.
const (
	creeperType  = "minecraft:creeper"
	creeperSwell = 30
	soundFuse    = 51
)

// ExplodeHooks is what an engine that runs mob explosions implements on its Hooks: an
// explosion caused by m at at, which minecraft:explode asked for. It runs in m's Apply,
// before the mob is removed.
type ExplodeHooks interface {
	Explode(m *Mob, at behavior.Vec3, e state.Explosion)
}

// TransformHooks is what an engine that turns mobs into other mobs implements on its Hooks:
// minecraft:transformation came due (TransformationComponent::transformIfAble). The engine
// makes the new entity from c and removes m. It runs in m's Apply.
type TransformHooks interface {
	Transform(m *Mob, c state.TransformationConfig)
}

// creeperTick is the creeper's own tick. Runs: think.
func (m *Mob) creeperTick() {
	if m.typ != creeperType {
		return
	}
	old := m.swell
	m.swellDir = -1
	if m.flags.Has(behavior.FlagIgnited) {
		m.swellDir = 1
	}
	m.swell = min(max(m.swell+m.swellDir, 0), creeperSwell)
	if m.swellDir > 0 && old == 0 {
		m.do(op{kind: opSound, n: soundFuse, at: m.Eyes()})
	}
}

// stateActions are the state components' hands (state.Actions). What they do to anything
// but the mob is queued for Apply. Teleports, trail blocks and spawn_entity are in
// actions.go; hurting others is not built yet.
type stateActions struct{ m *Mob }

// Hurt hurts the mob itself; other targets are not hurt yet.
func (a stateActions) Hurt(target behavior.Actor, _ behavior.ID, cause int, amount float32) {
	if target != nil && target.ID() == a.m.id {
		a.m.do(op{kind: opHurt, n: int32(cause), amount: amount}) //nolint:gosec // causes are small
	}
}

// Explode queues the explosion for Apply. A mob explodes at most once (it despawns).
func (a stateActions) Explode(at behavior.Vec3, e state.Explosion) {
	a.m.blast = e
	a.m.do(op{kind: opExplode, at: at})
}

// TNTExplodes is the tntexplodes game rule.
func (a stateActions) TNTExplodes() bool { return a.m.rt.Env.TNTExplodes() }
