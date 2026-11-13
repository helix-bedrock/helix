package circuit

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/redstone"
	"github.com/helix-bedrock/world/cube"
)

// Lecterns as BDS 1.26.60's LecternBlock (vtable 0xFB7F208): the producer made on place (slot
// 56, 0xCADD8E0: direction down, attached, 15 while powered_bit is set) and the pulse a page turn
// gives (emitRedstonePulse queues a tick; the tick listener 0xCADC3C0 turns it on on a redstone
// tick and off on the next).
const nameLectern = "minecraft:lectern"

// lecternComponent makes the lectern's producer.
func (t *Ticker) lecternComponent(c *regionCircuit, p redstone.Pos, states map[string]any) {
	if comp := c.sys.Create(p, redstone.KindProducer, redstone.Down); comp != nil {
		comp.SetStrength(onOff(bitState(states, statePowered)))
		comp.SetAttach(true)
	}
}

// PulseLectern is a page turned on the lectern at pos: a short pulse of full power, from the
// next redstone tick. False if the block is not a lectern. Runs: owner of the region.
func (t *Ticker) PulseLectern(r *world.Region, pos cube.Pos) bool {
	block, ok := r.Block(pos, 0)
	if !ok || t.kind(block) != kindLectern {
		return false
	}
	r.ScheduleTick(pos, block, 1, 0)
	return true
}

// lecternTick waits for a redstone tick, then flips the lectern: turning on gives full power
// and asks for the tick that turns it off.
func (t *Ticker) lecternTick(c *regionCircuit, pos cube.Pos, block uint32) {
	if c.sys.SignalTick() {
		c.r.ScheduleTick(pos, block, 1, 0)
		return
	}
	on := !bitState(t.blocks.States(block), statePowered)
	if comp := c.sys.Component(toPos(pos), redstone.KindProducer); comp != nil {
		comp.SetStrength(onOff(on))
	}
	next := t.with(block, statePowered, flag(on))
	t.setByCircuit(c, pos, next)
	if on {
		c.r.ScheduleTick(pos, next, 1, 0)
	}
}
