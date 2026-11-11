package player

import (
	"maps"
	"math"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Banners as BDS 1.26.60 runs them (BannerBlockActor, BannerItem, BannerBlock). The research
// note is private.
const (
	bannerItem     = "minecraft:banner"
	standingBanner = "minecraft:standing_banner"
	wallBanner     = "minecraft:wall_banner"
	bannerEntityID = "Banner"
	// The tags BDS saves a banner in, on the block entity and on the item.
	bannerBaseTag     = "Base"
	bannerTypeTag     = "Type"
	bannerPatternsTag = "Patterns"
	bannerPatternTag  = "Pattern"
	bannerColorTag    = "Color"
	// The states of the two blocks.
	bannerRotationState = "ground_sign_direction"
	bannerFacingState   = "facing_direction"
	// bannerRotations is how many ways a standing banner turns.
	bannerRotations = 16
	// colorMask keeps an aux value to a colour.
	colorMask = 0xf
)

// The banner types BDS saves.
const (
	// BannerNormal is any banner made by players.
	BannerNormal int32 = 0
	// BannerOminous is the illager banner: no loom changes it.
	BannerOminous int32 = 1
)

// BannerLayer is one pattern on a banner: the pattern's ID as banners save it (such as
// "bo" for border) and its colour in dye order (black 0 to white 15).
type BannerLayer struct {
	Pattern string
	Color   uint8
}

// Banner is what a banner shows: its base colour in dye order, its type and its
// patterns, bottom first.
type Banner struct {
	Base     uint8
	Type     int32
	Patterns []BannerLayer
}

// BannerPattern is a pattern looms can draw: its name, the ID banners save, and the
// type of banner pattern item it needs, -1 for one drawn with a dye alone.
type BannerPattern struct {
	Name, ID string
	ItemType int16
}

// BannerTable is what banners and looms read: BDS 1.26.60's patterns in its order, how
// many patterns one banner holds, the dyes with their colour and the banner pattern
// items with their type. Change it in Setup through [Setup.Banners]; it is shared once
// the Host runs, so it must not change after.
type BannerTable struct {
	MaxPatterns  int
	Patterns     []BannerPattern
	Dyes         map[string]uint8
	PatternItems map[string]int16
	byID         map[string]int
}

// vanillaBanners reads helix-data's banner facts.
func vanillaBanners() (*BannerTable, error) {
	d, err := data.BannerData()
	if err != nil {
		return nil, err
	}
	t := &BannerTable{MaxPatterns: d.MaxPatterns, Dyes: d.Dyes, PatternItems: d.PatternItems}
	for _, p := range d.Patterns {
		t.Patterns = append(t.Patterns, BannerPattern{Name: p.Name, ID: p.ID, ItemType: p.ItemType})
	}
	return t, nil
}

// index builds the lookup by ID: the first pattern with an ID wins, as BDS searches.
func (t *BannerTable) index() {
	t.byID = make(map[string]int, len(t.Patterns))
	for i, p := range t.Patterns {
		if _, ok := t.byID[p.ID]; !ok {
			t.byID[p.ID] = i
		}
	}
}

// Pattern finds a pattern by the ID banners save.
func (t *BannerTable) Pattern(id string) (BannerPattern, bool) {
	if t.byID == nil {
		t.index()
	}
	i, ok := t.byID[id]
	if !ok {
		return BannerPattern{}, false
	}
	return t.Patterns[i], true
}

// Banners returns the banner table to change before the Host is made. Runs: Setup only.
func (s *Setup) Banners() (*BannerTable, error) {
	g := s.h.game
	if g.banners == nil {
		t, err := vanillaBanners()
		if err != nil {
			return nil, err
		}
		g.banners = t
	}
	return g.banners, nil
}

// bannerEntity is a banner's block entity (type 28 in BDS).
type bannerEntity struct {
	saved map[string]any
	Banner
}

// SaveData is what BDS's banner saves: Base, Type, and Patterns only when there are any.
func (e *bannerEntity) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(e.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = bannerEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	m[bannerBaseTag] = int32(e.Base)
	m[bannerTypeTag] = e.Type
	delete(m, bannerPatternsTag)
	if len(e.Patterns) > 0 {
		m[bannerPatternsTag] = patternList(e.Patterns)
	}
	return m
}

// NetworkData is the same: BDS sends a banner as it saves it.
func (e *bannerEntity) NetworkData(pos cube.Pos) map[string]any { return e.SaveData(pos) }

// patternList is patterns as BDS saves them.
func patternList(patterns []BannerLayer) []any {
	l := make([]any, 0, len(patterns))
	for _, p := range patterns {
		l = append(l, map[string]any{bannerPatternTag: p.Pattern, bannerColorTag: int32(p.Color)})
	}
	return l
}

// tagInt is an int tag of m, and whether it is there.
func tagInt(m map[string]any, key string) (int32, bool) {
	v, ok := m[key].(int32)
	return v, ok
}

// readPatterns is BDS's reading of a Patterns list: at most limit, each a compound with
// both a Pattern and a Color; an unknown pattern becomes the first in the table.
func (t *BannerTable) readPatterns(v any, limit int) []BannerLayer {
	var out []BannerLayer
	l, _ := v.([]any)
	for i, x := range l {
		if i >= limit {
			break
		}
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		id, hasID := m[bannerPatternTag].(string)
		color, hasColor := tagInt(m, bannerColorTag)
		if !hasID || !hasColor {
			continue
		}
		if _, known := t.Pattern(id); !known && len(t.Patterns) > 0 {
			id = t.Patterns[0].ID
		}
		out = append(out, BannerLayer{Pattern: id, Color: uint8(color)}) //nolint:gosec // BDS keeps the low byte
	}
	return out
}

// loadBanner brings back a saved banner as BDS loads one.
func (s *Host) loadBanner(saved map[string]any) *bannerEntity {
	t := s.banners()
	e := &bannerEntity{saved: maps.Clone(saved)}
	base, _ := tagInt(saved, bannerBaseTag)
	e.Base = uint8(base) //nolint:gosec // BDS keeps the low byte
	e.Type, _ = tagInt(saved, bannerTypeTag)
	e.Patterns = t.readPatterns(saved[bannerPatternsTag], t.MaxPatterns)
	for _, k := range [...]string{bannerBaseTag, bannerTypeTag, bannerPatternsTag} {
		delete(e.saved, k)
	}
	return e
}

// bannerOf is what a banner item puts on its block, as BDS's setItemValues: the Base tag
// or else the aux value, at most the table's patterns, and the Type tag.
func (s *Host) bannerOf(item ItemStack) Banner {
	t := s.banners()
	b := Banner{Base: uint8(item.Meta) & colorMask} //nolint:gosec // masked to a colour
	if base, ok := tagInt(item.Tag, bannerBaseTag); ok {
		b.Base = uint8(base) //nolint:gosec // BDS keeps the low byte
	}
	b.Type, _ = tagInt(item.Tag, bannerTypeTag)
	b.Patterns = t.readPatterns(item.Tag[bannerPatternsTag], t.MaxPatterns)
	return b
}

// bannerItemOf is the item a banner block entity gives back, as BDS makes it: one banner
// with the base colour as its aux value, and the saved data without the position, ID,
// isMovable and Base as its tag.
func bannerItemOf(e *bannerEntity, pos cube.Pos) ItemStack {
	tag := e.SaveData(pos)
	for _, k := range [...]string{"x", "y", "z", "id", "isMovable", bannerBaseTag} {
		delete(tag, k)
	}
	it := ItemStack{Name: bannerItem, Count: 1, Meta: int16(e.Base)}
	if len(tag) > 0 {
		it.Tag = tag
	}
	return it
}

// BannerAt returns the banner at pos. Runs: owner of the region.
func BannerAt(r *world.Region, pos cube.Pos) (Banner, bool) {
	if be, ok := r.BlockEntity(pos); ok {
		if b, isBanner := be.(*bannerEntity); isBanner {
			out := b.Banner
			out.Patterns = append([]BannerLayer(nil), b.Patterns...)
			return out, true
		}
	}
	return Banner{}, false
}

// SetBannerAt changes the banner at pos and shows it to everyone; false if there is
// none. Runs: owner of the region.
func SetBannerAt(r *world.Region, pos cube.Pos, b Banner) bool {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return false
	}
	e, isBanner := be.(*bannerEntity)
	if !isBanner {
		return false
	}
	e.Banner = b
	e.Patterns = append([]BannerLayer(nil), b.Patterns...)
	r.SaveBlockEntity(pos)
	r.SendBlockEntity(pos)
	return true
}

