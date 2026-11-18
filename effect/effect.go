// Package effect is the list of status effects a player or mob has, as BDS 1.26.60 keeps it
// (Actor's effects vector, MobEffectInstance): the merge of a new effect into one already
// had, the countdown with stored effects, the save format and the visible effects actor
// data. What each effect does is the engine's.
package effect

import "slices"

// Infinite is the duration of an effect that never runs out, as BDS's /effect infinite
// gives.
const Infinite int32 = -1

// Instance is an effect an entity has; Duration is the ticks left (Infinite for an endless
// one).
type Instance struct {
	ID        int32
	Amplifier int32
	Duration  int32
	// Ambient marks an effect from a beacon or conduit (fainter particles); Hidden hides
	// its particles.
	Ambient, Hidden bool
	// age is the ticks an endless effect has had, which its tick is given.
	age int32
	// stored are the same effect at other levels that outlast this one, in the order they
	// were kept, each counting down too; the first comes back when this one runs out
	// (BDS's StoredEffects).
	stored []Instance
}

// List is an entity's effects, in ID order as BDS keeps them in a vector by ID. Runs: the
// owner's region.
type List []Instance

// index is where effect id is or would go.
func (l List) index(id int32) (int, bool) {
	return slices.BinarySearchFunc(l, id, func(o Instance, id int32) int { return int(o.ID - id) })
}

// Get is effect id, false if the entity does not have it.
func (l List) Get(id int32) (*Instance, bool) {
	i, ok := l.index(id)
	if !ok {
		return nil, false
	}
	return &l[i], true
}

// Amplifier is the amplifier of effect id, false if the entity does not have it.
func (l List) Amplifier(id int32) (int32, bool) {
	if e, ok := l.Get(id); ok {
		return e.Amplifier, true
	}
	return 0, false
}

// Clone is a copy of the list that shares nothing with it.
func (l List) Clone() List {
	out := slices.Clone(l)
	for i := range out {
		out[i].stored = slices.Clone(out[i].stored)
	}
	return out
}

// Give adds e, or merges it into the effect already had by BDS's MobEffectInstance update.
// It returns the effect as it now is and whether it was new.
func (l *List) Give(e Instance) (Instance, bool) {
	e.age, e.stored = 0, nil
	i, found := l.index(e.ID)
	if !found {
		*l = slices.Insert(*l, i, e)
		return e, true
	}
	Merge(&(*l)[i], e)
	return (*l)[i], false
}

// Remove takes effect id away, with any weaker one stored behind it. False if it was not
// had.
func (l *List) Remove(id int32) bool {
	i, ok := l.index(id)
	if !ok {
		return false
	}
	*l = slices.Delete(*l, i, i+1)
	return true
}

// Merge is BDS's MobEffectInstance update of old with e:
//   - stronger: old is kept for later if e runs out sooner, e takes over, and kept effects
//     that do not outlast e are dropped (all are kept if e is endless);
//   - weaker: kept for later if it does not run out sooner than old;
//   - same level: the longer duration wins (an endless old stays).
//
// Ambient and the particles come from e unless it is weaker.
func Merge(old *Instance, e Instance) {
	switch {
	case e.Amplifier > old.Amplifier:
		if shorter(e.Duration, old.Duration) {
			old.keep(Instance{ID: old.ID, Amplifier: old.Amplifier, Duration: old.Duration, Ambient: old.Ambient, Hidden: old.Hidden})
		}
		old.Amplifier, old.Duration, old.Ambient, old.Hidden, old.age = e.Amplifier, e.Duration, e.Ambient, e.Hidden, 0
		if e.Duration != Infinite {
			old.stored = slices.DeleteFunc(old.stored, func(k Instance) bool { return !longer(k.Duration, e.Duration) })
		}
	case e.Amplifier < old.Amplifier:
		if !shorter(e.Duration, old.Duration) {
			old.keep(e)
		}
	default:
		if longer(e.Duration, old.Duration) {
			old.Duration = e.Duration
		}
		old.Ambient, old.Hidden = e.Ambient, e.Hidden
	}
}

// keep stores k behind e: a kept effect at the same level takes k's duration if k
// outlasts it, else k is kept after the others.
func (e *Instance) keep(k Instance) {
	k.stored, k.age = nil, 0
	for i := range e.stored {
		if e.stored[i].Amplifier == k.Amplifier {
			if longer(k.Duration, e.stored[i].Duration) {
				e.stored[i].Duration = k.Duration
			}
			return
		}
	}
	e.stored = append(e.stored, k)
}

// longer reports whether duration a outlasts b; an endless one outlasts all but another
// endless one.
func longer(a, b int32) bool {
	if a == b || b == Infinite {
		return false
	}
	return a == Infinite || a > b
}

// shorter reports whether duration a runs out before b.
func shorter(a, b int32) bool { return longer(b, a) }

// Left is the ticks left the effect's tick is given this tick: for an endless effect, minus
// the ticks it has had, which it counts.
func (e *Instance) Left() int32 {
	if e.Duration != Infinite {
		return e.Duration
	}
	left := -e.age
	e.age++
	return left
}

// Outcome is what a countdown did to an effect.
type Outcome int

// Countdown outcomes.
const (
	// Running: the effect goes on as it was.
	Running Outcome = iota
	// Gone: the effect ran out with nothing stored behind it; the caller removes it.
	Gone
	// Restored: a stored effect came back at another level.
	Restored
)

// Countdown counts the effect down one tick, as BDS's MobEffectInstance tick:
// the stored effects count down too and go at 0; one that runs out gives way
// to the first stored effect, if any. Restored is reported only when the level changed, as
// BDS's effect system tells viewers only then.
func (e *Instance) Countdown() Outcome {
	e.stored = slices.DeleteFunc(e.stored, func(k Instance) bool {
		return k.Duration != Infinite && k.Duration <= 1
	})
	for k := range e.stored {
		if e.stored[k].Duration != Infinite {
			e.stored[k].Duration--
		}
	}
	if e.Duration == Infinite {
		return Running
	}
	e.Duration--
	if e.Duration > 0 {
		return Running
	}
	if len(e.stored) == 0 {
		return Gone
	}
	next, amp := e.stored[0], e.Amplifier
	e.Amplifier, e.Duration, e.Ambient, e.Hidden, e.age = next.Amplifier, next.Duration, next.Ambient, next.Hidden, 0
	e.stored = slices.Delete(e.stored, 0, 1)
	if e.Amplifier != amp {
		return Restored
	}
	return Running
}

// visibleMax is how many effects the visible effects actor data holds.
const visibleMax = 8

// Visible is the actor data (131) viewers draw the effects' particles from, as BDS's:
// up to eight effects with particles in ID order, seven bits each (the ID and
// the ambient bit).
func (l List) Visible() int64 {
	var v int64
	n := 0
	for _, e := range l {
		if e.Hidden || n == visibleMax {
			continue
		}
		bit := int64(0)
		if e.Ambient {
			bit = 1
		}
		v = v<<7 | int64(e.ID&0x3F)<<1 | bit
		n++
	}
	return v
}
