package player

import (
	"testing"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/world/cube"
)

// loomPlayer has a loom open with a white banner, red dye and material in its slots.
func loomPlayer(t *testing.T, banner, dye, material ItemStack) *Player {
	t.Helper()
	p := testPlayer(t)
	craftGame(t, p, nil)
	p.window.open, p.window.kind = true, ContainerLoom
	p.inventory.ui[loomBannerSlot] = p.s.freshStack(banner)
	p.inventory.ui[loomDyeSlot] = p.s.freshStack(dye)
	p.inventory.ui[loomMaterialSlot] = p.s.freshStack(material)
	return p
}

// loomActions are what a client asks for to draw a pattern.
func loomActions(p *Player, pattern string) []protocol.StackRequestAction {
	return []protocol.StackRequestAction{
		&protocol.CraftLoomRecipeStackRequestAction{Pattern: pattern, TimesCrafted: 1},
		smithConsume(p, protocol.ContainerLoomInput, loomBannerSlot),
		smithConsume(p, protocol.ContainerLoomDye, loomDyeSlot),
		take(1, slotInfo(containerCreatedOutput, createdOutputSlot, 0), slotInfo(containerCombined, 1, 0)),
	}
}

var (
	whiteBanner = ItemStack{Name: bannerItem, Meta: 15, Count: 1}
	redDye      = ItemStack{Name: "minecraft:red_dye", Count: 2}
)

// TestLoom draws a border: one banner and one dye go, the pattern item stays.
func TestLoom(t *testing.T) {
	p := loomPlayer(t, whiteBanner, redDye, ItemStack{})
	if r := p.stackRequest(protocol.ItemStackRequest{RequestID: 1, Actions: loomActions(p, "bo")}); r.Status != protocol.ItemStackResponseStatusOK {
		t.Fatalf("refused: %d", r.Status)
	}
	got := p.inventory.main[1]
	if got.Name != bannerItem || got.Meta != 15 || got.Count != 1 {
		t.Fatalf("result %+v", got)
	}
	layers := p.s.banners().readPatterns(got.Tag[bannerPatternsTag], 6)
	if len(layers) != 1 || layers[0] != (BannerLayer{Pattern: "bo", Color: 1}) {
		t.Fatalf("patterns %+v", layers)
	}
	if !p.inventory.ui[loomBannerSlot].Empty() || p.inventory.ui[loomDyeSlot].Count != 1 {
		t.Fatalf("slots %+v %+v", p.inventory.ui[loomBannerSlot], p.inventory.ui[loomDyeSlot])
	}
}

// TestLoomRefuses checks what BDS's loom refuses.
func TestLoomRefuses(t *testing.T) {
	six := make([]any, 6)
	for i := range six {
		six[i] = map[string]any{bannerPatternTag: "bo", bannerColorTag: int32(1)}
	}
	creeper := ItemStack{Name: "minecraft:creeper_banner_pattern", Count: 1}
	cases := []struct {
		name                  string
		pattern               string
		banner, dye, material ItemStack
	}{
		{"unknown pattern", "nope", whiteBanner, redDye, ItemStack{}},
		{"needs its item", "cre", whiteBanner, redDye, ItemStack{}},
		{"wrong item", "sku", whiteBanner, redDye, creeper},
		{"item for a dye pattern", "bo", whiteBanner, redDye, creeper},
		{"ink sac is no dye", "bo", whiteBanner, ItemStack{Name: "minecraft:ink_sac", Count: 1}, ItemStack{}},
		{"ominous", "bo", withTag(whiteBanner, bannerTypeTag, BannerOminous), redDye, ItemStack{}},
		{"full", "bo", withTag(whiteBanner, bannerPatternsTag, six), redDye, ItemStack{}},
	}
	for _, c := range cases {
		p := loomPlayer(t, c.banner, c.dye, c.material)
		in := LoomInput{Pattern: c.pattern, Times: 1, Banner: c.banner, Dye: c.dye, Material: c.material}
		if got := vanillaLoom(p, in); !got.Empty() {
			t.Errorf("%s: made %+v", c.name, got)
		}
	}
	p := loomPlayer(t, whiteBanner, redDye, creeper)
	if got := vanillaLoom(p, LoomInput{Pattern: "cre", Times: 1, Banner: whiteBanner, Dye: redDye, Material: creeper}); got.Empty() {
		t.Error("creeper with its item refused")
	}
}

// TestBannerRoundTrip saves and loads a banner, and turns it into an item and back.
func TestBannerRoundTrip(t *testing.T) {
	p := testPlayer(t)
	craftGame(t, p, nil)
	b := Banner{Base: 4, Type: BannerNormal, Patterns: []BannerLayer{{"bo", 1}, {"cre", 15}}}
	e := &bannerEntity{Banner: b}
	pos := cube.Pos{1, 2, 3}
	saved := e.SaveData(pos)
	if saved["id"] != bannerEntityID || saved[bannerBaseTag] != int32(4) || saved[bannerTypeTag] != int32(0) {
		t.Fatalf("saved %+v", saved)
	}
	back := p.s.loadBanner(saved)
	if back.Base != 4 || len(back.Patterns) != 2 || back.Patterns[1] != (BannerLayer{"cre", 15}) {
		t.Fatalf("loaded %+v", back.Banner)
	}
	item := bannerItemOf(back, pos)
	if item.Meta != 4 || item.Tag[bannerBaseTag] != nil || item.Tag["x"] != nil {
		t.Fatalf("item %+v", item)
	}
	if got := p.s.bannerOf(item); got.Base != 4 || len(got.Patterns) != 2 {
		t.Fatalf("placed %+v", got)
	}
	if empty := (&bannerEntity{}).SaveData(pos); empty[bannerPatternsTag] != nil {
		t.Fatal("an empty banner saved a Patterns list")
	}
	if r := bannerRotation(0); r != 8 {
		t.Fatalf("rotation facing south: %d", r)
	}
}
