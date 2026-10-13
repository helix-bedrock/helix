package path

import (
	"math"

	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/world"
)

// Source is what the pathfinder reads blocks from. *world.Tx is one.
type Source interface {
	Block(cube.Pos) world.Block
	Liquid(cube.Pos) (world.Liquid, bool)
}

// blockSource adapts a Source to world.BlockSource for block models.
type blockSource struct{ Source }

// context caches what one search learns about blocks.
type context struct {
	src   Source
	minY  int
	types map[cube.Pos]Type
}

func newContext(src Source, minY int) *context {
	return &context{src: src, minY: minY, types: make(map[cube.Pos]Type, 256)}
}

// rawType returns the path type of the block at pos alone.
func (c *context) rawType(pos cube.Pos) Type {
	if t, ok := c.types[pos]; ok {
		return t
	}
	t := c.classify(pos)
	c.types[pos] = t
	return t
}

func (c *context) classify(pos cube.Pos) Type {
	b := c.src.Block(pos)
	liquid, hasLiquid := c.src.Liquid(pos)
	_, water := liquid.(block.Water)
	_, lava := liquid.(block.Lava)
	switch v := b.(type) {
	case block.Air:
		if !hasLiquid {
			return Open
		}
	case block.WoodTrapdoor, block.CopperTrapdoor, block.LilyPad:
		return Trapdoor
	case block.Cactus:
		return DamageOther
	case block.CocoaBean:
		return Cocoa
	case block.Dripstone:
		return DamageCautious
	case block.Fire, block.Magma:
		return DamageFire
	case block.Campfire:
		if !v.Extinguished {
			return DamageFire
		}
	case block.WoodDoor:
		if v.Open {
			return DoorOpen
		}
		return DoorWoodClosed
	case block.CopperDoor:
		if v.Open {
			return DoorOpen
		}
		return DoorWoodClosed
	case block.Leaves:
		return Leaves
	case block.WoodFence, block.NetherBrickFence, block.Wall:
		return Fence
	case block.WoodFenceGate:
		if !v.Open {
			return Fence
		}
		if water {
			return Water
		}
		return Open
	}
	if lava {
		return Lava
	}
	if !c.pathfindable(pos, b) {
		return Blocked
	}
	if water {
		return Water
	}
	return Open
}

// pathfindable reports whether a land mob may stand inside the block: it
// has nothing to collide with.
func (c *context) pathfindable(pos cube.Pos, b world.Block) bool {
	return len(b.Model().BBox(pos, blockSource{c.src})) == 0
}

// landType is the path type of pos for a walking mob, which also depends
// on the block below and the blocks around.
func (c *context) landType(pos cube.Pos) Type {
	raw := c.rawType(pos)
	if raw != Open || pos[1] <= c.minY {
		return raw
	}
	switch c.rawType(pos.Side(cube.FaceDown)) {
	case Open, Water, Lava, Walkable:
		return Open
	case DamageFire:
		return DamageFire
	case DamageOther:
		return DamageOther
	case StickyHoney:
		return StickyHoney
	case PowderSnow:
		return DangerPowderSnow
	case DamageCautious:
		return DamageCautious
	case Trapdoor:
		return DangerTrapdoor
	}
	return c.fromNeighbours(pos, Walkable)
}

func (c *context) fromNeighbours(pos cube.Pos, fallback Type) Type {
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				if dx == 0 && dz == 0 {
					continue
				}
				switch c.rawType(pos.Add(cube.Pos{dx, dy, dz})) {
				case DamageOther:
					return DangerOther
				case DamageFire, Lava:
					return DangerFire
				case Water:
					return WaterBorder
				case DamageCautious:
					return DamageCautious
				}
			}
		}
	}
	return fallback
}

// floorLevel is the height a mob standing at pos stands on.
func (c *context) floorLevel(pos cube.Pos) float64 {
	below := pos.Side(cube.FaceDown)
	var top float64
	for _, bb := range c.src.Block(below).Model().BBox(below, blockSource{c.src}) {
		top = math.Max(top, bb.Max()[1])
	}
	return float64(below[1]) + top
}

func (c *context) isWater(pos cube.Pos) bool {
	l, ok := c.src.Liquid(pos)
	if !ok {
		return false
	}
	_, water := l.(block.Water)
	return water
}

// collides reports whether bb overlaps a block.
func (c *context) collides(bb cube.BBox) bool {
	lo, hi := bb.Min(), bb.Max()
	for x := int(math.Floor(lo[0])); x <= int(math.Floor(hi[0])); x++ {
		for y := int(math.Floor(lo[1])); y <= int(math.Floor(hi[1])); y++ {
			for z := int(math.Floor(lo[2])); z <= int(math.Floor(hi[2])); z++ {
				pos := cube.Pos{x, y, z}
				for _, b := range c.src.Block(pos).Model().BBox(pos, blockSource{c.src}) {
					if b.Translate(pos.Vec3()).IntersectsWith(bb) {
						return true
					}
				}
			}
		}
	}
	return false
}

// Solid reports whether the block at pos has anything to collide with.
func Solid(src Source, pos cube.Pos) bool {
	return len(src.Block(pos).Model().BBox(pos, blockSource{src})) > 0
}
