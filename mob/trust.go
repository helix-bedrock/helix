package mob

import (
	"slices"

	"github.com/helix-bedrock/entity/behavior"
)

// minecraft:trust (BDS 1.26.60 TrustComponent): the players a mob trusts, which
// defend_trusted_target defends and the "trusts" filter test reads. BDS keeps them in an
// unordered_set; Helix keeps them in the order they were added, so goals that walk them see
// that order, not libc++'s bucket order. A born baby trusts the players who fed its parents
// (createBornActor); BDS saves them as TrustedPlayersAmount and TrustedPlayer<n>
// (save, load).

// markerTrust is minecraft:trust.
const markerTrust = "minecraft:trust"

// hasTrust reports minecraft:trust.
func (m *Mob) hasTrust() bool { return m.states != nil && m.states.Markers[markerTrust] }

// Trusted are the IDs the mob trusts; nil without minecraft:trust.
func (m *Mob) Trusted() []behavior.ID {
	if !m.hasTrust() {
		return nil
	}
	if m.trusted == nil {
		var none [0]behavior.ID
		return none[:]
	}
	return m.trusted
}

// Trust adds the IDs to the trusted set, skipping NoID and ones already in it. Runs: the
// mob's region, outside its think.
func (m *Mob) Trust(ids ...behavior.ID) {
	for _, id := range ids {
		if id != behavior.NoID && !slices.Contains(m.trusted, id) {
			m.trusted = append(m.trusted, id)
		}
	}
	m.snap.trusted = m.trusted
}

// SavedTrust is the trusted set for saving; ok is false without minecraft:trust.
func (m *Mob) SavedTrust() ([]behavior.ID, bool) {
	if m.states == nil {
		return m.trusted, m.trusted != nil
	}
	return m.trusted, m.hasTrust()
}

// trusts is ActorTrustsSubjectTest: the mob has minecraft:trust and its
// set holds the subject.
func trusts(self behavior.Actor, subject behavior.ID) bool {
	switch s := self.(type) {
	case *Mob:
		return s.hasTrust() && slices.Contains(s.trusted, subject)
	case *Snapshot:
		return s.hasTrust && slices.Contains(s.trusted, subject)
	}
	return false
}