// noBanners stands in for a Host built without its table.
var noBanners = &BannerTable{}

// banners returns the Host's banner table.
func (s *Host) banners() *BannerTable {
	if s.game == nil || s.game.banners == nil {
		return noBanners
	}
	return s.game.banners
}

// bannerDrops gives a dropped banner what its block entity holds.
func bannerDrops(r *world.Region, pos cube.Pos, items []ItemStack) []ItemStack {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return items
	}
	e, isBanner := be.(*bannerEntity)
	if !isBanner {
		return items
	}
	for i := range items {
		if items[i].Name == bannerItem {
			n := items[i].Count
			items[i] = bannerItemOf(e, pos)
			items[i].Count = n
		}
	}
	return items
}

// isBannerBlock reports whether a block is a banner.
func isBannerBlock(name string) bool { return name == standingBanner || name == wallBanner }

// bannerRotation is the way a standing banner placed by someone looking along yaw faces.
func bannerRotation(yaw float32) int32 {
	return int32(math.Floor(float64((yaw+180)*bannerRotations/360+0.5))) & (bannerRotations - 1)
}

// placeBanner is BannerItem's useOn: never on a block's underside, only against a block
// of solid material; standing on top, else hanging on the side clicked.
func (p *Player) placeBanner(r *world.Region, clicked cube.Pos, face int32, held ItemStack) bool {
	f := cube.Face(face)
	if f == cube.FaceDown || !p.inReach(clicked) {
		return false
	}
	c, ok := r.Block(clicked, 0)
	if !ok || !p.s.reg.Blocks.Properties(c).SolidMaterial {
		return false
	}
	blocks := p.s.reg.Blocks
	pos := clicked.Side(f)
	var state uint32
	if f == cube.FaceUp {
		state, ok = blocks.StateID(standingBanner, map[string]any{bannerRotationState: bannerRotation(p.entity.Rotation().Yaw)})
	} else {
		state, ok = blocks.StateID(wallBanner, map[string]any{bannerFacingState: face})
	}
	if !ok {
		return false
	}
	if b, okAt := r.Block(pos, 0); !okAt || !p.s.game.replaceable(p, r, pos, b, state) || !p.s.attach.MayPlace(r, pos, state, f) || p.inside(pos, state) {
		return false
	}
	if g := p.s.game; g.placeEv.Has() {
		e := BlockPlaceEvent{Player: p, Pos: pos, Block: state}
		g.placeEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
	}
	if !r.SetBlock(pos, 0, state) {
		return false
	}
	if be, okBE := r.BlockEntity(pos); okBE {
		if e, isBanner := be.(*bannerEntity); isBanner {
			e.Banner = p.s.bannerOf(held)
			r.SaveBlockEntity(pos)
			r.SendBlockEntity(pos)
		}
	}
	r.UpdateNeighbours(pos)
	playSound(r, pos, p.s.blockSound(packet.SoundEventPlace, blockCenter(pos), state), nil)
	if !p.creative() {
		p.useHeld()
	}
	return true
}
