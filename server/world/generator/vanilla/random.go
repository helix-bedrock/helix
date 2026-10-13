// Package vanilla generates vanilla terrain: the shape of the land, caves
// and aquifers, biomes and surface blocks (Helix).
//
// It is ported from Pumpkin (github.com/Pumpkin-MC/Pumpkin, GPL-3.0),
// crates/pumpkin-util/src/random, pumpkin-util/src/noise and
// pumpkin-world/src/generation, and reads Java Edition's world generation
// data from helix-data: since 1.18 Bedrock and Java Edition generate the
// same terrain and biomes for a seed. Pumpkin computes much of it in 32-bit
// floats for speed; this port uses 64-bit floats throughout, as Java does,
// and casts to 32 bits only where Java does.
//
// A Generator is safe to use from many goroutines at once: each chunk is
// generated with state of its own.
package vanilla

import (
	"crypto/md5"
	"encoding/binary"
	"math"
	"math/bits"
)

// xoroshiro is Java Edition's Xoroshiro128++ random source.
type xoroshiro struct {
	lo, hi uint64
}

func newXoroshiro(lo, hi uint64) *xoroshiro {
	if lo|hi == 0 {
		lo, hi = 0x9E3779B97F4A7C15, 0x6A09E667F3BCC909
	}
	return &xoroshiro{lo: lo, hi: hi}
}

// xoroshiroFromSeed seeds a xoroshiro from a world seed, mixing it as Java
// does.
func xoroshiroFromSeed(seed uint64) *xoroshiro {
	lo := seed ^ 0x6A09E667F3BCC909
	hi := lo + 0x9E3779B97F4A7C15
	return newXoroshiro(mixStafford13(lo), mixStafford13(hi))
}

func mixStafford13(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (x *xoroshiro) next() uint64 {
	l, m := x.lo, x.hi
	n := bits.RotateLeft64(l+m, 17) + l
	m ^= l
	x.lo = bits.RotateLeft64(l, 49) ^ m ^ (m << 21)
	x.hi = bits.RotateLeft64(m, 28)
	return n
}

func (x *xoroshiro) nextBits(n uint) uint64 { return x.next() >> (64 - n) }

func (x *xoroshiro) nextInt32() int32 { return int32(x.next()) }

func (x *xoroshiro) nextInt64() int64 { return int64(x.next()) }

// nextIntN returns a uniform int in [0, bound), as Java's Xoroshiro does.
func (x *xoroshiro) nextIntN(bound int32) int32 {
	l := uint64(uint32(x.nextInt32()))
	m := l * uint64(bound)
	n := m & 0xFFFFFFFF
	if n < uint64(bound) {
		i := uint64(uint32(-bound)) % uint64(bound)
		for n < i {
			l = uint64(uint32(x.nextInt32()))
			m = l * uint64(bound)
			n = m & 0xFFFFFFFF
		}
	}
	return int32(m >> 32)
}

func (x *xoroshiro) nextFloat64() float64 { return float64(x.nextBits(53)) * 0x1p-53 }

func (x *xoroshiro) nextFloat32() float32 { return float32(x.nextBits(24)) * 0x1p-24 }

func (x *xoroshiro) skip(n int) {
	for range n {
		x.next()
	}
}

// splitter derives independent random sources by name or position.
type splitter struct {
	lo, hi uint64
}

func (x *xoroshiro) splitter() splitter {
	return splitter{lo: x.next(), hi: x.next()}
}

// byName returns the random source for a name, such as
// "minecraft:temperature": its MD5 mixed with the splitter.
func (s splitter) byName(name string) *xoroshiro {
	sum := md5.Sum([]byte(name))
	return newXoroshiro(binary.BigEndian.Uint64(sum[:8])^s.lo, binary.BigEndian.Uint64(sum[8:])^s.hi)
}

// atPos returns the random source for a block position.
func (s splitter) atPos(x, y, z int32) *xoroshiro {
	return newXoroshiro(uint64(hashPos(x, y, z))^s.lo, s.hi)
}

// hashPos is Java's Mth.getSeed for a block position.
func hashPos(x, y, z int32) int64 {
	l := int64(x*3129871) ^ int64(z)*116129781 ^ int64(y)
	l = l*l*42317861 + l*11
	return l >> 16
}

// lerp, lerp2 and lerp3 are linear interpolations.
func lerp(t, a, b float64) float64 { return a + t*(b-a) }

func lerp2(dx, dy, x0y0, x1y0, x0y1, x1y1 float64) float64 {
	return lerp(dy, lerp(dx, x0y0, x1y0), lerp(dx, x0y1, x1y1))
}

func lerp3(dx, dy, dz, x0y0z0, x1y0z0, x0y1z0, x1y1z0, x0y0z1, x1y0z1, x0y1z1, x1y1z1 float64) float64 {
	return lerp(dz, lerp2(dx, dy, x0y0z0, x1y0z0, x0y1z0, x1y1z0), lerp2(dx, dy, x0y0z1, x1y0z1, x0y1z1, x1y1z1))
}

func smoothstep(x float64) float64 { return x * x * x * (x*(x*6-15) + 10) }

func floorDiv(a, b int32) int32 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func floorMod(a, b int32) int32 { return a - floorDiv(a, b)*b }

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
