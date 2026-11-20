package world

import (
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/mcdb"
)

// Visible sub chunks (subhide.go): a sub chunk under the ground far from the player is still
// sent as it is when the player could see it, across a big cave or through a cave mouth. Each
// network chunk keeps, for every sub chunk, which pairs of its six faces are joined through
// cells that are not opaque full blocks (worked out once, when it is encoded). From the sub chunk
// the player is in, a walk goes from sub chunk to sub chunk through joined faces, as Java Edition
// culls caves but without its rule against turning back, and entering a sub chunk again by each
// other face: what Java misses it does not draw, while
// a client here would be shown a wall of stone, so every sub chunk joined to the player's within
// reach counts as in view.

const (
	// allLinks joins every face with every other: a sub chunk of air.
	allLinks  = 1<<(faceCount*faceCount) - 1
	faceCount = 6
	// visEvery is how many ticks a player staying in one sub chunk waits between walks: chunks
	// arrive and blocks change meanwhile.
	visEvery = 20
	// maxVisNodes bounds one walk: each sub chunk of the grid is entered at most once by each face.
	maxVisNodes = 1 << 17
	// subCells is the number of blocks in a sub chunk.
	subCells = 4096
)

// subKey is a sub chunk: chunk X, sub chunk Y, chunk Z.
type subKey [3]int32

// faceStep is the sub chunk next to another across each face, in cube.Face order.
var faceStep = [faceCount]subKey{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}

// subLinks returns which pairs of faces of sub are joined through cells that are not opaque full
// blocks: bit a*6+b for faces a and b.
func (t *hideTable) subLinks(sub *chunk.SubChunk) uint64 {
	layers := sub.Layers()
	if sub.Empty() || len(layers) == 0 {
		return allLinks
	}
	p := layers[0].Palette()
	open, solid := false, false
	for i := range p.Len() {
		if t.occluding(p.Value(uint16(i))) { //nolint:gosec // palettes are small
			solid = true
		} else {
			open = true
		}
	}
	switch {
	case !solid:
		return allLinks
	case !open:
		return 0
	}
	var cells subFill
	for i := range subCells {
		cells.open[i] = !t.occluding(sub.Block(byte(i>>8), byte(i&15), byte(i>>4&15), 0)) //nolint:gosec // i < 4096
	}
	return cells.links()
}

// subFill is a flood fill of one sub chunk's open cells, cell x<<8 | z<<4 | y.
type subFill struct {
	open, seen [subCells]bool
	queue      [subCells]uint16
}

// links fills each group of joined open cells in turn and joins the faces each touches.
func (f *subFill) links() uint64 {
	var links uint64
	for start := range subCells {
		if !f.open[start] || f.seen[start] {
			continue
		}
		faces := f.fill(uint16(start)) //nolint:gosec // < 4096
		for a := range faceCount {
			for b := range faceCount {
				if faces&(1<<a) != 0 && faces&(1<<b) != 0 {
					links |= 1 << (a*faceCount + b)
				}
			}
		}
	}
	return links
}

// fill visits the open cells joined to start and returns the faces they touch.
func (f *subFill) fill(start uint16) uint8 {
	var faces uint8
	n := 0
	f.queue[n], f.seen[start] = start, true
	n++
	for head := 0; head < n; head++ {
		c := f.queue[head]
		x, z, y := c>>8, c>>4&15, c&15
		faces |= cellFaces(x, y, z)
		for _, next := range [...]struct {
			ok   bool
			cell uint16
		}{{y > 0, c - 1}, {y < 15, c + 1}, {z > 0, c - 16}, {z < 15, c + 16}, {x > 0, c - 256}, {x < 15, c + 256}} {
			if next.ok && f.open[next.cell] && !f.seen[next.cell] {
				f.seen[next.cell] = true
				f.queue[n] = next.cell
				n++
			}
		}
	}
	return faces
}

// cellFaces returns the faces of the sub chunk a cell lies on, in cube.Face order.
func cellFaces(x, y, z uint16) uint8 {
	var faces uint8
	for i, on := range [faceCount]bool{y == 0, y == 15, z == 0, z == 15, x == 0, x == 15} {
		if on {
			faces |= 1 << i
		}
	}
	return faces
}

// visNode is a sub chunk the walk reached: from is the face it came in by (-1 for the first),
// dirs the directions the walk took to get there.
type visNode struct {
	at   subKey
	from int8
	dirs uint8
}

