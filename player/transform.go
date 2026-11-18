package player

import (
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/helix/mob"
)

// Transform turns the mob into another one when its minecraft:transformation comes due:
// BDS 1.26.60's BDS's code (from
// TransformationSystem) with BDS's code.
// In BDS's order:
//
//   - drop_equipment throws out everything the mob holds and wears (Mob);
//     drop_inventory has nothing to drop, mobs have no inventory yet;
//   - the new entity is made where the old one stands, with its rotation, init method
//     transformed (or the event of an "id<event>" into), its name tag copied, and the old one
//     removed without dying;
//   - preserve_equipment puts both hands and the five armour slots on the new mob where the
//     old one had an item;
//   - a persistent mob stays persistent; each transformation_sound plays at the old mob's
//     body (a name that is not a level sound event, such as the pig's "mob.pig.death",
//     plays nothing); a leashed mob's leash moves to the new one;
//   - add's component groups go on the new mob.
//
// Not done: keep_level (villager trade tiers), keep_owner (mobs have no owners yet), the
// villager cure's gossip, health is not carried (BDS does not either). With
// preserve_equipment the new mob does not roll its own equipment table.
func (m *Mob) Transform(_ *mob.Mob, c state.TransformationConfig) {
	r := m.entity.Region()
	if r == nil || m.gone || c.Into == "" {
		return
	}
	at := m.entity.Position()
	pitch, yaw, _, _ := m.m.Rotation()
	if c.DropEquipment {
		m.dropAllEquipment(at)
	}
	into, event := splitSpawnEvent(c.Into)
	if to, ok := legacyEntityName(into); ok {
		into = to
	}
	method := definition.InitTransformed
	if event != "" {
		method = definition.InitEvent
	}
	n, ok := m.s.newMob(into, at, yaw, method, event)
	if !ok {
		return
	}
	n.m.SetRotation(yaw, pitch)
	if name := m.m.NameTag(); name != "" {
		n.m.SetNameTag(name)
		n.m.SetNameVisible(m.m.Flag(mob.FlagAlwaysShowName))
	}
	if c.PreserveEquipment {
		m.passEquipment(n)
	}
	if m.m.Persistent() {
		n.m.SetPersistent()
	}
	if h := m.m.LeashHolder(); h != behavior.NoID {
		n.m.SetLeashHolder(h)
	}
	for _, g := range c.Add {
		n.m.AddGroup(g)
	}
	body := m.bodyCentre()
	for _, s := range c.TransformationSound {
		if !mob.SoundKnown(s) {
			continue
		}
		playSound(r, floorPos(body), soundEvent{name: s, pos: body, extra: -1, entity: into}, nil)
	}
	n.add(r.Dimension())
	m.remove(m.entity)
}

// splitSpawnEvent splits "minecraft:drowned<minecraft:as_adult>" into the identifier and the
// event; the event is "" when there is none.
func splitSpawnEvent(id string) (typ, event string) {
	if i := strings.IndexByte(id, '<'); i > 0 && strings.HasSuffix(id, ">") {
		return id[:i], id[i+1 : len(id)-1]
	}
	return id, ""
}

// legacyEntityName is the entity an old identifier names: vanilla's pig still turns into
// "minecraft:pig_zombie", the zombified piglin's old name (BDS 1.26.60 keeps the string
// beside "zombie_pigman"; its alias table was not traced).
func legacyEntityName(id string) (string, bool) {
	if id == "minecraft:pig_zombie" {
		return "minecraft:zombie_pigman", true
	}
	return "", false
}

// dropAllEquipment throws out every item the mob holds and wears, whatever its drop chances.
func (m *Mob) dropAllEquipment(at mgl32.Vec3) {
	r := m.entity.Region()
	for slot := range m.items.stacks {
		it := m.items.stacks[slot]
		if it.Empty() {
			continue
		}
		m.items.stacks[slot] = ItemStack{}
		m.m.SetEquipment(slot, behavior.Item{})
		m.s.spawnMobDrop(r, it, at)
	}
}

// passEquipment puts the mob's held and worn items on n, slot for slot, where it has one.
// The new mob then does not roll its own equipment table.
func (m *Mob) passEquipment(n *Mob) {
	n.needEquip = false
	for slot, it := range m.items.stacks {
		if !it.Empty() {
			n.setItem(slot, it)
			n.items.drop[slot] = m.items.drop[slot]
		}
	}
}

// bodyCentre is the middle of the mob's box, attach point 1.
func (m *Mob) bodyCentre() mgl32.Vec3 {
	b := m.m.Box()
	return mgl32.Vec3{(b.Min.X + b.Max.X) / 2, (b.Min.Y + b.Max.Y) / 2, (b.Min.Z + b.Max.Z) / 2}
}
