package player

import (
	"math"
	"math/rand/v2"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Mob equipment and death drops, as BDS 1.26.60 does them.
//
// A mob created (not loaded) rolls its minecraft:equipment table once it has read its
// components, and each item goes where BDS's
// BDS's code puts it: something wearable in its armour slot, something the
// offhand takes in the offhand, anything else in the main hand. The table's slot drop
// chances are kept for its death.
//
// On death (BDS's code, then the loot system's die event; read from the
// 1.21.3 names, not compared with 1.26.60 line by line): each slot with a drop chance, or
// an item the mob picked up, may drop: always at a chance of 1 or more, else only when a
// player killed it, at the slot's chance plus 1% per Looting level, worn down at random
// unless it always drops. Then its minecraft:loot table drops, rolled with the killer
// player's Looting, each item 0.05 below the mob's feet. Both need the doMobLoot game rule.
// Experience (minecraft:experience_reward's on_death) drops when its death has run its
// course.

// mobDropDelay is the pick-up delay of a mob's drops (10 ticks).
const mobDropDelay = 10

// lootDropBelow is how far under its feet a mob's loot appears (the loot system's -0.05).
const lootDropBelow = -0.05

// equipWearBase is the most a dropped piece of equipment keeps: its durability less 25.
const equipWearBase = 25

// lootingDropBonus is what each Looting level adds to an equipment slot's drop chance.
const lootingDropBonus = 0.01

// gameRuleMobLoot is the game rule death drops need.
const gameRuleMobLoot = "domobloot"

// mobItems is a mob's real equipment: the stacks, each slot's drop chance, and whether it
// picked the item up (always dropped; the pick-up goals do not set it yet).
type mobItems struct {
	stacks [mob.EquipSlots]ItemStack
	drop   [mob.EquipSlots]float32
	picked [mob.EquipSlots]bool
}

// lootRandom is the mob's random as the loot package draws from it.
type lootRandom struct{ r *mob.Random }

func (l lootRandom) Intn(n int32) int32 {
	if l.r == nil {
		if n <= 0 {
			return 0
		}
		return rand.Int32N(n) //nolint:gosec // gameplay randomness
	}
	return l.r.NextInt(n)
}

// Int32N is Intn, for item wear's Unbreaking roll.
func (l lootRandom) Int32N(n int32) int32 { return l.Intn(n) }

func (l lootRandom) Float() float32 {
	if l.r == nil {
		return rand.Float32() //nolint:gosec // gameplay randomness
	}
	return l.r.NextFloat()
}

// lootContext is what the mob's loot rolls depend on.
func (m *Mob) lootContext(r *world.Region) *loot.EntityContext {
	s := m.s
	diff := int32(s.level.Difficulty)
	day := int64(s.DayTime())
	c := &loot.EntityContext{
		Random: lootRandom{m.m.Rand()}, Difficulty: diff,
		Special: loot.SpecialMultiplier(diff, max(r.CurrentTick(), 0), day),
		OnFire:  m.m.FireTicks() > 0, Baby: m.m.Flag(behavior.FlagBaby),
		Variant: m.m.Variant(), Color: m.m.Color(), BoolProperty: m.m.BoolProperty,
	}
	return c
}

// equipFromTable rolls the definition's equipment table once the mob has read its
// components. Runs: the mob's Apply.
func (m *Mob) equipFromTable(r *world.Region) {
	cfg, ready := m.m.EquipmentTable()
	if !ready {
		return
	}
	m.needEquip = false
	if cfg == nil || cfg.Table == "" || m.s.entityLoot == nil {
		return
	}
	for _, d := range m.s.entityLoot.Roll(cfg.Table, m.lootContext(r), nil) {
		for _, it := range m.s.lootStacks(d) {
			m.setItem(m.s.equipSlotFor(it), it)
		}
	}
	for _, c := range cfg.SlotDropChance {
		if slot, ok := mob.EquipSlotByName(c.Slot); ok {
			m.items.drop[slot] = c.DropChance
		}
	}
	m.sendEquipment()
}

// equipSlotFor is where BDS's code puts an item: a wearable in its armour
// slot; the illager banner in the chest slot, as a captain wears it (gives slot 1
// for a banner whose Type is 1); an item the offhand takes in the offhand; anything else in
// the main hand. Other banners stay on the head, as before (that test,, was not
// read).
func (s *Host) equipSlotFor(it ItemStack) int {
	t, ok := s.reg.Items.ByName(it.Name)
	switch {
	case ok && t.Wearable:
		return mob.EquipHead + int(t.ArmorSlot)
	case it.Name == bannerItem:
		if typ, _ := tagInt(it.Tag, bannerTypeTag); typ == BannerOminous {
			return mob.EquipChest
		}
		return mob.EquipHead
	case strings.HasSuffix(it.Name, "banner"):
		return mob.EquipHead
	case ok && t.Offhand:
		return mob.EquipOffhand
	}
	return mob.EquipMainhand
}

// setItem puts a stack in a slot and tells the mob what it now holds.
func (m *Mob) setItem(slot int, it ItemStack) {
	if slot < 0 || slot >= mob.EquipSlots {
		return
	}
	it = m.s.freshStack(it)
	m.items.stacks[slot] = it
	m.m.SetEquipment(slot, mobItem(it))
}

// handPacket and armourPacket are the mob's equipment as those who see it get it.
func (m *Mob) handPacket(slot int) *packet.MobEquipment {
	window := byte(protocol.WindowIDInventory)
	if slot == mob.EquipOffhand {
		window = protocol.WindowIDOffHand
	}
	return &packet.MobEquipment{EntityRuntimeID: m.m.RuntimeID(), NewItem: m.items.stacks[slot].networkShown(m.s.reg), WindowID: window}
}

func (m *Mob) armourPacket() *packet.MobArmourEquipment {
	a, reg := &m.items.stacks, m.s.reg
	return &packet.MobArmourEquipment{
		EntityRuntimeID: m.m.RuntimeID(), Helmet: a[mob.EquipHead].networkShown(reg), Chestplate: a[mob.EquipChest].networkShown(reg),
		Leggings: a[mob.EquipLegs].networkShown(reg), Boots: a[mob.EquipFeet].networkShown(reg), Body: m.bodyItem().networkShown(reg),
	}
}

// bodyItem is what the mob shows in its body slot: its body equipment, else the first body
// armour (registry BodyArmor) in its equip slots, as horse armour put in a horse's
// inventory is drawn on the horse (BDS 1.26.60 MobArmorEquipment's Body, packet capture).
func (m *Mob) bodyItem() ItemStack {
	if st := m.items.stacks[mob.EquipBody]; !st.Empty() {
		return st
	}
	inv := m.m.Inventory()
	for k := range min(len(m.m.EquipSlots()), len(inv)) {
		if inv[k].Empty() {
			continue
		}
		if t, ok := m.s.reg.Items.ByName(inv[k].Name); ok && t.BodyArmor {
			return realStack(inv[k])
		}
	}
	return ItemStack{}
}

// setInventorySlot puts a stack in the mob's inventory slot, running its equip events, and
// shows its body armour again when an equip slot changed.
func (m *Mob) setInventorySlot(k int, it behavior.Item) {
	m.m.SetInventorySlot(k, it)
	if k < len(m.m.EquipSlots()) && m.entity != nil && m.entity.Region() != nil {
		m.sentBody = m.bodyItem()
		m.entity.Send(m.armourPacket())
	}
}

// sendEquipment shows those who see the mob all it holds and wears, as BDS broadcasts a new
// mob's: main hand, offhand, then armour, even when empty.
func (m *Mob) sendEquipment() {
	if m.entity == nil {
		return
	}
	m.entity.Send(m.handPacket(mob.EquipMainhand))
	m.entity.Send(m.handPacket(mob.EquipOffhand))
	m.entity.Send(m.armourPacket())
}

// spawnEquipment appends what BDS sends a player right after AddActor: the armour when any
// is worn, then each hand that holds something.
func (m *Mob) spawnEquipment(dst []packet.Packet) []packet.Packet {
	a := &m.items.stacks
	for _, slot := range []int{mob.EquipHead, mob.EquipChest, mob.EquipLegs, mob.EquipFeet, mob.EquipBody} {
		if !a[slot].Empty() || slot == mob.EquipBody && !m.bodyItem().Empty() {
			dst = append(dst, m.armourPacket())
			break
		}
	}
	for _, slot := range []int{mob.EquipMainhand, mob.EquipOffhand} {
		if !a[slot].Empty() {
			dst = append(dst, m.handPacket(slot))
		}
	}
	return dst
}

// lootStacks turns a loot drop into stacks: the item made as the drop says, split into full
// stacks. An item the registry does not know gives nothing.
func (s *Host) lootStacks(d loot.Drop) []ItemStack {
	if _, ok := s.reg.Items.ByName(d.Name); !ok || d.Count <= 0 {
		return nil
	}
	it := ItemStack{Name: d.Name, Count: 1}
	if d.Smelt {
		if r := s.Recipes().Smelt("furnace", s.craftItem(it)); r != nil && len(r.Output) > 0 {
			if out := itemFromNetwork(s.reg, r.Output[0]); !out.Empty() {
				it = ItemStack{Name: out.Name, Meta: out.Meta, Count: 1, Block: out.Block}
			}
		}
	}
	if id, ok := s.reg.Blocks.Block(it.Name); ok && it.Block == nil {
		blocks := s.reg.Blocks
		it.Block = map[string]any{"name": blocks.Name(id), "states": blocks.States(id), "version": blocks.Version(id)}
	}
	switch {
	case d.Ominous >= 0:
		it.Meta = int16(d.Ominous) //nolint:gosec // a small amplifier
	case d.Data >= 0:
		it.Meta = int16(d.Data) //nolint:gosec // an aux value
	}
	if d.Banner > 0 {
		it.Tag = map[string]any{"Type": d.Banner}
	}
	if d.Damage >= 0 {
		if most := s.maxDamage(it.Name); most > 0 {
			it = withDamage(it, int32(math.Floor(float64((1-d.Damage)*float32(most)))))
		}
	}
	if d.EnchantLevel > 0 || d.EnchantDraw {
		it = s.EnchantItem(it, d.EnchantLevel, d.EnchantBonus, d.Treasure)
	}
	for _, e := range d.Enchants {
		if id, ok := s.enchantByName(e.ID); ok {
			it = it.WithEnchant(id, int16(min(e.Level, math.MaxInt16))) //nolint:gosec // bounded
		}
	}
	per := max(int32(it.maxStack(s.reg)), 1)
	var out []ItemStack
	for left := d.Count; left > 0; left -= per {
		part := it
		part.Count = uint8(min(left, per, 255)) //nolint:gosec // bounded
		out = append(out, part)
	}
	return out
}

// enchantByName finds an enchantment by its name, with or without the namespace.
func (s *Host) enchantByName(name string) (int16, bool) {
	name = strings.TrimPrefix(name, "minecraft:")
	for _, e := range s.Enchantments() {
		if strings.TrimPrefix(e.Name, "minecraft:") == name {
			return e.ID, true
		}
	}
	return 0, false
}

// lootingOf is the Looting level of the item a player holds.
func (s *Host) lootingOf(p *Player) int32 {
	if p == nil {
		return 0
	}
	var n int32
	enchantLevels(p.Held(), func(id, lvl int16) {
		if e, ok := s.game.enchant(id); ok && e.Looting {
			n = max(n, int32(lvl))
		}
	})
	return n
}

// mobLoot reports the doMobLoot game rule, on when the level does not have it.
func (s *Host) mobLoot() bool {
	v, ok := s.GameRule(gameRuleMobLoot)
	return !ok || v
}

// killer is the player who gets credit for the mob's death: the last player to hurt it,
// while that still counts (100 ticks).
func (m *Mob) killer() *Player {
	if m.lastPlayer == nil || m.lastPlayer.entity == nil || !m.m.LastHitByPlayer() {
		return nil
	}
	return m.lastPlayer
}

// dropOnDeath drops the mob's equipment and loot as it dies. Runs: the mob's region.
func (m *Mob) dropOnDeath(r *world.Region) {
	if !m.s.mobLoot() {
		return
	}
	killer := m.killer()
	at := m.entity.Position()
	m.dropEquipment(r, at, killer)
	table := m.m.LootTable()
	if table == "" || m.s.entityLoot == nil {
		return
	}
	c := m.lootContext(r)
	c.KillerPlayer = killer != nil
	c.Looting = m.s.lootingOf(killer)
	if killer != nil {
		c.Killer = "minecraft:player"
		c.Damager = c.Killer
	}
	for _, d := range m.s.entityLoot.Roll(table, c, nil) {
		for _, it := range m.s.lootStacks(d) {
			m.s.spawnMobDrop(r, it, at.Add(mgl32.Vec3{0, lootDropBelow, 0}))
		}
	}
}

// dropEquipment matches BDS.
func (m *Mob) dropEquipment(r *world.Region, at mgl32.Vec3, killer *Player) {
	rnd := lootRandom{m.m.Rand()}
	bonus := float32(m.s.lootingOf(killer)) * lootingDropBonus
	for slot := range m.items.stacks {
		it := m.items.stacks[slot]
		if it.Empty() {
			continue
		}
		chance, picked := m.items.drop[slot], m.items.picked[slot]
		if chance == 0 && !picked {
			continue
		}
		if killer != nil {
			chance += bonus
		}
		always := picked || chance >= 1
		if !picked && !((killer != nil || always) && rnd.Float() < chance) {
			continue
		}
		if most := m.s.maxDamage(it.Name); !always && most > 0 {
			keep := max(most-equipWearBase, 1)
			a := rnd.Intn(keep)
			b := rnd.Intn(a + 1)
			if d := most - b; d <= keep {
				keep = max(d, 1)
			}
			it = withDamage(it, keep)
		}
		m.items.stacks[slot] = ItemStack{}
		m.m.SetEquipment(slot, behavior.Item{})
		m.s.spawnMobDrop(r, it, at)
	}
}

// spawnMobDrop throws an item out where a mob is, as BDS: the usual
// scatter and a 10 tick pick-up delay.
func (s *Host) spawnMobDrop(r *world.Region, it ItemStack, at mgl32.Vec3) {
	s.spawnItem(r.Dimension(), s.freshStack(it), at, mgl32.Vec3{dropDrift(), dropUp, dropDrift()}, world.Rotation{}, mobDropDelay, 0)
}

// dropExperience drops the mob's death experience when its death has run its course.
func (m *Mob) dropExperience(r *world.Region) {
	if !m.s.mobLoot() {
		return
	}
	m.s.SpawnExperience(r, m.entity.Position(), m.m.DeathExperience())
}

// Shoot is ShooterComponent's shot, fired from the mob at its target as BDS's
// BDS's code aims at a target: from the shooter's
// eyes with the projectile's offset, toward the middle of the target's box, starting a
// block out toward it; with gravity the pitch is the lower of the two that reach it at
// that speed (45 degrees up when none does). power above 0 replaces the projectile's own.
// An arrow takes the shooter's aux_val (a stray's slowness arrow, a bogged's poison). The mob is the
// arrow's owner (passed through until the arrow leaves its box, shown to clients) but not
// yet its attacker: no kill credit or death message.
func (m *Mob) Shoot(_ *mob.Mob, target behavior.ID, def string, aux int32, power float32, sound int) {
	r := m.entity.Region()
	if r == nil || m.gone {
		return
	}
	spec, ok := m.s.projectileSpec(def, "")
	if !ok {
		return
	}
	t := m.s.Actor(r, target)
	if t == nil {
		return
	}
	eyes := mglVec(m.m.Eyes())
	_, yaw, head, _ := m.m.Rotation()
	from := projectileEyes(spec, eyes, world.Rotation{Yaw: yaw, HeadYaw: head})
	tp, box := t.Pos(), t.Box()
	dx, dz := tp.X-from[0], tp.Z-from[2]
	dy := (box.Min.Y+box.Max.Y)/2 - from[1]
	h := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	if h < 1e-7 {
		return
	}
	speed := power
	if speed <= 0 {
		speed = spec.Power
	}
	start := mgl32.Vec3{from[0] + dx/h, from[1], from[2] + dz/h}
	dir := mgl32.Vec3{dx, dy, dz}.Normalize()
	if g := spec.Gravity; g != 0 {
		v2 := float64(speed * speed)
		gh := float64(g * h)
		disc := v2*v2 - float64(g)*(2*float64(dy)*v2+gh*float64(h))
		pitch := -45.0
		if disc >= 0 {
			sq := math.Sqrt(disc)
			pitch = -math.Min(math.Atan2(v2-sq, gh), math.Atan2(v2+sq, gh)) * 180 / math.Pi
		}
		yawDeg := math.Atan2(float64(dz), float64(dx))*180/math.Pi - 90
		p, y := pitch*math.Pi/180, yawDeg*math.Pi/180
		dir = mgl32.Vec3{float32(-math.Sin(y) * math.Cos(p)), float32(-math.Sin(p)), float32(math.Cos(y) * math.Cos(p))}
	}
	if pr := m.s.SpawnProjectile(r.Dimension(), spec, start, dir, speed, nil); pr != nil {
		pr.shooter, pr.separated = m, false
		if def == nameArrow && aux > 0 && aux <= math.MaxUint8 {
			pr.aux = uint8(aux)
		}
	}
	if sound != behavior.NoSound {
		m.Sound(m.m, sound, m.m.Eyes())
	}
}

// Explode is minecraft:explode going off (1.21.3 0x1405AFFF0):
// BDS's code with the mob as the source, so it is not hurt and distances are measured
// from it. Damage is an entity explosion; the creeper is not credited as the attacker yet
// (Explosion has players only). An empty particle or sound effect keeps the default.
func (m *Mob) Explode(_ *mob.Mob, at behavior.Vec3, e state.Explosion) {
	r := m.entity.Region()
	if r == nil || m.gone {
		return
	}
	x := NewExplosion(mglVec(at), e.Power)
	x.Source, x.Cause = m.entity, DamageEntityExplosion
	x.Kind = ExplosionPlugin
	if m.m.Type() == "minecraft:creeper" {
		x.Kind = ExplosionCreeper
	}
	x.Fire, x.BreaksBlocks = e.Fire, e.Breaks
	x.AllowUnderwater, x.MaxResistance = e.AllowUnderwater, e.MaxResistance
	x.DamageScale, x.KnockbackScale = e.DamageScaling, e.KnockbackScaling
	x.ToggleBlocks, x.NegatesFallDamage = e.TogglesBlocks, e.NegatesFallDamage
	if e.SoundEffect != "" {
		x.Sound = e.SoundEffect
	}
	m.s.Explode(r, x)
}
