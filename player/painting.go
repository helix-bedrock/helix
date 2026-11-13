package player

import (
	"maps"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// Paintings as BDS 1.26.60 runs them: HangingActor and Painting, HangingActorItem::_useOn. The
// research note is private.
const (
	paintingID = "minecraft:painting"
	// paintingOut is how far out from the middle of its wall block a painting hangs, and
	// paintingThick half its thickness.
	paintingOut   = 0.53125
	paintingThick = 0.0625
	// paintingShrink is taken off every side of its box.
	paintingShrink = 1.0 / 32
	// paintingEven is the size in pixels whose multiples sit between two blocks.
	paintingEven = 32
	// pixelsInBlock is a block's size in pixels.
	pixelsInBlock = 16
	// A painting checks it still hangs every paintingCheck ticks, give or take a fifth.
	paintingCheck       = 100
	paintingCheckSpread = 0.2
	// paintingDropDelay is the pick-up delay of a broken painting's item, and paintingDropUp
	// how far above the painting it drops.
	paintingDropDelay = 10
	paintingDropUp    = 1
	// paintingBreakBlock is the block whose particles a broken painting leaves.
	paintingBreakBlock = "minecraft:oak_planks"
	// The level events of a painting placed and broken, and its break particles.
	levelEventPaintingPlace    = 1040
	levelEventPaintingBreak    = 1041
	levelEventPaintingParticle = 2021

	paintingMotiveTag    = "Motive"
	paintingOldMotiveTag = "Motif"
	paintingDirTag       = "Direction"
	paintingOldDirTag    = "Dir"
	// paintingDefaultDir is the direction of a saved painting with none.
	paintingDefaultDir = 2
)

// Motive is a picture a painting can show: its name as the client knows it, its size in
// pixels (16 to a block) and whether a placed painting may pick it.
type Motive struct {
	Name          string
	Width, Height int32
	Placeable     bool
}

// vanillaMotives are BDS 1.26.60's motives in its order (the static table at 0xC021D00, listed
// by 0xBFF0E40 then 0xBFF1780); a placed painting draws from them in this order. The first is
// what a painting with an unknown motive shows.
var vanillaMotives = [...]Motive{
	{"Kebab", 16, 16, true}, {"Aztec", 16, 16, true}, {"Aztec2", 16, 16, true}, {"Alban", 16, 16, true},
	{"Bomb", 16, 16, true}, {"Plant", 16, 16, true}, {"Wasteland", 16, 16, true}, {"Pool", 32, 16, true},
	{"Courbet", 32, 16, true}, {"Sea", 32, 16, true}, {"Sunset", 32, 16, true}, {"Creebet", 32, 16, true},
	{"Wanderer", 16, 32, true}, {"Graham", 16, 32, true}, {"Match", 32, 32, true}, {"Bust", 32, 32, true},
	{"Stage", 32, 32, true}, {"Void", 32, 32, true}, {"SkullAndRoses", 32, 32, true}, {"Wither", 32, 32, true},
	{"Fighters", 64, 32, true}, {"Pointer", 64, 64, true}, {"Pigscene", 64, 64, true}, {"BurningSkull", 64, 64, true},
	{"Skeleton", 64, 48, true}, {"DonkeyKong", 64, 48, true}, {"Earth", 32, 32, false}, {"Wind", 32, 32, false},
	{"Fire", 32, 32, false}, {"Water", 32, 32, false}, {"baroque", 32, 32, true}, {"humble", 32, 32, true},
	{"meditative", 16, 16, true}, {"prairie_ride", 16, 32, true}, {"unpacked", 64, 64, true}, {"backyard", 48, 64, true},
	{"bouquet", 48, 48, true}, {"cavebird", 48, 48, true}, {"changing", 64, 32, true}, {"cotan", 48, 48, true},
	{"endboss", 48, 48, true}, {"fern", 48, 48, true}, {"finding", 64, 32, true}, {"lowmist", 64, 32, true},
	{"orb", 64, 64, true}, {"owlemons", 48, 48, true}, {"passage", 64, 32, true}, {"pond", 48, 64, true},
	{"tides", 48, 48, true}, {"sunflowers", 48, 48, true}, {"dennis", 48, 48, true},
}

// SetMotives replaces the motives paintings can show, in the order placing draws from. Saved
// paintings with a motive no longer listed show the first one.
func (s *Setup) SetMotives(motives []Motive) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.motives = slices.Clone(motives)
	return nil
}

