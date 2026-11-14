package player

import (
	"maps"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// End crystals as BDS 1.26.60 runs them: the item's use on a block (EndCrystalItem::_useOn,
// 0x4AE5AA0), the EnderCrystal actor (vtable 0xFAC6D88: normal tick 0xBD341C0, hurt
// 0xBD34A90, load 0xBD34740, save 0xBD34470) and its behaviour pack definition (health 1,
// on_hurt -> explode power 6, no fire, breaking blocks only with mobGriefing, fuse 0).
// The research note is private.
const (
	nameEndCrystalItem = "minecraft:end_crystal"
	nameEnderCrystal   = "minecraft:ender_crystal"
	nameObsidian       = "minecraft:obsidian"
	nameBedrock        = "minecraft:bedrock"

	// crystalSize is its collision box's width and height.
	crystalSize = 2
	// crystalPower is its explosion's radius.
	crystalPower = 6
	// crystalClearHeight is how many blocks above the clicked one must be air, and how high
	// the box that must hold no entity reaches from the clicked block's floor.
	crystalClearHeight = 2

	// The tags BDS saves a crystal's beam target with (only while it has one).
	crystalTargetX = "BlockTargetX"
	crystalTargetY = "BlockTargetY"
	crystalTargetZ = "BlockTargetZ"
)

// EndCrystal is an end crystal. Any damage sets it off: it explodes on its next tick, as its
// explode component's fuse is 0. Runs: owner of its entity.
type EndCrystal struct {
	s      *Host
	entity *world.Entity
	// exploding is set once it was hurt; attacker is who hurt it, if a player did.
	exploding bool
	attacker  *Player
	gone      bool
	saved     map[string]any
}

// Entity returns the crystal's entity.
func (c *EndCrystal) Entity() *world.Entity { return c.entity }

// SpawnEndCrystal adds an end crystal with its feet at feet.
func (s *Host) SpawnEndCrystal(d *world.Dimension, feet mgl32.Vec3) *EndCrystal {
	c := &EndCrystal{s: s}
	c.entity = d.AddEntity(c.config(itemUniqueIDBase|rand.Int64N(itemUniqueIDBase), s.runtimeIDs.Add(1), feet, world.Rotation{})) //nolint:gosec // an entity ID, not a secret
	return c
}

func (c *EndCrystal) config(uniqueID int64, runtimeID uint64, pos mgl32.Vec3, rot world.Rotation) world.EntityConfig {
	return world.EntityConfig{
		UniqueID: uniqueID, RuntimeID: runtimeID, Pos: pos, Rot: rot,
		Owner: c, Spawn: c.spawnPackets, Move: c.movePacket, Input: c.tick, Save: c.saveEntity,
	}
}

// spawnPackets show it to a player. Not yet compared with what BDS sends.
func (c *EndCrystal) spawnPackets(e *world.Entity) []packet.Packet {
	meta := protocol.EntityMetadata{
		protocol.EntityDataKeyFlags:  int64(1 << protocol.EntityDataFlagHasCollision),
		protocol.EntityDataKeyWidth:  float32(crystalSize),
		protocol.EntityDataKeyHeight: float32(crystalSize),
		protocol.EntityDataKeyScale:  float32(1),
	}
	return []packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: nameEnderCrystal,
		Position: e.Position(), EntityMetadata: meta,
	}}
}

func (c *EndCrystal) movePacket(e *world.Entity) packet.Packet {
	rot := e.Rotation()
	return &packet.MoveActorAbsolute{
		EntityRuntimeID: e.RuntimeID(), Position: e.Position(), Rotation: mgl32.Vec3{rot.Pitch, rot.Yaw, rot.HeadYaw},
	}
}

// tick explodes a hurt crystal, else is BDS's normal tick: in the End it lights a fire in its
// own block when that is air and the block under it is not.
func (c *EndCrystal) tick(e *world.Entity) {
	r := e.Region()
	if r == nil || c.gone {
		return
	}
	if c.exploding {
		c.explode(r, e)
		return
	}
	if r.Dimension() != c.s.end {
		return
	}
	at := e.Position()
	pos := floorPos(at)
	blocks := c.s.reg.Blocks
	below, _ := r.Block(pos.Side(cube.FaceDown), 0)
	here, _ := r.Block(pos, 0)
	if below == blocks.Air() || here != blocks.Air() {
		return
	}
	if fire, ok := blocks.Block(nameFire); ok && r.SetBlock(pos, 0, fire) {
		r.UpdateNeighbours(pos)
	}
}

// explode is its explode component going off: power 6 at its position, no fire, blocks broken
// only when mobGriefing is on.
func (c *EndCrystal) explode(r *world.Region, e *world.Entity) {
	c.gone = true
	x := NewExplosion(e.Position(), crystalPower)
	x.Source, x.Attacker, x.Cause, x.Kind = e, c.attacker, DamageEntityExplosion, ExplosionEndCrystal
	x.BreaksBlocks = c.s.level.MobGriefing
	c.s.Explode(r, x)
	e.Remove()
}

// Hurt is BDS's hurt: any damage above nothing sets it off. attacker may be nil.
func (c *EndCrystal) Hurt(damage float32, attacker *Player) {
	if c.gone || c.exploding || damage <= 0 {
		return
	}
	c.exploding, c.attacker = true, attacker
}

