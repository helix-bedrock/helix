package vanilla

import (
	"testing"

	"github.com/helix-bedrock/helix/server/world"
)

func TestBiomeTreeLoads(t *testing.T) {
	tree, err := loadBiomeTree("overworld")
	if err != nil {
		t.Fatal(err)
	}
	r, err := newRouter(0, "overworld")
	if err != nil {
		t.Fatal(err)
	}
	c := r.prog.newCtx()
	var last *biomeNode
	seen := map[string]int{}
	for x := int32(-2048); x < 2048; x += 64 {
		for z := int32(-2048); z < 2048; z += 64 {
			p := r.climateAt(c, x, 64, z)
			seen[tree.search(&p, &last).String()]++
		}
	}
	t.Logf("%d biomes at y=64 over 4096 blocks: %v", len(seen), seen)
	if len(seen) < 10 {
		t.Fatalf("only %d biomes", len(seen))
	}
	_ = world.Overworld
}
