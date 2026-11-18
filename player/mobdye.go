package player

import (
	"slices"
	"strings"
)

// dyeItemSuffix ends every dye's name.
const dyeItemSuffix = "_dye"

// dyeMob is a dye used on a mob with minecraft:is_dyeable (a sheep, a tamed wolf's collar),
// as BDS 1.26.60's BDS's code checks it before the components'
// interactions: the dye's colour is 15 less its dye index, and when the mob's colour (actor
// data 3) differs it takes the dye's and one dye is used (not in creative). True if it did.
func (p *Player) dyeMob(o *Mob) bool {
	if o.gone || o.m.Dead() || !o.m.Dyeable() {
		return false
	}
	c, ok := dyeColor(p.inventory.Held().Name)
	if !ok || c == o.m.Color() {
		return false
	}
	o.m.SetColor(c)
	p.useUpHeld()
	p.flush()
	return true
}

// dyeColor is the colour index (white 0 to black 15) of the dye called name.
func dyeColor(name string) (int32, bool) {
	c, ok := strings.CutPrefix(name, "minecraft:")
	if !ok {
		return 0, false
	}
	if c, ok = strings.CutSuffix(c, dyeItemSuffix); !ok {
		return 0, false
	}
	i := slices.Index(dyeColors[:], c)
	return int32(15 - i), i >= 0 //nolint:gosec // below 16
}
