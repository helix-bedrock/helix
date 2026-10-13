package vanilla

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Density functions, read from Java Edition's worldgen data the way
// Pumpkin's code generator reads it (tools/pumpkin-codegen/src/
// noise_router.rs, parse_vanilla_df) and evaluated as Pumpkin evaluates
// them.

// node is a density function. Nodes are shared and never change; what
// they cache lives in an evalCtx.
type node interface {
	eval(c *evalCtx, x, y, z int32) float64
}

// evalCtx is the state of evaluating density functions for one chunk:
// caches and the last biome search result. It is used by one goroutine.
type evalCtx struct {
	cache  []cacheSlot
	interp []interpSlot
}

type cacheSlot struct {
	x, y, z int32
	set     bool
	v       float64
}

type interpSlot struct {
	x, y, z int32
	set     bool
	corner  [8]float64
}

func (p *program) newCtx() *evalCtx {
	return &evalCtx{cache: make([]cacheSlot, p.caches), interp: make([]interpSlot, p.interps)}
}

// program holds the density functions of one generator.
type program struct {
	data    map[string]json.RawMessage
	named   map[string]node
	noises  map[string]*normalNoise
	split   splitter
	seed    uint64
	caches  int
	interps int
	blended *blendedNoise
}

type constant float64

func (k constant) eval(*evalCtx, int32, int32, int32) float64 { return float64(k) }

type gradient struct {
	axis               int
	from, to           int32
	fromValue, toValue float64
	tiling             string
}

func (g gradient) eval(_ *evalCtx, x, y, z int32) float64 {
	coord := [3]int32{x, y, z}[g.axis]
	rng := g.to - g.from
	factor := (g.toValue - g.fromValue) / float64(rng)
	switch g.tiling {
	case "repeat":
		return g.fromValue + float64(floorMod(coord-g.from, rng))*factor
	case "mirrored_repeat":
		rel := coord - g.from
		tile := floorDiv(rel, rng)
		local := rel - tile*rng
		if tile&1 == 0 {
			return g.fromValue + float64(local)*factor
		}
		return g.fromValue + float64(rng-local)*factor
	}
	lo, hi := min(g.from, g.to), max(g.from, g.to)
	return g.fromValue + float64(min(max(coord, lo), hi)-g.from)*factor
}

type binaryOp struct {
	op   string
	a, b node
}

func (n binaryOp) eval(c *evalCtx, x, y, z int32) float64 {
	a := n.a.eval(c, x, y, z)
	switch n.op {
	case "add":
		return a + n.b.eval(c, x, y, z)
	case "sub":
		return a - n.b.eval(c, x, y, z)
	case "mul":
		if a == 0 {
			return 0
		}
		return a * n.b.eval(c, x, y, z)
	case "div":
		if a == 0 {
			return 0
		}
		b := n.b.eval(c, x, y, z)
		if b == 0 {
			return 0
		}
		return a / b
	case "min":
		return math.Min(a, n.b.eval(c, x, y, z))
	case "max":
		return math.Max(a, n.b.eval(c, x, y, z))
	case "pow":
		return math.Pow(a, n.b.eval(c, x, y, z))
	}
	return 0
}

type unary struct {
	op string
	in node
}

func (n unary) eval(c *evalCtx, x, y, z int32) float64 {
	d := n.in.eval(c, x, y, z)
	switch n.op {
	case "abs":
		return math.Abs(d)
	case "square":
		return d * d
	case "cube":
		return d * d * d
	case "half_negative":
		if d > 0 {
			return d
		}
		return d * 0.5
	case "quarter_negative":
		if d > 0 {
			return d
		}
		return d * 0.25
	case "squeeze":
		k := clamp(d, -1, 1)
		return k/2 - k*k*k/24
	case "invert", "reciprocal":
		if d == 0 {
			return math.Inf(1)
		}
		return 1 / d
	case "negate":
		return -d
	case "sqrt":
		return math.Sqrt(d)
	case "log":
		return math.Log(d)
	case "sign":
		switch {
		case d > 0:
			return 1
		case d < 0:
			return -1
		}
		return 0
	}
	return d
}

type clampNode struct {
	in     node
	lo, hi float64
}

func (n clampNode) eval(c *evalCtx, x, y, z int32) float64 {
	return clamp(n.in.eval(c, x, y, z), n.lo, n.hi)
}

