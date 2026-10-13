package vanilla

import "math"

// Noise, ported from Pumpkin's pumpkin-util/src/noise/perlin.rs.

var gradients = [16][3]float64{
	{1, 1, 0}, {-1, 1, 0}, {1, -1, 0}, {-1, -1, 0},
	{1, 0, 1}, {-1, 0, 1}, {1, 0, -1}, {-1, 0, -1},
	{0, 1, 1}, {0, -1, 1}, {0, 1, -1}, {0, -1, -1},
	{1, 1, 0}, {0, -1, 1}, {-1, 1, 0}, {0, -1, -1},
}

const (
	roundOff          = 33554432.0
	noiseOffsetScale  = 256.0
	standardDeviation = 0.2702247831245211
	targetDeviation   = 0.3333333333333333
	inputFactor       = 1.0181268882175227
	persistence       = 0.5
	lacunarity        = 2.0
)

// halfRoundOff is the largest float64 below 2^24, as Pumpkin has it.
var halfRoundOff = math.Float64frombits(0x416F_FFFF_FFFF_FFFF)

// wrap keeps coordinates small enough that float64 stays precise.
func wrap(x float64) float64 {
	if x >= -halfRoundOff && x < halfRoundOff {
		return x
	}
	return x - math.Floor(x/roundOff+0.5)*roundOff
}

// perlin is Java's ImprovedNoise.
type perlin struct {
	perm       [256]uint8
	ox, oy, oz float64
}

func newPerlin(r *xoroshiro) *perlin {
	p := &perlin{ox: r.nextFloat64() * noiseOffsetScale, oy: r.nextFloat64() * noiseOffsetScale, oz: r.nextFloat64() * noiseOffsetScale}
	for i := range p.perm {
		p.perm[i] = uint8(i)
	}
	for i := range 256 {
		j := int(r.nextIntN(int32(256-i))) + i
		p.perm[i], p.perm[j] = p.perm[j], p.perm[i]
	}
	return p
}

func (p *perlin) permute(x int32) int32 { return int32(p.perm[x&0xFF]) }

func grad(hash int32, x, y, z float64) float64 {
	g := &gradients[hash&15]
	return g[0]*x + g[1]*y + g[2]*z
}

// sampleAndLerp samples the lattice cell at (x, y, z) with the relative
// position given; ryOrig is the y used for the smoothing, which differs from
// ry for smeared noise.
func (p *perlin) sampleAndLerp(x, y, z int32, rx, ry, rz, ryOrig float64) float64 {
	x0, x1 := p.permute(x), p.permute(x+1)
	xy00, xy01 := p.permute(x0+y), p.permute(x0+y+1)
	xy10, xy11 := p.permute(x1+y), p.permute(x1+y+1)
	d000 := grad(p.permute(xy00+z), rx, ry, rz)
	d100 := grad(p.permute(xy10+z), rx-1, ry, rz)
	d010 := grad(p.permute(xy01+z), rx, ry-1, rz)
	d110 := grad(p.permute(xy11+z), rx-1, ry-1, rz)
	d001 := grad(p.permute(xy00+z+1), rx, ry, rz-1)
	d101 := grad(p.permute(xy10+z+1), rx-1, ry, rz-1)
	d011 := grad(p.permute(xy01+z+1), rx, ry-1, rz-1)
	d111 := grad(p.permute(xy11+z+1), rx-1, ry-1, rz-1)
	return lerp3(smoothstep(rx), smoothstep(ryOrig), smoothstep(rz), d000, d100, d010, d110, d001, d101, d011, d111)
}

func (p *perlin) get(x, y, z float64) float64 {
	x, y, z = wrap(x)+p.ox, wrap(y)+p.oy, wrap(z)+p.oz
	fx, fy, fz := math.Floor(x), math.Floor(y), math.Floor(z)
	rx, ry, rz := x-fx, y-fy, z-fz
	return p.sampleAndLerp(int32(fx), int32(fy), int32(fz), rx, ry, rz, ry)
}

// smearedPerlin is the y-smeared noise of the old blended terrain noise.
type smearedPerlin struct {
	p     *perlin
	fudge float64
}

