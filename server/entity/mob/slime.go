package mob

import "github.com/helix-bedrock/helix/server/block/cube"

// Slimes are the one mob whose BDS spawn rule says almost nothing: where
// they spawn is decided by BDS's own code. Outside swamps they spawn only
// in slime chunks below y=40. Swamp slimes (by moon phase and light) are not
// done yet.
//
// A chunk is a slime chunk when the first output of a 32-bit Mersenne
// Twister seeded with (x * 0x1f1f1f1f) ^ z is divisible by 10, as Bedrock
// Edition decides it (the algorithm as published by the Bedrock community,
// for example the Minecraft Wiki's "Slime" page, not checked against BDS).
func slimeChunk(x, z int32) bool {
	return mt19937First(uint32(x)*0x1f1f1f1f^uint32(z))%10 == 0
}

// mt19937First returns the first output of MT19937 seeded with seed.
func mt19937First(seed uint32) uint32 {
	var mt [398]uint32
	mt[0] = seed
	for i := uint32(1); i < 398; i++ {
		mt[i] = 1812433253*(mt[i-1]^(mt[i-1]>>30)) + i
	}
	y := (mt[0] & 0x80000000) | (mt[1] & 0x7fffffff)
	v := mt[397] ^ (y >> 1)
	if y&1 != 0 {
		v ^= 0x9908b0df
	}
	v ^= v >> 11
	v ^= (v << 7) & 0x9d2c5680
	v ^= (v << 15) & 0xefc60000
	v ^= v >> 18
	return v
}

// slimeMaySpawn applies BDS's coded slime rule to a spot.
func slimeMaySpawn(pos cube.Pos) bool {
	return pos[1] < 40 && slimeChunk(int32(pos[0]>>4), int32(pos[2]>>4))
}
