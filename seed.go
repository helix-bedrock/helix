package helix

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"unicode/utf8"
)

// seedSpace is the whitespace BDS trims from both ends of level-seed.
const seedSpace = " \t\n\v\f\r"

// seedHashMultiplier is the multiplier of the text seed hash.
const seedHashMultiplier = 31

// SeedFromText turns a level-seed value into a world seed as BDS does. The value is
// trimmed; a whole base-10 number that fits in 64 bits is the seed; any other text is
// hashed by its code points (h = h*31 + c in 32 bits, stopping at bad UTF-8) and the
// 32-bit hash, sign-extended, is the seed. ok is false for an empty value, which means
// a random seed.
func SeedFromText(s string) (seed int64, ok bool) {
	s = strings.Trim(s, seedSpace)
	if s == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, true
	}
	return int64(textSeedHash(s)), true
}

// textSeedHash hashes text by its code points, stopping at the first bad UTF-8.
func textSeedHash(s string) int32 {
	var h int32
	for s != "" {
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size <= 1 {
			break
		}
		h = h*seedHashMultiplier + r
		s = s[size:]
	}
	return h
}

// randomSeed is a new world's seed when none is set: any 64-bit value, as BDS draws.
func randomSeed() int64 {
	return int64(rand.Uint64()) //nolint:gosec // a seed, the bits as drawn
}