const shiftUpEpsilon = float64(float32(1e-7))

func (s *smearedPerlin) get(x, origY, z float64) float64 {
	p := s.p
	x, y, z := wrap(x)+p.ox, wrap(origY)+p.oy, wrap(z)+p.oz
	fx, fy, fz := math.Floor(x), math.Floor(y), math.Floor(z)
	rx, ry, rz := x-fx, y-fy, z-fz
	limit := ry
	if origY >= 0 && origY < ry {
		limit = origY
	}
	fudged := ry - math.Floor(limit/s.fudge+shiftUpEpsilon)*s.fudge
	return p.sampleAndLerp(int32(fx), int32(fy), int32(fz), rx, fudged, rz, ry)
}

type layer[N interface {
	get(x, y, z float64) float64
}] struct {
	noise     N
	frequency float64
	amplitude float64
}

type stack[N interface {
	get(x, y, z float64) float64
}] []layer[N]

func (s stack[N]) get(x, y, z float64) float64 {
	var v float64
	for _, l := range s {
		v += l.amplitude * l.noise.get(x*l.frequency, y*l.frequency, z*l.frequency)
	}
	return v
}

type octave struct {
	index     int
	frequency float64
	amplitude float64
}

func amplitudeOf(mods []float64, i int) float64 {
	if len(mods) == 0 {
		return 1
	}
	return mods[i]
}

func buildOctaves(base int, baseAmp float64, count int, normalize bool, mods []float64) []octave {
	freq := math.Pow(lacunarity, float64(base))
	amp := baseAmp
	if normalize {
		amp = baseAmp * (math.Pow(persistence, -float64(count-1)) / (math.Pow(persistence, -float64(count)) - 1))
	}
	var out []octave
	for i := range count {
		if m := amplitudeOf(mods, i); m != 0 {
			out = append(out, octave{index: base + i, frequency: freq, amplitude: amp * m})
		}
		freq *= lacunarity
		amp *= persistence
	}
	return out
}

func normalizationFactor(target float64, octaves []octave) float64 {
	var variance float64
	for _, o := range octaves {
		d := standardDeviation * math.Abs(o.amplitude)
		variance += d * d
	}
	dev := math.Sqrt(variance)
	if dev == 0 {
		return 0
	}
	return target * targetDeviation / (dev * math.Sqrt2)
}

func parityNormalization(baseAmp float64, count int, mods []float64) float64 {
	lo, hi := math.MaxInt32, math.MinInt32
	for i := range count {
		if amplitudeOf(mods, i) != 0 {
			lo, hi = min(lo, i), max(hi, i)
		}
	}
	expected := 0.1 * (1 + 1/float64(hi-lo+1))
	return baseAmp * 0.5 * targetDeviation / expected
}

func parityBaseAmplitude(base int, amps []float64) float64 {
	octaves := buildOctaves(base, 1, len(amps), true, amps)
	var target float64
	for _, o := range octaves {
		target += math.Abs(o.amplitude)
	}
	n := normalizationFactor(target, octaves)
	if n == 0 {
		return 1
	}
	return parityNormalization(1, len(amps), amps) / n
}

// normalNoise is Java's NormalNoise: two octave noises summed, the second
// slightly stretched, scaled to a standard deviation of about a third.
type normalNoise struct {
	stack stack[*perlin]
	max   float64
}

// noiseParams are a noise's first octave and amplitudes, from
// worldgen/noise. Java Edition 26 writes them as base_octave,
// amplitude_modifiers and octave_count; base_amplitude is not read, as in
// Pumpkin, because the parity factors below give the same value.
type noiseParams struct {
	FirstOctave *int      `json:"firstOctave"`
	Amplitudes  []float64 `json:"amplitudes"`
	BaseOctave  *int      `json:"base_octave"`
	Modifiers   []float64 `json:"amplitude_modifiers"`
	OctaveCount int       `json:"octave_count"`
}

// resolved returns the first octave and the amplitudes.
func (p noiseParams) resolved() (int, []float64) {
	first := -7
	if p.FirstOctave != nil {
		first = *p.FirstOctave
	} else if p.BaseOctave != nil {
		first = *p.BaseOctave
	}
	amps := p.Amplitudes
	if amps == nil {
		amps = p.Modifiers
	}
	if amps == nil {
		amps = make([]float64, max(p.OctaveCount, 1))
		for i := range amps {
			amps[i] = 1
		}
	}
	return first, amps
}

