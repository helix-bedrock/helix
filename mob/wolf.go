package mob

// A wolf shaking off water, as BDS 1.26.60's Wolf. Its normal tick (0xBD83D90) marks it wet
// while it is in water or rain, and a shake that is running stops there (ActorEvent 79). Its
// newServerAiStep (Wolf::newServerAiStep 0xBD83A50) starts a shake once the wet wolf is out
// of the water and rain, on the ground and done with its path: ActorEvent 8, which the
// client animates (Wolf::handleEntityEvent 0xBD84E80). The shake timer then goes up 0.05 a
// tick; its first tick sends the entity_act game event at the wolf's body, and once it was
// 2 the wolf is dry again.
const (
	wolfType            = "minecraft:wolf"
	eventShakeWetness   = 8
	eventShakeStop      = 79
	gameEventEntityAct  = "entity_act"
	wolfShakeStep       = 0.05
	wolfShakeEnd        = 2
	wolfBodyHeightShare = 0.5
)

// wolfShake is the wolf's wet and shake state.
type wolfShake struct {
	wet, shaking bool
	// anim is the shake timer (Wolf's mShakeAnim).
	anim float32
}

// wolfTick runs the wolf's shake: its normal tick part, then its newServerAiStep part.
// Runs: think.
func (m *Mob) wolfTick() {
	if m.typ != wolfType || m.Dead() {
		return
	}
	s := &m.wolf
	wetNow := m.Wet()
	if wetNow {
		s.wet = true
		if s.shaking || s.anim > 0 {
			s.shaking, s.anim = false, 0
			m.do(op{kind: opActorEvent, n: eventShakeStop})
		}
	}
	if s.shaking || s.anim > 0 {
		if s.anim == 0 {
			at := m.body.Pos
			at.Y += m.body.Height * wolfBodyHeightShare
			m.postGameEvent(gameEventEntityAct, at)
		}
		was := s.anim
		s.anim += wolfShakeStep
		if was >= wolfShakeEnd {
			*s = wolfShake{}
		}
	}
	m.wolfStartShake(wetNow)
}

// wolfStartShake is newServerAiStep's start of a shake.
func (m *Mob) wolfStartShake(wetNow bool) {
	s := &m.wolf
	if !s.wet || s.shaking || s.anim != 0 || wetNow || !m.body.OnGround {
		return
	}
	if m.nv != nil && !m.nv.Done() {
		return
	}
	s.shaking, s.anim = true, 0
	m.do(op{kind: opActorEvent, n: eventShakeWetness})
}
