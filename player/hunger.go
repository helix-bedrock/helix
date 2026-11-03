package player

import (
	"encoding/json"
	"fmt"

	"github.com/helix-bedrock/helix-data/data"
)

// ExhaustionValues are how much exhaustion each action costs, as the
// player definition's minecraft:exhaustion_values gives them.
type ExhaustionValues struct {
	Heal       float32 `json:"heal"`
	Jump       float32 `json:"jump"`
	SprintJump float32 `json:"sprint_jump"`
	Mine       float32 `json:"mine"`
	Attack     float32 `json:"attack"`
	Damage     float32 `json:"damage"`
	Walk       float32 `json:"walk"`
	Sprint     float32 `json:"sprint"`
	Swim       float32 `json:"swim"`
	Lunge      float32 `json:"lunge"`
}

// exhaustionStep is the exhaustion that burns one point of saturation or
// hunger.
const exhaustionStep = 4

// Food is a player's hunger, saturation and exhaustion.
type Food struct {
	Hunger, Saturation, Exhaustion float32
}

// Food returns the player's hunger, saturation and exhaustion. Runs: the
// player's region.
func (p *Player) Food() Food {
	a := &p.attributes
	return Food{Hunger: a.get(attrHunger), Saturation: a.get(attrSaturation), Exhaustion: a.get(attrExhaustion)}
}

// SetFood sets the player's hunger, saturation and exhaustion, each
// clamped to 0 to 20, and tells the player. Runs: the player's region.
func (p *Player) SetFood(f Food) {
	p.setAttributes(attrHunger, f.Hunger, attrSaturation, f.Saturation, attrExhaustion, f.Exhaustion)
}

// vanillaExhaust is BDS's causeFoodExhaustion and its exhaustion and
// hunger attribute delegates: the amount is added (up to the attribute's
// maximum); at 4 or more, 4 is taken off and one point burns, from
// saturation while there is any, else from hunger unless the difficulty is
// peaceful. Nothing burns in creative or spectator.
func vanillaExhaust(p *Player, amount float32) {
	if amount <= 0 || p.creative() {
		return
	}
	f := p.Food()
	f.Exhaustion = min(f.Exhaustion+amount, playerAttributes[attrExhaustion].max)
	if f.Exhaustion >= exhaustionStep {
		f.Exhaustion -= exhaustionStep
		switch {
		case p.invulnerable():
		case f.Saturation > 0:
			f.Saturation = max(f.Saturation-1, 0)
		case p.s.level.Difficulty != difficultyPeaceful:
			f.Hunger = max(f.Hunger-1, 0)
		}
	}
	p.SetFood(f)
}

// level.dat's difficulties.
const (
	difficultyPeaceful = 0
	difficultyNormal   = 2
	difficultyHard     = 3
)

// Numbers of BDS 1.26.60's hunger.
const (
	// foodTimerEnd is the tick at which regaining health or starving acts.
	foodTimerEnd = 80
	// regenHunger is the hunger from which health comes back.
	regenHunger = 18
	// starveDamage is what starving deals; starveEasyFloor and starveNormalFloor
	// are the health it stops at on easy and normal.
	starveDamage      = 1
	starveEasyFloor   = 10
	starveNormalFloor = 2
	// peacefulHungerEvery and peacefulHealthEvery are how often peaceful gives them
	// back.
	peacefulHungerEvery = 10
	peacefulHealthEvery = 20
)

// foodState is BDS's hunger timers.
type foodState struct {
	timer, count int32
}

// vanillaFoodTick is BDS's hunger attribute tick and the health attribute's peaceful
// regeneration: peaceful gives hunger and health back, a nearly full player regains
// health at the cost of exhaustion, and an empty one starves to the difficulty's limit.
// Nothing in creative or spectator.
func vanillaFoodTick(p *Player) {
	if p.invulnerable() {
		return
	}
	lv, st := &p.s.level, &p.food
	st.count++
	health, hunger := p.Health(), p.attributes.get(attrHunger)
	maxHealth := playerAttributes[attrHealth].max
	peaceful := lv.Difficulty == difficultyPeaceful
	if peaceful && st.count%peacefulHungerEvery == 0 {
		p.setAttributes(attrHunger, hunger+1)
	}
	if peaceful && lv.NaturalRegeneration && st.count%peacefulHealthEvery == 0 {
		p.SetHealth(health + 1)
	}
	sys := p.s.game.sys
	switch {
	case hunger >= regenHunger && lv.NaturalRegeneration && health > 0 && health < maxHealth:
		st.timer++
		if st.timer >= foodTimerEnd {
			st.timer = 0
			p.SetHealth(p.Health() + 1)
			sys.Exhaust(p, sys.Exhaustion.Heal)
		}
	case hunger <= 0:
		st.timer++
		if st.timer >= foodTimerEnd {
			st.timer = 0
			if lv.Difficulty == difficultyHard || health > starveEasyFloor || lv.Difficulty == difficultyNormal && health >= starveNormalFloor {
				sys.Hurt(p, starveDamage, DamageSource{Cause: DamageStarve})
			}
		}
	default:
		st.timer = 0
	}
}

// vanillaExhaustionValues reads the player definition's exhaustion values.
func vanillaExhaustionValues() (ExhaustionValues, error) {
	var v ExhaustionValues
	defs, err := data.Entities()
	if err != nil {
		return v, err
	}
	var def struct {
		Entity struct {
			Components struct {
				Values *ExhaustionValues `json:"minecraft:exhaustion_values"`
			} `json:"components"`
		} `json:"minecraft:entity"`
	}
	if err := json.Unmarshal(defs["minecraft:player"], &def); err != nil {
		return v, fmt.Errorf("player: exhaustion values: %w", err)
	}
	if def.Entity.Components.Values == nil {
		return v, fmt.Errorf("player: exhaustion values: none in the player definition")
	}
	return *def.Entity.Components.Values, nil
}
