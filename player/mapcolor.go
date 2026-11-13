package player

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/helix-bedrock/helix-data/data/files"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/worldgen/noise"
	wgrand "github.com/helix-bedrock/worldgen/rand"
)

// Map colours as BDS 1.26.60's map render reads them (MapItem render 0xC8FC7B0): each block's
// minecraft:map_color component or BlockType colour, from helix-data's bds/map_colors.json,
// times the biome tint its tint method names (0xCD54EF0).
const mapColoursFile = "bds/map_colors.json"

// The tint methods, BDS's order.
const (
	tintNone = iota
	tintDefaultFoliage
	tintBirchFoliage
	tintEvergreenFoliage
	tintDryFoliage
	tintGrass
	tintWater
)

// BDS's fixed tints (0xCD548A0, 0xCD548B0, 0xCD548C0) and swamp grass (0xCD54AF0).
const (
	tintBirch      = 0x80a755
	tintEvergreen  = 0x619961
	tintDry        = 0x5c3c32
	swampGrassWarm = 0x6a7039
	swampGrassCold = 0x4c763c
	// swampNoiseSeed, swampNoiseScale and swampNoiseCold are the swamp grass noise: one
	// simplex octave seeded 2345, sampled at the block times 0.0225, cold below -0.1.
	swampNoiseSeed  = 2345
	swampNoiseScale = 0.0225
	swampNoiseCold  = -0.1
)

// mapColour is an RGBA colour, each part 0 to 1, as BDS's mce::Color.
type mapColour [4]float32

// none reports whether the colour is BDS's Color::NIL, which the render looks through.
func (c mapColour) none() bool { return c == mapColour{} }

// blockMapColour is what one block state draws on a map.
type blockMapColour struct {
	colour mapColour
	tint   uint8
	// log is set for logs: top when pillar_axis is y, side otherwise.
	log       bool
	top, side mapColour
	// bed takes the colour of its bed block entity.
	bed bool
}

// biomeTint is a biome's map tints: its climate and the minecraft:map_tints overrides.
type biomeTint struct {
	temperature, downfall float32
	water                 uint32
	foliage, grass        uint32
	hasFoliage, hasGrass  bool
	swampGrass            bool
}

// mapPalette is every state's map colour and every biome's tints.
type mapPalette struct {
	states []blockMapColour
	biomes map[int16]biomeTint
	water  []bool
	swamp  *noise.SimplexOctaves
}

type mapColoursDoc struct {
	Blocks map[string]struct {
		Colour [4]float32 `json:"colour"`
		Tint   uint8      `json:"tint"`
		Top    *[4]float32
		Side   *[4]float32
		Bed    bool `json:"bed"`
	} `json:"blocks"`
}

func byteColour(c [4]float32) mapColour {
	return mapColour{c[0] / 255, c[1] / 255, c[2] / 255, c[3] / 255}
}

// loadMapPalette reads the map colours of the registry's blocks and the tints of its biomes.
func loadMapPalette(reg *registry.Registry) (*mapPalette, error) {
	raw, err := fs.ReadFile(files.Files(), mapColoursFile)
	if err != nil {
		return nil, fmt.Errorf("player: map colours: %w", err)
	}
	var doc mapColoursDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("player: map colours: %w", err)
	}
	n := reg.Blocks.Count()
	p := &mapPalette{states: make([]blockMapColour, n), water: make([]bool, n), biomes: map[int16]biomeTint{}}
	for id := range uint32(n) {
		p.water[id] = reg.Blocks.Properties(id).Material == registry.MaterialWater
		e, ok := doc.Blocks[reg.Blocks.Name(id)]
		if !ok {
			continue
		}
		c := blockMapColour{colour: byteColour(e.Colour), tint: e.Tint, bed: e.Bed}
		if e.Top != nil && e.Side != nil {
			c.log, c.top, c.side = true, byteColour(*e.Top), byteColour(*e.Side)
		}
		p.states[id] = c
	}
	for _, b := range reg.Biomes.All() {
		p.biomes[b.ID] = biomeTint{temperature: b.Temperature, downfall: b.Downfall, water: uint32(b.MapWaterColour)} //nolint:gosec // an ARGB colour
	}
	if err := p.loadMapTints(reg); err != nil {
		return nil, err
	}
	p.swamp = noise.NewSimplexOctaves(wgrand.NewMT(swampNoiseSeed), 1, false)
	return p, nil
}

