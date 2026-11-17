package player

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
)

// Mob items: what the mob runtime's item components (mob/items.go) need from the engine.
// Item entities are shown to mobs as itemEntity actors, the real stacks travel in
// behavior.Item.Data, and itemRules answers the item questions from the registry.

// itemEntity is an item entity as the mob runtime sees it. Mobs read it while they think;
// an item's own think step never changes what it reads (stack, delay, taken, position).
type itemEntity struct{ i *ItemActor }

func (a itemEntity) ID() behavior.ID {
	if a.i.entity == nil {
		return behavior.NoID
	}
	return behavior.ID(a.i.entity.UniqueID())
}

func (a itemEntity) Pos() behavior.Vec3 {
	if a.i.entity == nil {
		return behavior.Vec3{}
	}
	return vec(a.i.entity.Position())
}

func (a itemEntity) Box() behavior.Box {
	p := a.Pos()
	return behavior.Box{
		Min: behavior.Vec3{X: p.X - itemHalfSize, Y: p.Y - itemHalfSize, Z: p.Z - itemHalfSize},
		Max: behavior.Vec3{X: p.X + itemHalfSize, Y: p.Y + itemHalfSize, Z: p.Z + itemHalfSize},
	}
}

func (a itemEntity) Eyes() behavior.Vec3                { return a.Pos() }
func (a itemEntity) Alive() bool                        { return !a.Removed() }
func (a itemEntity) Removed() bool                      { return a.i.taken || a.i.stack.Empty() }
func (a itemEntity) Pickable() bool                     { return false }
func (a itemEntity) Interacts() bool                    { return true }
func (a itemEntity) IsMob() bool                        { return false }
func (a itemEntity) IsPlayer() bool                     { return false }
func (a itemEntity) Flag(int) bool                      { return false }
func (a itemEntity) Type() string                       { return itemEntityID }
func (a itemEntity) GameMode() int                      { return -1 }
func (a itemEntity) Riders() []behavior.ID              { return nil }
func (a itemEntity) Vehicle() behavior.ID               { return behavior.NoID }
func (a itemEntity) Owner() behavior.ID                 { return a.i.thrower }
func (a itemEntity) Team() int                          { return 0 }
func (a itemEntity) Dimension() int                     { return 0 }
func (a itemEntity) HeadRotation() (pitch, yaw float32) { return 0, 0 }
func (a itemEntity) BodyYaw() float32                   { return 0 }
func (a itemEntity) Invulnerable() bool                 { return false }
func (a itemEntity) HasHealth() bool                    { return false }
func (a itemEntity) Targetable() bool                   { return false }
func (a itemEntity) Monster() bool                      { return false }
func (a itemEntity) ArmourCoverage() float32            { return 0 }
func (a itemEntity) WearsHeadOf(string) bool            { return false }
func (a itemEntity) Target() behavior.ID                { return behavior.NoID }
func (a itemEntity) HijacksMount() bool                 { return false }
func (a itemEntity) LastHurtByMob() behavior.ID         { return behavior.NoID }
func (a itemEntity) LastHurtMob() (behavior.ID, int32)  { return behavior.NoID, 0 }
func (a itemEntity) BlocksSpace() bool                  { return false }
func (a itemEntity) AttachPos(int) behavior.Vec3        { return a.Pos() }

var _ behavior.Actor = itemEntity{}

// itemEntityID is the item entity's identifier.
const itemEntityID = "minecraft:item"

// --- mob.ItemEnv ---

// ItemOf is the stack an item entity holds, its real stack in Data.
func (s *Host) ItemOf(a behavior.Actor) (behavior.Item, bool) {
	e, ok := a.(itemEntity)
	if !ok {
		return behavior.Item{}, false
	}
	return mobItem(e.i.stack), true
}

// PickupDelay is an item entity's pick-up delay.
func (s *Host) PickupDelay(a behavior.Actor) int32 {
	if e, ok := a.(itemEntity); ok {
		return e.i.pickupDelay
	}
	return 0
}

// ItemOwner is the player who dropped an item entity, NoID for any other item.
func (s *Host) ItemOwner(a behavior.Actor) behavior.ID {
	if e, ok := a.(itemEntity); ok {
		return e.i.thrower
	}
	return behavior.NoID
}

// Items appends the item entities in r whose boxes touch b.
func (s *Host) Items(r *world.Region, dst []behavior.Actor, b behavior.Box) []behavior.Actor {
	sc := s.scratch()
	defer sc.done()
	sc.list = r.AppendEntitiesIn(sc.list, b.Min.X-itemHalfSize, b.Min.Z-itemHalfSize, b.Max.X+itemHalfSize, b.Max.Z+itemHalfSize)
	for _, e := range sc.list {
		i, ok := e.Owner().(*ItemActor)
		if !ok || i.taken {
			continue
		}
		a := itemEntity{i}
		bx := a.Box()
		if bx.Min.X < b.Max.X && bx.Max.X > b.Min.X && bx.Min.Y < b.Max.Y && bx.Max.Y > b.Min.Y && bx.Min.Z < b.Max.Z && bx.Max.Z > b.Min.Z {
			dst = append(dst, a)
		}
	}
	return dst
}

