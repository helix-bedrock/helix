package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/state"
)

// The hooks of hold_ground (behavior.Holder, the pillager), send_event (behavior.EventSender,
// the evoker's spells) and knockback_roar (behavior.Roarer, the ravager). BDS 1.26.60 gives
// every Mob a LookControlComponent (Mob::reloadHardcodedClient 0xBFD62E0). Actor::
// playAmbientSound (0xB3AFCC0) plays Actor::getAmbientSound (0xB3AFD60), the level sound event
// named by actor data 110, at attach point 3; a baby whose sound is ambient plays ambient.baby.
// SendEventGoal::start (0xBFA0770) writes the spell colour to actor data 77.

const (
	// dataSpellColor is the actor data key of the spell particle colour.
	dataSpellColor = 77
	// attachAmbient is the attach point Actor::playAmbientSound plays at.
	attachAmbient = 3
	// damageRoar is the roar's damage cause.
	damageRoar = 11
)

// HasLookControl implements behavior.Holder: every mob has a look control.
func (m *Mob) HasLookControl() bool { return true }

// AmbientSound implements behavior.Holder. The other sounds Actor::getAmbientSound picks
// (12, 492) for components Helix's mobs do not have yet are not chosen.
func (m *Mob) AmbientSound() {
	name, _ := m.stringData(state.DataAmbientEvent)
	n, ok := m.rt.sounds[name]
	if !ok {
		return
	}
	if name == "ambient" && m.Baby() {
		if b, ok := m.rt.sounds["ambient.baby"]; ok {
			n = b
		}
	}
	m.PlaySoundAt(n, m.AttachPos(attachAmbient))
}

// stringData is a string actor data value the mob's components wrote.
func (m *Mob) stringData(key int) (string, bool) {
	for _, d := range m.data {
		if d.Key == key {
			s, ok := d.Value.(string)
			return s, ok
		}
	}
	return "", false
}

// SendEvent implements behavior.EventSender: the event fires on the other mob at Apply.
func (m *Mob) SendEvent(to behavior.ID, event string) {
	m.do(op{kind: opMobEvent, id: to, name: event, other: m.id})
}

// SetSpellColor implements behavior.EventSender and behavior.Summoner.
func (m *Mob) SetSpellColor(argb int32) { m.setData(dataSpellColor, argb) }

// RoarHurt implements behavior.Roarer: the hit lands at Apply through the engine's hurt,
// which runs the target's own immunity checks.
func (m *Mob) RoarHurt(a behavior.Actor, damage float32) {
	m.do(op{kind: opHurtBy, id: a.ID(), n: damageRoar, amount: damage})
}

// RoarGameEvent implements behavior.Roarer. Helix's game events come from players only, so
// none is posted.
func (m *Mob) RoarGameEvent(behavior.Vec3) {}