// Motives returns the motives paintings can show.
func (s *Host) Motives() []Motive { return slices.Clone(s.game.motives) }

// motive finds a motive by name, else the first (BDS falls back to Kebab).
func (s *Host) motive(name string) Motive {
	for _, m := range s.game.motives {
		if m.Name == name {
			return m
		}
	}
	if len(s.game.motives) > 0 {
		return s.game.motives[0]
	}
	return vanillaMotives[0]
}

// paintingDir is BDS's hanging direction of a painting on the side face of a block: 0 south,
// 1 west, 2 north, 3 east; false for the top and bottom.
func paintingDir(face cube.Face) (int32, bool) {
	switch face {
	case cube.FaceSouth:
		return 0, true
	case cube.FaceWest:
		return 1, true
	case cube.FaceNorth:
		return 2, true
	case cube.FaceEast:
		return 3, true
	}
	return 0, false
}

// paintingYaw is the yaw of a painting hanging in dir.
func paintingYaw(dir int32) float64 {
	switch dir {
	case 0:
		return 180
	case 2:
		return 0
	}
	return float64(90 * dir)
}

// Painting is a painting on a wall. Runs: owner of its entity.
type Painting struct {
	s      *Host
	entity *world.Entity
	motive Motive
	dir    int32
	gone   bool
	// check is the ticks to its next look at whether it still hangs.
	check int32
	near  []*world.Entity
	saved map[string]any
}

// Entity returns the painting's entity.
func (p *Painting) Entity() *world.Entity { return p.entity }

// Motive returns what it shows.
func (p *Painting) Motive() Motive { return p.motive }

// Direction returns the way it faces: 0 south, 1 west, 2 north, 3 east.
func (p *Painting) Direction() int32 { return p.dir }

// halfSize is half its size in blocks across and up.
func (m Motive) halfSize() (across, up float64) {
	return float64(m.Width) / paintingEven, float64(m.Height) / paintingEven
}

// evenOffset is how far a painting of size pixels is moved to sit between blocks.
func evenOffset(size int32) float64 {
	if size%paintingEven == 0 {
		return 0.5
	}
	return 0
}

// paintingPos is BDS's position of a painting hanging in dir on the block wall (0xBFC3D80).
func paintingPos(wall cube.Pos, dir int32, m Motive) mgl64.Vec3 {
	pos := blockCenter(wall)
	side := evenOffset(m.Width)
	pos[1] += evenOffset(m.Height)
	switch dir {
	case 0:
		pos[2] += paintingOut
		pos[0] += side
	case 1:
		pos[0] -= paintingOut
		pos[2] += side
	case 2:
		pos[2] -= paintingOut
		pos[0] -= side
	case 3:
		pos[0] += paintingOut
		pos[2] -= side
	}
	return pos
}

// paintingWall is the wall block of a painting at pos, as BDS's wouldSurvive finds it (0xBFC4A10).
func paintingWall(pos mgl64.Vec3, dir int32, m Motive) cube.Pos {
	side := evenOffset(m.Width)
	switch dir {
	case 0:
		pos[2] -= paintingOut
		pos[0] -= side
	case 1:
		pos[0] += paintingOut
		pos[2] -= side
	case 2:
		pos[2] += paintingOut
		pos[0] += side
	case 3:
		pos[0] -= paintingOut
		pos[2] += side
	}
	return floorPos(pos)
}

// paintingBox is BDS's box of a painting at pos (0xBFC3F60).
func paintingBox(pos mgl64.Vec3, dir int32, m Motive) (lo, hi mgl64.Vec3) {
	across, up := m.halfSize()
	half := mgl64.Vec3{across, up, paintingThick}
	if dir == 1 || dir == 3 {
		half = mgl64.Vec3{paintingThick, up, across}
	}
	shrink := mgl64.Vec3{paintingShrink, paintingShrink, paintingShrink}
	return pos.Sub(half).Add(shrink), pos.Add(half).Sub(shrink)
}