// SpecialMultiplier is the level's special multiplier.
func (s *Host) SpecialMultiplier(r *world.Region) float32 {
	return loot.SpecialMultiplier(s.level.Difficulty, max(r.CurrentTick(), 0), int64(s.DayTime()))
}

// itemActorByID is the item entity with the unique ID in r, nil when there is none.
func (s *Host) itemActorByID(r *world.Region, id behavior.ID) *ItemActor {
	i, _ := s.entityByID(r, id).(*ItemActor)
	return i
}

// mobItem is a stack as the mob runtime holds it.
func mobItem(st ItemStack) behavior.Item {
	if st.Empty() {
		return behavior.Item{}
	}
	return behavior.Item{Name: st.Name, Aux: st.Meta, Count: int32(st.Count), Data: st}
}

// realStack is the engine's stack for a mob's item: the one it carries with its new count,
// or a new stack of the name and aux.
func realStack(it behavior.Item) ItemStack {
	if it.Empty() {
		return ItemStack{}
	}
	n := uint8(min(max(it.Count, 0), 255)) //nolint:gosec // clamped
	if st, ok := it.Data.(ItemStack); ok && st.Name == it.Name {
		st.Count = n
		return st
	}
	return ItemStack{Name: it.Name, Meta: it.Aux, Count: n}
}

// --- mob.ItemHooks ---

// SetSlot puts the goal's stack in the slot and shows it. A picked-up item always drops.
func (m *Mob) SetSlot(_ *mob.Mob, slot int, it behavior.Item) {
	if slot < 0 || slot >= mob.EquipSlots {
		return
	}
	st := m.s.freshStack(realStack(it))
	m.items.stacks[slot] = st
	m.items.picked[slot] = !st.Empty()
	m.m.SetEquipment(slot, mobItem(st))
	if m.entity == nil {
		return
	}
	if slot <= mob.EquipOffhand {
		m.entity.Send(m.handPacket(slot))
		return
	}
	m.entity.Send(m.armourPacket())
}

// DropItem throws the stack out at the mob.
func (m *Mob) DropItem(_ *mob.Mob, it behavior.Item) {
	if m.entity == nil || it.Empty() {
		return
	}
	if r := m.entity.Region(); r != nil {
		m.s.spawnMobDrop(r, realStack(it), m.entity.Position())
	}
}

// SetItemCount sets the item entity's count, removing it at 0.
func (m *Mob) SetItemCount(_ *mob.Mob, id behavior.ID, n int32) {
	r := m.region()
	if r == nil {
		return
	}
	i := m.s.itemActorByID(r, id)
	if i == nil || i.taken {
		return
	}
	if n <= 0 {
		i.taken = true
		i.entity.Remove()
		return
	}
	i.stack.Count = uint8(min(n, 255)) //nolint:gosec // clamped
	i.showCount()
}

// TakeItem shows the mob taking the item entity.
func (m *Mob) TakeItem(_ *mob.Mob, id behavior.ID) {
	r := m.region()
	if r == nil || m.entity == nil {
		return
	}
	if i := m.s.itemActorByID(r, id); i != nil && i.entity != nil {
		i.entity.Send(&packet.TakeItemActor{ItemEntityRuntimeID: i.entity.RuntimeID(), TakerEntityRuntimeID: m.m.RuntimeID()})
	}
}

// region is the region holding the mob, nil when it is not in one.
func (m *Mob) region() *world.Region {
	if m.entity == nil {
		return nil
	}
	return m.entity.Region()
}

var _ mob.ItemHooks = (*Mob)(nil)

// --- behavior.ItemRules ---

// itemRules answers the mob item components from the item registry and BDS's stack rules.
type itemRules struct{ s *Host }

// The potion-like items whose aux is a variant (Item::getVariant).
var variantItems = map[string]bool{"minecraft:potion": true, "minecraft:splash_potion": true, "minecraft:lingering_potion": true}

// Matches reports the stack matching an item descriptor: a name ("minecraft:x", "x"), a
// name with an aux ("minecraft:x:15"), or an object with item/name (and aux) or tags (a
// q.all_tags or q.any_tag Molang call).
func (r itemRules) Matches(d string, it behavior.Item) bool {
	if it.Empty() {
		return false
	}
	d = strings.TrimSpace(d)
	if strings.HasPrefix(d, "{") {
		var o struct {
			Item, Name, Tags string
			Aux              *int16 `json:"aux"`
		}
		if json.Unmarshal([]byte(d), &o) != nil {
			return false
		}
		switch {
		case o.Tags != "":
			return r.tagsMatch(o.Tags, it.Name)
		case o.Item != "":
			return r.nameMatches(o.Item, it)
		case o.Name != "":
			if o.Aux != nil && *o.Aux != it.Aux {
				return false
			}
			return r.nameMatches(o.Name, it)
		}
		return false
	}
	return r.nameMatches(d, it)
}

