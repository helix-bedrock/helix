package mob

import "github.com/helix-bedrock/entity/behavior"

// The mob side of behavior.charge_held_item (pillagers and piglins loading crossbows), as BDS
// 1.26.60 runs it: BDS's code, playSoundIncrementally
// and BDS's code. The loaded
// arrow is kept as the mob's charged flag, not in the engine's stack.

// The crossbow loading sounds (level sound events): start, middle and end, each 4 later with
// Quick Charge.
const (
	soundCrossbowLoadingStart  = 245
	soundCrossbowLoadingMiddle = 246
	soundCrossbowLoadingEnd    = 247
	soundCrossbowQuickOffset   = 4
	// crossbowFullLoad is a crossbow's load time without Quick Charge.
	crossbowFullLoad = 25
	// attachWeapon is BDS's weapon attach point (ActorLocation 2), where the sounds play.
	attachWeapon = 2
	// chargeFull is the charge amount of a full charge.
	chargeFull = 127
)

// Chargeable implements behavior.Charger: only the crossbow can be charged.
func (m *Mob) Chargeable(it behavior.Item) bool { return it.Name == itemCrossbow }

// Charged implements behavior.Charger: a crossbow the mob has loaded.
func (m *Mob) Charged(it behavior.Item) bool { return it.Name == itemCrossbow && m.charged }

// LoadCharge implements behavior.Charger: the crossbow is loaded with the ammo.
func (m *Mob) LoadCharge(it, _ behavior.Item) behavior.Item {
	if it.Name == itemCrossbow {
		m.charged = true
	}
	return it
}

// ChargeTick implements behavior.Charger: the loading sounds when the use time left reaches
// 90%, 50% and 10% of the load time, at the weapon point.
func (m *Mob) ChargeTick(it behavior.Item) {
	d := m.UseDuration(it)
	left := m.chargeUseLeft(it, d)
	var sound int
	switch {
	case left == int32(0.9*float32(d)):
		sound = soundCrossbowLoadingStart
	case left == int32(0.5*float64(d)):
		sound = soundCrossbowLoadingMiddle
	case left == int32(float32(d)*0.1):
		sound = soundCrossbowLoadingEnd
	default:
		return
	}
	if d != crossbowFullLoad {
		sound += soundCrossbowQuickOffset
	}
	m.PlaySoundAt(sound, m.AttachPos(attachWeapon))
}

// chargeUseLeft matches BDS: the load time scaled by the charge
// still to go, 0 once the charge pose is up or the crossbow is loaded.
func (m *Mob) chargeUseLeft(it behavior.Item, d int32) int32 {
	if m.Flag(behavior.FlagCharged) || m.Charged(it) || !m.Chargeable(it) {
		return 0
	}
	return d * (chargeFull - int32(m.chargeAmount)) / chargeFull
}

var _ behavior.Charger = (*Mob)(nil)