// walkVisible lists in v.visible the sub chunks the player at eye may see, when it moved to another
// sub chunk, its client got chunks, or visEvery ticks passed. Runs: the viewer's region, under
// netMu.
func (v *Viewer) walkVisible(r *Region, eye mgl32.Vec3) {
	b := blockAt(eye)
	start := subKey{int32(b[0] >> 4), int32(b[1] >> 4), int32(b[2] >> 4)} //nolint:gosec // block coordinates fit
	now := r.CurrentTick()
	if start == v.visAt && v.visNets == len(v.nets) && now-v.visTick < visEvery {
		return
	}
	v.visAt, v.visNets, v.visTick = start, len(v.nets), now
	v.visReach = min(v.lastRadius, maxVisChunks)
	v.visBottom = int32(r.d.r[0] >> 4)                  //nolint:gosec // world heights fit
	v.visHeight = int32((r.d.r[1] - r.d.r[0] + 1) >> 4) //nolint:gosec // world heights fit
	side := int(2*v.visReach + 1)
	v.visGrid = slices.Grow(v.visGrid[:0], side*side*int(v.visHeight))[:side*side*int(v.visHeight)]
	clear(v.visGrid)
	i := v.visIndex(start)
	if i < 0 {
		return
	}
	v.visGrid[i] = visStart
	queue := append(v.visQueue[:0], visNode{at: start, from: -1})
	for head := 0; head < len(queue) && len(queue) < maxVisNodes; head++ {
		n := queue[head]
		links, ok := v.linksOf(r, n.at)
		if !ok {
			continue
		}
		for d := range int8(faceCount) {
			if n.from >= 0 && links&(1<<(int(n.from)*faceCount+int(d))) == 0 {
				continue
			}
			s := faceStep[d]
			next := subKey{n.at[0] + s[0], n.at[1] + s[1], n.at[2] + s[2]}
			j := v.visIndex(next)
			in := uint8(1) << (d ^ 1)
			// A sub chunk is walked on from each face it is entered by: the open cells behind one
			// face may not reach those behind another, so the first way in is not the only one.
			if j < 0 || v.visGrid[j]&(in|visStart) != 0 {
				continue
			}
			if _, has := v.nets[mcdb.ChunkPos{next[0], next[2]}]; !has {
				continue
			}
			v.visGrid[j] |= in
			queue = append(queue, visNode{at: next, from: d ^ 1, dirs: n.dirs | 1<<d})
		}
	}
	v.visQueue = queue[:0]
}

// visIndex is the place of sub chunk k in the walk's grid around visAt, -1 outside it.
func (v *Viewer) visIndex(k subKey) int {
	side := 2*v.visReach + 1
	dx, dz, dy := k[0]-v.visAt[0]+v.visReach, k[2]-v.visAt[2]+v.visReach, k[1]-v.visBottom
	if dx < 0 || dx >= side || dz < 0 || dz >= side || dy < 0 || dy >= v.visHeight {
		return -1
	}
	return int((dx*side+dz)*v.visHeight + dy)
}

// visStart marks the walk's first sub chunk in visGrid, left by every face; bits 0 to 5 are the
// faces a sub chunk was entered by.
const visStart = 1 << faceCount

// maxVisChunks bounds how far, in chunks, the walk goes from the player.
const maxVisChunks = 8

// linksOf returns the face links of sub chunk at, from the latest network form the region has
// of its chunk, else the one the client has.
func (v *Viewer) linksOf(r *Region, at subKey) (uint64, bool) {
	pos := mcdb.ChunkPos{at[0], at[2]}
	nc := v.nets[pos]
	if c, ok := r.chunks[pos]; ok && c.net != nil {
		nc = c.net
	}
	if nc == nil {
		return 0, false
	}
	i := at[1] - nc.bottom
	if i < 0 || int(i) >= len(nc.links) {
		return 0, false
	}
	return nc.links[i], true
}

// visibleSub reports whether the walk reached sub chunk y of the chunk at pos. Under netMu.
func (v *Viewer) visibleSub(pos mcdb.ChunkPos, y int32) bool {
	if i := v.visIndex(subKey{pos[0], y, pos[1]}); i >= 0 && i < len(v.visGrid) {
		return v.visGrid[i] != 0
	}
	return false
}
