package mob

import "github.com/helix-bedrock/entity/behavior"

// The goals that tie a mob to others of its kind: play's chase flag
// (IsChasingDuringPlayFlagComponent), follow_target_captain's leader (BDS's code,
// saved as TargetCaptainID by BDS's code, BDS 1.26.60) and follow_caravan's
// head and tail. The other mob's half of a caravan link is written at
// Apply, as BDS 1.26.60's joinCaravan and leaveCaravan do in place.

// actorEventCaravanUpdated is the actor event the caravan's front gets with its length.
const actorEventCaravanUpdated = 64

// caravanWalkLimit bounds the walks along a caravan, so a looped one cannot hang the tick.
const caravanWalkLimit = 4096

// ActorEventDataHooks is what an engine that sends actor events with data implements on its
// Hooks.
type ActorEventDataHooks interface {
	// ActorEventData sends the actor event with its data about the mob.
	ActorEventData(m *Mob, id, data int32)
}

// ChasingDuringPlay implements behavior.Playmate: the flag on the mob, or on another mob as
// its last snapshot shows it.
func (m *Mob) ChasingDuringPlay(id behavior.ID) bool {
	if id == m.id {
		return m.chasing
	}
	if m.v.r == nil {
		return false
	}
	s, ok := goalWorld{&m.v}.Actor(id).(*Snapshot)
	return ok && s.chasing
}

// SetChasingDuringPlay implements behavior.Playmate.
func (m *Mob) SetChasingDuringPlay(on bool) { m.chasing = on }

// TargetLeader implements behavior.LeaderFollower.
func (m *Mob) TargetLeader() behavior.ID { return m.targetLeader }

// SetTargetLeader implements behavior.LeaderFollower. Runs: the mob's think, or before its
// first tick when loaded.
func (m *Mob) SetTargetLeader(id behavior.ID) { m.targetLeader = id }

// InCaravan implements behavior.CaravanMember: the head still exists (BDS's code).
func (m *Mob) InCaravan() bool {
	return m.caravanHead != behavior.NoID && m.v.r != nil && goalWorld{&m.v}.Actor(m.caravanHead) != nil
}

// CaravanHead implements behavior.CaravanMember.
func (m *Mob) CaravanHead() behavior.ID { return m.caravanHead }

// CaravanTail implements behavior.CaravanMember.
func (m *Mob) CaravanTail() behavior.ID { return m.caravanTail }

// JoinCaravan implements behavior.Caravanner: the mob follows head now; head's tail is set at
// Apply.
func (m *Mob) JoinCaravan(head behavior.ID) {
	m.caravanHead = head
	m.do(op{kind: opJoinCaravan, id: head})
}

// LeaveCaravan implements behavior.Caravanner: the mob follows nobody now; the old head's
// tail is cleared at Apply.
func (m *Mob) LeaveCaravan() {
	old := m.caravanHead
	m.caravanHead = behavior.NoID
	m.do(op{kind: opLeaveCaravan, id: old})
}

// InCaravan implements behavior.CaravanMember for another mob's view.
func (s *Snapshot) InCaravan() bool { return s.inCaravan }

// CaravanHead implements behavior.CaravanMember for another mob's view.
func (s *Snapshot) CaravanHead() behavior.ID { return s.caravanHead }

// CaravanTail implements behavior.CaravanMember for another mob's view.
func (s *Snapshot) CaravanTail() behavior.ID { return s.caravanTail }

// runCaravanOp links or unlinks the head at Apply. A head that another mob joined first this
// tick keeps that one; this mob then follows nobody, as it would have picked another head in
// BDS, where the joins run one after another.
func (m *Mob) runCaravanOp(o *op) {
	r, env := m.v.r, m.rt.Env
	head := env.Mob(r, o.id)
	switch o.kind {
	case opJoinCaravan:
		if head == nil || head.despawned {
			m.caravanHead = behavior.NoID
			return
		}
		if t := head.caravanTail; t != behavior.NoID && t != m.id && env.Mob(r, t) != nil {
			m.caravanHead = behavior.NoID
			return
		}
		head.caravanTail = m.id
		head.snapshot()
	case opLeaveCaravan:
		if head == nil {
			return
		}
		head.caravanTail = behavior.NoID
		head.snapshot()
	}
	m.caravanUpdated(head)
}

// caravanUpdated sends the caravan's front, found from the mob from, the caravan-updated
// actor event with how many mobs follow on from it.
func (m *Mob) caravanUpdated(from *Mob) {
	r, env := m.v.r, m.rt.Env
	front := from
	for i := 0; i < caravanWalkLimit && front.caravanHead != behavior.NoID; i++ {
		next := env.Mob(r, front.caravanHead)
		if next == nil {
			break
		}
		front = next
	}
	if front.despawned {
		return
	}
	n, at := int32(1), front
	for n < caravanWalkLimit && at.caravanTail != behavior.NoID {
		next := env.Mob(r, at.caravanTail)
		if next == nil {
			break
		}
		at = next
		n++
	}
	if h, ok := front.hooks.(ActorEventDataHooks); ok {
		h.ActorEventData(front, actorEventCaravanUpdated, n)
	}
}
