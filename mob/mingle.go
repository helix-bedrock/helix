package mob

import "github.com/helix-bedrock/entity/behavior"

// The hooks of mingle (behavior.Mingler, the villager) and swim_with_entity
// (behavior.SwimCompanion, the dolphin). MingleComponent lives on each villager; another
// mob's is read from its snapshot and changed at Apply, so a change shows a tick later.
// StateVectorComponent's posDelta and posPrev are a mob's velocity and its position before its
// last move, and a player's last movement and the position before it.

// MovingActor is an actor that reports its StateVectorComponent.
type MovingActor interface {
	// PosDelta is the entity's posDelta.
	PosDelta() behavior.Vec3
	// PrevPos is the entity's posPrev.
	PrevPos() behavior.Vec3
}

// Mingle implements behavior.Mingler.
func (m *Mob) Mingle(id behavior.ID) (behavior.MingleState, bool) {
	if id == m.id {
		return m.mingle, m.hasMingle
	}
	if s := m.snapshotOf(id); s != nil {
		return s.mingle, s.hasMingle
	}
	return behavior.MingleState{}, false
}

// SetMingle implements behavior.Mingler: the mob's own at once, another's at Apply.
func (m *Mob) SetMingle(id behavior.ID, s behavior.MingleState) {
	if id == m.id {
		m.mingle, m.hasMingle = s, true
		return
	}
	m.do(op{kind: opSetMingle, id: id, mingle: s})
}

// Motion implements behavior.SwimCompanion.
func (m *Mob) Motion(id behavior.ID) behavior.Vec3 {
	if a, ok := m.actor(id).(MovingActor); ok {
		return a.PosDelta()
	}
	return behavior.Vec3{}
}

// LastMove implements behavior.SwimCompanion.
func (m *Mob) LastMove(id behavior.ID) behavior.Vec3 {
	a := m.actor(id)
	mv, ok := a.(MovingActor)
	if !ok {
		return behavior.Vec3{}
	}
	p, q := mv.PrevPos(), a.Pos()
	return behavior.Vec3{X: p.X - q.X, Y: p.Y - q.Y, Z: p.Z - q.Z}
}

// PosDelta implements MovingActor.
func (s *Snapshot) PosDelta() behavior.Vec3 { return s.vel }

// PrevPos implements MovingActor.
func (s *Snapshot) PrevPos() behavior.Vec3 { return s.prevPos }