func newNormalNoise(r *xoroshiro, p noiseParams) *normalNoise {
	firstOctave, amps := p.resolved()
	base := parityBaseAmplitude(firstOctave, amps)
	octaves := buildOctaves(firstOctave, base, len(amps), true, amps)
	var target float64
	for _, o := range octaves {
		target += math.Abs(o.amplitude)
	}
	norm := normalizationFactor(target, octaves)
	first, second := r.splitter(), r.splitter()
	n := &normalNoise{max: target * targetDeviation * 6}
	for _, o := range octaves {
		name := "octave_" + itoa(o.index)
		f := norm * o.amplitude
		n.stack = append(n.stack,
			layer[*perlin]{noise: newPerlin(first.byName(name)), frequency: o.frequency, amplitude: f},
			layer[*perlin]{noise: newPerlin(second.byName(name)), frequency: o.frequency * inputFactor, amplitude: f})
	}
	return n
}

func (n *normalNoise) get(x, y, z float64) float64 { return n.stack.get(x, y, z) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}

// blendedNoise is the old blended terrain noise ("old_blended_noise"),
// Pumpkin's InterpolatedNoiseSampler.
type blendedNoise struct {
	minLimit, maxLimit, main         stack[*smearedPerlin]
	xzMul, yMul, mainXZMul, mainYMul float64
}

const (
	blendedBaseScale        = 684.412
	blendedMainFactor       = 12.75
	blendedLimitFirstOctave = -15
	blendedMainFirstOctave  = -7
)

var blendedLimitFactor = float64(float32(0.99998474))

type blendedParams struct {
	XZScale              float64 `json:"xz_scale"`
	YScale               float64 `json:"y_scale"`
	XZFactor             float64 `json:"xz_factor"`
	YFactor              float64 `json:"y_factor"`
	SmearScaleMultiplier float64 `json:"smear_scale_multiplier"`
}

func newBlendedNoise(r *xoroshiro, p blendedParams) *blendedNoise {
	xz, y := blendedBaseScale*p.XZScale, blendedBaseScale*p.YScale
	limitSmear := y * p.SmearScaleMultiplier
	mainSmear := limitSmear / p.YFactor
	b := &blendedNoise{xzMul: xz, yMul: y, mainXZMul: xz / p.XZFactor, mainYMul: y / p.YFactor}
	b.minLimit = fbm(r, blendedLimitFirstOctave, limitSmear, blendedLimitFactor)
	b.maxLimit = fbm(r, blendedLimitFirstOctave, limitSmear, blendedLimitFactor)
	b.main = fbm(r, blendedMainFirstOctave, mainSmear, blendedMainFactor)
	return b
}

func fbm(r *xoroshiro, firstOctave int, smear, valueFactor float64) stack[*smearedPerlin] {
	octaves := -firstOctave + 1
	factor := 1.0
	vf := valueFactor / (math.Pow(2, float64(octaves)) - 1)
	s := make(stack[*smearedPerlin], 0, octaves)
	for range octaves {
		s = append(s, layer[*smearedPerlin]{noise: &smearedPerlin{p: newPerlin(r), fudge: smear * factor}, frequency: factor, amplitude: vf})
		factor /= 2
		vf *= 2
	}
	return s
}

func (b *blendedNoise) get(x, y, z float64) float64 {
	alpha := clamp(b.main.get(x*b.mainXZMul, y*b.mainYMul, z*b.mainXZMul)+0.5, 0, 1)
	if alpha == 0 {
		return b.minLimit.get(x*b.xzMul, y*b.yMul, z*b.xzMul)
	}
	if alpha == 1 {
		return b.maxLimit.get(x*b.xzMul, y*b.yMul, z*b.xzMul)
	}
	return lerp(alpha, b.minLimit.get(x*b.xzMul, y*b.yMul, z*b.xzMul), b.maxLimit.get(x*b.xzMul, y*b.yMul, z*b.xzMul))
}