type lerpNode struct{ alpha, first, second node }

func (n lerpNode) eval(c *evalCtx, x, y, z int32) float64 {
	a, f, s := n.alpha.eval(c, x, y, z), n.first.eval(c, x, y, z), n.second.eval(c, x, y, z)
	return f + a*(s-f)
}

type rangeChoice struct {
	in, whenIn, whenOut node
	lo, hi              float64
}

func (n rangeChoice) eval(c *evalCtx, x, y, z int32) float64 {
	v := n.in.eval(c, x, y, z)
	if v >= n.lo && v < n.hi {
		return n.whenIn.eval(c, x, y, z)
	}
	return n.whenOut.eval(c, x, y, z)
}

type intervalSelect struct {
	in         node
	thresholds []float64
	funcs      []node
}

func (n intervalSelect) eval(c *evalCtx, x, y, z int32) float64 {
	v := n.in.eval(c, x, y, z)
	i := sort.Search(len(n.thresholds), func(i int) bool { return v < n.thresholds[i] })
	if i >= len(n.funcs) {
		i = len(n.funcs) - 1
	}
	return n.funcs[i].eval(c, x, y, z)
}

type slice struct {
	axis  int
	coord int32
	in    node
}

func (n slice) eval(c *evalCtx, x, y, z int32) float64 {
	p := [3]int32{x, y, z}
	p[n.axis] = n.coord
	return n.in.eval(c, p[0], p[1], p[2])
}

type distanceToPoint struct {
	p      [3]int32
	metric string
}

