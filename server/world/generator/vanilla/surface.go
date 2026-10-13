package vanilla

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/item"
	"github.com/helix-bedrock/helix/server/world"
)

// Surface rules, Java's material rules, ported from Pumpkin's
// pumpkin-world/src/generation/surface and read from Java Edition 26's
// worldgen/material_rule and material_condition.

// surfaceCtx is the state of placing a column's surface.
type surfaceCtx struct {
	g              *Generator
	x, y, z        int32
	biomeID        int
	runDepth       int32
	secondaryDepth float64
	secondarySet   bool
	stoneAbove     int32
	stoneBelow     int32
	fluidHeight    int32
	surfaceMinY    int32
	surfaceMinYSet bool
	heights        *[16][16]int32
	estimates      [4]int32
	estimatesFor   [2]int32
	estimatesSet   bool
	eval           *evalCtx
}

type rule interface {
	apply(c *surfaceCtx) world.Block
}

type condition interface {
	test(c *surfaceCtx) bool
}

type sequenceRule []rule

func (s sequenceRule) apply(c *surfaceCtx) world.Block {
	for _, r := range s {
		if b := r.apply(c); b != nil {
			return b
		}
	}
	return nil
}

type conditionRule struct {
	cond condition
	then rule
}

func (r conditionRule) apply(c *surfaceCtx) world.Block {
	if r.cond.test(c) {
		return r.then.apply(c)
	}
	return nil
}

type blockRule struct{ b world.Block }

func (r blockRule) apply(*surfaceCtx) world.Block { return r.b }

// skipRule is a rule Helix cannot apply yet, such as ore veins: it never
// places a block, so the rules after it decide.
type skipRule struct{}

func (skipRule) apply(*surfaceCtx) world.Block { return nil }

type bandlandsRule struct{}

func (bandlandsRule) apply(c *surfaceCtx) world.Block { return c.g.terracotta(c.x, c.y, c.z) }

type biomeCond map[int]bool

func (b biomeCond) test(c *surfaceCtx) bool { return b[c.biomeID] }

type notCond struct{ c condition }

func (n notCond) test(c *surfaceCtx) bool { return !n.c.test(c) }

type holeCond struct{}

func (holeCond) test(c *surfaceCtx) bool { return c.runDepth <= 0 }

type steepCond struct{}

func (steepCond) test(c *surfaceCtx) bool {
	lx, lz := c.x&15, c.z&15
	h := c.heights
	zs, za := max(lz-1, 0), min(lz+1, 15)
	if h[lx][za] >= h[lx][zs]+4 {
		return true
	}
	xs, xa := max(lx-1, 0), min(lx+1, 15)
	return h[xs][lz] >= h[xa][lz]+4
}

type abovePreliminarySurface struct{}

func (abovePreliminarySurface) test(c *surfaceCtx) bool { return c.y >= c.estimateSurface() }

// anchor is a vertical anchor: an absolute Y, or blocks above the bottom
// or below the top of the world.
type anchor struct {
	kind string
	v    int32
}

func parseAnchor(raw json.RawMessage) anchor {
	var m map[string]int32
	_ = json.Unmarshal(raw, &m)
	for k, v := range m {
		return anchor{kind: k, v: v}
	}
	return anchor{kind: "absolute"}
}

func (a anchor) y(g *Generator) int32 {
	switch a.kind {
	case "above_bottom":
		return g.r.set.MinY + a.v
	case "below_top":
		return g.r.set.MinY + g.r.set.Height - 1 - a.v
	}
	return a.v
}

type yAboveCond struct {
	anchor   anchor
	mult     int32
	addStone bool
}

func (y yAboveCond) test(c *surfaceCtx) bool {
	v := c.y
	if y.addStone {
		v += c.stoneAbove
	}
	return v >= y.anchor.y(c.g)+c.runDepth*y.mult
}

type waterCond struct {
	offset, mult int32
	addStone     bool
}

func (w waterCond) test(c *surfaceCtx) bool {
	if c.fluidHeight == math.MinInt32 {
		return true
	}
	v := c.y
	if w.addStone {
		v += c.stoneAbove
	}
	return v >= c.fluidHeight+w.offset+c.runDepth*w.mult
}

type stoneDepthCond struct {
	offset         int32
	addSurface     bool
	secondaryRange int32
	ceiling        bool
}

func (s stoneDepthCond) test(c *surfaceCtx) bool {
	depth := c.stoneAbove
	if s.ceiling {
		depth = c.stoneBelow
	}
	var add int32
	if s.addSurface {
		add = c.runDepth
	}
	var rng int32
	if s.secondaryRange != 0 {
		rng = int32(float32((c.secondary()+1)/2) * float32(s.secondaryRange))
	}
	return depth <= 1+s.offset+add+rng
}

