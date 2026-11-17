package mob

import "github.com/helix-bedrock/entity/behavior"

// lastHurtByTicks is how long a mob remembers what hurt it: Mob's setLastHurtByMob sets
// mLastHurtByMobTime to 60 (BDS 1.26.60 0xB3A0620).
const lastHurtByTicks = 60

// forgetHurtBy is Actor::baseTick's (0xB39F790) last hurt by step: the record goes once its
// time ran out or the attacker is gone or dead, and the time counts down. Hurt by target
// goals read the record, so a mob stops coming back for an attacker it lost. Runs: think.
func (m *Mob) forgetHurtBy() {
	if m.lastHurtBy != behavior.NoID {
		a := m.actor(m.lastHurtBy)
		if m.lastHurtByTime <= 0 || a == nil || !a.Alive() {
			m.lastHurtBy, m.lastHurtByTime = behavior.NoID, 0
		}
	}
	if m.lastHurtByTime > 0 {
		m.lastHurtByTime--
	}
}
