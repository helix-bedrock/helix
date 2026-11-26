package mob

import "github.com/helix-bedrock/entity/behavior"

// Sneaking, sprinting and the movement attribute's modifiers (ocelotattack's Prowler).
// BDS 1.26.60's BDS's code sets actor flag 1; BDS's code
// sets flag 3 and adds or removes the "Sprinting speed boost" modifier
// (UUID D208FC00-42AA-4AAD-9276-D5446530DE43, 0.3, multiply total, on the current value) on
// the movement attribute. The attribute's value is AttributeInstance's recalculation:
// additions, then multiply base on the sum, then each multiply total.

// flagSneaking matches BDS's actor flag.
const flagSneaking = 1

// The attribute modifier operations BDS's recalculation knows.
const (
	modAdd = iota
	modMultiplyBase
	modMultiplyTotal
)

// sprintModifier matches BDS's speed boost.
var sprintModifier = attrModifier{id: "D208FC00-42AA-4AAD-9276-D5446530DE43", op: modMultiplyTotal, amount: 0.3}

// attrModifier is an AttributeModifier on the current value of the movement attribute.
type attrModifier struct {
	id     string
	op     uint8
	amount float32
}

// SetSneaking implements behavior.Prowler.
func (m *Mob) SetSneaking(on bool) { m.SetFlag(flagSneaking, on) }

// Sneaking reports the sneaking actor flag.
func (m *Mob) Sneaking() bool { return m.flags.Has(flagSneaking) }

// SetSprinting implements behavior.Prowler.
func (m *Mob) SetSprinting(on bool) {
	m.SetFlag(behavior.FlagSprinting, on)
	if on {
		m.addSpeedModifier(sprintModifier)
	} else {
		m.removeSpeedModifier(sprintModifier.id)
	}
}

// addSpeedModifier adds a modifier the movement attribute does not have yet.
func (m *Mob) addSpeedModifier(mod attrModifier) {
	for _, have := range m.speedMods {
		if have.id == mod.id {
			return
		}
	}
	m.speedMods = append(m.speedMods, mod)
	m.applySpeed()
}

// removeSpeedModifier takes the modifier with the ID off the movement attribute.
func (m *Mob) removeSpeedModifier(id string) {
	for i, have := range m.speedMods {
		if have.id == id {
			m.speedMods = append(m.speedMods[:i], m.speedMods[i+1:]...)
			m.applySpeed()
			return
		}
	}
}

// applySpeed sets the movement value from its base and modifiers in BDS's order.
func (m *Mob) applySpeed() {
	sum := m.speedBase
	for _, mod := range m.speedMods {
		if mod.op == modAdd {
			sum += mod.amount
		}
	}
	v := sum
	for _, mod := range m.speedMods {
		if mod.op == modMultiplyBase {
			v += sum * mod.amount
		}
	}
	for _, mod := range m.speedMods {
		if mod.op == modMultiplyTotal {
			v *= 1 + mod.amount
		}
	}
	m.speed = max(v, 0)
}