type noiseThresholdCond struct {
	n      *normalNoise
	lo, hi float64
	is3D   bool
}

func (t noiseThresholdCond) test(c *surfaceCtx) bool {
	y := 0.0
	if t.is3D {
		y = float64(c.y)
	}
	v := t.n.get(float64(c.x), y, float64(c.z))
	return v >= t.lo && v <= t.hi
}

type verticalGradientCond struct {
	split           splitter
	trueAt, falseAt anchor
}

func (v verticalGradientCond) test(c *surfaceCtx) bool {
	t, f := v.trueAt.y(c.g), v.falseAt.y(c.g)
	if c.y <= t {
		return true
	}
	if c.y >= f {
		return false
	}
	chance := float32(1) + (float32(c.y)-float32(t))/(float32(f)-float32(t))*(0-1)
	return v.split.atPos(c.x, c.y, c.z).nextFloat32() < chance
}

func (c *surfaceCtx) secondary() float64 {
	if !c.secondarySet {
		c.secondaryDepth = c.g.secondaryNoise.get(float64(c.x), 0, float64(c.z))
		c.secondarySet = true
	}
	return c.secondaryDepth
}

// estimateSurface is where the surface roughly is, from the preliminary
// surface level at the chunk's corners, as Java estimates it.
func (c *surfaceCtx) estimateSurface() int32 {
	if c.surfaceMinYSet {
		return c.surfaceMinY
	}
	cx, cz := c.x>>4, c.z>>4
	if !c.estimatesSet || c.estimatesFor != [2]int32{cx, cz} {
		c.estimatesFor, c.estimatesSet = [2]int32{cx, cz}, true
		for i, d := range [4][2]int32{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
			c.estimates[i] = c.g.preliminarySurface(c.eval, (cx+d[0])<<4, (cz+d[1])<<4)
		}
	}
	e := c.estimates
	s := int32(math.Floor(lerp2(float64(float32(c.x&15)/16), float64(float32(c.z&15)/16),
		float64(e[0]), float64(e[1]), float64(e[2]), float64(e[3]))))
	c.surfaceMinY, c.surfaceMinYSet = s+c.runDepth-8, true
	return c.surfaceMinY
}

// rules parses material rules and conditions.
type rules struct {
	g    *Generator
	data map[string]json.RawMessage
	rule map[string]rule
	cond map[string]condition
}

func (p *rules) parseRule(raw json.RawMessage) (rule, error) {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		short := strings.TrimPrefix(name, "minecraft:")
		if r, ok := p.rule[short]; ok {
			return r, nil
		}
		d, ok := p.data["material_rule/"+short]
		if !ok {
			return nil, fmt.Errorf("unknown material rule %q", name)
		}
		r, err := p.parseRule(d)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", short, err)
		}
		p.rule[short] = r
		return r, nil
	}
	var o map[string]json.RawMessage
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, err
	}
	var typ string
	_ = json.Unmarshal(o["type"], &typ)
	switch strings.TrimPrefix(typ, "minecraft:") {
	case "sequence":
		var list []json.RawMessage
		_ = json.Unmarshal(o["sequence"], &list)
		var s sequenceRule
		for _, e := range list {
			r, err := p.parseRule(e)
			if err != nil {
				return nil, err
			}
			s = append(s, r)
		}
		return s, nil
	case "condition":
		c, err := p.parseCond(o["if_true"])
		if err != nil {
			return nil, err
		}
		r, err := p.parseRule(o["then_run"])
		if err != nil {
			return nil, err
		}
		return conditionRule{cond: c, then: r}, nil
	case "block":
		var state string
		if json.Unmarshal(o["result_state"], &state) != nil {
			var s struct {
				Name string `json:"Name"`
			}
			_ = json.Unmarshal(o["result_state"], &s)
			state = s.Name
		}
		b := javaBlock(state)
		if b == nil {
			return skipRule{}, nil
		}
		return blockRule{b: b}, nil
	case "bandlands":
		return bandlandsRule{}, nil
	case "ore_vein":
		// Ore veins are a material rule since Java Edition 26; not done yet.
		return skipRule{}, nil
	}
	return nil, fmt.Errorf("unknown material rule type %q", typ)
}

