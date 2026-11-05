package player

import (
	"github.com/helix-bedrock/helix/world"
)

// voidDepth is how far below a dimension's lowest block the void starts (BDS 1.26.60's
// OutOfWorldSystem: below the BlockSource's min height minus 16). It follows the dimension's
// range, so a world built deeper moves the void down with it.
const voidDepth = 16

// voidDamage is what the void deals a player each tick it is in it (BDS's Mob::outOfWorld).
const voidDamage = 4

// OutOfWorldFunc reports whether the entity is in the void. Runs: owner of the entity.
type OutOfWorldFunc func(s *Host, e *world.Entity) bool

// VoidFunc is what the void does to an entity in it, once a tick. Runs: owner of the entity.
type VoidFunc func(s *Host, e *world.Entity)

// OutOfWorlder is an entity owner with its own answer to the void (BDS's Actor::outOfWorld,
// which each kind of actor overrides). An owner without one is removed.
type OutOfWorlder interface {
	OutOfWorld(s *Host, e *world.Entity)
}

// VoidEvent fires each tick an entity is in the void, before the void does anything to it.
// Cancel it and nothing happens this tick. Damage is what a player is dealt (TuneVoidDamage);
// it means nothing for entities the void removes. Runs: owner of the entity.
type VoidEvent struct {
	Cancellable
	Entity *world.Entity
	// Player is the entity's player, nil for anything else.
	Player *Player
	Damage float32
}

// OnVoid listens to entities in the void.
func OnVoid(s *Setup, fn func(e *VoidEvent), p Priority) error {
	return addListener(s, &s.h.game.voidEv, fn, p)
}

// vanillaOutOfWorld is BDS's check: the entity's position (a player's eyes, as BDS keeps it)
// is below its dimension's lowest block by more than voidDepth.
func vanillaOutOfWorld(_ *Host, e *world.Entity) bool {
	r := e.Region()
	if r == nil {
		return false
	}
	return e.Position()[1] < float32(r.Dimension().Range()[0]-voidDepth)
}

// vanillaVoid fires the event, then hands the entity to its owner's OutOfWorld; anything
// else is removed, as BDS's Actor::outOfWorld does.
func vanillaVoid(s *Host, e *world.Entity) {
	p, _ := e.Owner().(*Player)
	if s.game != nil && s.game.voidEv.Has() {
		ev := VoidEvent{Entity: e, Player: p}
		if p != nil {
			ev.Damage = p.Tuning(TuneVoidDamage)
		}
		s.game.voidEv.fire(s.log, &ev)
		if ev.Cancelled() {
			return
		}
		if p != nil {
			p.voidHurt(ev.Damage)
			return
		}
	}
	if o, ok := e.Owner().(OutOfWorlder); ok {
		o.OutOfWorld(s, e)
		return
	}
	e.Remove()
}

// checkVoid runs the void systems on one entity, once a tick, as BDS's OutOfWorldSystem runs
// on ticked actors. Entities call it at the start of their tick and a player at the end of
// its input; where BDS's system falls in the tick against the actors' own was not worked out.
// Runs: owner of the entity.
func (s *Host) checkVoid(e *world.Entity) {
	// A Host built without vanilla's Setup, or a plugin that cleared a hook, gets BDS's.
	var out OutOfWorldFunc
	var void VoidFunc
	if s.game != nil {
		out, void = s.game.sys.OutOfWorld, s.game.sys.Void
	}
	if out == nil {
		out = vanillaOutOfWorld
	}
	if void == nil {
		void = vanillaVoid
	}
	if e.Region() != nil && out(s, e) {
		void(s, e)
	}
}

// OutOfWorld hurts the player with the void each tick (BDS's Mob::outOfWorld): fall distance
// cleared, then TuneVoidDamage. Creative and spectator players are hurt too, as in BDS.
func (p *Player) OutOfWorld(*Host, *world.Entity) { p.voidHurt(p.Tuning(TuneVoidDamage)) }

func (p *Player) voidHurt(damage float32) {
	p.move.fall = 0
	hurt := vanillaHurt
	if p.s.game != nil && p.s.game.sys.Hurt != nil {
		hurt = p.s.game.sys.Hurt
	}
	hurt(p, damage, DamageSource{Cause: DamageVoid})
}

// OutOfWorld removes the item.
func (i *ItemActor) OutOfWorld(_ *Host, e *world.Entity) {
	i.taken = true
	e.Remove()
}

// OutOfWorld removes the orb.
func (o *XPOrb) OutOfWorld(_ *Host, e *world.Entity) { o.remove(e) }

// OutOfWorld removes the TNT unexploded.
func (t *PrimedTNT) OutOfWorld(_ *Host, e *world.Entity) {
	t.gone = true
	e.Remove()
}

// OutOfWorld removes the cart, letting its riders go; its items go with it.
func (m *Minecart) OutOfWorld(_ *Host, e *world.Entity) {
	if r := e.Region(); r != nil {
		m.remove(r)
	}
}

// OutOfWorld removes the projectile. A trident with Loyalty returns in BDS; Helix has no
// Loyalty yet.
func (t *Projectile) OutOfWorld(_ *Host, e *world.Entity) { t.remove(e) }

// OutOfWorld removes the cushion with no drop, letting its riders go.
func (c *Cushion) OutOfWorld(_ *Host, e *world.Entity) {
	if r := e.Region(); r != nil {
		c.Break(r, false)
	}
}
