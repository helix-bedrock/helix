package player

import (
	"encoding/json"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
)

// Bottles o' enchanting, from BDS 1.26.60's GrantXpSubcomponent (parse 0x714d030, hit
// 0x714d290): the xp_bottle entity's grant_xp shows a potion splash in the bottle's colour and
// leaves experience orbs worth minXP plus a random number below maxXP - minXP.
const (
	nameXPBottleItem = "minecraft:experience_bottle"
	nameXPBottle     = "minecraft:xp_bottle"
	// xpBottleColour is the splash's colour, BDS's (0.2196, 0.3647, 0.7765, 1) as ARGB.
	xpBottleColour uint32 = 0xff385dc6
)

// grantXPRange is grant_xp's minXP and maxXP.
type grantXPRange struct {
	Min int32 `json:"minXP"`
	Max int32 `json:"maxXP"`
}

// grantXPComponent is grant_xp as BDS reads it: xp as one number, xp holding the range, or
// the range itself.
type grantXPComponent struct {
	grantXPRange
	XP json.RawMessage `json:"xp"`
}

// parseGrantXP reads grant_xp's range.
func parseGrantXP(raw json.RawMessage) (lo, hi int32, err error) {
	var c grantXPComponent
	if err := json.Unmarshal(raw, &c); err != nil {
		return 0, 0, err
	}
	if len(c.XP) == 0 {
		return c.Min, c.Max, nil
	}
	var one int32
	if json.Unmarshal(c.XP, &one) == nil {
		return one, one, nil
	}
	var r grantXPRange
	if err := json.Unmarshal(c.XP, &r); err != nil {
		return 0, 0, err
	}
	return r.Min, r.Max, nil
}

// grantXPAmount is BDS's roll: the least, plus a random number below the difference when
// there is one.
func grantXPAmount(lo, hi int32) int32 {
	if hi > lo {
		return lo + rand.Int32N(hi-lo) //nolint:gosec // gameplay randomness
	}
	return lo
}

// grantXP is the bottle breaking where it hit: the splash, then the orbs.
func (t *Projectile) grantXP(r *world.Region, at mgl32.Vec3) {
	t.splashShow(r, at, xpBottleColour, false)
	t.s.SpawnExperience(r, at, grantXPAmount(t.spec.XPMin, t.spec.XPMax))
}
