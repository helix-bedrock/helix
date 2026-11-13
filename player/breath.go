package player

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/world/cube"
)

// Breathing and drowning. A player runs the entity library's minecraft:breathable component,
// the same one mobs run, as BDS's players and mobs share BreathableComponent (1.26.60 tick
// sub_79139D0): its numbers come from the player definition (15 seconds of air, hurt from
// -1 second, 3.75 seconds to breathe back in, no bubbles).

// The damage causes of running out of air.
const (
	DamageSuffocation DamageCause = 4
	DamageDrowning    DamageCause = 9
)

// airTag is where BDS saves an actor's air supply.
const airTag = "Air"

// breathState is the player's air: the component and what clients were last told.
type breathState struct {
	b *state.Breathable
	// air and maxAir are the air data sent; flag is the breathing flag shown.
	air, maxAir int16
	flag        bool
	// loaded is the saved air, applied when the component is made.
	loaded    int16
	hasLoaded bool
}

// Breathable is the player's minecraft:breathable component: its air supply (Air, SetAir)
// and its settings (Config, SetConfig, kept until the player leaves). Runs: the player's
// region.
func (p *Player) Breathable() *state.Breathable {
	if p.breath.b == nil {
		p.breath.b = state.NewBreathable(p.s.game.breathable, nil)
		p.breath.air, p.breath.maxAir, p.breath.flag = p.breath.b.Air(), p.breath.b.MaxAir(), true
		if p.breath.hasLoaded {
			p.breath.b.SetAir(p.breath.loaded)
		}
	}
	return p.breath.b
}

// SetBreathable replaces the player definition's minecraft:breathable for every player.
func (s *Setup) SetBreathable(c state.BreathableConfig) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.breathable = c
	return nil
}

// vanillaBreathable reads the player definition's minecraft:breathable over BDS's defaults.
func vanillaBreathable() (state.BreathableConfig, error) {
	c := state.DefaultBreathable()
	defs, err := data.Entities()
	if err != nil {
		return c, err
	}
	var def struct {
		Entity struct {
			Components struct {
				Breathable *struct {
					TotalSupply      *int32   `json:"total_supply"`
					SuffocateTime    *int32   `json:"suffocate_time"`
					InhaleTime       *float32 `json:"inhale_time"`
					BreathesAir      *bool    `json:"breathes_air"`
					BreathesWater    *bool    `json:"breathes_water"`
					BreathesLava     *bool    `json:"breathes_lava"`
					BreathesSolids   *bool    `json:"breathes_solids"`
					GeneratesBubbles *bool    `json:"generates_bubbles"`
				} `json:"minecraft:breathable"`
			} `json:"components"`
		} `json:"minecraft:entity"`
	}
	if err := json.Unmarshal(defs["minecraft:player"], &def); err != nil {
		return c, fmt.Errorf("player: breathable: %w", err)
	}
	b := def.Entity.Components.Breathable
	if b == nil {
		return c, nil
	}
	setIf(&c.TotalSupply, b.TotalSupply)
	setIf(&c.SuffocateTime, b.SuffocateTime)
	setIf(&c.InhaleTime, b.InhaleTime)
	setIf(&c.BreathesAir, b.BreathesAir)
	setIf(&c.BreathesWater, b.BreathesWater)
	setIf(&c.BreathesLava, b.BreathesLava)
	setIf(&c.BreathesSolids, b.BreathesSolids)
	setIf(&c.GeneratesBubbles, b.GeneratesBubbles)
	return c, nil
}