func (p *rules) parseCond(raw json.RawMessage) (condition, error) {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		short := strings.TrimPrefix(name, "minecraft:")
		if c, ok := p.cond[short]; ok {
			return c, nil
		}
		d, ok := p.data["material_condition/"+short]
		if !ok {
			return nil, fmt.Errorf("unknown material condition %q", name)
		}
		c, err := p.parseCond(d)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", short, err)
		}
		p.cond[short] = c
		return c, nil
	}
	var o map[string]json.RawMessage
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, err
	}
	var typ string
	_ = json.Unmarshal(o["type"], &typ)
	num := func(k string) int32 {
		var v float64
		_ = json.Unmarshal(o[k], &v)
		return int32(v)
	}
	flag := func(k string) bool {
		var v bool
		_ = json.Unmarshal(o[k], &v)
		return v
	}
	switch strings.TrimPrefix(typ, "minecraft:") {
	case "biome":
		var names []string
		_ = json.Unmarshal(o["biome_is"], &names)
		b := biomeCond{}
		for _, n := range names {
			if id, ok := bedrockBiomeIDs[strings.TrimPrefix(n, "minecraft:")]; ok {
				b[id] = true
			}
		}
		return b, nil
	case "not":
		c, err := p.parseCond(o["invert"])
		if err != nil {
			return nil, err
		}
		return notCond{c}, nil
	case "hole":
		return holeCond{}, nil
	case "steep":
		return steepCond{}, nil
	case "above_preliminary_surface":
		return abovePreliminarySurface{}, nil
	case "y_above":
		return yAboveCond{anchor: parseAnchor(o["anchor"]), mult: num("surface_depth_multiplier"), addStone: flag("add_stone_depth")}, nil
	case "water":
		return waterCond{offset: num("offset"), mult: num("surface_depth_multiplier"), addStone: flag("add_stone_depth")}, nil
	case "stone_depth":
		var t string
		_ = json.Unmarshal(o["surface_type"], &t)
		return stoneDepthCond{offset: num("offset"), addSurface: flag("add_surface_depth"),
			secondaryRange: num("secondary_depth_range"), ceiling: t == "ceiling"}, nil
	case "noise_threshold":
		var n string
		_ = json.Unmarshal(o["noise"], &n)
		nz, err := p.g.r.prog.noise(n)
		if err != nil {
			return nil, err
		}
		var lo, hi float64
		_ = json.Unmarshal(o["min_threshold"], &lo)
		_ = json.Unmarshal(o["max_threshold"], &hi)
		return noiseThresholdCond{n: nz, lo: lo, hi: hi, is3D: flag("is_3d")}, nil
	case "vertical_gradient":
		var n string
		_ = json.Unmarshal(o["random_name"], &n)
		return verticalGradientCond{split: p.g.r.prog.split.byName(n).splitter(),
			trueAt: parseAnchor(o["true_at_and_below"]), falseAt: parseAnchor(o["false_at_and_above"])}, nil
	case "temperature":
		return falseCond{}, nil
	}
	return nil, fmt.Errorf("unknown material condition type %q", typ)
}

type falseCond struct{}

func (falseCond) test(*surfaceCtx) bool { return false }

// javaBlock returns the Helix block for a Java Edition block name used by
// the surface rules, or nil for one Helix does not have.
func javaBlock(name string) world.Block {
	switch strings.TrimPrefix(name, "minecraft:") {
	case "stone":
		return block.Stone{}
	case "grass_block":
		return block.Grass{}
	case "dirt":
		return block.Dirt{}
	case "coarse_dirt":
		return block.Dirt{Coarse: true}
	case "podzol":
		return block.Podzol{}
	case "mycelium":
		return block.Mycelium{}
	case "mud":
		return block.Mud{}
	case "gravel":
		return block.Gravel{}
	case "sand":
		return block.Sand{}
	case "red_sand":
		return block.Sand{Red: true}
	case "sandstone":
		return block.Sandstone{Type: block.NormalSandstone()}
	case "red_sandstone":
		return block.Sandstone{Type: block.NormalSandstone(), Red: true}
	case "snow_block":
		return block.Snow{}
	case "ice":
		return block.Ice{}
	case "packed_ice":
		return block.PackedIce{}
	case "calcite":
		return block.Calcite{}
	case "terracotta":
		return block.Terracotta{}
	case "orange_terracotta":
		return block.StainedTerracotta{Colour: item.ColourOrange()}
	case "white_terracotta":
		return block.StainedTerracotta{Colour: item.ColourWhite()}
	case "bedrock":
		return block.Bedrock{}
	case "deepslate":
		return block.Deepslate{Type: block.NormalDeepslate(), Axis: cube.Y}
	case "tuff":
		return block.Tuff{}
	case "granite":
		return block.Granite{}
	case "water":
		return block.Water{Still: true, Depth: 8}
	case "lava":
		return block.Lava{Still: true, Depth: 8}
	case "air":
		return block.Air{}
	case "cinnabar":
		return block.Cinnabar{}
	case "sulfur":
		return block.Sulfur{}
	}
	return nil
}
