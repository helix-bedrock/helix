package vanilla

import (
	"encoding/json"
	"os"
	"testing"
)

// TestClimateMatchesJava compares the six climate values with Java
// Edition's for seed 0 (testdata/README.md).
func TestClimateMatchesJava(t *testing.T) {
	b, err := os.ReadFile("testdata/climate_seed0.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows [][9]int64
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	r, err := newRouter(0, "overworld")
	if err != nil {
		t.Fatal(err)
	}
	c := r.prog.newCtx()
	names := []string{"temperature", "vegetation", "continentalness", "erosion", "depth", "weirdness"}
	off, mismatches := map[string]int64{}, 0
	for _, row := range rows {
		got := r.climateAt(c, int32(row[0])*4, int32(row[1])*4, int32(row[2])*4)
		for i := range 6 {
			if d := got[i] - row[3+i]; d != 0 {
				mismatches++
				if d < 0 {
					d = -d
				}
				off[names[i]] = max(off[names[i]], d)
			}
		}
	}
	t.Logf("%d values, %d differ; largest difference per value (of 10000): %v", len(rows)*6, mismatches, off)
	for name, d := range off {
		// Java rounds through 32-bit floats in places this port does not:
		// allow the last digit to differ, nothing more.
		if d > 1 {
			t.Errorf("%s differs from Java by up to %d", name, d)
		}
	}
}
