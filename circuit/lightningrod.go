package circuit

import (
	"strings"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/redstone"
	"github.com/helix-bedrock/world/cube"
)

// Lightning rods as BDS 1.26.60's LightningRodBlock (vtable 0xFC2B708): the producer made on
// place (slot 56, 0xD5584C0: the direction the rod points away from, powered to the block it
// stands on, 15 while powered_bit is set), a strike (onLightningHit, slot 16 0xD558E70) and the
// tick that ends it (listener 0xD5582D0).
const (
	// rodPowerTicks is how long a struck rod gives power.
	rodPowerTicks  = 8
	stateRodFacing = "facing_direction"
)

// isRodName reports whether name is a lightning rod: plain, any oxidation, waxed or not.
func isRodName(name string) bool { return strings.HasSuffix(name, "lightning_rod") }

// rodComponent makes the rod's producer: attached to the block it stands on, wire may run up
// past it, 15 while powered.
func (t *Ticker) rodComponent(c *regionCircuit, p redstone.Pos, states map[string]any) {
	dir := faceFromIndex(intState(states, stateRodFacing)).Opposite()
	if comp := c.sys.Create(p, redstone.KindProducer, dir); comp != nil {
		comp.SetStrength(onOff(bitState(states, statePowered)))
		comp.SetAttach(true)
		comp.SetAllowUp(true)
	}
}

// StrikeRod is lightning striking the rod at pos (BDS's onLightningHit): it turns on, gives
// full power and turns off 8 ticks later; a second strike starts the wait again. False if the
// block is not a rod. Runs: owner of the region.
func (t *Ticker) StrikeRod(r *world.Region, pos cube.Pos) bool {
	block, ok := r.Block(pos, 0)
	if !ok || t.kind(block) != kindRod {
		return false
	}
	r.CancelTick(pos, block)
	c := t.circuitOf(r)
	on := t.with(block, statePowered, flag(true))
	t.setByCircuit(c, pos, on)
	if comp := c.sys.Component(toPos(pos), redstone.KindProducer); comp != nil {
		comp.SetStrength(fullPower)
	}
	r.ScheduleTick(pos, on, rodPowerTicks, 0)
	return true
}

// rodTick turns a struck rod off.
func (t *Ticker) rodTick(r *world.Region, pos cube.Pos, block uint32) {
	c := t.circuitOf(r)
	t.setByCircuit(c, pos, t.with(block, statePowered, flag(false)))
	if comp := c.sys.Component(toPos(pos), redstone.KindProducer); comp != nil {
		comp.SetStrength(0)
	}
}

// RodSupport is the block a rod at pos with block's state stands on: the one behind where it
// points, which BDS's strike cleans of oxidation.
func RodSupport(states map[string]any, pos cube.Pos) cube.Pos {
	f, _ := states[stateRodFacing].(int32)
	if f < 0 || f > int32(cube.FaceEast) {
		f = int32(cube.FaceUp)
	}
	return pos.Side(cube.Face(f).Opposite())
}
