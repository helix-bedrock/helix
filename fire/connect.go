package fire

import (
	"maps"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Fire's connection states, which the client reads to draw flames on the sides of the
// blocks around fire that floats beside them (on a log, under leaves). BDS 1.26.60 gives
// fire the minecraft:connection trait with its own callback (registered at 0x5C20CE0, mask
// 0x926: east, west, south, north and up; the callback 0xCAFFDF0): each state is 1 when the
// block that way has flame odds above 0. The trait updates the states when fire is placed and
// when a neighbour changes; the change is the same block type, so fire's onPlace does not run
// again.

// connectionFaces are the faces with a connection state, by state name.
var connectionFaces = [...]struct {
	state string
	face  cube.Face
}{
	{"minecraft:connection_east", cube.FaceEast},
	{"minecraft:connection_west", cube.FaceWest},
	{"minecraft:connection_south", cube.FaceSouth},
	{"minecraft:connection_north", cube.FaceNorth},
	{"minecraft:connection_up", cube.FaceUp},
}

// connected is the fire state block with its connections set from the blocks around pos.
func (t *Ticker) connected(r *world.Region, pos cube.Pos, block uint32) uint32 {
	states := t.blocks.States(block)
	with := maps.Clone(states)
	changed := false
	for _, c := range connectionFaces {
		if _, has := states[c.state]; !has {
			continue
		}
		on := false
		if b, ok := r.Block(pos.Side(c.face), 0); ok && t.blocks.Properties(b).FlameOdds > 0 {
			on = true
		}
		// The states are bits or ints depending on how the registry read them.
		switch cur := states[c.state].(type) {
		case uint8:
			if v := b2u8(on); cur != v {
				with[c.state], changed = v, true
			}
		case int32:
			if v := int32(b2u8(on)); cur != v {
				with[c.state], changed = v, true
			}
		}
	}
	if !changed {
		return block
	}
	id, ok := t.blocks.StateID(t.blocks.Name(block), with)
	if !ok {
		return block
	}
	return id
}

// connect sets the fire at pos to its connected state, as a state change that does not run
// fire's onPlace.
func (t *Ticker) connect(r *world.Region, pos cube.Pos, block uint32) {
	want := t.connected(r, pos, block)
	if want == block {
		return
	}
	l := t.local(r)
	l.aging = true
	r.SetBlock(pos, 0, want)
	l.aging = false
}

func b2u8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