// paintingFits is BDS's HangingActor survive check (0xBFC4BC0) without the facing rules of the
// blocks behind and in front, which are not ported: no block's box in its box, every block
// behind it of solid material, and no other painting in its box. self is left out.
func (s *Host) paintingFits(r *world.Region, wall cube.Pos, pos mgl64.Vec3, dir int32, m Motive, self *Painting, near *[]*world.Entity) bool {
	lo, hi := paintingBox(pos, dir, m)
	from, to := floorPos(lo), floorPos(hi)
	for x := from[0]; x <= to[0]; x++ {
		for y := from[1]; y <= to[1]; y++ {
			for z := from[2]; z <= to[2]; z++ {
				at := cube.Pos{x, y, z}
				b, ok := r.Block(at, 0)
				if !ok {
					return false
				}
				base := blockVec(at)
				for _, box := range s.boxesAt(r, at, b) {
					if overlaps(lo, hi, base.Add(mgl64.Vec3{float64(box[0]), float64(box[1]), float64(box[2])}), base.Add(mgl64.Vec3{float64(box[3]), float64(box[4]), float64(box[5])})) {
						return false
					}
				}
			}
		}
	}
	across, up := max(m.Width/pixelsInBlock, 1), max(m.Height/pixelsInBlock, 1)
	_, halfUp := m.halfSize()
	halfAcross, _ := m.halfSize()
	y0 := int(math.Floor(float64(pos[1] - halfUp)))
	for i := range int(across) {
		for j := range int(up) {
			at := cube.Pos{wall[0], y0 + j, wall[2]}
			if dir == 1 || dir == 3 {
				at[2] = int(math.Floor(float64(pos[2]-halfAcross))) + i
			} else {
				at[0] = int(math.Floor(float64(pos[0]-halfAcross))) + i
			}
			b, ok := r.Block(at, 0)
			if !ok || !s.reg.Blocks.Properties(b).SolidMaterial {
				return false
			}
		}
	}
	*near = r.AppendEntitiesIn((*near)[:0], lo[0]-1, lo[2]-1, hi[0]+1, hi[2]+1)
	defer clear(*near)
	for _, e := range *near {
		o, ok := e.Owner().(*Painting)
		if !ok || o == self || o.gone {
			continue
		}
		oLo, oHi := paintingBox(e.Position(), o.dir, o.motive)
		if overlaps(lo, hi, oLo, oHi) {
			return false
		}
	}
	return true
}

// pickMotive is Painting's placement (0xBFF64D0): of the placeable motives that fit, the
// biggest by area, one of them at random. False if none fits.
func (s *Host) pickMotive(r *world.Region, wall cube.Pos, dir int32, near *[]*world.Entity) (Motive, bool) {
	var fit []Motive
	best := int32(0)
	for _, m := range s.game.motives {
		if !m.Placeable {
			continue
		}
		if area := m.Width * m.Height; area >= best && s.paintingFits(r, wall, paintingPos(wall, dir, m), dir, m, nil, near) {
			fit, best = append(fit, m), area
		}
	}
	fit = slices.DeleteFunc(fit, func(m Motive) bool { return m.Width*m.Height < best })
	if len(fit) == 0 {
		return Motive{}, false
	}
	return fit[rand.IntN(len(fit))], true //nolint:gosec // a picture, not a secret
}

// SpawnPainting hangs a painting showing m on the block wall, facing dir (0 south, 1 west, 2
// north, 3 east), whether or not it fits there. Runs: owner of the region.
func (s *Host) SpawnPainting(r *world.Region, wall cube.Pos, dir int32, m Motive) *Painting {
	p := &Painting{s: s, motive: m, dir: dir & 3}
	pos := paintingPos(wall, p.dir, m)
	yaw := paintingYaw(p.dir)
	p.entity = r.Dimension().AddEntity(p.config(itemUniqueIDBase|rand.Int64N(itemUniqueIDBase), s.runtimeIDs.Add(1), pos, world.Rotation{Yaw: float32(yaw), HeadYaw: float32(yaw)})) //nolint:gosec // an entity ID, not a secret
	return p
}

