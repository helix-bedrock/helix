package mob

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/helix-bedrock/helix/registry"
)

// TestOracleBlockFacts compares the goal block facts with BDS 1.26.60's, as
// the component oracle recorded them for every cell of its randpos arena:
// the ground test (material blocks motion and is not never-buildable, block
// type flag 0x40000), a non-empty collision shape, and Material::isSolid. It
// compares each block's default state, so a block placed in another state
// can differ in shape. The file holds BDS outputs and is never committed;
// the test is skipped unless ORACLE_CELLS names one.
func TestOracleBlockFacts(t *testing.T) {
	file := os.Getenv("ORACLE_CELLS")
	if file == "" {
		t.Skip("ORACLE_CELLS not set")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	s := registry.NewSetup(nil, "minecraft")
	if err := registry.Vanilla(s); err != nil {
		t.Fatal(err)
	}
	reg, err := s.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	b := NewBlocks(reg.Blocks)
	const size = 56
	seen := map[string]bool{}
	var bad []string
	for i := 0; i+size <= len(raw); i += size {
		r := raw[i : i+size]
		name := strings.TrimRight(string(r[:48]), "\x00")
		if seen[name] {
			continue
		}
		seen[name] = true
		id, ok := reg.Blocks.Block(name)
		if !ok {
			bad = append(bad, name+": not in the registry")
			continue
		}
		g := b.Goal(id)
		ground := r[49]&5 == 4 && r[50] != 0
		if g.Ground != ground || g.HasCollision != (r[51] != 0) || g.Solid != (r[49]&0x10 != 0) {
			bad = append(bad, name+": ours ground/collision/solid "+tf(g.Ground)+tf(g.HasCollision)+tf(g.Solid)+
				", bds "+tf(ground)+tf(r[51] != 0)+tf(r[49]&0x10 != 0))
		}
	}
	sort.Strings(bad)
	for _, s := range bad {
		t.Error(s)
	}
	t.Logf("%d block kinds, %d differ", len(seen), len(bad))
}

func tf(b bool) string {
	if b {
		return "T"
	}
	return "F"
}
