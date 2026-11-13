package player

import (
	"errors"
	"image/color"
	"math"
	"math/rand/v2"
	"strconv"
	"sync"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Maps, from BDS 1.26.60: EmptyMapItem::use (0xC985C20), MapItem's inventory tick
// (0xC8FDF90), the map render (0xC8FC7B0), MapItemSavedData (save 0xD75E9F0, origin
// 0xD75CF40, decorations 0xD760EE0, update packets 0xD763470) and the map manager
// (create 0xCDDD2E0, expand 0xCDDDE60, copy and lock 0xCDDDC20). The research note is
// private.
const (
	itemFilledMapName = "minecraft:filled_map"
	itemEmptyMapName  = "minecraft:empty_map"

	mapTagUUID      = "map_uuid"
	mapTagNameIndex = "map_name_index"
	mapTagScale     = "map_scale"
	mapTagPlayers   = "map_display_players"
	mapTagInit      = "map_is_init"

	// mapMetaLocator is an empty map that makes locator maps; mapMetaLocked a locked map,
	// mapMetaExplorer the first explorer map aux.
	mapMetaLocator  = 2
	mapMetaExplorer = 3
	mapMetaLocked   = 6

	// mapSize is a map's width and height in pixels; mapMaxScale its largest scale.
	mapSize     = 128
	mapPixels   = mapSize * mapSize
	mapHalf     = mapSize / 2
	mapMaxScale = 4
	// mapRenderTicks is how many inventory ticks a held map waits between redraws, after
	// starting again at a random 0 to mapRenderJitter-1.
	mapRenderTicks  = 20
	mapRenderJitter = 6
	// mapDecorationTicks is how often a holder is sent the decorations when no pixel changed.
	mapDecorationTicks = 5
	// mapSaveTicks is how often changed maps are saved.
	mapSaveTicks = 600

	// The decoration types BDS uses for players: on the map, at its edge, far away.
	mapDecorationPlayer       = 0
	mapDecorationPlayerOffMap = 6
	mapDecorationPlayerFar    = 13
	// mapOffMapLimit is how far off the map (in map pixels) a player still shows at the edge.
	mapOffMapLimit = 320
	// mapNetherNoise is BDS's nether pixel hash bit: stone where set, else dirt.
	mapNetherNoise = 0x100000

	gameModeSpectator = 6
)

// The shades of a pixel: darker, normal and lighter, BDS's 180, 220 and 255 out of 255.
var mapShades = [3]float32{0.7058824, 0.8627451, 1}

// BDS's nether map colours: dirt and stone.
var (
	mapNetherDirt  = mapColour{0.5921569, 0.42745098, 0.3019608, 1}
	mapNetherStone = mapColour{0.4392157, 0.4392157, 0.4392157, 1}
)

// mapData is one map's saved data, BDS's MapItemSavedData. Runs: under mu, from any region.
type mapData struct {
	mu        sync.Mutex
	id        int64
	parent    int64
	dim       mcdb.Dimension
	x, z      int32
	scale     uint8
	locked    bool
	unlimited bool
	explored  bool
	// pixels is each pixel as BDS keeps it: red, green, blue and alpha bytes, red lowest.
	pixels [mapPixels]uint32
	// kept is the saved decorations, written back as they were.
	kept []any
	// tracked is the players the map follows, by unique ID.
	tracked map[int64]*mapTracked
	// trackRound counts the tracking passes; a player missed by one is no longer followed.
	trackRound uint64
	// renderTick counts the holders' ticks to the next redraw.
	renderTick int32
	dirty      bool
}

// mapTracked is a player a map follows: its marker and the pixels it has not been sent.
type mapTracked struct {
	decoration protocol.MapDecoration
	shown      bool
	// changed is set with the rectangle of pixels the player has not been sent.
	changed        bool
	x0, z0, x1, z1 int32
	ticks          int32
	round          uint64
}

// mapStore holds the maps loaded from the world and made since, by ID.
type mapStore struct {
	mu    sync.Mutex
	store mcdb.WorldStore
	maps  map[int64]*mapData
}

func newMapStore(store mcdb.WorldStore) *mapStore {
	return &mapStore{store: store, maps: map[int64]*mapData{}}
}

// mapKey is a map's level key.
func mapKey(id int64) string { return mcdb.LevelPrefixMap + strconv.FormatInt(id, 10) }

// get returns the map with an ID, loading it from the world; nil if there is none.
func (s *Host) mapByID(id int64) *mapData {
	if id == -1 || s.maps == nil {
		return nil
	}
	st := s.maps
	st.mu.Lock()
	defer st.mu.Unlock()
	if d, ok := st.maps[id]; ok {
		return d
	}
	if st.store == nil {
		return nil
	}
	raw, err := st.store.LoadLevel(mapKey(id))
	if err != nil {
		if !errors.Is(err, mcdb.ErrNotFound) {
			s.log.Warn("load map", "id", id, "err", err)
		}
		return nil
	}
	d, err := decodeMap(id, raw)
	if err != nil {
		s.log.Warn("load map", "id", id, "err", err)
		return nil
	}
	st.maps[id] = d
	return d
}

// addMap stores a new map.
func (s *Host) addMap(d *mapData) {
	s.maps.mu.Lock()
	s.maps.maps[d.id] = d
	s.maps.mu.Unlock()
}

// newMapID is an ID no map has.
func (s *Host) newMapID() int64 {
	for {
		id := rand.Int64N(math.MaxInt64) //nolint:gosec // an ID, not a secret
		if s.mapByID(id) == nil {
			return id
		}
	}
}

// saveMaps saves every map changed since it was last saved. Runs: anywhere.
func (s *Host) saveMaps() {
	if s.maps == nil || s.maps.store == nil {
		return
	}
	s.maps.mu.Lock()
	all := make([]*mapData, 0, len(s.maps.maps))
	for _, d := range s.maps.maps {
		all = append(all, d)
	}
	s.maps.mu.Unlock()
	for _, d := range all {
		d.mu.Lock()
		if !d.dirty {
			d.mu.Unlock()
			continue
		}
		raw, err := d.encode()
		d.dirty = false
		d.mu.Unlock()
		if err == nil {
			err = s.maps.store.StoreLevel(mapKey(d.id), raw)
		}
		if err != nil {
			s.log.Error("save map", "id", d.id, "err", err)
		}
	}
}

// mapSave is a map as BDS saves it, in its order.
type mapSave struct {
	MapID             int64               `nbt:"mapId"`
	ParentMapID       int64               `nbt:"parentMapId"`
	Dimension         uint8               `nbt:"dimension"`
	XCenter           int32               `nbt:"xCenter"`
	ZCenter           int32               `nbt:"zCenter"`
	Scale             uint8               `nbt:"scale"`
	Width             int16               `nbt:"width"`
	Height            int16               `nbt:"height"`
	UnlimitedTracking uint8               `nbt:"unlimitedTracking"`
	Colors            [mapPixels * 4]byte `nbt:"colors"`
	FullyExplored     uint8               `nbt:"fullyExplored"`
	Decorations       []any               `nbt:"decorations"`
	MapLocked         uint8               `nbt:"mapLocked"`
}

// encode is the map's saved value. Runs: under mu.
func (d *mapData) encode() ([]byte, error) {
	v := mapSave{
		MapID: d.id, ParentMapID: d.parent, Dimension: uint8(d.dim), XCenter: d.x, ZCenter: d.z, //nolint:gosec // dimensions are 0 to 2
		Scale: d.scale, Width: mapSize, Height: mapSize, UnlimitedTracking: boolByte(d.unlimited),
		FullyExplored: boolByte(d.explored), Decorations: d.kept, MapLocked: boolByte(d.locked),
	}
	if v.Decorations == nil {
		v.Decorations = []any{}
	}
	for i, p := range d.pixels {
		v.Colors[4*i], v.Colors[4*i+1], v.Colors[4*i+2], v.Colors[4*i+3] = byte(p), byte(p>>8), byte(p>>16), byte(p>>24)
	}
	return nbt.MarshalEncoding(v, nbt.LittleEndian)
}

// decodeMap reads a saved map.
func decodeMap(id int64, raw []byte) (*mapData, error) {
	var m map[string]any
	if err := nbt.UnmarshalEncoding(raw, &m, nbt.LittleEndian); err != nil {
		return nil, err
	}
	d := &mapData{id: id, parent: -1, tracked: map[int64]*mapTracked{}}
	if v, ok := m["parentMapId"].(int64); ok {
		d.parent = v
	}
	if v, ok := m["dimension"].(uint8); ok {
		d.dim = mcdb.Dimension(v)
	}
	d.x, _ = m["xCenter"].(int32)
	d.z, _ = m["zCenter"].(int32)
	d.scale, _ = m["scale"].(uint8)
	d.scale = min(d.scale, mapMaxScale)
	b, _ := m["unlimitedTracking"].(uint8)
	d.unlimited = b != 0
	b, _ = m["fullyExplored"].(uint8)
	d.explored = b != 0
	b, _ = m["mapLocked"].(uint8)
	d.locked = b != 0
	d.kept, _ = m["decorations"].([]any)
	if c, ok := m["colors"].([mapPixels * 4]byte); ok {
		for i := range d.pixels {
			d.pixels[i] = uint32(c[4*i]) | uint32(c[4*i+1])<<8 | uint32(c[4*i+2])<<16 | uint32(c[4*i+3])<<24
		}
	}
	return d, nil
}

// setOrigin centres the map for a scale at a block, BDS's setOrigin for an endless world:
// the grid of maps of that scale, offset by 64.
func (d *mapData) setOrigin(scale uint8, x, z int32) {
	size := int32(mapSize) << scale
	half := size >> 1
	d.scale = scale
	d.x = int32(math.Floor(float64(x+64)/float64(size)))*size + half - 64
	d.z = int32(math.Floor(float64(z+64)/float64(size)))*size + half - 64
	d.dirty = true
}

// createMaps makes a new map at a block, with its larger maps (BDS's createMapSavedData): one
// map for every scale, each the parent of the one below, and returns the one of scale.
func (s *Host) createMaps(dim mcdb.Dimension, pos cube.Pos, scale uint8) *mapData {
	scale = min(scale, mapMaxScale)
	var want *mapData
	parent := int64(-1)
	for sc := int(mapMaxScale); sc >= 0; sc-- {
		d := &mapData{id: s.newMapID(), parent: parent, dim: dim, tracked: map[int64]*mapTracked{}}
		d.setOrigin(uint8(sc), int32(pos[0]), int32(pos[2])) //nolint:gosec // block X and Z fit int32
		s.addMap(d)
		parent = d.id
		if uint8(sc) == scale {
			want = d
		}
	}
	return want
}

// mapID is the ID a map item names, -1 without one.
func mapID(st ItemStack) int64 {
	if v, ok := st.Tag[mapTagUUID].(int64); ok {
		return v
	}
	return -1
}

// mapFlag is a byte tag of a map item.
func mapFlag(st ItemStack, key string) bool {
	v, _ := st.Tag[key].(uint8)
	return v != 0
}

// mapDimension is BDS's number of an open dimension, as maps keep it.
func (s *Host) mapDimension(d *world.Dimension) mcdb.Dimension {
	return mcdb.Dimension(s.dimensionID(d))
}

// useEmptyMap is BDS's EmptyMapItem::use: a filled map of the empty map's scale, centred on
// the player, its name numbered by the player's maps, a locator if the empty map was one.
func (p *Player) useEmptyMap(held ItemStack) bool {
	if held.Name != itemEmptyMapName || p.entity == nil {
		return false
	}
	r := p.entity.Region()
	if r == nil {
		return false
	}
	filled := ItemStack{Name: itemFilledMapName, Count: 1}
	if held.Meta == mapMetaLocator {
		filled = withTag(filled, mapTagPlayers, uint8(1))
	}
	if display, ok := held.Tag["display"].(map[string]any); ok {
		if name, ok := display["Name"].(string); ok {
			filled = withName(filled, name)
		}
	}
	p.mapIndex++
	filled = withTag(filled, mapTagNameIndex, p.mapIndex)
	scale, _ := tagInt(held.Tag, mapTagScale)
	pos := p.blockPos()
	d := p.s.createMaps(p.s.mapDimension(p.dim), pos, uint8(min(max(scale, 0), mapMaxScale))) //nolint:gosec // clamped
	filled = withTag(filled, mapTagUUID, d.id)
	p.send(p.s.mapCreation(d))
	inv := &p.inventory
	if s := &inv.main[inv.held]; s.Count > 1 {
		s.Count--
		p.sendHeld()
		if rest := p.Give(p.s.freshStack(filled)); !rest.Empty() {
			p.Drop(rest)
		}
	} else {
		*s = p.s.freshStack(filled)
		p.sendHeld()
	}
	playSound(r, pos, soundEvent{name: packet.SoundEventUicartographyTableUse, pos: p.entity.Position(), extra: -1, entity: "minecraft:player"}, nil)
	p.sendEquipment()
	p.flush()
	return true
}

// tickMaps is MapItem's inventory tick for every map the player carries: the map follows the
// players, a held one is redrawn every 20 or so ticks, and held ones send what changed.
func (p *Player) tickMaps() {
	if p.entity == nil || p.dead() {
		return
	}
	inv := &p.inventory
	for i := range inv.main {
		if inv.main[i].Name == itemFilledMapName {
			p.tickMap(&inv.main[i], i == int(inv.held), func() { p.sendSlot(i) })
		}
	}
	if inv.offhand[0].Name == itemFilledMapName {
		p.tickMap(&inv.offhand[0], true, p.sendOffhand)
	}
}

// tickMap is one carried map's tick.
func (p *Player) tickMap(st *ItemStack, held bool, resend func()) {
	r := p.entity.Region()
	if r == nil {
		return
	}
	dim := p.s.mapDimension(p.dim)
	d := p.s.mapByID(mapID(*st))
	if d == nil {
		if mapID(*st) != -1 {
			return
		}
		// A filled map with no data is made where the player is, as BDS does.
		d = p.s.createMaps(dim, p.blockPos(), 0)
		*st = withTag(*st, mapTagUUID, d.id)
		resend()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.locked && st.Meta != mapMetaLocked {
		st.Meta = mapMetaLocked
		resend()
	}
	p.s.trackPlayers(d)
	if !held {
		return
	}
	if d.dim == dim && !d.locked {
		d.renderTick++
		if d.renderTick-1 == mapRenderTicks {
			d.renderTick = rand.Int32N(mapRenderJitter) //nolint:gosec // a redraw time
			pos := p.entity.Position()
			p.s.renderMap(r, d, int32(math.Floor(float64(pos[0]))), int32(math.Floor(float64(pos[2]))))
		}
	}
	if pk := d.updatePacket(p.uniqueID); pk != nil {
		p.send(pk)
	}
}

// trackPlayers follows every online player on the map and works out their markers, BDS's
// tickByActor. Runs: under d.mu.
func (s *Host) trackPlayers(d *mapData) {
	scale := float32(int32(1) << d.scale)
	d.trackRound++
	for _, o := range s.global.online() {
		state := o.State()
		if state.Region == nil || s.mapDimension(state.Region.Dimension()) != d.dim || state.GameMode == gameModeSpectator {
			continue
		}
		t := d.tracked[o.uniqueID]
		if t == nil {
			t = &mapTracked{changed: true, x0: 0, z0: 0, x1: mapSize - 1, z1: mapSize - 1}
			d.tracked[o.uniqueID] = t
		}
		t.round = d.trackRound
		dx := int32(math.Round(float64((state.Position[0] - float32(d.x)) / scale)))
		dz := int32(math.Round(float64((state.Position[2] - float32(d.z)) / scale)))
		kind := byte(mapDecorationPlayer)
		if dx < -mapHalf || dx >= mapHalf || dz < -mapHalf || dz >= mapHalf {
			switch {
			case mapAbs(dx) < mapOffMapLimit && mapAbs(dz) < mapOffMapLimit:
				kind = mapDecorationPlayerOffMap
			case d.unlimited:
				kind = mapDecorationPlayerFar
			default:
				t.shown = false
				continue
			}
		}
		yaw := state.Rotation.Yaw
		off := float32(8)
		if yaw < 0 {
			off = -8
		}
		t.decoration = protocol.MapDecoration{
			Type: kind, Rotation: byte(int8(int32((yaw + off) * 16 / 360))), //nolint:gosec // BDS's byte
			X: mapMarkerOffset(dx), Y: mapMarkerOffset(dz), Colour: color.RGBA{R: 255, G: 255, B: 255, A: 255},
		}
		t.shown = true
	}
	for id, t := range d.tracked {
		if t.round != d.trackRound {
			t.shown = false
			if t.changed {
				continue
			}
			delete(d.tracked, id)
		}
	}
}

func mapAbs(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// mapMarkerOffset is a marker's byte position, BDS's twice the pixel clamped to the edge.
func mapMarkerOffset(v int32) byte {
	switch {
	case v >= 63:
		return 127
	case v < -62:
		return 0x80
	}
	return byte(int8(2 * v)) //nolint:gosec // within -124 to 124
}

// decorations are the map's player markers and the players they follow. Runs: under d.mu.
func (d *mapData) decorations() ([]protocol.MapDecoration, []protocol.MapTrackedObject) {
	var decs []protocol.MapDecoration
	var objs []protocol.MapTrackedObject
	for id, t := range d.tracked {
		if !t.shown {
			continue
		}
		objs = append(objs, protocol.MapTrackedObject{Type: protocol.MapObjectTypeEntity, EntityUniqueID: protocol.Option(id)})
		decs = append(decs, t.decoration)
	}
	return decs, objs
}

// basePacket is a map packet with no update in it.
func (d *mapData) basePacket() *packet.ClientBoundMapItemData {
	return &packet.ClientBoundMapItemData{
		MapID: d.id, Dimension: byte(d.dim), LockedMap: d.locked, //nolint:gosec // dimensions are 0 to 2
		Origin: protocol.BlockPos{d.x, 0, d.z}, Scale: protocol.Option(d.scale),
	}
}

// withPixels adds the pixels of a rectangle and the decorations, BDS's texture update.
func (d *mapData) withPixels(pk *packet.ClientBoundMapItemData, x0, z0, w, h int32) {
	decs, objs := d.decorations()
	pk.Decorations, pk.TrackedObjects = protocol.Option(decs), protocol.Option(objs)
	px := make([]color.RGBA, 0, w*h)
	for z := z0; z < z0+h; z++ {
		for x := x0; x < x0+w; x++ {
			px = append(px, mapWirePixel(d.pixels[x+z*mapSize]))
		}
	}
	pk.Width, pk.Height, pk.XOffset, pk.YOffset = protocol.Option(w), protocol.Option(h), protocol.Option(x0), protocol.Option(z0)
	pk.Pixels = protocol.Option(px)
}

// mapWirePixel is a pixel as the protocol writes it: the same four bytes BDS sends.
func mapWirePixel(v uint32) color.RGBA {
	return color.RGBA{A: byte(v >> 24), R: byte(v >> 16), G: byte(v >> 8), B: byte(v)}
}

// updatePacket is what a player holding the map is sent this tick, BDS's getUpdatePacket:
// the pixels it has not seen with the markers, else the markers every 5 ticks. Runs: under d.mu.
func (d *mapData) updatePacket(player int64) *packet.ClientBoundMapItemData {
	t := d.tracked[player]
	if t == nil {
		return nil
	}
	pk := d.basePacket()
	if t.changed {
		t.changed = false
		d.withPixels(pk, t.x0, t.z0, t.x1-t.x0+1, t.z1-t.z0+1)
		return pk
	}
	t.ticks++
	if (t.ticks-1)%mapDecorationTicks != 0 {
		return nil
	}
	decs, objs := d.decorations()
	pk.Decorations, pk.TrackedObjects = protocol.Option(decs), protocol.Option(objs)
	return pk
}

// mapCreation is the packet that makes the client know a map: its ID and those of its larger
// maps, scale, dimension and centre (BDS 0x8824050).
func (s *Host) mapCreation(d *mapData) *packet.ClientBoundMapItemData {
	pk := d.basePacket()
	ids := []int64{d.id}
	for at := d; at.scale < mapMaxScale; {
		next := s.mapByID(at.parent)
		if next == nil {
			break
		}
		ids = append(ids, next.id)
		at = next
	}
	pk.MapsIncludedIn = protocol.Option(ids)
	return pk
}

// sendFullMap sends a player a map whole, as BDS answers a MapInfoRequest: the creation packet
// then every pixel with the markers.
func (p *Player) sendFullMap(d *mapData) {
	creation := p.s.mapCreation(d)
	d.mu.Lock()
	full := d.basePacket()
	d.withPixels(full, 0, 0, mapSize, mapSize)
	d.mu.Unlock()
	p.send(creation, full)
	p.flush()
}

// handleMapInfoRequest is a client asking for a map it has an item of.
func handleMapInfoRequest(p *Player, pk *packet.MapInfoRequest) {
	if d := p.s.mapByID(pk.MapID); d != nil {
		p.sendFullMap(d)
	}
}

// handleMapCreateLockedCopy is a client locking a map at a cartography table under an ID of its
// own (BDS 0x8CB52C0): the player must carry the map; the copy is locked.
func handleMapCreateLockedCopy(p *Player, pk *packet.MapCreateLockedCopy) {
	if !p.carriesMap(pk.OriginalMapID) || p.s.mapByID(pk.NewMapID) != nil {
		return
	}
	if d := p.s.lockedCopy(pk.OriginalMapID, pk.NewMapID); d != nil {
		p.sendFullMap(d)
	}
}

// carriesMap reports whether a map item with an ID is in the player's slots.
func (p *Player) carriesMap(id int64) bool {
	inv := &p.inventory
	for _, l := range [][]ItemStack{inv.main[:], inv.offhand[:], inv.ui[:]} {
		for _, s := range l {
			if s.Name == itemFilledMapName && mapID(s) == id {
				return true
			}
		}
	}
	return false
}

// lockedCopy copies a map under a new ID, locked (BDS's copyAndLockMap). Nil if there is no
// map or the ID is taken.
func (s *Host) lockedCopy(from, to int64) *mapData {
	orig := s.mapByID(from)
	if orig == nil || s.mapByID(to) != nil {
		return nil
	}
	orig.mu.Lock()
	d := &mapData{
		id: to, parent: orig.parent, dim: orig.dim, x: orig.x, z: orig.z, scale: orig.scale,
		unlimited: orig.unlimited, explored: orig.explored, pixels: orig.pixels, locked: true,
		tracked: map[int64]*mapTracked{}, dirty: true,
	}
	orig.mu.Unlock()
	s.addMap(d)
	return d
}

// mapSample is one block a pixel is drawn from: its colour, the block's height and how deep
// the water over it is.
type mapSample struct {
	colour mapColour
	block  uint32
	height int
	depth  int
}

// sampleColumn finds the block a column shows on a map: the highest that has a colour, and
// below water how deep the water goes. False where the chunk is not loaded.
func (s *Host) sampleColumn(r *world.Region, x, z int) (mapSample, bool) {
	h, ok := r.Height(x, z)
	if !ok {
		return mapSample{}, false
	}
	rng := r.Dimension().Range()
	air := s.reg.Blocks.Air()
	// The height map passes plants and the like; they still draw, so start over them.
	y := min(h, rng[1])
	for y < rng[1] {
		b, _ := r.Block(cube.Pos{x, y, z}, 0)
		if b == air {
			break
		}
		y++
	}
	for ; y > rng[0]; y-- {
		pos := cube.Pos{x, y, z}
		b, _ := r.Block(pos, 0)
		c := s.colourAt(r, pos, b)
		if c.none() {
			continue
		}
		sm := mapSample{colour: c, block: b, height: y}
		if s.mapPalette.water[b] {
			for d := y - 1; d > rng[0]; d-- {
				under, _ := r.Block(cube.Pos{x, d, z}, 0)
				if !s.mapPalette.water[under] {
					break
				}
				sm.depth++
			}
		}
		return sm, true
	}
	return mapSample{block: air, height: rng[0]}, true
}

// renderMap draws the map around a player, BDS's render: each pixel the most common colour of
// its blocks, shaded by the height against the pixel north of it (water by its depth) and a
// checkerboard, within a circle around the player; the Nether draws noise.
func (s *Host) renderMap(r *world.Region, d *mapData, px, pz int32) {
	scale := int32(1) << d.scale
	radius := int32(mapSize) / scale
	nether := r.Dimension() == s.nether
	if nether {
		radius /= 2
	}
	cx := (px-d.x)/scale + mapHalf
	cz := (pz-d.z)/scale + mapHalf
	xStart := int32(-1)
	if cx-radius >= 0 {
		xStart = cx - radius
	}
	xStart++
	if cx-radius >= mapSize-1 {
		xStart = mapSize - 1
	}
	xEnd := min(max(cx+radius, 0), mapSize)
	zStart := int32(-1)
	if cz-radius-1 >= 0 {
		zStart = cz - radius - 1
	}
	if cz-radius-1 >= mapSize {
		zStart = mapSize - 1
	}
	zEnd := min(max(cz+radius, 0), mapSize)
	samples := scale * scale
	for x := xStart; x < xEnd; x++ {
		var prev float32
		havePrev := false
		for z := zStart; z < zEnd; z++ {
			if z < -1 || z >= mapSize {
				continue
			}
			wx, wz := scale*(x-mapHalf+d.x/scale), scale*(d.z/scale+z-mapHalf)
			if _, ok := r.Height(int(wx), int(wz)); !ok {
				continue
			}
			var colour mapColour
			var height float32
			var depth int32
			water := false
			if nether {
				hash := wx + 231871*wz
				if (hash*(1926993*hash+11))&mapNetherNoise != 0 {
					colour = mapNetherStone
				} else {
					colour = mapNetherDirt
				}
				height = 100
			} else {
				colour, height, depth, water = s.pixelBlocks(r, int(wx), int(wz), int(scale))
			}
			if !havePrev {
				prev = height
			}
			checker := (z + x) & 1
			slope := (float32(checker)-0.5)*0.4 + (height-prev)*4/float32(scale+4)
			prev, havePrev = height, true
			shade := 1
			if slope > 0.6 {
				shade = 2
			}
			if slope < -0.6 {
				shade = 0
			}
			if water {
				w := float32(depth/samples)*0.1 + float32(checker)*0.2
				switch {
				case w > 0.9:
					shade = 0
				case w < 0.5:
					shade = 2
				default:
					shade = 1
				}
			}
			dist := (x-cx)*(x-cx) + (z-cz)*(z-cz)
			if z < 0 || dist >= radius*radius || (checker == 0 && dist > (radius-2)*(radius-2)) {
				continue
			}
			d.setPixel(x, z, mapPixel(colour, mapShades[shade]))
		}
	}
}

// pixelBlocks samples the blocks of one pixel: the colour most of them have, their mean
// height, their total water depth and whether the colour is water's.
func (s *Host) pixelBlocks(r *world.Region, wx, wz, scale int) (mapColour, float32, int32, bool) {
	type count struct {
		colour mapColour
		block  uint32
		n      int
	}
	var counts []count
	var height float32
	var depth int32
	for dx := range scale {
		for dz := range scale {
			sm, ok := s.sampleColumn(r, wx+dx, wz+dz)
			if !ok {
				continue
			}
			depth += int32(sm.depth) //nolint:gosec // a column's depth
			height += float32(sm.height) / float32(scale*scale)
			found := false
			for i := range counts {
				if counts[i].colour == sm.colour && counts[i].block == sm.block {
					counts[i].n++
					found = true
					break
				}
			}
			if !found {
				counts = append(counts, count{colour: sm.colour, block: sm.block, n: 1})
			}
		}
	}
	best := count{}
	for _, c := range counts {
		if c.n > best.n {
			best = c
		}
	}
	return best.colour, height, depth, s.mapPalette.water[best.block] && best.n > 0
}

// mapPixel is a colour shaded as BDS stores a pixel: red, green and blue bytes, opaque.
func mapPixel(c mapColour, shade float32) uint32 {
	part := func(v float32) uint32 { return uint32(min(max(v*shade, 0), 1) * 255) }
	return part(c[0]) | part(c[1])<<8 | part(c[2])<<16 | 0xff000000
}

// setPixel changes a pixel and marks it for every player the map follows. Runs: under mu.
func (d *mapData) setPixel(x, z int32, v uint32) {
	i := x + z*mapSize
	if d.pixels[i] == v {
		return
	}
	d.pixels[i] = v
	d.dirty = true
	for _, t := range d.tracked {
		if !t.changed {
			t.changed, t.x0, t.z0, t.x1, t.z1 = true, x, z, x, z
			continue
		}
		t.x0, t.z0, t.x1, t.z1 = min(t.x0, x), min(t.z0, z), max(t.x1, x), max(t.z1, z)
	}
}
