package player

import (
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Armour stands as BDS 1.26.60 runs them: ArmorStand (vtable 0xFAFA028), a mob of the
// armor_stand definition with its own hurt (slot 134, 0xC084820), interaction (slot 117,
// 0xC0841B0, with the equip lambda 0xC29F530 and the pose lambda 0xC29F210), tick (slot 25,
// 0xC085BE0) and save (0xC083FC0 / slot 136), and ArmorStandItem::_useOn (0xC9CC010).
const (
	armorStandID = "minecraft:armor_stand"
	// armorStandPoses is how many poses it has; poses go round 0 to 12.
	armorStandPoses = 13
	// armorStandHitWindow is how many ticks after a first hit a second one breaks it.
	armorStandHitWindow = 5
	// The wobble a first hit starts: hurt time, and the structural value BDS sets with it.
	armorStandHurtTime   = 10
	armorStandStructural = -10
	// armorStandFireSeconds is how long fire sets it burning, and what fire and burning cost
	// its health.
	armorStandFireSeconds = 5
	armorStandFireBurn    = 0.05
	armorStandTickBurn    = 1
	// armorStandDropUp is how far above its feet its own item drops.
	armorStandDropUp = 1
	// Level events: broken, hit, fallen, placed, and the break particles.
	levelEventArmorStandBreak    = 1060
	levelEventArmorStandHit      = 1061
	levelEventArmorStandFall     = 1062
	levelEventArmorStandPlace    = 1063
	levelEventArmorStandParticle = 2017
	// The pose save: a Pose compound with PoseIndex and LastSignal.
	armorStandPoseTag   = "Pose"
	armorStandIndexTag  = "PoseIndex"
	armorStandSignalTag = "LastSignal"
	// armorStandAimRelative is the highest clicked height still read as relative to the
	// stand; the client's clicked position was not checked against BDS's handler, which
	// reads it as a world position, so both are taken.
	armorStandAimRelative = 2.5
)

// BDS's equipment slot numbers in the armour stand's code: the hands, then the armour
// container's slots from 2.
const (
	standSlotMainhand = 0
	standSlotArmour   = 2
)

// armorStand is what an armour stand keeps beyond any mob: its pose, the last redstone
// signal it took a pose from, and when it was last hit.
type armorStand struct {
	pose, lastSignal int32
	lastHit          int64
	// hurt counts the wobble down; while it runs the actor data is sent each tick.
	hurt int32
	// hurtDir is BDS's hurt direction, turned over on each wobble.
	hurtDir int32
}

// isArmorStand reports the mob to be an armour stand.
func (m *Mob) isArmorStand() bool { return m.stand != nil }

// standSlot is the mob equipment slot of BDS's slot number.
func standSlot(n int) int {
	switch n {
	case standSlotMainhand:
		return mob.EquipMainhand
	case 1:
		return mob.EquipOffhand
	case standSlotArmour:
		return mob.EquipHead
	case standSlotArmour + 1:
		return mob.EquipChest
	case standSlotArmour + 2:
		return mob.EquipLegs
	case standSlotArmour + 3:
		return mob.EquipFeet
	}
	return mob.EquipBody
}

// standHeightSlot is the slot an empty hand reaches at height dy above the feet, in BDS's
// order of tests: feet, chest (taking the main hand first when it holds something), legs,
// head. False below the feet's band and in the gaps between.
func (m *Mob) standHeightSlot(dy float32) (int, bool) {
	switch {
	case dy >= 0.1 && dy < 0.55:
		return standSlotArmour + 3, true
	case dy >= 0.9 && dy < 1.6:
		if !m.items.stacks[mob.EquipMainhand].Empty() {
			return standSlotMainhand, true
		}
		return standSlotArmour + 1, true
	case dy >= 0.4 && dy < 1.2:
		return standSlotArmour + 2, true
	case dy >= 1.6:
		return standSlotArmour, true
	}
	return 0, false
}

// standItemSlot is where a held item goes: a wearable into its armour slot, anything else
// into the main hand.
func (s *Host) standItemSlot(it ItemStack) int {
	if t, ok := s.reg.Items.ByName(it.Name); ok && t.Wearable {
		return standSlotArmour + int(t.ArmorSlot)
	}
	return standSlotMainhand
}

// itemLocked reports an item with the minecraft:item_lock tag set, which BDS never lets
// onto an armour stand.
func itemLocked(it ItemStack) bool {
	v, ok := tagInt(it.Tag, "minecraft:item_lock")
	return ok && v != 0
}

// interactArmorStand is a player's click on an armour stand: sneaking turns the pose on
// by one; otherwise the held item goes on, or the item at the clicked height comes off.
func (p *Player) interactArmorStand(o *Mob, d *protocol.UseItemOnEntityTransactionData) {
	if !p.selectSlot(d.HotBarSlot, d.HeldItem.Stack.NetworkID) {
		p.sendHeld()
		p.flush()
		return
	}
	r := p.entity.Region()
	if r == nil || o.gone || p.entity.Position().Sub(o.entity.Position()).Len() > attackRangeCreative {
		return
	}
	// Mob's own interactions come first (0xB3D1540): a name tag names the stand.
	if p.nameMob(o) {
		return
	}
	if p.Moving(MoveSneaking) {
		o.setStandPose((o.stand.pose + 1) % armorStandPoses)
		return
	}
	held := p.Held()
	var slot int
	if held.Empty() {
		dy := d.ClickedPosition[1]
		if dy > armorStandAimRelative || dy < 0 {
			dy -= float32(o.entity.Position()[1])
		}
		var ok bool
		if slot, ok = o.standHeightSlot(dy); !ok {
			return
		}
	} else {
		if held.Name == armorStandID || itemLocked(held) {
			return
		}
		slot = p.s.standItemSlot(held)
	}
	// BDS refuses the interaction when the hand and the slot hold the same stack, an empty
	// hand at an empty slot included, so nothing swaps and no sound plays.
	worn := o.items.stacks[standSlot(slot)]
	if worn.Empty() && held.Empty() {
		return
	}
	if !held.Empty() {
		if worn.Count == held.Count && sameStack(worn, held) {
			return
		}
		if held.Count >= 2 && !worn.Empty() {
			return
		}
	}
	p.equipArmorStand(r, o, slot)
}

// equipArmorStand is BDS's equip lambda for a slot: in creative a held item goes on as a
// copy of one into an empty slot; a held stack of two or more puts one on (used outside
// creative); anything else swaps the held stack with what is in the slot. Then the
// player's items and the stand's are sent and the place sound plays.
func (p *Player) equipArmorStand(r *world.Region, o *Mob, n int) {
	slot := standSlot(n)
	held, worn := p.Held(), o.items.stacks[slot]
	inv := &p.inventory
	switch {
	case p.creative() && !held.Empty() && worn.Empty():
		o.setItem(slot, splitOne(held))
	case !held.Empty() && held.Count >= 2:
		o.setItem(slot, splitOne(held))
		if !p.creative() {
			held.Count--
			inv.main[inv.held] = held
		}
	default:
		o.setItem(slot, held)
		inv.main[inv.held] = worn
	}
	o.sendEquipment()
	p.sendHeld()
	p.sendEquipment()
	p.flush()
	at := o.entity.Position().Add(mgl64.Vec3{0.5, 0.5, 0.5})
	playSound(r, floorPos(at), soundEvent{name: packet.SoundEventArmorPlace, pos: at, extra: -1}, nil)
}

// setStandPose puts the stand in a pose and shows it.
func (o *Mob) setStandPose(pose int32) {
	o.stand.pose = pose
	if o.entity != nil && o.entity.Region() != nil {
		o.entity.Send(actorData(o.entity.RuntimeID(), protocol.EntityMetadata{protocol.EntityDataKeyPoseIndex: pose}))
	}
}

// standHurt is ArmorStand's hurt: what each cause does to a stand, which never takes
// damage the way mobs do. It reports the damage it took, 1 when it broke or wobbled.
func (o *Mob) standHurt(by *Player, cause DamageCause) float32 {
	r := o.entity.Region()
	if r == nil || o.gone {
		return 0
	}
	switch cause {
	case DamageFall:
		pos := o.entity.Position()
		levelEvent(r, floorPos(pos), pos, levelEventArmorStandFall, 0)
		return 0
	case DamageFire:
		if o.m.FireTicks() <= 0 {
			o.SetOnFire(armorStandFireSeconds)
		}
		return o.standBurn(r, armorStandFireBurn)
	case DamageFireTick:
		return o.standBurn(r, armorStandTickBurn)
	case DamageBlockExplosion, DamageEntityExplosion, DamageSelfDestruct:
		o.standDropEquipment(r)
		o.remove(o.entity)
		return 1
	case DamageVoid:
		o.remove(o.entity)
		return 1
	case causeProjectile:
		o.standBreak(r, true)
		return 1
	}
	if by == nil || by.entity == nil {
		return 0
	}
	if by.creative() {
		o.standBreak(r, false)
		return 1
	}
	pos := o.entity.Position()
	levelEvent(r, floorPos(pos), pos, levelEventArmorStandHit, 0)
	now := r.CurrentTick()
	if now-o.stand.lastHit > armorStandHitWindow {
		o.stand.lastHit = now
		o.standWobble()
		return 1
	}
	o.standBreak(r, true)
	return 1
}

// standBurn takes health off; at none the stand drops what it holds and goes.
func (o *Mob) standBurn(r *world.Region, amount float32) float32 {
	h := o.m.HealthValue() - amount
	if h <= 0 {
		o.standDropEquipment(r)
		o.remove(o.entity)
		return amount
	}
	o.m.SetHealth(h)
	return amount
}

// standWobble shows a first hit: hurt time 10, the hurt direction turned over.
func (o *Mob) standWobble() {
	o.stand.hurt = armorStandHurtTime
	o.stand.hurtDir = -o.stand.hurtDir
	o.entity.Send(actorData(o.entity.RuntimeID(), protocol.EntityMetadata{
		protocol.EntityDataKeyHurtDirection:       o.stand.hurtDir,
		protocol.EntityDataKeyHurt:                int32(armorStandHurtTime),
		protocol.EntityDataKeyStructuralIntegrity: int32(armorStandStructural),
	}))
}

// standBreak takes the stand down with BDS's particles and sound. With drops, as a second
// hit in survival, its own item and what it holds come out if doentitydrops is on; a
// creative player's break leaves nothing.
func (o *Mob) standBreak(r *world.Region, drops bool) {
	pos := o.entity.Position()
	if drops && o.s.level.DoEntityDrops {
		o.s.spawnMobDrop(r, ItemStack{Name: armorStandID, Count: 1}, pos.Add(mgl64.Vec3{0, armorStandDropUp, 0}))
		o.standDropEquipment(r)
	}
	_, h := o.m.Size()
	at := floorPos(pos)
	levelEvent(r, at, pos, levelEventArmorStandParticle, int32(h))
	levelEvent(r, at, pos, levelEventArmorStandBreak, 0)
	o.remove(o.entity)
}

// standDropEquipment throws out the armour, then the main hand and offhand, each scattered
// as BDS's (half a block across, a quarter to one and three quarters up), if doentitydrops
// is on.
func (o *Mob) standDropEquipment(r *world.Region) {
	if !o.s.level.DoEntityDrops {
		return
	}
	pos := o.entity.Position()
	for _, slot := range [...]int{mob.EquipHead, mob.EquipChest, mob.EquipLegs, mob.EquipFeet, mob.EquipMainhand, mob.EquipOffhand} {
		it := o.items.stacks[slot]
		if it.Empty() {
			continue
		}
		at := pos.Add(mgl64.Vec3{float64(rand.Float32()*0.5 - 0.25), float64(rand.Float32()*1.5 + 0.25), float64(rand.Float32()*0.5 - 0.25)}) //nolint:gosec // a scatter, not a secret
		o.s.spawnMobDrop(r, it, at)
		o.setItem(slot, ItemStack{})
	}
	o.sendEquipment()
}

// tickArmorStand is ArmorStand's tick: the wobble counts down, and a redstone signal at
// its block or the block under it puts it in pose signal-1 (up to 12) when the signal
// changes.
func (o *Mob) tickArmorStand(r *world.Region) {
	st := o.stand
	if st.hurt > 0 {
		st.hurt--
		o.entity.Send(actorData(o.entity.RuntimeID(), protocol.EntityMetadata{protocol.EntityDataKeyHurt: st.hurt}))
	}
	signal := o.s.circuits.Signal
	if signal == nil {
		return
	}
	at := floorPos(o.entity.Position())
	s := signal(r, at)
	if s <= 0 {
		s = signal(r, at.Side(cube.FaceDown))
	}
	if s <= 0 {
		st.lastSignal = 0
		return
	}
	if s != st.lastSignal {
		if pose := min(s, armorStandPoses) - 1; pose != st.pose {
			o.setStandPose(pose)
		}
	}
	st.lastSignal = s
}

// saveArmorStand adds the Pose compound.
func (o *Mob) saveArmorStand(data map[string]any) {
	data[armorStandPoseTag] = map[string]any{armorStandIndexTag: o.stand.pose, armorStandSignalTag: o.stand.lastSignal}
}

// loadArmorStand reads it back; a missing field is 0, as BDS reads it.
func (o *Mob) loadArmorStand(data map[string]any) {
	m, ok := data[armorStandPoseTag].(map[string]any)
	if !ok {
		return
	}
	if v, ok := tagInt(m, armorStandIndexTag); ok {
		o.stand.pose = ((v % armorStandPoses) + armorStandPoses) % armorStandPoses
	}
	if v, ok := tagInt(m, armorStandSignalTag); ok {
		o.stand.lastSignal = v
	}
}

// placeArmorStand is ArmorStandItem::_useOn: not on the bottom of a block; in the clicked
// block if it can be built over, else next to it; that block and the one above must be air
// or built over, with no entity in the two blocks. Those blocks are cleared (liquids
// stay), and the stand faces back toward the player in steps of 45 degrees.
func (p *Player) placeArmorStand(r *world.Region, clicked cube.Pos, face int32) bool {
	if face == int32(cube.FaceDown) || !p.inReach(clicked) {
		return false
	}
	at := clicked
	if b, ok := r.Block(clicked, 0); !ok || !p.s.buildOver(b) {
		at = clicked.Side(cube.Face(face))
	}
	above := at.Side(cube.FaceUp)
	for _, pos := range [...]cube.Pos{at, above} {
		b, ok := r.Block(pos, 0)
		if !ok || !p.s.buildOver(b) {
			return false
		}
	}
	if p.s.entityIn(r, blockVec(at), blockVec(at).Add(mgl64.Vec3{1, 2, 1})) {
		return false
	}
	blocks := p.s.reg.Blocks
	for _, pos := range [...]cube.Pos{at, above} {
		b, _ := r.Block(pos, 0)
		if m := blocks.Properties(b).Material; b != blocks.Air() && m != registry.MaterialWater && m != registry.MaterialLava {
			r.SetBlock(pos, 0, blocks.Air())
			r.UpdateNeighbours(pos)
		}
	}
	yaw := float32(math.Mod(float64(p.entity.Rotation().Yaw), 360))
	if yaw < 0 {
		yaw += 360
	}
	yaw = float32(math.Floor(float64((yaw-180+22.5)/45))) * 45
	pos := blockVec(at).Add(mgl64.Vec3{0.5, 0, 0.5})
	if _, ok := p.s.SpawnMob(r.Dimension(), armorStandID, pos, yaw); !ok {
		return false
	}
	if !p.creative() {
		p.useHeld()
	}
	levelEvent(r, at, pos, levelEventArmorStandPlace, 0)
	return true
}

// buildOver reports a block an armour stand can be put in: air, or one with BDS's
// replaceable component.
func (s *Host) buildOver(b uint32) bool {
	return b == s.reg.Blocks.Air() || s.game.replaceables[s.reg.Blocks.Name(b)]
}

// entityIn reports any player or mob with its box inside lo to hi.
func (s *Host) entityIn(r *world.Region, lo, hi mgl64.Vec3) bool {
	for _, e := range r.AppendEntitiesIn(nil, lo[0]-1, lo[2]-1, hi[0]+1, hi[2]+1) {
		elo, ehi, _, ok := s.EntityBox(e)
		if ok && overlaps(lo, hi, elo, ehi) {
			return true
		}
	}
	return false
}

// splitOne is one item of s as a stack of its own. Its tags are copied, not shared: the
// item left in the hand is worn and renamed in place, and that must not change the one on
// the stand.
func splitOne(s ItemStack) ItemStack {
	s.Count = 1
	if s.Tag != nil {
		s.Tag = cloneNBT(s.Tag)
	}
	return s
}

// cloneNBT deep copies an NBT compound: nested compounds and lists are copied too.
func cloneNBT(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneNBTValue(v)
	}
	return out
}

// cloneNBTValue deep copies one NBT value; numbers and strings are values already.
func cloneNBTValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneNBT(t)
	case []any:
		l := make([]any, len(t))
		for i, x := range t {
			l[i] = cloneNBTValue(x)
		}
		return l
	case []map[string]any:
		l := make([]map[string]any, len(t))
		for i, x := range t {
			l[i] = cloneNBT(x)
		}
		return l
	case []int32:
		return append([]int32(nil), t...)
	case []byte:
		return append([]byte(nil), t...)
	case []int64:
		return append([]int64(nil), t...)
	}
	return v
}
