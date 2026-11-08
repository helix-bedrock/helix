// Package portal is BDS 1.26.60's portals as blocks: the nether portal's frame rules,
// lighting and breaking, the portal records BDS's PortalForcer keeps, finding or
// building the portal a traveller arrives at, the End's arrival platform and the end
// portal frame. Moving players between dimensions is the player package's.
//
// Everything here runs on the owner of the region it is given.
package portal

import (
	"fmt"

	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Block names.
const (
	nameObsidian   = "minecraft:obsidian"
	nameNetherrack = "minecraft:netherrack"
	namePortal     = "minecraft:portal"
	nameFire       = "minecraft:fire"
	nameSoulFire   = "minecraft:soul_fire"
	nameEndPortal  = "minecraft:end_portal"
	nameFrame      = "minecraft:end_portal_frame"

	stateAxis      = "portal_axis"
	stateEye       = "end_portal_eye_bit"
	stateDirection = "minecraft:cardinal_direction"
)

// BDS's PortalShape limits (sub_CDAFD10).
const (
	minWidth, maxWidth   = 2, 21
	minHeight, maxHeight = 3, 21
	// maxDrop is how far down from the lit block the shape looks for its floor.
	maxDrop = 21
	// maxEdge is how far along the floor it looks for a side.
	maxEdge = 22
)

// Axis is the axis a nether portal lies along.
type Axis uint8

// The axes, as BDS numbers them (1 X, 2 Z).
const (
	AxisX Axis = 1
	AxisZ Axis = 2
)

// Blocks are the blocks portals are built of, resolved from the registry.
type Blocks struct {
	reg                                         *registry.Blocks
	air, obsidian, netherrack, endPortal        uint32
	portalX, portalZ                            uint32
	portalType, fireType, soulFireType, frameTy uint32
	obsidianType, endPortalType                 uint32
}

// NewBlocks resolves the portal blocks.
func NewBlocks(reg *registry.Blocks) (*Blocks, error) {
	b := &Blocks{reg: reg, air: reg.Air()}
	for name, dst := range map[string]*uint32{nameObsidian: &b.obsidian, nameNetherrack: &b.netherrack, nameEndPortal: &b.endPortal} {
		id, ok := reg.Block(name)
		if !ok {
			return nil, fmt.Errorf("portal: %s is not registered", name)
		}
		*dst = id
	}
	var ok bool
	if b.portalX, ok = reg.StateID(namePortal, map[string]any{stateAxis: "x"}); !ok {
		return nil, fmt.Errorf("portal: %s has no x axis", namePortal)
	}
	if b.portalZ, ok = reg.StateID(namePortal, map[string]any{stateAxis: "z"}); !ok {
		return nil, fmt.Errorf("portal: %s has no z axis", namePortal)
	}
	b.portalType = b.typeOf(b.portalX)
	b.obsidianType, b.endPortalType = b.typeOf(b.obsidian), b.typeOf(b.endPortal)
	for name, dst := range map[string]*uint32{nameFire: &b.fireType, nameSoulFire: &b.soulFireType, nameFrame: &b.frameTy} {
		id, ok := reg.Block(name)
		if !ok {
			return nil, fmt.Errorf("portal: %s is not registered", name)
		}
		*dst = b.typeOf(id)
	}
	return b, nil
}

func (b *Blocks) typeOf(id uint32) uint32 {
	t, _ := b.reg.Type(id)
	return t
}

// IsPortal reports whether block is a nether portal block.
func (b *Blocks) IsPortal(block uint32) bool { return b.typeOf(block) == b.portalType }

// IsFire reports whether block is fire (not soul fire), whose onPlace lights portals.
func (b *Blocks) IsFire(block uint32) bool { return b.typeOf(block) == b.fireType }

// IsEndPortal reports whether block is an end portal block.
func (b *Blocks) IsEndPortal(block uint32) bool { return b.typeOf(block) == b.endPortalType }

// portalFor is the portal block along axis.
func (b *Blocks) portalFor(a Axis) uint32 {
	if a == AxisZ {
		return b.portalZ
	}
	return b.portalX
}

// axisOf is a portal block's axis: X unless it says z.
func (b *Blocks) axisOf(block uint32) Axis {
	if v, _ := b.reg.States(block)[stateAxis].(string); v == "z" {
		return AxisZ
	}
	return AxisX
}

// empty is BDS's PortalShape empty test: air, fire or portal.
func (b *Blocks) empty(block uint32) bool {
	if block == b.air {
		return true
	}
	t := b.typeOf(block)
	return t == b.fireType || t == b.soulFireType || t == b.portalType
}

func (b *Blocks) isObsidian(block uint32) bool { return b.typeOf(block) == b.obsidianType }

// shape is a nether portal frame BDS's PortalShape found.
type shape struct {
	axis          Axis
	right         cube.Pos
	bottomLeft    cube.Pos
	width, height int
	portals       int
}

// valid is BDS's PortalShape isValid: a frame was found and fits the limits.
func (s shape) valid() bool {
	return s.width >= minWidth && s.width <= maxWidth && s.height >= minHeight && s.height <= maxHeight
}

// full reports whether every block inside the frame is a portal.
func (s shape) full() bool { return s.portals == s.width*s.height }

// axisStep is the step along the frame's width and the step back to its other side.
func axisStep(a Axis) (right, left cube.Pos) {
	if a == AxisX {
		return cube.Pos{-1, 0, 0}, cube.Pos{1, 0, 0}
	}
	return cube.Pos{0, 0, 1}, cube.Pos{0, 0, -1}
}

// findShape is BDS's PortalShape (sub_CDAFD10) from pos along axis a: down to the
// floor, out to the left side, then the width, the height and the top row.
func (b *Blocks) findShape(r *world.Region, pos cube.Pos, a Axis) shape {
	s := shape{axis: a}
	right, left := axisStep(a)
	s.right = right
	at := func(p cube.Pos) uint32 {
		blk, _ := r.Block(p, 0)
		return blk
	}
	bottom := r.Dimension().Range()[0]
	for i := 0; i < maxDrop && pos[1] > bottom && b.empty(at(pos.Side(cube.FaceDown))); i++ {
		pos = pos.Side(cube.FaceDown)
	}
	d := b.edge(at, pos, left)
	if d == 0 {
		return s
	}
	s.bottomLeft = pos.Add(cube.Pos{left[0] * (d - 1), 0, left[2] * (d - 1)})
	s.width = b.edge(at, s.bottomLeft, right)
	if s.width < minWidth || s.width > maxWidth {
		s.width = 0
		return s
	}
rows:
	for s.height = 0; s.height < maxHeight; s.height++ {
		for i := range s.width {
			p := s.bottomLeft.Add(cube.Pos{right[0] * i, s.height, right[2] * i})
			blk := at(p)
			if !b.empty(blk) {
				break rows
			}
			if b.IsPortal(blk) {
				s.portals++
			}
			if i == 0 && !b.isObsidian(at(p.Add(left))) {
				break rows
			}
			if i == s.width-1 && !b.isObsidian(at(p.Add(right))) {
				break rows
			}
		}
	}
	for i := range s.width {
		if !b.isObsidian(at(s.bottomLeft.Add(cube.Pos{right[0] * i, s.height, right[2] * i}))) {
			s.height = 0
			break
		}
	}
	return s
}

// edge is BDS's sub_CDB0210: how far from pos along dir the floor of empty blocks on
// obsidian goes before an obsidian side, 0 if there is none within reach.
func (b *Blocks) edge(at func(cube.Pos) uint32, pos, dir cube.Pos) int {
	i := 0
	for ; i < maxEdge; i++ {
		p := pos.Add(cube.Pos{dir[0] * i, 0, dir[2] * i})
		if !b.empty(at(p)) || !b.isObsidian(at(p.Side(cube.FaceDown))) {
			break
		}
	}
	if b.isObsidian(at(pos.Add(cube.Pos{dir[0] * i, 0, dir[2] * i}))) {
		return i
	}
	return 0
}

// fill sets the inside of s to portal blocks. Like BDS's createPortalBlocks
// (sub_CDB0D00, which writes into a buffer that trySpawnPortal applies after), every
// block is set before any neighbour update goes out: a portal told about its neighbour
// while the frame is half filled would see it not full and break.
func (b *Blocks) fill(r *world.Region, s shape) {
	portal := b.portalFor(s.axis)
	for h := range s.height {
		for i := range s.width {
			r.SetBlock(s.bottomLeft.Add(cube.Pos{s.right[0] * i, h, s.right[2] * i}), 0, portal)
		}
	}
	for h := range s.height {
		for i := range s.width {
			r.UpdateNeighbours(s.bottomLeft.Add(cube.Pos{s.right[0] * i, h, s.right[2] * i}))
		}
	}
}

// record is the portal record of s.
func (s shape) record() Record {
	return Record{Pos: s.bottomLeft, Span: int8(s.width), XA: int8(s.right[0]), ZA: int8(s.right[2])} //nolint:gosec // widths and steps are small
}
