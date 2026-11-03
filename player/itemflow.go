package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/world/cube"
)

// How strongly a current pushes an item each tick, from BDS 1.26.60.
const (
	waterFlowStrength = 0.014
	lavaFlowStrength  = 0.0035
	// flowMinLength is the shortest flow vector BDS normalises.
	flowMinLength = 0.0001
	// fallingFlowDown is what a falling liquid beside a solid block adds
	// downward, before the vector is normalised.
	fallingFlowDown = 6
	// flowDepths is the depth step a neighbour one lower counts as.
	flowDepths = 8
)

// blockReader reads blocks; *world.Region is one.
type blockReader interface {
	Block(pos cube.Pos, layer uint8) (uint32, bool)
}

// liquidDepth returns the depth of a liquid of the given Material at pos, as
// saved, and false if there is none. The second layer is read first, as BDS's
// liquid block lookup does.
func liquidDepth(reg *registry.Registry, r blockReader, pos cube.Pos, material uint8) (int32, bool) {
	for _, layer := range [...]uint8{1, 0} {
		b, ok := r.Block(pos, layer)
		if !ok || b == reg.Blocks.Air() {
			continue
		}
		if reg.Blocks.Properties(b).Material == material {
			depth, _ := reg.Blocks.States(b)["liquid_depth"].(int32)
			return depth, true
		}
	}
	return 0, false
}

// flatDepth is a liquid's depth with falling counted as 0, as the flow does.
func flatDepth(depth int32) int32 {
	if depth >= liquidFalling {
		return 0
	}
	return depth
}

// solidAt reports whether layer 0 at pos has a solid material. BDS's "blocks
// motion" material flag is not in helix-data yet, so solid stands in for it.
func solidAt(reg *registry.Registry, r blockReader, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && reg.Blocks.Properties(b).SolidMaterial
}

// liquidFlow is BDS's flow of the liquid at pos: towards shallower neighbours
// and neighbours with a drop below them, and down along solid walls when it
// falls. It is a unit vector, or zero. Stairs and slabs letting liquid through
// only on some sides are not ported.
func liquidFlow(reg *registry.Registry, r blockReader, pos cube.Pos, material uint8) mgl32.Vec3 {
	own, ok := liquidDepth(reg, r, pos, material)
	depth := int32(-1)
	if ok {
		depth = flatDepth(own)
	}
	var f mgl32.Vec3
	// BDS looks at the sides clockwise from north.
	for _, s := range direction.Clockwise() {
		side := pos.Side(s)
		var step int32
		if n, same := liquidDepth(reg, r, side, material); same {
			step = flatDepth(n) - depth
		} else if solidAt(reg, r, side) {
			continue
		} else if n, below := liquidDepth(reg, r, cube.Pos{side[0], side[1] - 1, side[2]}, material); below && n < liquidFalling {
			step = n - depth + flowDepths
		} else {
			continue
		}
		to := cube.Pos{}.Side(s)
		f[0] += float32(to[0] * int(step))
		f[2] += float32(to[2] * int(step))
	}
	if ok && own >= liquidFalling {
		for _, s := range direction.Clockwise() {
			side := pos.Side(s)
			if solidAt(reg, r, side) || solidAt(reg, r, cube.Pos{side[0], side[1] + 1, side[2]}) {
				f = unitOrZero(f)
				f[1] -= fallingFlowDown
				break
			}
		}
	}
	return unitOrZero(f)
}

func unitOrZero(v mgl32.Vec3) mgl32.Vec3 {
	l := float32(math.Sqrt(float64(v.Dot(v))))
	if l < flowMinLength {
		return mgl32.Vec3{}
	}
	return v.Mul(1 / l)
}

// applyFlow pushes the item along the current of the liquid blocks its box
// touches, as BDS's liquid physics does: lava if it touches any, else water,
// and only if one of those blocks, or a block beside them, is not a source.
func (i *ItemActor) applyFlow(r blockReader, pos mgl32.Vec3) {
	const half = itemHalfSize
	lo := cube.Pos{itemFloor(pos[0] - half), itemFloor(pos[1] - half), itemFloor(pos[2] - half)}
	hi := cube.Pos{itemFloor(pos[0] + half), itemFloor(pos[1] + half), itemFloor(pos[2] + half)}
	reg := i.s.reg
	for _, m := range [...]struct {
		material uint8
		strength float32
	}{{registry.MaterialLava, lavaFlowStrength}, {registry.MaterialWater, waterFlowStrength}} {
		var sum mgl32.Vec3
		found, flowing := false, false
		for x := lo[0]; x <= hi[0]; x++ {
			for y := lo[1]; y <= hi[1]; y++ {
				for z := lo[2]; z <= hi[2]; z++ {
					at := cube.Pos{x, y, z}
					d, ok := liquidDepth(reg, r, at, m.material)
					if !ok {
						continue
					}
					found = true
					flowing = flowing || d > 0 || i.flowingBeside(r, at, m.material)
					sum = sum.Add(liquidFlow(reg, r, at, m.material))
				}
			}
		}
		if !found {
			continue
		}
		if flowing {
			i.vel = i.vel.Add(unitOrZero(sum).Mul(m.strength))
		}
		return
	}
}

// flowingBeside reports whether a block next to pos holds moving liquid.
func (i *ItemActor) flowingBeside(r blockReader, pos cube.Pos, material uint8) bool {
	for _, n := range [...]cube.Pos{
		{pos[0], pos[1] - 1, pos[2]}, {pos[0], pos[1] + 1, pos[2]},
		{pos[0] + 1, pos[1], pos[2]}, {pos[0] - 1, pos[1], pos[2]},
		{pos[0], pos[1], pos[2] + 1}, {pos[0], pos[1], pos[2] - 1},
	} {
		if d, ok := liquidDepth(i.s.reg, r, n, material); ok && d > 0 {
			return true
		}
	}
	return false
}
