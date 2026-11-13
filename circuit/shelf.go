package circuit

import (
	"strings"

	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Shelves and redstone, from BDS 1.26.60's shelf power handler (0xD520720): a shelf that
// gains power plays activate (538), one that loses it deactivate (539), and powered shelves
// side by side facing the same way join in rows of up to three, kept in
// powered_shelf_type: 0 alone, 1 the row's start, 2 its middle, 3 its end. A row runs from
// its start toward the next side, the cardinal direction one turn on from the shelf's.
// Not ported: BDS's repair of a row whose shelves changed without power changing
// (0xD521640).
const (
	stateShelfType   = "powered_shelf_type"
	stateShelfFacing = "minecraft:cardinal_direction"

	shelfAlone  = 0
	shelfStart  = 1
	shelfMiddle = 2
	shelfEnd    = 3
)

// shelfCardinals is BDS's cardinal direction numbering, and shelfSteps the block each one
// points to.
var (
	shelfCardinals = [4]string{"south", "west", "north", "east"}
	shelfSteps     = [4]cube.Pos{{0, 0, 1}, {-1, 0, 0}, {0, 0, -1}, {1, 0, 0}}
)

// isShelf reports whether name is one of the shelves.
func isShelf(name string) bool { return strings.HasSuffix(name, "_shelf") }

// shelfDirection is a shelf state's cardinal direction in BDS's numbering.
func shelfDirection(states map[string]any) int {
	for i, c := range shelfCardinals {
		if states[stateShelfFacing] == c {
			return i
		}
	}
	return 0
}

// shelfSide is the block steps along the row from pos: positive toward the next side,
// negative toward the previous one.
func shelfSide(pos cube.Pos, dir, steps int) cube.Pos {
	turn := 1
	if steps < 0 {
		turn, steps = 3, -steps
	}
	step := shelfSteps[(dir+turn)&3]
	return pos.Add(cube.Pos{step[0] * steps, step[1] * steps, step[2] * steps})
}

// shelfJoins is BDS's 0xD523870: the shelf at pos is powered and faces dir.
func (t *Ticker) shelfJoins(c *regionCircuit, pos cube.Pos, dir int) bool {
	block, ok := c.r.Block(pos, 0)
	if !ok || !isShelf(t.blocks.Name(block)) {
		return false
	}
	states := t.blocks.States(block)
	return bitState(states, statePowered) && shelfDirection(states) == dir
}

// shelfIs is BDS's 0xD5232A0: the shelf at pos is powered with the row place typ.
func (t *Ticker) shelfIs(c *regionCircuit, pos cube.Pos, typ int32) bool {
	block, ok := c.r.Block(pos, 0)
	if !ok || !isShelf(t.blocks.Name(block)) {
		return false
	}
	states := t.blocks.States(block)
	return bitState(states, statePowered) && intState(states, stateShelfType) == typ
}

// setShelf is BDS's 0xD523B50: the shelf's power bit and row place.
func (t *Ticker) setShelf(c *regionCircuit, pos cube.Pos, powered bool, typ int32) {
	block, ok := c.r.Block(pos, 0)
	if !ok || !isShelf(t.blocks.Name(block)) {
		return
	}
	t.setByCircuit(c, pos, t.with(t.with(block, statePowered, flag(powered)), stateShelfType, typ))
}

// shelfPower is a shelf's onRedstoneUpdate.
func (t *Ticker) shelfPower(c *regionCircuit, pos cube.Pos, block uint32, on bool) {
	states := t.blocks.States(block)
	powered := bitState(states, statePowered)
	dir := shelfDirection(states)
	switch {
	case on && !powered:
		blockuse.Sound(t.blocks, c.r, pos, packet.SoundEventActivate, block)
		t.linkShelf(c, pos, dir)
	case !on && powered:
		blockuse.Sound(t.blocks, c.r, pos, packet.SoundEventDeactivate, block)
		t.unlinkShelf(c, pos, dir)
	}
}

// linkShelf powers the shelf and joins it to the powered shelves beside it, in BDS's order
// of tries.
func (t *Ticker) linkShelf(c *regionCircuit, pos cube.Pos, dir int) {
	next, next2 := shelfSide(pos, dir, 1), shelfSide(pos, dir, 2)
	prev, prev2 := shelfSide(pos, dir, -1), shelfSide(pos, dir, -2)
	if t.shelfJoins(c, next, dir) && t.shelfJoins(c, next2, dir) && t.shelfIs(c, next, shelfStart) && t.shelfIs(c, next2, shelfEnd) {
		t.setShelf(c, pos, true, shelfStart)
		t.setShelf(c, next, true, shelfMiddle)
		return
	}
	nextAlone := t.shelfJoins(c, next, dir) && t.shelfIs(c, next, shelfAlone)
	prevAlone := t.shelfJoins(c, prev, dir) && t.shelfIs(c, prev, shelfAlone)
	switch {
	case nextAlone && prevAlone:
		t.setShelf(c, prev, true, shelfStart)
		t.setShelf(c, pos, true, shelfMiddle)
		t.setShelf(c, next, true, shelfEnd)
	case nextAlone:
		t.setShelf(c, pos, true, shelfStart)
		t.setShelf(c, next, true, shelfEnd)
	case t.shelfJoins(c, prev, dir) && t.shelfJoins(c, prev2, dir) && t.shelfIs(c, prev, shelfEnd) && t.shelfIs(c, prev2, shelfStart):
		t.setShelf(c, prev, true, shelfMiddle)
		t.setShelf(c, pos, true, shelfEnd)
	case prevAlone:
		t.setShelf(c, prev, true, shelfStart)
		t.setShelf(c, pos, true, shelfEnd)
	default:
		t.setShelf(c, pos, true, shelfAlone)
	}
}

// unlinkShelf takes the shelf out of its row and turns it off: the row's rest on each side
// becomes a shorter row or lone shelves.
func (t *Ticker) unlinkShelf(c *regionCircuit, pos cube.Pos, dir int) {
	next, prev := shelfSide(pos, dir, 1), shelfSide(pos, dir, -1)
	if t.shelfJoins(c, next, dir) {
		switch {
		case t.shelfIs(c, next, shelfEnd):
			t.setShelf(c, next, true, shelfAlone)
		case t.shelfIs(c, next, shelfMiddle):
			t.setShelf(c, next, true, shelfStart)
		}
	}
	if t.shelfJoins(c, prev, dir) {
		switch {
		case t.shelfIs(c, prev, shelfStart):
			t.setShelf(c, prev, true, shelfAlone)
		case t.shelfIs(c, prev, shelfMiddle):
			t.setShelf(c, prev, true, shelfEnd)
		}
	}
	t.setShelf(c, pos, false, shelfAlone)
}
