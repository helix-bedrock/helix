package player

import (
	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/liquid"
	"github.com/helix-bedrock/world/cube"
)

// applyFlow pushes the item along the current of the liquid blocks its box touches, as BDS's
// liquid physics does (liquid.Push): an item looks in its whole box for lava and water.
func (i *ItemActor) applyFlow(r liquid.BlockReader, pos mgl64.Vec3) {
	const half = itemHalfSize
	box := liquid.Area{
		Min: cube.Pos{itemFloor(pos[0] - half), itemFloor(pos[1] - half), itemFloor(pos[2] - half)},
		Max: cube.Pos{itemFloor(pos[0] + half), itemFloor(pos[1] + half), itemFloor(pos[2] + half)},
	}
	i.vel = i.vel.Add(liquid.Push(i.s.reg.Blocks, r, box, box))
}