// setIf sets *dst to *v when the definition has v.
func setIf[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// tickBreath is the player's breathable tick. Creative and spectator players always breathe.
// Runs: the player's region, once a tick.
func (p *Player) tickBreath() {
	if p.entity == nil || p.dead() {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	p.Breathable().TickFor(breather{p: p, r: r}, breathWorld{p: p, r: r}, p.invulnerable())
}

// loadAir takes the saved air supply.
func (p *Player) loadAir(saved map[string]any) {
	if v, ok := saved[airTag].(int16); ok {
		p.breath.loaded, p.breath.hasLoaded = v, true
	}
}

// saveAir writes the air supply where BDS keeps it.
func (p *Player) saveAir(saved map[string]any) {
	if p.breath.b != nil {
		saved[airTag] = p.breath.b.Air()
	} else if p.breath.hasLoaded {
		saved[airTag] = p.breath.loaded
	}
}

// breathing reports the breathing actor flag the player shows.
func (p *Player) breathing() bool { return p.breath.b == nil || p.breath.flag }

// breather is the player as the breathable component sees it.
type breather struct {
	p *Player
	r *world.Region
}

func (b breather) Alive() bool { return !b.p.dead() }

// Breath is BDS's medium (sub_7370980) at the player's breathing point: the definition's
// block lists, then water or lava in either layer, then a solid block whose box holds the
// point. BDS reads the block's own shape for the last; Helix reads its collision boxes.
func (b breather) Breath(breathe, nonBreathe []string) state.Medium {
	s := b.p.s
	at := b.p.AttachPos(AttachBreathing)
	pos := cube.Pos{int(math.Floor(float64(at[0]))), int(math.Floor(float64(at[1]))), int(math.Floor(float64(at[2])))}
	block, ok := b.r.Block(pos, 0)
	if !ok {
		return state.MediumAir
	}
	name := s.reg.Blocks.Name(block)
	switch {
	case descriptorsHave(breathe, name):
		return state.MediumBreathable
	case descriptorsHave(nonBreathe, name):
		return state.MediumNotBreathable
	}
	if liq, ok := s.liquidIn(b.r, pos); ok {
		switch s.reg.Blocks.Properties(liq).Material {
		case registry.MaterialWater:
			return state.MediumWater
		case registry.MaterialLava:
			return state.MediumLava
		}
	}
	if !s.reg.Blocks.Properties(block).SolidMaterial {
		return state.MediumAir
	}
	in := at.Sub(mgl32.Vec3{float32(pos[0]), float32(pos[1]), float32(pos[2])})
	for _, box := range s.boxesAt(b.r, pos, block) {
		if in[0] >= box[0] && in[0] <= box[3] && in[1] >= box[1] && in[1] <= box[4] && in[2] >= box[2] && in[2] <= box[5] {
			return state.MediumSolid
		}
	}
	return state.MediumAir
}

// descriptorsHave reports name among block descriptors, which may leave out minecraft:.
func descriptorsHave(list []string, name string) bool {
	for _, d := range list {
		if !strings.Contains(d, ":") {
			d = "minecraft:" + d
		}
		if d == name {
			return true
		}
	}
	return false
}

func (b breather) HasEffect(id int) bool {
	_, ok := b.p.Effect(int32(id)) //nolint:gosec // effect IDs are small
	return ok
}

// UnderWater is Actor::isHeadInLiquid with water (sub_B39C240): water at the breathing point
// whose surface is above it.
func (b breather) UnderWater() bool {
	s := b.p.s
	at := b.p.AttachPos(AttachBreathing)
	pos := cube.Pos{int(math.Floor(float64(at[0]))), int(math.Floor(float64(at[1]))), int(math.Floor(float64(at[2])))}
	liq, ok := s.liquidIn(b.r, pos)
	if !ok || s.reg.Blocks.Properties(liq).Material != registry.MaterialWater {
		return false
	}
	return s.liquidHeightOf(liq, pos) > at[1]
}

// SetFlag shows the breathing flag; the component sets no other.
func (b breather) SetFlag(n int, on bool) {
	if n != protocol.EntityDataFlagBreathing || b.p.breath.flag == on {
		return
	}
	b.p.breath.flag = on
	b.p.sendActorFlags()
	b.p.send(actorDataAt(b.p.runtimeID, protocol.EntityMetadata{protocol.EntityDataKeyFlags: b.p.actorFlags()}, b.p.entity.Tick()))
}

func (b breather) AttachPos(loc int) behavior.Vec3 {
	v := b.p.AttachPos(AttachPoint(loc)) //nolint:gosec // BDS's actor locations
	return behavior.Vec3{X: v[0], Y: v[1], Z: v[2]}
}

// Respiration is the highest Respiration level the player wears.
func (b breather) Respiration() int32 {
	best := int32(0)
	for _, it := range b.p.Armour().Items() {
		for _, e := range it.Enchants() {
			if e.ID == enchantRespiration {
				best = max(best, int32(e.Level))
			}
		}
	}
	return best
}

func (b breather) Hurt(cause int, amount float32) {
	b.p.s.game.sys.Hurt(b.p, amount, DamageSource{Cause: DamageCause(cause)}) //nolint:gosec // a cause number
}

// SetAirSupply sends the air data to the player and those who see it when it changes.
func (b breather) SetAirSupply(air, maxAir int16) {
	p := b.p
	if p.breath.air == air && p.breath.maxAir == maxAir {
		return
	}
	p.breath.air, p.breath.maxAir = air, maxAir
	pk := actorDataAt(p.runtimeID, protocol.EntityMetadata{protocol.EntityDataKeyAirSupply: air, protocol.EntityDataKeyAirSupplyMax: maxAir}, p.entity.Tick())
	p.entity.Send(pk)
	p.send(pk)
}

// breathWorld is the world as the breathable component sees it from a player.
type breathWorld struct {
	p *Player
	r *world.Region
}

func (w breathWorld) Random() behavior.Random { return breathRandom{} }

// breathRandom is the random the breathable tick rolls Respiration with.
type breathRandom struct{}

// NextInt returns 0 to n-1, 0 for n of 0 or less.
func (breathRandom) NextInt(n int32) int32 {
	if n <= 0 {
		return 0
	}
	return rand.Int32N(n) //nolint:gosec // gameplay randomness
}

// NextFloat returns 0 up to 1.
func (breathRandom) NextFloat() float32 { return rand.Float32() } //nolint:gosec // gameplay randomness

// NextBool returns true or false.
func (breathRandom) NextBool() bool { return rand.IntN(2) == 0 } //nolint:gosec // gameplay randomness

// NextGaussian returns a normally spread number.
func (breathRandom) NextGaussian() float32 { return float32(rand.NormFloat64()) } //nolint:gosec // gameplay randomness

func (w breathWorld) LevelEvent(id int32, at behavior.Vec3, _ string) {
	levelEvent(w.r, w.p.blockPos(), mgl32.Vec3{at.X, at.Y, at.Z}, id, bubbleData)
}

// bubbleData is the data of BDS's bubble level event from the breathable tick.
const bubbleData = 8

// liquidIn returns the liquid at pos: the block itself, else its second layer.
func (s *Host) liquidIn(r *world.Region, pos cube.Pos) (uint32, bool) {
	for layer := uint8(0); layer < 2; layer++ {
		b, ok := r.Block(pos, layer)
		if !ok {
			return 0, false
		}
		switch s.reg.Blocks.Properties(b).Material {
		case registry.MaterialWater, registry.MaterialLava:
			return b, true
		}
	}
	return 0, false
}

// liquidHeightOf is BlockUtils::getLiquidBlockHeight (0xB656EF0) of liquid block liq at pos.
func (s *Host) liquidHeightOf(liq uint32, pos cube.Pos) float32 {
	depth, _ := s.reg.Blocks.States(liq)["liquid_depth"].(int32)
	if depth >= liquidFalling {
		depth = 0
	}
	return float32(pos[1]) + 1 - float32(depth)/liquidLevels
}