// BlastBox is its box: 2 wide and 2 high from its feet.
func (c *EndCrystal) BlastBox() (lo, hi mgl32.Vec3, ok bool) {
	if c.gone || c.entity == nil {
		return lo, hi, false
	}
	at := c.entity.Position()
	half := float32(crystalSize) / 2
	return mgl32.Vec3{at[0] - half, at[1], at[2] - half}, mgl32.Vec3{at[0] + half, at[1] + crystalSize, at[2] + half}, true
}

// BlastHit sets it off, so crystals set each other off.
func (c *EndCrystal) BlastHit(damage float32, src DamageSource) { c.Hurt(damage, src.Attacker) }

// BlastPush does nothing: it does not move.
func (c *EndCrystal) BlastPush(mgl32.Vec3) {}

func (c *EndCrystal) saveEntity(e *world.Entity) (chunk.Entity, bool) {
	if c.gone {
		return chunk.Entity{}, false
	}
	data := maps.Clone(c.saved)
	if data == nil {
		data = map[string]any{}
	}
	pos, rot := e.Position(), e.Rotation()
	data["identifier"] = nameEnderCrystal
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Motion"] = []float32{0, 0, 0}
	data["Rotation"] = []float32{rot.Yaw, rot.Pitch}
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
}

// loadEndCrystal brings back a saved crystal; its beam target is kept as saved.
func (s *Host) loadEndCrystal(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity) bool {
	pos, ok := floats(saved.Data["Pos"])
	if !ok || len(pos) != 3 {
		return false
	}
	c := &EndCrystal{s: s, saved: saved.Data}
	var rot world.Rotation
	if v, isList := floats(saved.Data["Rotation"]); isList && len(v) == 2 {
		rot = world.Rotation{Yaw: v[0], Pitch: v[1], HeadYaw: v[0]}
	}
	c.entity = r.AddLoadedEntity(at, c.config(saved.ID, s.runtimeIDs.Add(1), mgl32.Vec3{pos[0], pos[1], pos[2]}, rot))
	return c.entity != nil
}

// endCrystalNear finds a crystal the player may hit, by its runtime ID.
func (p *Player) endCrystalNear(id uint64) *EndCrystal {
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
		if c, ok := e.Owner().(*EndCrystal); ok && !c.gone {
			return c
		}
	}
	return nil
}

// useOnEndCrystal is a use on actor transaction aimed at a crystal: a hit with any damage sets
// it off. False if it was not aimed at a crystal.
func (p *Player) useOnEndCrystal(d *protocol.UseItemOnEntityTransactionData) bool {
	c := p.endCrystalNear(d.TargetEntityRuntimeID)
	if c == nil {
		return false
	}
	if d.ActionType != protocol.UseItemOnEntityActionAttack || p.entity.Position().Sub(c.entity.Position()).Len() > attackRangeCreative {
		return true
	}
	damage := p.attributes.get(attrAttackDamage) + p.Tuning(TuneAttackDamage)
	if held := p.inventory.Held(); !held.Empty() {
		damage += float32(p.s.game.combat.damage[held.Name])
	}
	c.Hurt(damage, p)
	return true
}

// placeEndCrystal is BDS's EndCrystalItem::_useOn: on obsidian or bedrock with two air blocks
// above and no entity in the two blocks from the clicked block's floor, a crystal on top of
// the block, the entity_place game event, and one used up outside creative.
func (p *Player) placeEndCrystal(r *world.Region, clicked cube.Pos) bool {
	blocks := p.s.reg.Blocks
	b, _ := r.Block(clicked, 0)
	if name := blocks.Name(b); name != nameObsidian && name != nameBedrock {
		return false
	}
	for dy := 1; dy <= crystalClearHeight; dy++ {
		if above, _ := r.Block(clicked.Add(cube.Pos{0, dy, 0}), 0); above != blocks.Air() {
			return false
		}
	}
	lo := mgl32.Vec3{float32(clicked[0]), float32(clicked[1]), float32(clicked[2])}
	hi := lo.Add(mgl32.Vec3{1, crystalClearHeight, 1})
	if p.entityIn(r, lo, hi) {
		return false
	}
	feet := mgl32.Vec3{lo[0] + 0.5, lo[1] + 1, lo[2] + 0.5}
	c := p.s.SpawnEndCrystal(r.Dimension(), feet)
	if c.entity == nil {
		return false
	}
	p.s.postGameEvent(r, gameEventEntityPlace, feet, p, 0, false)
	p.useUpHeld()
	p.flush()
	return true
}

// entityIn reports an entity whose box overlaps lo to hi.
func (p *Player) entityIn(r *world.Region, lo, hi mgl32.Vec3) bool {
	p.touching = r.AppendEntitiesIn(p.touching[:0], lo[0]-attackScan, lo[2]-attackScan, hi[0]+attackScan, hi[2]+attackScan)
	defer clear(p.touching)
	for _, e := range p.touching {
		o, ok := e.Owner().(Blastable)
		if !ok {
			continue
		}
		bl, bh, has := o.BlastBox()
		if has && bl[0] < hi[0] && bh[0] > lo[0] && bl[1] < hi[1] && bh[1] > lo[1] && bl[2] < hi[2] && bh[2] > lo[2] {
			return true
		}
	}
	return false
}