func (p *Painting) config(uniqueID int64, runtimeID uint64, pos mgl64.Vec3, rot world.Rotation) world.EntityConfig {
	return world.EntityConfig{
		UniqueID: uniqueID, RuntimeID: runtimeID, Pos: pos, Rot: rot,
		Owner: p, Spawn: p.spawnPackets, Move: p.movePacket, Input: p.tick, Save: p.saveEntity,
	}
}

// spawnPackets are BDS's AddPainting (0x8AF8A00).
func (p *Painting) spawnPackets(e *world.Entity) []packet.Packet {
	return []packet.Packet{&packet.AddPainting{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), Position: world.Vec32(e.Position()), Direction: p.dir, Title: p.motive.Name,
	}}
}

func (p *Painting) movePacket(e *world.Entity) packet.Packet {
	rot := e.Rotation()
	return &packet.MoveActorAbsolute{EntityRuntimeID: e.RuntimeID(), Position: world.Vec32(e.Position()), Rotation: mgl32.Vec3{float32(rot.Pitch), float32(rot.Yaw), float32(rot.HeadYaw)}}
}

// tick is HangingActor's tick (0xBFC44D0): every 80 to 119 ticks, with the chunks around it
// loaded, a painting that no longer fits breaks.
func (p *Painting) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || p.gone {
		return
	}
	if p.s.checkVoid(e); p.gone {
		return
	}
	p.check--
	if p.check >= 0 {
		return
	}
	low := int32(math.Floor(paintingCheck * (1 - paintingCheckSpread)))
	high := int32(math.Ceil(paintingCheck * (1 + paintingCheckSpread)))
	p.check = low + rand.Int32N(high-low) //nolint:gosec // a delay, not a secret
	pos := e.Position()
	at := floorPos(pos)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if _, ok := r.Block(cube.Pos{at[0] + dx*blocksInChunk, at[1], at[2] + dz*blocksInChunk}, 0); !ok {
				return
			}
		}
	}
	if !p.s.paintingFits(r, paintingWall(pos, p.dir, p.motive), pos, p.dir, p.motive, p, &p.near) {
		p.Break(r, false)
	}
}

// Hurt is HangingActor's hurt (0xBFC56B0): any hit takes it down. It leaves no item: BDS
// 1.26.60's drop (0xBFF6140) is gated by a check (0xB3CA270, 0xB3CA010) that, tested in game on
// BDS by the lead (2026-11-10), lets no painting drop, even for a survival player. True if
// it broke.
func (p *Painting) Hurt(r *world.Region, _ *Player) bool {
	if p.gone {
		return false
	}
	p.Break(r, false)
	return true
}

// Break takes it down with BDS's sound and particles (0xBFF63A0). Vanilla passes drops false;
// with drops set it leaves its item if doentitydrops is on.
func (p *Painting) Break(r *world.Region, drops bool) {
	if p.gone {
		return
	}
	p.gone = true
	pos := p.entity.Position()
	at := floorPos(pos)
	levelEvent(r, at, pos, levelEventPaintingBreak, 0)
	if planks, ok := p.s.reg.Blocks.Block(paintingBreakBlock); ok {
		levelEvent(r, at, pos, levelEventPaintingParticle, int32(p.s.reg.Blocks.Hash(planks))) //nolint:gosec // hashes are sent as int32
	}
	if drops && p.s.level.DoEntityDrops {
		p.s.spawnItem(r.Dimension(), ItemStack{Name: paintingID, Count: 1}, pos.Add(mgl64.Vec3{0, paintingDropUp, 0}), mgl64.Vec3{}, world.Rotation{}, paintingDropDelay, 0)
	}
	p.entity.Remove()
}

// BlastBox is its box.
func (p *Painting) BlastBox() (lo, hi mgl64.Vec3, ok bool) {
	if p.gone || p.entity == nil {
		return lo, hi, false
	}
	lo, hi = paintingBox(p.entity.Position(), p.dir, p.motive)
	return lo, hi, true
}

// BlastHit takes it down, as any hurt does.
func (p *Painting) BlastHit(float64, DamageSource) {
	if r := p.entity.Region(); r != nil {
		p.Hurt(r, nil)
	}
}

// BlastPush does nothing: a painting does not move.
func (p *Painting) BlastPush(mgl64.Vec3) {}

