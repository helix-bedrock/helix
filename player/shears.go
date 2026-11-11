package player

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Shears on blocks, from BDS 1.26.60's ShearsItem. Mining speed and wear are mining.go's and
// durability.go's, cutting trip wire circuit's. The item's use on a block (vtable slot 132,
// sub_C8DD9A0) carves pumpkins. Not done: honeycomb from beehives and bee nests, dispensers
// shearing (slot 86, sub_C8DE070), and shearing mobs (their minecraft:interact).

// The names and numbers of carving a pumpkin.
const (
	nameShears        = "minecraft:shears"
	namePumpkin       = "minecraft:pumpkin"
	nameCarvedPumpkin = "minecraft:carved_pumpkin"
	namePumpkinSeeds  = "minecraft:pumpkin_seeds"
	// stateCardinal is the carved pumpkin's facing.
	stateCardinal = "minecraft:cardinal_direction"
)

// carvedFacing is BDS's table from the face clicked to the carved face (0x1A94C48): the
// face carved looks out of the side clicked. The top and bottom have none, and BDS then
// places the carved pumpkin's default state.
var carvedFacing = [...]string{cube.FaceNorth: "north", cube.FaceSouth: "south", cube.FaceWest: "west", cube.FaceEast: "east"}

// useShearsOn is ShearsItem's use on a block: a pumpkin becomes a carved pumpkin facing the
// clicked side and throws out one pumpkin seed; the shears wear by 1 and the carve sound
// plays at the block's centre. False for any other block.
func (p *Player) useShearsOn(r *world.Region, clicked cube.Pos, face int32) bool {
	blocks := p.s.reg.Blocks
	b, ok := r.Block(clicked, 0)
	if !ok || blocks.Name(b) != namePumpkin {
		return false
	}
	carved, ok := blocks.Block(nameCarvedPumpkin)
	if !ok {
		return false
	}
	if f := int(face); f >= 0 && f < len(carvedFacing) && carvedFacing[f] != "" {
		states := map[string]any{}
		for k, v := range blocks.States(carved) {
			states[k] = v
		}
		states[stateCardinal] = carvedFacing[f]
		if id, ok := blocks.StateID(nameCarvedPumpkin, states); ok {
			carved = id
		}
	}
	if r.SetBlock(clicked, 0, carved) {
		r.UpdateNeighbours(clicked)
	}
	if seed, ok := p.s.NewItem(namePumpkinSeeds, 1, 0); ok {
		d := NewBlockDrop(clicked, seed)
		p.s.spawnItem(r.Dimension(), d.Stack, d.Pos, d.Velocity, world.Rotation{}, d.PickupDelay, 0)
	}
	p.hurtHeld(1)
	playSound(r, clicked, soundEvent{name: packet.SoundEventPumpkinCarve, pos: blockCenter(clicked), extra: -1}, nil)
	return true
}