// biomeTintsDoc is the part of a biome file the map tints are read from.
type biomeTintsDoc struct {
	Biome struct {
		Description struct {
			Identifier string `json:"identifier"`
		} `json:"description"`
		Components struct {
			Tints *struct {
				Foliage string          `json:"foliage"`
				Grass   json.RawMessage `json:"grass"`
			} `json:"minecraft:map_tints"`
		} `json:"components"`
	} `json:"minecraft:biome"`
}

// loadMapTints reads the minecraft:map_tints of the vanilla biome files, BDS's
// CustomMapTintBiomeJsonComponent: a foliage colour and a grass tint or noise.
func (p *mapPalette) loadMapTints(reg *registry.Registry) error {
	const dir = "vanilla/biomes"
	entries, err := fs.ReadDir(files.Files(), dir)
	if err != nil {
		return fmt.Errorf("player: map tints: %w", err)
	}
	for _, e := range entries {
		raw, err := fs.ReadFile(files.Files(), path.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("player: map tints: %w", err)
		}
		var d biomeTintsDoc
		if json.Unmarshal(raw, &d) != nil || d.Biome.Components.Tints == nil {
			continue
		}
		b, ok := reg.Biomes.ByName(d.Biome.Description.Identifier)
		if !ok {
			continue
		}
		t := p.biomes[b.ID]
		tints := d.Biome.Components.Tints
		if c, ok := hexColour(tints.Foliage); ok {
			t.foliage, t.hasFoliage = c, true
		}
		var grass struct {
			Type string `json:"type"`
			Tint string `json:"tint"`
		}
		if json.Unmarshal(tints.Grass, &grass) == nil {
			switch grass.Type {
			case "noise":
				t.swampGrass = true
			case "tint":
				if c, ok := hexColour(grass.Tint); ok {
					t.grass, t.hasGrass = c, true
				}
			}
		}
		p.biomes[b.ID] = t
	}
	return nil
}

// hexColour reads "#rrggbb".
func hexColour(s string) (uint32, bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	return uint32(v), err == nil
}

// colourMap is BDS's colour map gradient of a biome's climate: the corner colours of the
// grass or foliage map, mixed by temperature and by downfall times temperature.
func colourMap(t biomeTint, corners [4][3]float32) uint32 {
	temp := min(max(t.temperature, 0), 1)
	rain := min(max(t.downfall, 0), 1)
	a := min(max(int32((1-temp)*255), 0), 255)
	b := min(max(int32((1-rain*temp)*255), 0), 255)
	u, v := float32(a)*0.00390625, float32(b)*0.00390625
	var out [3]uint32
	for i := range 3 {
		c := (1-v)*((1-u)*corners[0][i]+u*corners[1][i]) + v*((1-u)*corners[2][i]+u*corners[3][i])
		out[i] = uint32(min(max(c, 0), 1) * 255)
	}
	return out[0]<<16 | out[1]<<8 | out[2]
}

// The corner colours (red, green, blue) of BDS's foliage (0xCD548D0) and grass maps.
var (
	foliageCorners = [4][3]float32{{0.10196079, 0.7490196, 0}, {0.29411766, 0.65882355, 0.45882353}, {0.68235296, 0.6431373, 0.16470589}, {0.3764706, 0.6313726, 0.48235294}}
	grassCorners   = [4][3]float32{{0.2784314, 0.8156863, 0.2}, {0.42352942, 0.7058824, 0.5764706}, {0.7490196, 0.7137255, 0.33333334}, {0.5019608, 0.7058824, 0.5921569}}
)

// tintOf is one biome's colour for a tint method at a block, BDS's callbacks.
func (p *mapPalette) tintOf(method uint8, b biomeTint, x, z int) uint32 {
	switch method {
	case tintDefaultFoliage:
		if b.hasFoliage {
			return b.foliage
		}
		return colourMap(b, foliageCorners)
	case tintBirchFoliage:
		return tintBirch
	case tintEvergreenFoliage:
		return tintEvergreen
	case tintDryFoliage:
		return tintDry
	case tintGrass:
		if b.hasGrass {
			return b.grass
		}
		if b.swampGrass {
			if p.swamp.Sum2(float32(x)*swampNoiseScale, float32(z)*swampNoiseScale) < swampNoiseCold {
				return swampGrassCold
			}
			return swampGrassWarm
		}
		return colourMap(b, grassCorners)
	case tintWater:
		return b.water & 0xffffff
	}
	return 0xffffff
}

