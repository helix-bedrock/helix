package player

import (
	"maps"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// Barrels: a 27 slot container with no lid to block, saved as "Barrel" with its Items like a
// chest. Placing faces it by BDS's look_facing rule (helix-data's placement table). While
// anyone has it open it shows open_bit, set with the barrel's open sound at the block's middle
// when the first viewer opens it and back with the close sound when the last one leaves; hoppers
// and comparators see it as any container.
//
// Not checked against BDS 1.26.60's BarrelBlockActor (vtable 0xFBFBF70): the open_bit and the
// sounds follow what the game is known to do; the sound position and whether BDS also sends a
// BlockEvent were not read.

// Barrel numbers and names.
const (
	barrelBlock    = "minecraft:barrel"
	barrelEntityID = "Barrel"
	barrelSize     = 27
	barrelOpenBit  = "open_bit"
)

// barrelOpen sets a barrel's open_bit, with its sound, when it opens or closes.
func (c *Chest) barrelOpen(r *world.Region, open bool) {
	if c.machine == nil || !c.machine.barrel {
		return
	}
	blocks := c.s.reg.Blocks
	b, ok := r.Block(c.pos, 0)
	if !ok || blocks.Name(b) != barrelBlock {
		return
	}
	states := maps.Clone(blocks.States(b))
	states[barrelOpenBit] = boolByte(open)
	id, ok := blocks.StateID(barrelBlock, states)
	if !ok {
		return
	}
	if id != b {
		r.SetBlock(c.pos, 0, id)
	}
	sound := packet.SoundEventBarrelClose
	if open {
		sound = packet.SoundEventBarrelOpen
	}
	playSound(r, c.pos, c.s.blockSound(sound, blockCenter(c.pos), id), nil)
}
