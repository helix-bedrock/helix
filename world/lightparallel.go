package world

import "github.com/helix-bedrock/world/cube"

// Light settles to the same levels whatever order it spreads in, and one change reaches at
// most about 31 blocks: 15 taking light away, then 15 more spreading what is left back. So
// changes far apart relight on idle cores at once. The changed cells are grouped by
// lightCellShift cells: cells next to each other (corners too) join one cluster, so two
// clusters' changes are at least a cell apart, and what they read and write stays far
// enough apart that no two clusters touch the same byte of light.

// lightCellShift is the size of a cell, 128 blocks.
const lightCellShift = 7

// lightCluster is the changes of one cluster and its own work space.
type lightCluster struct {
	w             *lightWork
	changed, more []cube.Pos
}

// relightAll is relight, split into clusters relit on idle cores when the changes are far
// apart.
func (r *Region) relightAll(sky bool, changed, more []cube.Pos) {
	n := r.groupLight(changed, more)
	if n <= 1 {
		r.relight(r.light, sky, changed, more)
		return
	}
	r.lightSky = sky
	if r.lightFn == nil {
		r.lightFn = r.relightCluster
	}
	r.Parallel(n, r.lightFn)
	for i := range n {
		k := &r.lightClusters[i]
		clear(k.changed)
		clear(k.more)
		k.changed, k.more = k.changed[:0], k.more[:0]
	}
}

// relightCluster relights cluster i with its own work space.
func (r *Region) relightCluster(i int) {
	k := &r.lightClusters[i]
	r.relight(k.w, r.lightSky, k.changed, k.more)
}

func relightCell(p cube.Pos) [2]int32 {
	return [2]int32{int32(p[0] >> lightCellShift), int32(p[2] >> lightCellShift)} //nolint:gosec // block X and Z fit int32
}

// groupLight sorts the changes into clusters and reports how many there are. With one, the
// clusters are not filled.
func (r *Region) groupLight(changed, more []cube.Pos) int {
	if r.lightCells == nil {
		r.lightCells = map[[2]int32]int{}
	}
	clear(r.lightCells)
	r.lightOrder = r.lightOrder[:0]
	for _, list := range [2][]cube.Pos{changed, more} {
		for _, p := range list {
			c := relightCell(p)
			if _, ok := r.lightCells[c]; !ok {
				r.lightCells[c] = -1
				r.lightOrder = append(r.lightOrder, c)
			}
		}
	}
	if len(r.lightOrder) <= 1 {
		return len(r.lightOrder)
	}
	// Cells touching, corners included, make one cluster.
	n := 0
	stack := make([][2]int32, 0, len(r.lightOrder))
	for _, start := range r.lightOrder {
		if r.lightCells[start] >= 0 {
			continue
		}
		r.lightCells[start] = n
		stack = append(stack[:0], start)
		for len(stack) > 0 {
			c := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for dx := int32(-1); dx <= 1; dx++ {
				for dz := int32(-1); dz <= 1; dz++ {
					nb := [2]int32{c[0] + dx, c[1] + dz}
					if id, ok := r.lightCells[nb]; ok && id < 0 {
						r.lightCells[nb] = n
						stack = append(stack, nb)
					}
				}
			}
		}
		n++
	}
	if n <= 1 {
		return n
	}
	for len(r.lightClusters) < n {
		r.lightClusters = append(r.lightClusters, lightCluster{w: &lightWork{}})
	}
	for _, p := range changed {
		k := &r.lightClusters[r.lightCells[relightCell(p)]]
		k.changed = append(k.changed, p)
	}
	for _, p := range more {
		k := &r.lightClusters[r.lightCells[relightCell(p)]]
		k.more = append(k.more, p)
	}
	return n
}
