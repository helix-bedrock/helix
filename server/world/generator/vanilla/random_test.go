package vanilla

import "testing"

// The expected values are Java Edition's, from Pumpkin's tests of the same
// generator (pumpkin-util/src/random/xoroshiro128.rs).

func TestMixStafford13(t *testing.T) {
	for in, want := range map[uint64]int64{0: 0, 1: 6238072747940578789, 64: -8456553050427055661, 253: -4045451768301188906} {
		if got := int64(mixStafford13(in)); got != want {
			t.Errorf("mix(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestXoroshiro(t *testing.T) {
	x := xoroshiroFromSeed(0)
	for _, want := range []int32{-160476802, 781697906, 653572596, 1337520923, -505875771} {
		if got := x.nextInt32(); got != want {
			t.Fatalf("nextInt32 = %d, want %d", got, want)
		}
	}
	x = xoroshiroFromSeed(0)
	for _, want := range []int64{3038984756725240190, -3694039286755638414, 4633751808701151732} {
		if got := x.nextInt64(); got != want {
			t.Fatalf("nextInt64 = %d, want %d", got, want)
		}
	}
	x = xoroshiroFromSeed(0)
	for _, want := range []float64{0.16474369376959186, 0.7997457290026366, 0.2511961888876212} {
		if got := x.nextFloat64(); got != want {
			t.Fatalf("nextFloat64 = %v, want %v", got, want)
		}
	}
	x = xoroshiroFromSeed(0)
	for _, want := range []int32{9, 1, 1, 3, 8, 9, 0, 3, 6, 3} {
		if got := x.nextIntN(10); got != want {
			t.Fatalf("nextIntN(10) = %d, want %d", got, want)
		}
	}
	for _, want := range []int32{9784805, 470346, 13560642, 7320226} {
		if got := x.nextIntN(0xFFFFFF); got != want {
			t.Fatalf("nextIntN(0xFFFFFF) = %d, want %d", got, want)
		}
	}
}
