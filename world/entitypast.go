package world

import "github.com/go-gl/mathgl/mgl32"

// PastMoves is how many earlier positions an entity keeps, one per world tick it moved in:
// enough for a second of movement, which covers what a player with a slow connection saw of it.
const PastMoves = 20

// pastMoves are an entity's earlier positions, each with the last world tick it held it. Owned
// by the entity's region; flat, so the collector never scans it.
type pastMoves struct {
	pos   [PastMoves]mgl32.Vec3
	until [PastMoves]int64
	next  int
	count int
	// lastTick is the world tick the last position was kept in, so one tick of many moves
	// keeps only where it started.
	lastTick int64
}

// keep records pos, held until world tick now.
func (p *pastMoves) keep(pos mgl32.Vec3, now int64) {
	if p.count > 0 && p.lastTick == now {
		return
	}
	p.pos[p.next], p.until[p.next] = pos, now
	p.next = (p.next + 1) % PastMoves
	p.count = min(p.count+1, PastMoves)
	p.lastTick = now
}

// AppendPast appends to dst every position the entity held from world tick since on,
// its current one last, and returns it. Runs: owner.
func (e *Entity) AppendPast(dst []mgl32.Vec3, since int64) []mgl32.Vec3 {
	p := &e.past
	for i := range p.count {
		at := (p.next - p.count + i + PastMoves) % PastMoves
		if p.until[at] >= since {
			dst = append(dst, p.pos[at])
		}
	}
	return append(dst, e.pos)
}