func (n distanceToPoint) eval(_ *evalCtx, x, y, z int32) float64 {
	dx, dy, dz := float64(x-n.p[0]), float64(y-n.p[1]), float64(z-n.p[2])
	switch n.metric {
	case "euclidean_squared":
		return dx*dx + dy*dy + dz*dz
	case "manhattan":
		return math.Abs(dx) + math.Abs(dy) + math.Abs(dz)
	case "chebyshev":
		return math.Max(math.Abs(dx), math.Max(math.Abs(dy), math.Abs(dz)))
	}
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

type noiseNode struct {
	n          *normalNoise
	xz, y      float64
	sx, sy, sz node
	shifted    bool
}

func (n noiseNode) eval(c *evalCtx, x, y, z int32) float64 {
	if !n.shifted {
		return n.n.get(float64(x)*n.xz, float64(y)*n.y, float64(z)*n.xz)
	}
	return n.n.get(float64(x)*n.xz+n.sx.eval(c, x, y, z), float64(y)*n.y+n.sy.eval(c, x, y, z), float64(z)*n.xz+n.sz.eval(c, x, y, z))
}

// shiftA and shiftB displace other noises, Pumpkin's ShiftA and ShiftB.
type shift struct {
	n *normalNoise
	b bool
}

func (s shift) eval(_ *evalCtx, x, _, z int32) float64 {
	if s.b {
		return s.n.get(float64(z)*0.25, float64(x)*0.25, 0) * 4
	}
	return s.n.get(float64(x)*0.25, 0, float64(z)*0.25) * 4
}

type blendedNode struct{ b *blendedNoise }

func (n blendedNode) eval(_ *evalCtx, x, y, z int32) float64 {
	return n.b.get(float64(x), float64(y), float64(z))
}

// cache remembers its input's value at the last position.
type cache struct {
	in   node
	slot int
}

func (n cache) eval(c *evalCtx, x, y, z int32) float64 {
	s := &c.cache[n.slot]
	if s.set && s.x == x && s.y == y && s.z == z {
		return s.v
	}
	v := n.in.eval(c, x, y, z)
	*s = cacheSlot{x: x, y: y, z: z, set: true, v: v}
	return v
}

// interpolated samples its input at the corners of the cell a position is
// in and interpolates between them, as Java's noise chunk does.
type interpolated struct {
	in      node
	cxz, cy int32
	slot    int
}

func (n interpolated) eval(c *evalCtx, x, y, z int32) float64 {
	rx, ry, rz := floorMod(x, n.cxz), floorMod(y, n.cy), floorMod(z, n.cxz)
	if rx == 0 && ry == 0 && rz == 0 {
		return n.in.eval(c, x, y, z)
	}
	ox, oy, oz := x-rx, y-ry, z-rz
	s := &c.interp[n.slot]
	if !s.set || s.x != ox || s.y != oy || s.z != oz {
		s.x, s.y, s.z, s.set = ox, oy, oz, true
		i := 0
		for dz := range int32(2) {
			for dy := range int32(2) {
				for dx := range int32(2) {
					s.corner[i] = n.in.eval(c, ox+dx*n.cxz, oy+dy*n.cy, oz+dz*n.cxz)
					i++
				}
			}
		}
	}
	k := s.corner
	return lerp3(float64(rx)/float64(n.cxz), float64(ry)/float64(n.cy), float64(rz)/float64(n.cxz),
		k[0], k[1], k[2], k[3], k[4], k[5], k[6], k[7])
}

type findTopSurface struct {
	density, upper node
	lower, cell    int32
}

func (n findTopSurface) eval(c *evalCtx, x, _, z int32) float64 {
	upper := n.upper.eval(c, x, 0, z)
	top := int32(math.Floor(upper/float64(n.cell))) * n.cell
	if top <= n.lower {
		return float64(n.lower)
	}
	for y := top; y >= n.lower; y -= n.cell {
		if n.density.eval(c, x, y, z) > 0 {
			return float64(y)
		}
	}
	return float64(n.lower)
}

// splineNode is a cubic spline over a location function.
type splineNode struct{ s *spline }

func (n splineNode) eval(c *evalCtx, x, y, z int32) float64 { return n.s.eval(c, x, y, z) }

type spline struct {
	fixed      bool
	value      float64
	location   node
	locations  []float64
	values     []*spline
	derivative []float64
}

func (s *spline) eval(c *evalCtx, x, y, z int32) float64 {
	if s.fixed {
		return s.value
	}
	loc := float64(float32(s.location.eval(c, x, y, z)))
	n := len(s.locations)
	i := sort.Search(n, func(i int) bool { return loc < s.locations[i] })
	if i == 0 {
		return s.values[0].eval(c, x, y, z) + s.derivative[0]*(loc-s.locations[0])
	}
	if i == n {
		return s.values[n-1].eval(c, x, y, z) + s.derivative[n-1]*(loc-s.locations[n-1])
	}
	x0, x1 := s.locations[i-1], s.locations[i]
	v0, v1 := s.values[i-1].eval(c, x, y, z), s.values[i].eval(c, x, y, z)
	d0, d1 := s.derivative[i-1], s.derivative[i]
	t := (loc - x0) / (x1 - x0)
	h00 := (1 + 2*t) * (1 - t) * (1 - t)
	h10 := t * (1 - t) * (1 - t)
	h01 := t * t * (3 - 2*t)
	h11 := t * t * (t - 1)
	return h00*v0 + h10*d0*(x1-x0) + h01*v1 + h11*d1*(x1-x0)
}

// parse turns a density function in JSON into a node. Strings name other
// density functions, which are parsed once and shared.
func (p *program) parse(raw json.RawMessage) (node, error) {
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return constant(f), nil
	}
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return p.named_(name)
	}
	var o map[string]json.RawMessage
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, fmt.Errorf("density function: %w", err)
	}
	var typ string
	_ = json.Unmarshal(o["type"], &typ)
	typ = strings.TrimPrefix(typ, "minecraft:")
	sub := func(keys ...string) (node, error) {
		for _, k := range keys {
			if v, ok := o[k]; ok {
				return p.parse(v)
			}
		}
		return nil, fmt.Errorf("%s: missing %s", typ, keys[0])
	}
	num := func(def float64, keys ...string) float64 {
		for _, k := range keys {
			var v float64
			if json.Unmarshal(o[k], &v) == nil {
				return v
			}
		}
		return def
	}
	str := func(key, def string) string {
		var v string
		if json.Unmarshal(o[key], &v) == nil {
			return v
		}
		return def
	}
	axis := map[string]int{"x": 0, "y": 1, "z": 2}
	switch typ {
	case "constant":
		return constant(num(0, "value", "argument")), nil
	case "y_clamped_gradient":
		return gradient{axis: 1, from: int32(num(-64, "from_y")), to: int32(num(320, "to_y")),
			fromValue: num(0, "from_value"), toValue: num(0, "to_value")}, nil
	case "gradient":
		return gradient{axis: axis[strings.ToLower(str("axis", "y"))], from: int32(num(0, "from_coordinate", "from_y")),
			to: int32(num(0, "to_coordinate", "to_y")), fromValue: num(0, "from_value"), toValue: num(0, "to_value"),
			tiling: str("tiling", "clamp_to_edge")}, nil
	case "distance_to_point":
		var pt [3]int32
		_ = json.Unmarshal(o["point"], &pt)
		return distanceToPoint{p: pt, metric: str("metric", "euclidean")}, nil
	case "lerp":
		a, err := sub("alpha")
		if err != nil {
			return nil, err
		}
		f, err := sub("first")
		if err != nil {
			return nil, err
		}
		s, err := sub("second")
		if err != nil {
			return nil, err
		}
		return lerpNode{a, f, s}, nil
	case "slice":
		in, err := sub("input")
		if err != nil {
			return nil, err
		}
		return slice{axis: axis[strings.ToLower(str("axis", "y"))], coord: int32(num(0, "coordinate")), in: in}, nil
	case "old_blended_noise":
		if p.blended == nil {
			bp := blendedParams{XZScale: num(0.25, "xz_scale"), YScale: num(0.125, "y_scale"), XZFactor: num(80, "xz_factor"),
				YFactor: num(160, "y_factor"), SmearScaleMultiplier: num(8, "smear_scale_multiplier")}
			p.blended = newBlendedNoise(p.split.byName("minecraft:terrain"), bp)
		}
		return blendedNode{p.blended}, nil
	case "add", "mul", "min", "max", "sub", "div", "pow":
		a, err := sub("left", "argument1", "base")
		if err != nil {
			return nil, err
		}
		b, err := sub("right", "argument2", "exponent")
		if err != nil {
			return nil, err
		}
		return binaryOp{op: typ, a: a, b: b}, nil
	case "abs", "square", "cube", "half_negative", "quarter_negative", "squeeze", "invert", "reciprocal", "negate", "sqrt", "log", "sign":
		in, err := sub("argument", "input")
		if err != nil {
			return nil, err
		}
		return unary{op: typ, in: in}, nil
	case "clamp":
		in, err := sub("input", "argument")
		if err != nil {
			return nil, err
		}
		return clampNode{in: in, lo: num(math.Inf(-1), "min", "min_value"), hi: num(math.Inf(1), "max", "max_value")}, nil
	case "range_choice":
		in, err := sub("input")
		if err != nil {
			return nil, err
		}
		wi, err := sub("when_in_range")
		if err != nil {
			return nil, err
		}
		wo, err := sub("when_out_of_range")
		if err != nil {
			return nil, err
		}
		return rangeChoice{in: in, whenIn: wi, whenOut: wo, lo: num(math.Inf(-1), "min_inclusive"), hi: num(math.Inf(1), "max_exclusive")}, nil
	case "interval_select":
		in, err := sub("input")
		if err != nil {
			return nil, err
		}
		var th []float64
		var fs []json.RawMessage
		_ = json.Unmarshal(o["thresholds"], &th)
		_ = json.Unmarshal(o["functions"], &fs)
		n := intervalSelect{in: in}
		for _, t := range th {
			n.thresholds = append(n.thresholds, float64(float32(t)))
		}
		for _, f := range fs {
			fn, err := p.parse(f)
			if err != nil {
				return nil, err
			}
			n.funcs = append(n.funcs, fn)
		}
		if len(n.funcs) == 0 {
			return nil, fmt.Errorf("interval_select: no functions")
		}
		return n, nil
	case "spline":
		s, err := p.parseSpline(o["spline"])
		if err != nil {
			return nil, err
		}
		return splineNode{s}, nil
	case "noise", "shifted_noise":
		var noiseName string
		_ = json.Unmarshal(o["noise"], &noiseName)
		nz, err := p.noise(noiseName)
		if err != nil {
			return nil, err
		}
		n := noiseNode{n: nz, xz: num(1, "xz_scale"), y: num(1, "y_scale")}
		_, hasX := o["shift_x"]
		_, hasY := o["shift_y"]
		_, hasZ := o["shift_z"]
		if typ == "shifted_noise" || hasX || hasY || hasZ {
			n.shifted = true
			n.sx, n.sy, n.sz = constant(0), constant(0), constant(0)
			if hasX {
				if n.sx, err = p.parse(o["shift_x"]); err != nil {
					return nil, err
				}
			}
			if hasY {
				if n.sy, err = p.parse(o["shift_y"]); err != nil {
					return nil, err
				}
			}
			if hasZ {
				if n.sz, err = p.parse(o["shift_z"]); err != nil {
					return nil, err
				}
			}
		}
		return n, nil
	case "shift_a", "shift_b", "shift":
		var noiseName string
		if json.Unmarshal(o["noise"], &noiseName) != nil {
			_ = json.Unmarshal(o["argument"], &noiseName)
		}
		nz, err := p.noise(noiseName)
		if err != nil {
			return nil, err
		}
		return shift{n: nz, b: typ == "shift_b"}, nil
	case "blend_alpha":
		// No blending with old chunks: full alpha, no offset.
		return constant(1), nil
	case "blend_offset", "beardifier", "end_islands", "end_outer_islands":
		// Structures (beardifier) and the end are not generated yet.
		return constant(0), nil
	case "blend_density":
		return sub("input", "argument")
	case "cache", "flat_cache", "cache_flat", "cache_2d", "cache_once", "cache_all_in_cell":
		in, err := sub("input", "argument")
		if err != nil {
			return nil, err
		}
		p.caches++
		return cache{in: in, slot: p.caches - 1}, nil
	case "interpolated":
		in, err := sub("input", "argument")
		if err != nil {
			return nil, err
		}
		p.interps++
		return interpolated{in: in, cxz: int32(num(4, "cell_size_xz")), cy: int32(num(8, "cell_size_y")), slot: p.interps - 1}, nil
	case "find_top_surface", "weird_utility_density":
		d, err := sub("density")
		if err != nil {
			return nil, err
		}
		u, err := sub("upper_bound")
		if err != nil {
			return nil, err
		}
		return findTopSurface{density: d, upper: u, lower: int32(num(-64, "lower_bound")), cell: int32(num(8, "cell_height"))}, nil
	}
	return nil, fmt.Errorf("unknown density function type %q", typ)
}

