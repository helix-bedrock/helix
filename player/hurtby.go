package player

import "github.com/helix-bedrock/entity/behavior"

// What a player last hurt and what last hurt it, as BDS 1.26.60 keeps them on every Mob:
// a tamed wolf's owner_hurt_by_target goal reads who hurt its owner, and owner_hurt_target
// what its owner hit. A mob that hurts the player is kept lastHurtByTicks (setLastHurtByMob
// 0xB3A0620 sets 60) and forgotten when that runs out or it is gone or dead
// (Actor::baseTick 0xB39F790). Each hit the player lands on a mob moves the stamp on, so a
// pet starts on it once per hit.
type hurtRecord struct {
	by     behavior.ID
	byTime int32
	mob    behavior.ID
	stamp  int32
}

// lastHurtByTicks is how long the player remembers the mob that hurt it.
const lastHurtByTicks = 60

// hurtByMob records the mob that hurt the player. Runs: the player's region.
func (p *Player) hurtByMob(id behavior.ID) {
	p.hurts.by, p.hurts.byTime = id, lastHurtByTicks
}

// hurtMob records a mob the player hurt. Runs: the player's region.
func (p *Player) hurtMob(id behavior.ID) {
	p.hurts.mob = id
	p.hurts.stamp++
}

// tickHurtBy forgets the mob that hurt the player once its time ran out or it is gone or
// dead, then counts the time down. Runs: the player's region tick.
func (p *Player) tickHurtBy() {
	h := &p.hurts
	if h.by != behavior.NoID {
		if h.byTime <= 0 || !p.hurtByAlive() {
			h.by, h.byTime = behavior.NoID, 0
		}
	}
	if h.byTime > 0 {
		h.byTime--
	}
}

// hurtByAlive reports that the mob that hurt the player is still in its region and alive.
func (p *Player) hurtByAlive() bool {
	r := p.entity.Region()
	if r == nil {
		return false
	}
	o, ok := p.s.entityByID(r, p.hurts.by).(*Mob)
	return ok && !o.gone && !o.m.Dead()
}
