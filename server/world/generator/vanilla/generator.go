package vanilla

import (
	"fmt"
	"math"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/item"
	"github.com/helix-bedrock/helix/server/world"
	"github.com/helix-bedrock/helix/server/world/chunk"
)

// Generator generates vanilla overworld terrain: its shape, caves, biomes
// and surface. Features (trees, ores, plants) and structures are not
// generated yet.
type Generator struct {
	r       *router
	tree    *biomeNode
	surface rule
	br      world.BlockRegistry

	surfaceNoise, secondaryNoise *normalNoise
	bandOffset                   *normalNoise
	bands                        [192]world.Block
	split                        splitter
	aquiferSplit                 splitter

	stone, water, lava uint32
}

// New creates an overworld generator for a seed, using br to turn blocks
// into runtime IDs.
func New(seed int64, br world.BlockRegistry) (*Generator, error) {
	r, err := newRouter(uint64(seed), "overworld")
	if err != nil {
		return nil, err
	}
	tree, err := loadBiomeTree("overworld")
	if err != nil {
		return nil, err
	}
	g := &Generator{r: r, tree: tree, br: br, split: r.prog.split}
	g.aquiferSplit = g.split.byName("minecraft:aquifer").splitter()
	for _, n := range []struct {
		name string
		dest **normalNoise
	}{{"surface", &g.surfaceNoise}, {"surface_secondary", &g.secondaryNoise}, {"clay_bands_offset", &g.bandOffset}} {
		if *n.dest, err = r.prog.noise(n.name); err != nil {
			return nil, err
		}
	}
	g.bands = terracottaBands(g.split.byName("minecraft:clay_bands"))
	wg, err := data.Worldgen()
	if err != nil {
		return nil, err
	}
	p := &rules{g: g, data: wg, rule: map[string]rule{}, cond: map[string]condition{}}
	if g.surface, err = p.parseRule(r.materialRule); err != nil {
		return nil, fmt.Errorf("vanilla: surface rules: %w", err)
	}
	g.stone = br.BlockRuntimeID(block.Stone{})
	g.water = br.BlockRuntimeID(block.Water{Still: true, Depth: 8})
	g.lava = br.BlockRuntimeID(block.Lava{Still: true, Depth: 8})
	return g, nil
}

const (
	kindAir uint8 = iota
	kindStone
	kindWater
	kindLava
)

// fluidAt is what fills an empty block without aquifers: lava deep down,
// water below the sea, air above, Java's fluid picker.
func (g *Generator) fluidAt(y int32) uint8 {
	if y < min(-54, g.r.set.SeaLevel) {
		return kindLava
	}
	if y < g.r.set.SeaLevel {
		return kindWater
	}
	return kindAir
}

// GenerateChunk generates the chunk at pos. It is safe to call from many
// goroutines at once.
func (g *Generator) GenerateChunk(pos world.ChunkPos, c *chunk.Chunk) {
	ctx := g.r.prog.newCtx()
	bx, bz := pos[0]<<4, pos[1]<<4
	minY, height := g.r.set.MinY, g.r.set.Height
	if rng := c.Range(); int32(rng.Min()) > minY {
		minY = int32(rng.Min())
	}

	// Biomes, one per 4x4x4 cell as Java samples them.
	var last *biomeNode
	quarts := height / 4
	biomes := make([]world.Biome, 16*quarts)
	for qx := range int32(4) {
		for qz := range int32(4) {
			for qy := range quarts {
				p := g.r.climateAt(ctx, bx+qx*4, minY+qy*4, bz+qz*4)
				b := g.tree.search(&p, &last)
				biomes[(qx*4+qz)*quarts+qy] = b
				id := uint32(b.EncodeBiome())
				for lx := range uint8(4) {
					for lz := range uint8(4) {
						for ly := range int16(4) {
							c.SetBiome(uint8(qx*4)+lx, int16(minY+qy*4)+ly, uint8(qz*4)+lz, id)
						}
					}
				}
			}
		}
	}

	// Terrain shape, cell by cell so each cell's corners are computed once.
	var aq *aquifer
	if g.r.set.Aquifers {
		aq = g.newAquifer(ctx, pos[0], pos[1])
	}
	kinds := make([]uint8, 16*16*height)
	at := func(lx, y, lz int32) *uint8 { return &kinds[(lx*16+lz)*height+(y-minY)] }
	for cx := range int32(4) {
		for cz := range int32(4) {
			for cy := range height / 8 {
				for lx := range int32(4) {
					for lz := range int32(4) {
						for ly := range int32(8) {
							x, y, z := bx+cx*4+lx, minY+cy*8+ly, bz+cz*4+lz
							d := g.r.finalDensity.eval(ctx, x, y, z)
							var k uint8
							switch {
							case aq != nil:
								k = aq.at(x, y, z, d)
							case d > 0:
								k = kindStone
							default:
								k = g.fluidAt(y)
							}
							*at(cx*4+lx, y, cz*4+lz) = k
						}
					}
				}
			}
		}
	}

	// Heights: the first non-air block from the top, plus one.
	var heights [16][16]int32
	for lx := range int32(16) {
		for lz := range int32(16) {
			heights[lx][lz] = minY
			for y := minY + height - 1; y >= minY; y-- {
				if *at(lx, y, lz) != kindAir {
					heights[lx][lz] = y + 1
					break
				}
			}
		}
	}

	// Surface: run the material rules down every column.
	sc := &surfaceCtx{g: g, heights: &heights, eval: ctx}
	ruleIDs := map[world.Block]uint32{}
	for lx := range int32(16) {
		for lz := range int32(16) {
			x, z := bx+lx, bz+lz
			sc.x, sc.z, sc.secondarySet, sc.surfaceMinYSet = x, z, false, false
			sc.runDepth = g.runDepth(x, z)
			stoneAbove, fluidHeight := int32(0), int32(math.MinInt32)
			bottom := int32(math.MaxInt32)
			for y := heights[lx][lz] - 1; y >= minY; y-- {
				k := *at(lx, y, lz)
				switch k {
				case kindAir:
					stoneAbove, fluidHeight = 0, math.MinInt32
					continue
				case kindWater, kindLava:
					if fluidHeight == math.MinInt32 {
						fluidHeight = y + 1
					}
					continue
				}
				if bottom >= y {
					bottom = minY
					for s := y - 1; s >= minY; s-- {
						if *at(lx, s, lz) != kindStone {
							bottom = s + 1
							break
						}
					}
				}
				stoneAbove++
				sc.y, sc.stoneAbove, sc.stoneBelow, sc.fluidHeight = y, stoneAbove, y-bottom+1, fluidHeight
				sc.biomeID = biomes[((lx>>2)*4+(lz>>2))*quarts+(y-minY)>>2].EncodeBiome()
				b := g.surface.apply(sc)
				if b == nil {
					continue
				}
				rid, ok := ruleIDs[b]
				if !ok {
					rid = g.br.BlockRuntimeID(b)
					ruleIDs[b] = rid
				}
				if rid == g.stone {
					continue
				}
				*at(lx, y, lz) = 255
				c.SetBlock(uint8(lx), int16(y), uint8(lz), 0, rid)
			}
		}
	}

	// Write the terrain blocks the surface left alone.
	for lx := range int32(16) {
		for lz := range int32(16) {
			for y := minY; y < heights[lx][lz]; y++ {
				var rid uint32
				switch *at(lx, y, lz) {
				case kindStone:
					rid = g.stone
				case kindWater:
					rid = g.water
				case kindLava:
					rid = g.lava
				default:
					continue
				}
				c.SetBlock(uint8(lx), int16(y), uint8(lz), 0, rid)
			}
		}
	}
}