// placePainting is HangingActorItem::_useOn (0xC914F60): only on the side of a block, with the
// biggest motive that fits; the level event at the wall and the item used up outside creative.
func (p *Player) placePainting(r *world.Region, clicked cube.Pos, face int32) bool {
	dir, ok := paintingDir(cube.Face(face))
	if !ok || !p.inReach(clicked) {
		return false
	}
	m, ok := p.s.pickMotive(r, clicked, dir, &p.touching)
	if !ok {
		return false
	}
	p.s.SpawnPainting(r, clicked, dir, m)
	levelEvent(r, clicked, blockVec(clicked), levelEventPaintingPlace, 0)
	if !p.creative() {
		p.useHeld()
	}
	return true
}

// paintingNear finds a painting near the player by its runtime ID.
func (p *Player) paintingNear(id uint64) *Painting {
	r := p.entity.Region()
	if r == nil {
		return nil
	}
	pos := p.entity.Position()
	p.touching = r.AppendEntitiesIn(p.touching[:0], pos[0]-attackScan, pos[2]-attackScan, pos[0]+attackScan, pos[2]+attackScan)
	defer clear(p.touching)
	for _, e := range p.touching {
		if e.RuntimeID() != id {
			continue
		}
		if o, ok := e.Owner().(*Painting); ok && !o.gone {
			return o
		}
	}
	return nil
}

// useOnPainting is a use on actor transaction aimed at a painting: a hit takes it down,
// a click does nothing. False if it was not aimed at a painting.
func (p *Player) useOnPainting(d *protocol.UseItemOnEntityTransactionData) bool {
	o := p.paintingNear(d.TargetEntityRuntimeID)
	if o == nil {
		return false
	}
	if p.entity.Position().Sub(o.entity.Position()).Len() > cartReach {
		return true
	}
	if d.ActionType == protocol.UseItemOnEntityActionAttack {
		if r := p.entity.Region(); r != nil {
			o.Hurt(r, p)
		}
	}
	return true
}

// saveEntity saves it as BDS does: the motive and its direction in both forms.
func (p *Painting) saveEntity(e *world.Entity) (chunk.Entity, bool) {
	if p.gone {
		return chunk.Entity{}, false
	}
	data := maps.Clone(p.saved)
	if data == nil {
		data = map[string]any{}
	}
	pos, rot := e.Position(), e.Rotation()
	data["identifier"] = paintingID
	data["Pos"] = []float32{float32(pos[0]), float32(pos[1]), float32(pos[2])}
	data["Rotation"] = []float32{rot.Yaw, rot.Pitch}
	data[paintingMotiveTag] = p.motive.Name
	data[paintingDirTag] = uint8(p.dir) //nolint:gosec // 0 to 3
	data[paintingOldDirTag] = uint8(oldPaintingDir(p.dir))
	delete(data, paintingOldMotiveTag)
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
}

// oldPaintingDir is the old Dir numbering BDS still writes and reads: south and north swapped.
func oldPaintingDir(dir int32) int32 {
	switch dir {
	case 0:
		return 2
	case 2:
		return 0
	}
	return dir
}

// loadPainting brings back a saved painting as BDS reads one (0xBFF5BD0, 0xBFC5350).
func (s *Host) loadPainting(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity) bool {
	pos, ok := floats(saved.Data["Pos"])
	if !ok || len(pos) != 3 {
		return false
	}
	name, ok := saved.Data[paintingMotiveTag].(string)
	if !ok {
		name, _ = saved.Data[paintingOldMotiveTag].(string)
	}
	p := &Painting{s: s, motive: s.motive(name), dir: paintingDefaultDir, saved: saved.Data}
	if d, ok := saved.Data[paintingDirTag].(uint8); ok {
		p.dir = int32(d & 3)
	} else if d, ok := saved.Data[paintingOldDirTag].(uint8); ok && d < 4 {
		p.dir = oldPaintingDir(int32(d))
	}
	yaw := paintingYaw(p.dir)
	p.entity = r.AddLoadedEntity(at, p.config(saved.ID, s.runtimeIDs.Add(1), mgl64.Vec3{float64(pos[0]), float64(pos[1]), float64(pos[2])}, world.Rotation{Yaw: float32(yaw), HeadYaw: float32(yaw)}))
	return true
}
