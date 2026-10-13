package mob

import (
	"encoding/json"

	"github.com/helix-bedrock/helix/server/entity"
	"github.com/helix-bedrock/helix/server/molang"
	"github.com/helix-bedrock/helix/server/world/sound"
)

// ambientSpec is minecraft:ambient_sound_interval: how often a mob makes
// its idle sound, and which variant it makes.
type ambientSpec struct {
	value, rng float64
	event      string
	variants   []ambientVariant
}

type ambientVariant struct {
	cond  *molang.Expr
	event string
}

func parseAmbient(raw json.RawMessage) *ambientSpec {
	a := &ambientSpec{value: numberField(raw, "value", 8), rng: numberField(raw, "range", 16), event: "ambient"}
	if e := field(raw, "event_name"); e != nil {
		_ = json.Unmarshal(e, &a.event)
	}
	var vs []struct {
		Condition string `json:"condition"`
		Event     string `json:"event_name"`
	}
	_ = json.Unmarshal(field(raw, "event_names"), &vs)
	for _, v := range vs {
		if c, err := molang.Parse(v.Condition); err == nil {
			a.variants = append(a.variants, ambientVariant{cond: c, event: v.Event})
		}
	}
	return a
}

// molangEnv is what Molang in a mob's definition can ask about it.
func (m *Mob) molangEnv() molang.Env {
	return molang.Env{Rand: m.s.rand, Query: func(name string) float64 {
		switch name {
		case "is_baby":
			return b2f(m.Baby())
		case "head_is_in_water", "is_in_water":
			return b2f(m.s.inWater)
		case "is_on_ground":
			return b2f(m.s.onGround)
		case "last_hit_by_player":
			return b2f(m.s.killer != nil && isPlayer(m.s.killer))
		case "equipment_count":
			return b2f(!m.s.held.Empty())
		case "is_using_item", "variant", "player_level":
			return 0
		}
		return 0
	}}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// playSound plays one of the mob's sound events where it is.
func (m *Mob) playSound(event string) {
	if event == "" || event == "undefined" {
		return
	}
	m.tx.PlaySound(m.data.Pos, sound.MobEvent{Entity: m.s.t.def.ID, Event: event, Baby: m.Baby()})
}

// tickAmbient makes the mob's idle sound now and then.
func (m *Mob) tickAmbient() {
	a := m.s.spec.ambient
	if a == nil {
		return
	}
	if m.s.ambientIn > 0 {
		m.s.ambientIn--
		return
	}
	m.s.ambientIn = int((a.value + m.s.rand.Float64()*a.rng) * 20)
	if m.s.ticks < 20 {
		// Not on the first tick: a herd spawning would make its sound at once.
		return
	}
	event := a.event
	env := m.molangEnv()
	for _, v := range a.variants {
		if v.cond.Eval(env) != 0 {
			event = v.event
			break
		}
	}
	m.playSound(event)
}

// dropExperience drops the mob's experience reward as orbs.
func (m *Mob) dropExperience() {
	x := m.s.spec.xp
	if x == nil {
		return
	}
	n := int(x.Eval(m.molangEnv()))
	for _, orb := range entity.NewExperienceOrbs(m.data.Pos.Add([3]float64{0, 0.5, 0}), n) {
		m.tx.AddEntity(orb)
	}
}
