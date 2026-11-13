package player

import (
	"math"
	"strings"

	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/world"
)

// EntityTarget is an entity other than a player that a selector picked (@e). It is only
// handed out on the region that owns the entity, so commands act on it at once.
type EntityTarget struct {
	e *world.Entity
}

// typedOwner is an entity owner that knows its entity type, such as a mob.
type typedOwner interface{ Type() string }

// Entity is the picked entity.
func (t EntityTarget) Entity() *world.Entity { return t.e }

// Name is the entity's name tag, else its type without the namespace.
func (t EntityTarget) Name() string {
	if m, ok := t.e.Owner().(*Mob); ok {
		if tag := m.m.NameTag(); tag != "" {
			return tag
		}
	}
	if o, ok := t.e.Owner().(typedOwner); ok {
		return strings.TrimPrefix(o.Type(), "minecraft:")
	}
	return "entity"
}

// entityType is the identifier the selector's type argument compares with.
func (t EntityTarget) entityType() string {
	if o, ok := t.e.Owner().(typedOwner); ok {
		return o.Type()
	}
	return ""
}

// KillCreative reports whether /kill leaves a player alone as BDS's KillCommand filter
// does: in creative, or on default in a creative world.
func KillCreative(p *Player) bool {
	mode := p.State().GameMode
	return mode == GameModeCreative || mode == GameModeDefault && p.s.level.GameMode == GameModeCreative
}

// Kill is BDS's Actor::kill as /kill calls it: anything living takes a self destruct hurt
// no health survives (no hurt time, absorption or totem stops it), anything else is
// removed. A player is killed on its own region; an EntityTarget must be on the caller's.
func Kill(t command.Target) {
	switch t := t.(type) {
	case *Player:
		t.Run(func(p *Player) { p.TakeDamage(math.MaxFloat32, DamageSource{Cause: DamageSelfDestruct}) })
	case EntityTarget:
		if l, ok := t.e.Owner().(Living); ok {
			l.TakeDamage(math.MaxFloat32, DamageSource{Cause: DamageSelfDestruct})
			return
		}
		t.e.Remove()
	}
}
