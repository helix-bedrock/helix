package liquid

import (
	"math"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/world/cube"
)

// The current's push, from BDS 1.26.60's LiquidPhysicsSystem (sub_77DCDE0): items and mobs are
// pushed alike, only the boxes they look in differ.
const (
	// WaterPush and LavaPush are how strongly a current pushes each tick.
	WaterPush = 0.014
	LavaPush  = 0.0035
	// flowMinLength is the shortest flow vector BDS normalises.
	flowMinLength = 0.0001
	// fallingFlowDown is what a falling liquid beside a solid block adds downward, before the
	// vector is normalised.
	fallingFlowDown = 6
	// flowDepths is the depth step a neighbour one lower counts as.
	flowDepths = 8
	// fallingDepth and above is falling liquid.
	fallingDepth = 8
)

// BlockReader reads blocks; *world.Region is one.
type BlockReader interface {
	Block(pos cube.Pos, layer uint8) (uint32, bool)
}

// Area is the blocks from Min to Max, both included.
type Area struct{ Min, Max cube.Pos }

// depthOf returns the depth of a liquid of the given material at pos, as saved, and false if
// there is none. The second layer is read first, as BDS's liquid block lookup does.
func depthOf(blocks *registry.Blocks, r BlockReader, pos cube.Pos, material uint8) (int32, bool) {
	for _, layer := range [...]uint8{1, 0} {
		b, ok := r.Block(pos, layer)
		if !ok || b == blocks.Air() {
			continue
		}
		if blocks.Properties(b).Material == material {
			depth, _ := blocks.States(b)["liquid_depth"].(int32)
			return depth, true
		}
	}
	return 0, false
}

// flatDepth is a liquid's depth with falling counted as 0, as the flow does.
func flatDepth(depth int32) int32 {
	if depth >= fallingDepth {
		return 0
	}
	return depth
}

// solidAt reports whether layer 0 at pos has a solid material. BDS's "blocks motion" material
// flag is not in helix-data yet, so solid stands in for it.
func solidAt(blocks *registry.Blocks, r BlockReader, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && blocks.Properties(b).SolidMaterial
}

// Flow is BDS's flow of the liquid of a material at pos (LiquidBlock's flow, sub_D55CEC0):
// towards shallower neighbours and neighbours with a drop below them, and down along solid
// walls when it falls. It is a unit vector, or zero. Stairs and slabs letting liquid through
// only on some sides are not ported.
func Flow(blocks *registry.Blocks, r BlockReader, pos cube.Pos, material uint8) mgl64.Vec3 {
	own, ok := depthOf(blocks, r, pos, material)
	depth := int32(-1)
	if ok {
		depth = flatDepth(own)
	}
	var f mgl64.Vec3
	// BDS looks at the sides clockwise from north.
	for _, s := range direction.Clockwise() {
		side := pos.Side(s)
		var step int32
		if n, same := depthOf(blocks, r, side, material); same {
			step = flatDepth(n) - depth
		} else if solidAt(blocks, r, side) {
			continue
		} else if n, below := depthOf(blocks, r, cube.Pos{side[0], side[1] - 1, side[2]}, material); below && n < fallingDepth {
			step = n - depth + flowDepths
		} else {
			continue
		}
		to := cube.Pos{}.Side(s)
		f[0] += float64(to[0] * int(step))
		f[2] += float64(to[2] * int(step))
	}
	if ok && own >= fallingDepth {
		for _, s := range direction.Clockwise() {
			side := pos.Side(s)
			if solidAt(blocks, r, side) || solidAt(blocks, r, cube.Pos{side[0], side[1] + 1, side[2]}) {
				f = unitOrZero(f)
				f[1] -= fallingFlowDown
				break
			}
		}
	}
	return unitOrZero(f)
}

func unitOrZero(v mgl64.Vec3) mgl64.Vec3 {
	l := math.Sqrt(v.Dot(v))
	if l < flowMinLength {
		return mgl64.Vec3{}
	}
	return v.Mul(1 / l)
}

// Push is the speed BDS's LiquidPhysicsSystem adds this tick to something whose box covers
// the blocks of lava and water: lava if any lava block is in lava, else water if any water
// block is in water, and only when one of those blocks, or a block beside them, is not a
// source. The push follows the sum of their flows, at LavaPush or WaterPush. Zero for none.
// Items look in their whole box for both; mobs in two shrunk boxes.
func Push(blocks *registry.Blocks, r BlockReader, lava, water Area) mgl64.Vec3 {
	for _, m := range [...]struct {
		material uint8
		strength float32
		area     Area
	}{{registry.MaterialLava, LavaPush, lava}, {registry.MaterialWater, WaterPush, water}} {
		var sum mgl64.Vec3
		found, flowing := false, false
		a := m.area
		for x := a.Min[0]; x <= a.Max[0]; x++ {
			for y := a.Min[1]; y <= a.Max[1]; y++ {
				for z := a.Min[2]; z <= a.Max[2]; z++ {
					at := cube.Pos{x, y, z}
					d, ok := depthOf(blocks, r, at, m.material)
					if !ok {
						continue
					}
					found = true
					flowing = flowing || d > 0 || flowingBeside(blocks, r, at, m.material)
					sum = sum.Add(Flow(blocks, r, at, m.material))
				}
			}
		}
		if !found {
			continue
		}
		if flowing {
			return unitOrZero(sum).Mul(float64(m.strength))
		}
		return mgl64.Vec3{}
	}
	return mgl64.Vec3{}
}

// flowingBeside reports whether a block next to pos holds moving liquid.
func flowingBeside(blocks *registry.Blocks, r BlockReader, pos cube.Pos, material uint8) bool {
	for _, n := range [...]cube.Pos{
		{pos[0], pos[1] - 1, pos[2]}, {pos[0], pos[1] + 1, pos[2]},
		{pos[0] + 1, pos[1], pos[2]}, {pos[0] - 1, pos[1], pos[2]},
		{pos[0], pos[1], pos[2] + 1}, {pos[0], pos[1], pos[2] - 1},
	} {
		if d, ok := depthOf(blocks, r, n, material); ok && d > 0 {
			return true
		}
	}
	return false
}
