package mob

import (
	"math"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/state"
)

// The home component's restriction types BDS tells apart; any other value is "none".
const (
	homeRandomMovement = "random_movement"
	homeAllMovement    = "all_movement"
	// responseSetHome is the event response that sets the mob's home.
	responseSetHome = "set_home_position"
)

// homeState is the mob's minecraft:home, nil without one.
func (m *Mob) homeState() *state.Home {
	if m.states == nil {
		return nil
	}
	return m.states.Home
}

// setHome matches BDS's set_home_position: a mob with minecraft:home gets the block its
// position is in, in its dimension, as its home. Others are left alone.
func (m *Mob) setHome() {
	h := m.homeState()
	if h == nil {
		return
	}
	p := m.body.Pos
	h.SetHome(behavior.Pos{
		X: int32(math.Floor(float64(p.X))), Y: int32(math.Floor(float64(p.Y))), Z: int32(math.Floor(float64(p.Z))),
	}, m.Dimension())
}

// Home implements behavior.Mob: the home when it is in the mob's dimension, with the
// restriction radius, or 0 when the restriction type is none, as BDS's goals read it.
func (m *Mob) Home() (behavior.Pos, int32, bool) {
	h := m.homeState()
	if h == nil {
		return behavior.Pos{}, 0, false
	}
	p, dim, ok := h.Get()
	if !ok || dim != m.Dimension() {
		return behavior.Pos{}, 0, false
	}
	var r int32
	switch t := h.Config().RestrictionType; t {
	case homeRandomMovement, homeAllMovement:
		r = h.Config().RestrictionRadius
	}
	return p, r, true
}