// runDepth is how deep the surface layer reaches at a column.
func (g *Generator) runDepth(x, z int32) int32 {
	n := g.surfaceNoise.get(float64(x), 0, float64(z))
	return int32(n*2.75 + 3 + float64(float32(g.split.atPos(x, 0, z).nextFloat64()*0.25)))
}

// preliminarySurface is the rough surface height at a column, at the
// biome cell's corner as Java samples it.
func (g *Generator) preliminarySurface(c *evalCtx, x, z int32) int32 {
	return int32(math.Floor(g.r.preliminarySurface.eval(c, x&^3, 0, z&^3)))
}

// terracotta returns the badlands band block at a position.
func (g *Generator) terracotta(x, y, z int32) world.Block {
	offset := int32(math.Round(g.bandOffset.get(float64(x), 0, float64(z)) * 4))
	i := int((int64(y+offset)%192 + 192) % 192)
	return g.bands[i]
}

func terracottaBands(r *xoroshiro) [192]world.Block {
	var b [192]world.Block
	plain := world.Block(block.Terracotta{})
	stained := func(c item.Colour) world.Block { return block.StainedTerracotta{Colour: c} }
	for i := range b {
		b[i] = plain
	}
	for i := 0; i < len(b); {
		i += int(r.nextIntN(5)) + 1
		if i >= len(b) {
			break
		}
		b[i] = stained(item.ColourOrange())
		i++
	}
	add := func(minSize int32, s world.Block) {
		n := r.nextIntN(10) + 6
		for range n {
			w := minSize + r.nextIntN(3)
			start := r.nextIntN(int32(len(b)))
			for m := range w {
				if int(start+m) >= len(b) {
					break
				}
				b[start+m] = s
			}
		}
	}
	add(1, stained(item.ColourYellow()))
	add(2, stained(item.ColourBrown()))
	add(1, stained(item.ColourRed()))
	bands := r.nextIntN(7) + 9
	for i, n := 0, int32(0); n < bands && i < len(b); n++ {
		b[i] = stained(item.ColourWhite())
		if i > 1 && r.next()&1 != 0 {
			b[i-1] = stained(item.ColourLightGrey())
		}
		if i+1 < len(b) && r.next()&1 != 0 {
			b[i+1] = stained(item.ColourLightGrey())
		}
		i += int(r.nextIntN(16)) + 4
	}
	return b
}

// DefaultSpawn returns a spawn point on land near the origin.
func (g *Generator) DefaultSpawn(world.Dimension) cube.Pos {
	ctx := g.r.prog.newCtx()
	for r := int32(0); r < 256; r += 16 {
		for _, d := range [][2]int32{{r, 0}, {0, r}, {-r, 0}, {0, -r}} {
			for y := g.r.set.MinY + g.r.set.Height - 1; y > g.r.set.SeaLevel; y-- {
				if g.r.finalDensity.eval(ctx, d[0], y, d[1]) > 0 {
					return cube.Pos{int(d[0]), int(y) + 1, int(d[1])}
				}
			}
		}
	}
	return cube.Pos{0, int(g.r.set.SeaLevel) + 1, 0}
}