func (p *program) parseSpline(raw json.RawMessage) (*spline, error) {
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return &spline{fixed: true, value: float64(float32(f))}, nil
	}
	var o map[string]json.RawMessage
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, fmt.Errorf("spline: %w", err)
	}
	if v, ok := o["value"]; ok {
		if _, hasPoints := o["points"]; !hasPoints {
			return p.parseSpline(v)
		}
	}
	loc, err := p.parse(o["coordinate"])
	if err != nil {
		return nil, err
	}
	var pts []struct {
		Location   float64         `json:"location"`
		Value      json.RawMessage `json:"value"`
		Derivative float64         `json:"derivative"`
	}
	if err := json.Unmarshal(o["points"], &pts); err != nil {
		return nil, fmt.Errorf("spline points: %w", err)
	}
	s := &spline{location: loc}
	for _, pt := range pts {
		v, err := p.parseSpline(pt.Value)
		if err != nil {
			return nil, err
		}
		s.locations = append(s.locations, float64(float32(pt.Location)))
		s.values = append(s.values, v)
		s.derivative = append(s.derivative, float64(float32(pt.Derivative)))
	}
	if len(s.locations) == 0 {
		return nil, fmt.Errorf("spline with no points")
	}
	return s, nil
}

// named_ returns the density function with a name, such as
// "minecraft:overworld/continents".
func (p *program) named_(name string) (node, error) {
	short := strings.TrimPrefix(name, "minecraft:")
	if n, ok := p.named[short]; ok {
		return n, nil
	}
	switch short {
	case "zero":
		return constant(0), nil
	case "y":
		return gradient{axis: 1, from: -4064, to: 4062, fromValue: -4064, toValue: 4062}, nil
	}
	raw, ok := p.data["density_function/"+short]
	if !ok {
		return nil, fmt.Errorf("unknown density function %q", name)
	}
	n, err := p.parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", short, err)
	}
	p.named[short] = n
	return n, nil
}

// noise returns the noise with a name, such as "minecraft:temperature",
// seeded for the world.
func (p *program) noise(name string) (*normalNoise, error) {
	short := strings.TrimPrefix(name, "minecraft:")
	if n, ok := p.noises[short]; ok {
		return n, nil
	}
	raw, ok := p.data["noise/"+short]
	if !ok {
		return nil, fmt.Errorf("unknown noise %q", name)
	}
	var params noiseParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("noise %s: %w", short, err)
	}
	n := newNormalNoise(p.split.byName("minecraft:"+short), params)
	p.noises[short] = n
	return n, nil
}
