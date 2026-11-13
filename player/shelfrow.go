package player

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Powered shelf rows, from BDS 1.26.60's shelf use listener (0xD51F060) and its row
// builders (two shelves 0xD5236F0, three 0xD5234B0): clicking a powered shelf swaps its
// whole row with the hotbar. The row is listed end, middle, start, and the list fills the
// hotbar from the right: the last shelf takes slots 6 to 8, the one before it 3 to 5, the
// first 0 to 2, so a lone shelf swaps with slots 6 to 8. Each hotbar slot swaps with the
// shelf slot of the same place, unless both are empty or the hotbar item is locked. The
// circuit package links the rows (powered_shelf_type).
const (
	stateShelfType = "powered_shelf_type"
	// The row places of powered_shelf_type.
	shelfAlone  = 0
	shelfStart  = 1
	shelfMiddle = 2
	shelfEnd    = 3
	// shelfRowSize is the most shelves a row holds, filling the hotbar.
	shelfRowSize = 3
)

// shelfCardinals is BDS's cardinal direction numbering, and shelfSteps the block each one
// points to.
var (
	shelfCardinals = [4]string{"south", "west", "north", "east"}
	shelfSteps     = [4]cube.Pos{{0, 0, 1}, {-1, 0, 0}, {0, 0, -1}, {1, 0, 0}}
)

// shelfDirection is a shelf state's cardinal direction in BDS's numbering.
func shelfDirection(states map[string]any) int {
	for i, c := range shelfCardinals {
		if states[stateShelfFacing] == c {
			return i
		}
	}
	return 0
}

// shelfSide is the block steps along a row from pos: positive toward the next side (the
// cardinal one turn on), negative toward the previous one.
func shelfSide(pos cube.Pos, dir, steps int) cube.Pos {
	turn := 1
	if steps < 0 {
		turn, steps = 3, -steps
	}
	step := shelfSteps[(dir+turn)&3]
	return pos.Add(cube.Pos{step[0] * steps, step[1] * steps, step[2] * steps})
}

// rowPlace is one shelf of a row and where it stands.
type rowPlace struct {
	sh  *Shelf
	pos cube.Pos
}

// rowShelf is the shelf at pos when it is powered, faces dir and has the row place typ.
func (p *Player) rowShelf(r *world.Region, pos cube.Pos, dir int, typ int32) (*rowPlace, bool) {
	block, ok := r.Block(pos, 0)
	if !ok || !isShelfBlock(p.s.reg.Blocks.Name(block)) {
		return nil, false
	}
	states := p.s.reg.Blocks.States(block)
	if powered, _ := states[stateShelfPower].(uint8); powered == 0 || shelfDirection(states) != dir {
		return nil, false
	}
	if typ2, _ := states[stateShelfType].(int32); typ2 != typ {
		return nil, false
	}
	sh, ok := ShelfAt(r, pos)
	if !ok {
		return nil, false
	}
	return &rowPlace{sh: sh, pos: pos}, true
}

// shelfRow is the row of the powered shelf at pos, listed end, middle, start and filled
// from the right; false if its row is broken.
func (p *Player) shelfRow(r *world.Region, pos cube.Pos, states map[string]any) ([shelfRowSize]*rowPlace, bool) {
	var row [shelfRowSize]*rowPlace
	dir := shelfDirection(states)
	typ, _ := states[stateShelfType].(int32)
	sh, ok := ShelfAt(r, pos)
	if !ok {
		return row, false
	}
	self := &rowPlace{sh: sh, pos: pos}
	switch typ {
	case shelfAlone:
		row[2] = self
		return row, true
	case shelfStart:
		mid, okMid := p.rowShelf(r, shelfSide(pos, dir, 1), dir, shelfMiddle)
		end, okEnd := p.rowShelf(r, shelfSide(pos, dir, 2), dir, shelfEnd)
		if okMid && okEnd {
			return [shelfRowSize]*rowPlace{end, mid, self}, true
		}
		if end, ok := p.rowShelf(r, shelfSide(pos, dir, 1), dir, shelfEnd); ok {
			return [shelfRowSize]*rowPlace{nil, end, self}, true
		}
	case shelfMiddle:
		end, okEnd := p.rowShelf(r, shelfSide(pos, dir, 1), dir, shelfEnd)
		start, okStart := p.rowShelf(r, shelfSide(pos, dir, -1), dir, shelfStart)
		if okEnd && okStart {
			return [shelfRowSize]*rowPlace{end, self, start}, true
		}
	case shelfEnd:
		mid, okMid := p.rowShelf(r, shelfSide(pos, dir, -1), dir, shelfMiddle)
		start, okStart := p.rowShelf(r, shelfSide(pos, dir, -2), dir, shelfStart)
		if okMid && okStart {
			return [shelfRowSize]*rowPlace{self, mid, start}, true
		}
		if start, ok := p.rowShelf(r, shelfSide(pos, dir, -1), dir, shelfStart); ok {
			return [shelfRowSize]*rowPlace{nil, self, start}, true
		}
	}
	return row, false
}

// swapShelfRow swaps the powered shelf's row with the hotbar, with BDS's multi_swap sound
// when anything moved. False if the row is broken.
func (p *Player) swapShelfRow(r *world.Region, pos cube.Pos, states map[string]any) bool {
	row, ok := p.shelfRow(r, pos, states)
	if !ok {
		return false
	}
	inv := &p.inventory
	moved := false
	for i := range shelfRowSize * shelfSlots {
		at := row[i/shelfSlots]
		if at == nil {
			continue
		}
		slot := i % shelfSlots
		held, on := inv.main[i], at.sh.items[slot]
		if (held.Empty() && on.Empty()) || itemLocked(held) {
			continue
		}
		at.sh.SetItem(r, at.pos, slot, held)
		inv.main[i] = on
		p.sendSlot(i)
		moved = true
	}
	if moved {
		playSound(r, pos, soundEvent{name: packet.SoundEventMultiItemSwap, pos: blockVec(pos), extra: -1}, nil)
		p.flush()
	}
	return true
}
