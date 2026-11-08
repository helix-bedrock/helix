package portal

import (
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// BDS 1.26.60's PortalForcer numbers (sub_CDA9350).
const (
	// SearchRadius is how far on x and z a traveller's arrival looks for a portal.
	SearchRadius = 128
	// buildRadius is how far on x and z it looks for a spot to build one.
	buildRadius = 16
	// A portal built in the open stands at forcedMinY or higher, and forcedTopGap
	// below the top of the dimension or lower.
	forcedMinY   = 70
	forcedTopGap = 10
	// The built frame is frameColumns wide (two portal columns inside) and its
	// rows go from one below the floor to frameTop.
	frameColumns = 4
	frameTop     = 3
	// buildPasses is how many times BDS places the frame, as Java's Teleporter did.
	buildPasses = 4
)

// The End's arrival (TheEndDimension translate, sub_4B9DC70; the platform in
// sub_CDA9350).
var (
	// EndArrival is where every traveller into the End lands.
	EndArrival = cube.Pos{100, 50, 0}
)

const (
	endPlatformReach = 2
	endPlatformAir   = 3
)

// Portals are the portal blocks and records of a server.
type Portals struct {
	*Blocks
	Records *Records
}

// Light is BDS's PortalBlock::trySpawnPortal (sub_D52A9C0): fire lit at pos in a valid
// empty obsidian frame (along X first, then Z) fills it with portal blocks and records
// it. It reports whether a portal was made.
func (p *Portals) Light(r *world.Region, dim mcdb.Dimension, pos cube.Pos) bool {
	for _, a := range [...]Axis{AxisX, AxisZ} {
		s := p.findShape(r, pos, a)
		if !s.valid() || s.portals != 0 {
			continue
		}
		p.fill(r, s)
		p.Records.Add(dim, s.record())
		return true
	}
	return false
}

// Check is the portal block's neighborChanged (sub_D52A490): a portal whose frame is no
// longer whole and full breaks into air.
func (p *Portals) Check(r *world.Region, pos cube.Pos, block uint32) {
	s := p.findShape(r, pos, p.axisOf(block))
	if s.valid() && s.full() {
		return
	}
	if r.SetBlock(pos, 0, p.air) {
		r.UpdateNeighbours(pos)
	}
}

// Arrival is where a traveller ends up in a portal: its feet position.
type Arrival struct {
	Pos mgl32.Vec3
}

// Find looks for a portal near pos in dim for a traveller, dropping records whose
// portal is gone (BDS drops them when the block is removed). Its chunk must be loaded.
func (p *Portals) Find(r *world.Region, dim mcdb.Dimension, pos cube.Pos, along float32) (Arrival, bool) {
	for {
		rec, col, ok := p.Records.Nearest(dim, pos, SearchRadius)
		if !ok {
			return Arrival{}, false
		}
		if b, loaded := r.Block(col, 0); loaded && !p.IsPortal(b) {
			p.Records.Remove(dim, func(o Record) bool { return o == rec })
			continue
		}
		return arrivalIn(rec, col, along), true
	}
}

// arrivalIn is a traveller's place in rec: the column as far along its width as it was
// along the portal it left (along in 0..1), or col when that is unknown (below 0), in
// the middle of the block.
func arrivalIn(rec Record, col cube.Pos, along float32) Arrival {
	if along >= 0 && rec.Span > 0 {
		i := min(int(along*float32(rec.Span)), int(rec.Span)-1)
		col = rec.column(i)
	}
	return Arrival{Pos: mgl32.Vec3{float32(col[0]) + 0.5, float32(col[1]), float32(col[2]) + 0.5}}
}

// Along is how far along the portal near pos a traveller standing at feet is, 0..1,
// or -1 when there is no portal record there.
func (p *Portals) Along(dim mcdb.Dimension, pos cube.Pos, feet mgl32.Vec3) float32 {
	rec, _, ok := p.Records.Nearest(dim, pos, maxWidth)
	if !ok || rec.Span <= 0 {
		return -1
	}
	at := cube.Pos{int(math.Floor(float64(feet[0]))), pos[1], int(math.Floor(float64(feet[2])))}
	for i := range int(rec.Span) {
		if c := rec.column(i); c[0] == at[0] && c[2] == at[2] {
			return (float32(i) + 0.5) / float32(rec.Span)
		}
	}
	return -1
}

// Make is BDS's PortalForcer building a portal for a traveller at from (sub_CDA9350,
// Java 1.12's Teleporter.makePortal shape): the nearest spot within 16 blocks where a
// 3 by 4 floor with 4 free blocks above fits, else one where a single row fits, else in
// the open at a clamped height on a built platform with BDS's netherrack ledge. The
// portal is recorded and the traveller's arrival returned.
func (p *Portals) Make(r *world.Region, dim mcdb.Dimension, from mgl32.Vec3, rng *rand.Rand) Arrival {
	rg := r.Dimension().Range()
	at := func(x, y, z int) uint32 {
		b, _ := r.Block(cube.Pos{x, y, z}, 0)
		return b
	}
	fx, fy, fz := int(math.Floor(float64(from[0]))), int(math.Floor(float64(from[1]))), int(math.Floor(float64(from[2])))
	best := -1.0
	bx, by, bz, bdir := fx, fy, fz, 0
	start := rng.IntN(4)
	search := func(dirs, depth int) {
		for x := fx - buildRadius; x <= fx+buildRadius; x++ {
			dx := float64(x) + 0.5 - float64(from[0])
			for z := fz - buildRadius; z <= fz+buildRadius; z++ {
				dz := float64(z) + 0.5 - float64(from[2])
			column:
				for y := rg[1]; y >= rg[0]; y-- {
					if at(x, y, z) != p.air {
						continue
					}
					for y > rg[0] && at(x, y-1, z) == p.air {
						y--
					}
					for k := start; k < start+dirs; k++ {
						sx, sz := dirSteps(k)
						for w := range depth {
							for l := range frameColumns {
								for h := -1; h < frameColumns; h++ {
									px, py, pz := x+(l-1)*sx+w*sz, y+h, z+(l-1)*sz-w*sx
									b := at(px, py, pz)
									if (h < 0 && !p.solid(b)) || (h >= 0 && b != p.air) {
										continue column
									}
								}
							}
						}
						dy := float64(y) + 0.5 - float64(from[1])
						if d := dx*dx + dy*dy + dz*dz; best < 0 || d < best {
							best, bx, by, bz, bdir = d, x, y, z, k%4
						}
					}
				}
			}
		}
	}
	search(4, 3)
	if best < 0 {
		search(2, 1)
	}
	sx, sz := dirSteps(bdir)
	if best < 0 {
		by = min(max(fy, forcedMinY), rg[1]-forcedTopGap)
		p.platform(r, bx, by, bz, sx, sz)
	}
	axis := AxisX
	if sx == 0 {
		axis = AxisZ
	}
	portal := p.portalFor(axis)
	// Each pass sets every frame and portal block first, as BDS's setBlock with flag 2
	// (no neighbour update), and only then updates the neighbours of all of them in the
	// same order (sub_CE5EAC0 after the pass). A portal told of a neighbour while the
	// frame is half built would find it not whole and break.
	for range buildPasses {
		for l := range frameColumns {
			for h := -1; h <= frameTop; h++ {
				b := portal
				if l == 0 || l == frameColumns-1 || h == -1 || h == frameTop {
					b = p.obsidian
				}
				r.SetBlock(cube.Pos{bx + (l-1)*sx, by + h, bz + (l-1)*sz}, 0, b)
			}
		}
		for l := range frameColumns {
			for h := -1; h <= frameTop; h++ {
				r.UpdateNeighbours(cube.Pos{bx + (l-1)*sx, by + h, bz + (l-1)*sz})
			}
		}
	}
	rec := Record{Pos: cube.Pos{bx, by, bz}, Span: 2, XA: int8(sx), ZA: int8(sz)} //nolint:gosec // steps are -1, 0 or 1
	p.Records.Add(dim, rec)
	return arrivalIn(rec, rec.Pos, -1)
}

// dirSteps is Java's Teleporter direction k: the step along the portal.
func dirSteps(k int) (sx, sz int) {
	sx, sz = k%2, 1-k%2
	if k%4 >= 2 {
		sx, sz = -sx, -sz
	}
	return sx, sz
}

// solid is the floor test of the spot search: a block with a collision box that is
// not a liquid.
func (p *Portals) solid(b uint32) bool {
	props := p.reg.Properties(b)
	return len(props.Collision) > 0
}

// platform builds the open-air floor of a forced portal: obsidian under three rows of
// two blocks across the portal with air above, then netherrack wherever the floor
// around it is air (BDS-only, sub_CDA9350).
func (p *Portals) platform(r *world.Region, x, y, z, sx, sz int) {
	set := func(pos cube.Pos, b uint32) {
		if r.SetBlock(pos, 0, b) {
			r.UpdateNeighbours(pos)
		}
	}
	for across := -1; across <= 1; across++ {
		for l := 1; l < 3; l++ {
			for h := -1; h < frameTop; h++ {
				pos := cube.Pos{x + (l-1)*sx + across*sz, y + h, z + (l-1)*sz - across*sx}
				if h < 0 {
					set(pos, p.obsidian)
				} else {
					set(pos, p.air)
				}
			}
		}
	}
	ledge := func(pos cube.Pos) {
		if b, _ := r.Block(pos, 0); b == p.air {
			set(pos, p.netherrack)
		}
	}
	for across := -2; across <= 2; across++ {
		if across == -2 || across == 2 {
			for l := 0; l < 2; l++ {
				ledge(cube.Pos{x + l*sx + across*sz, y - 1, z + l*sz - across*sx})
			}
			continue
		}
		for l := -1; l <= 2; l++ {
			ledge(cube.Pos{x + l*sx + across*sz, y - 1, z + l*sz - across*sx})
		}
	}
}

// EndPlatform builds the End's arrival platform (sub_CDA9350, dimension 2): obsidian
// 5 by 5 under EndArrival and air three blocks up.
func (p *Portals) EndPlatform(r *world.Region) {
	for dx := -endPlatformReach; dx <= endPlatformReach; dx++ {
		for dz := -endPlatformReach; dz <= endPlatformReach; dz++ {
			for dy := -1; dy < endPlatformAir; dy++ {
				pos := EndArrival.Add(cube.Pos{dx, dy, dz})
				b := p.air
				if dy < 0 {
					b = p.obsidian
				}
				if r.SetBlock(pos, 0, b) {
					r.UpdateNeighbours(pos)
				}
			}
		}
	}
}