// nameMatches compares a name with an optional ":aux" ending.
func (r itemRules) nameMatches(d string, it behavior.Item) bool {
	if i := strings.LastIndexByte(d, ':'); i > 0 && strings.Count(d, ":") == 2 {
		if aux, err := strconv.ParseInt(d[i+1:], 10, 16); err == nil {
			return int16(aux) == it.Aux && itemName(d[:i]) == it.Name
		}
	}
	return itemName(d) == it.Name
}

// itemAliases are BDS's names for renamed items that entity files still use, as its item
// registry's alias table (BDS 1.26.60 0xC66BA90) maps them; BDS looks names up in lower
// case.
var itemAliases = map[string]string{
	"minecraft:carrotonastick":    "minecraft:carrot_on_a_stick",
	"minecraft:horsearmorleather": "minecraft:leather_horse_armor",
	"minecraft:horsearmoriron":    "minecraft:iron_horse_armor",
	"minecraft:horsearmorgold":    "minecraft:golden_horse_armor",
	"minecraft:horsearmordiamond": "minecraft:diamond_horse_armor",
}

// itemName is the item a descriptor's name means: namespaced, lower case and through
// BDS's aliases.
func itemName(d string) string {
	n := ns(d)
	if a, ok := itemAliases[strings.ToLower(n)]; ok {
		return a
	}
	return n
}

// tagsMatch runs a q.all_tags('a', 'b') or q.any_tag(...) call on the item's tags.
func (r itemRules) tagsMatch(expr, name string) bool {
	t, ok := r.s.reg.Items.ByName(name)
	if !ok {
		return false
	}
	open := strings.IndexByte(expr, '(')
	end := strings.LastIndexByte(expr, ')')
	if open < 0 || end < open {
		return false
	}
	fn := strings.ToLower(strings.TrimSpace(expr[:open]))
	all := strings.HasSuffix(fn, "all_tags")
	if !all && !strings.HasSuffix(fn, "any_tag") {
		return false
	}
	anyHas := false
	for _, a := range strings.Split(expr[open+1:end], ",") {
		tag := strings.Trim(strings.TrimSpace(a), `'"`)
		has := false
		for _, h := range t.Tags {
			if h == tag {
				has = true
				break
			}
		}
		if all && !has {
			return false
		}
		anyHas = anyHas || has
	}
	return anyHas
}

// ns puts the minecraft namespace on a bare name.
func ns(n string) string {
	if strings.IndexByte(n, ':') < 0 {
		return "minecraft:" + n
	}
	return n
}

// MaxStack is the item's maximum stack size.
func (r itemRules) MaxStack(it behavior.Item) int32 {
	if t, ok := r.s.reg.Items.ByName(it.Name); ok && t.MaxStack > 0 {
		return int32(t.MaxStack)
	}
	return 1
}

// Stackable reports a maximum stack of two or more and not a damaged damageable item.
func (r itemRules) Stackable(it behavior.Item) bool {
	return r.MaxStack(it) >= 2 && !(r.s.maxDamage(it.Name) > 0 && r.Damage(it) > 0)
}

// Stacks reports the same item, aux, block and user data.
func (r itemRules) Stacks(a, b behavior.Item) bool {
	if a.Empty() || b.Empty() {
		return false
	}
	return realStack(a).stacksWith(realStack(b))
}

// SameKind is the same item, and the same aux unless the item is damageable.
func (r itemRules) SameKind(a, b behavior.Item) bool {
	return a.Name == b.Name && (a.Aux == b.Aux || r.s.maxDamage(a.Name) > 0)
}

// SameItem is the same item, whatever its aux or damage.
func (r itemRules) SameItem(a, b behavior.Item) bool { return a.Name == b.Name }

// Damage is the stack's Damage tag.
func (r itemRules) Damage(it behavior.Item) int16 {
	return int16(min(itemDamage(realStack(it)), 1<<15-1)) //nolint:gosec // clamped
}

// Wearable is the armour slot the item goes in.
func (r itemRules) Wearable(it behavior.Item) (int, bool) {
	t, ok := r.s.reg.Items.ByName(it.Name)
	if !ok || !t.Wearable {
		return 0, false
	}
	return int(t.ArmorSlot), true
}

// Bound reports curse of binding on the stack.
func (r itemRules) Bound(it behavior.Item) bool {
	for _, e := range flatEnchants(realStack(it)) {
		if e.ID == enchantBinding {
			return true
		}
	}
	return false
}

// Variant is a potion's aux.
func (r itemRules) Variant(it behavior.Item) (int32, bool) {
	if variantItems[it.Name] {
		return int32(it.Aux), true
	}
	return 0, false
}

// Recipe is not answered yet (shareables' craft_into, villagers turning wheat into bread).
func (r itemRules) Recipe(behavior.Item, behavior.Item) (int32, bool) { return 0, false }

// BaseGameAtLeast is true: Helix runs the current base game version.
func (r itemRules) BaseGameAtLeast(int, int, int) bool { return true }

var _ behavior.ItemRules = itemRules{}