// The blocks BDS mixes a tint over (sub_CD55430): 1 the ring at 4, 2 the 3 by 3 around, 3 the
// corners at 8 and the block.
var (
	tintRing   = [][2]int{{-4, -4}, {0, -4}, {4, -4}, {4, 0}, {4, 4}, {0, 4}, {-4, 4}, {-4, 0}}
	tintSquare = [][2]int{{-1, -1}, {0, -1}, {1, -1}, {1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {0, 0}}
	tintWide   = [][2]int{{-8, -8}, {-8, 8}, {8, 8}, {8, -8}, {0, 0}}
)

// tintSamples is the sampling of each tint method (0xCD54EF0).
func tintSamples(method uint8) [][2]int {
	switch method {
	case tintGrass:
		return tintWide
	case tintWater:
		return tintSquare
	}
	return tintRing
}

// tint is the colour a tint method gives at pos, the mean of its samples' biomes (0xCD809D0).
func (p *mapPalette) tint(r *world.Region, method uint8, pos cube.Pos) mapColour {
	if method == tintNone || method > tintWater {
		return mapColour{1, 1, 1, 1}
	}
	var sum [3]float32
	samples := tintSamples(method)
	for _, o := range samples {
		at := cube.Pos{pos[0] + o[0], pos[1], pos[2] + o[1]}
		id, _ := r.Biome(at)
		c := p.tintOf(method, p.biomes[id], at[0], at[2])
		sum[0] += float32(c >> 16 & 0xff)
		sum[1] += float32(c >> 8 & 0xff)
		sum[2] += float32(c & 0xff)
	}
	n := float32(len(samples)) * 255
	return mapColour{min(sum[0]/n, 1), min(sum[1]/n, 1), min(sum[2]/n, 1), 1}
}

// colourAt is the map colour of the block at pos, NIL where it draws nothing.
func (s *Host) colourAt(r *world.Region, pos cube.Pos, state uint32) mapColour {
	p := s.mapPalette
	if int(state) >= len(p.states) {
		return mapColour{}
	}
	e := p.states[state]
	switch {
	case e.log:
		if axis, _ := s.reg.Blocks.States(state)["pillar_axis"].(string); axis == "y" {
			return e.top
		}
		return e.side
	case e.bed:
		return s.bedMapColour(r, pos, state)
	}
	if e.colour.none() || e.tint == tintNone {
		return e.colour
	}
	t := p.tint(r, e.tint, pos)
	return mapColour{e.colour[0] * t[0], e.colour[1] * t[1], e.colour[2] * t[2], e.colour[3] * t[3]}
}

// bedDyeColours are BDS's bed map colours by dye (BedBlock::getMapColor 0xCCD2A70).
var bedDyeColours = [16][4]float32{
	{249, 255, 254, 255}, {216, 127, 51, 255}, {178, 76, 216, 255}, {102, 153, 216, 255},
	{229, 229, 51, 255}, {127, 204, 25, 255}, {242, 127, 165, 255}, {76, 76, 76, 255},
	{153, 153, 153, 255}, {76, 127, 153, 255}, {153, 90, 205, 255}, {51, 76, 178, 255},
	{102, 76, 51, 255}, {102, 127, 51, 255}, {153, 51, 51, 255}, {25, 25, 25, 255},
}

// bedMapColour is a bed's map colour: none without its block entity, white for the head,
// else its colour.
func (s *Host) bedMapColour(r *world.Region, pos cube.Pos, state uint32) mapColour {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return mapColour{}
	}
	bed, ok := be.(*bedEntity)
	if !ok {
		return mapColour{}
	}
	if blockuse.BedIsHead(s.reg.Blocks, state) {
		return byteColour(bedDyeColours[0])
	}
	if int(bed.color) >= len(bedDyeColours) {
		return mapColour{}
	}
	return byteColour(bedDyeColours[bed.color])
}
