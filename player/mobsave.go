package player

import (
	"maps"
	"math"
	"strconv"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/state"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/world/chunk"
	"github.com/helix-bedrock/world/mcdb"
)

// Mobs saved with their chunk, in BDS 1.26.60's actor format, so a BDS world's mobs load
// in Helix and Helix's in BDS. The field names and order are Actor::saveWithoutId
// (0xB3B6C10): UniqueID, Pos, Rotation, Motion, CustomName, CustomNameVisible,
// FallDistance, OnGround, Invulnerable, PortalCooldown, IsGlobal, IsAutonomous,
// LastDimensionId, LinksTag, LootDropped, Color, Color2, Strength, StrengthMax, Sheared,
// IsIllagerCaptain, OwnerNew, Sitting, IsBaby, IsTamed, IsTrusting, IsOrphaned, IsAngry,
// IsOutOfControl, Variant, MarkVariant, Saddled, Chested, ShowBottom, IsGliding,
// IsSwimming, IsEating, IsScared, IsStunned, IsRoaring, SkinID; then the Mob's
// (0xBFE21E0): Mainhand, Offhand, Armor, HurtTime, DeathTime, Dead, Surface,
// NaturalSpawn, TargetID, Attributes, ActiveEffects; and the components' own: identifier,
// definitions (the group stack), Persistent (read back at 0xB3974B0 into the same
// persistent marker a name tag adds), Fire, Air.
//
// What Helix runs is written from the live mob; every other field the mob was loaded with
// (Tags, LinksTag, trade data, effects, component timers) is written back unchanged, so
// nothing a BDS world holds is lost. Not saved yet beyond that: component timers that
// Helix keeps (age, breeding cooldown, anger time), effects and equipment drop chances;
// those start fresh on load. Mobs of a type with no definition stay in the chunk's data
// untouched.

// BDS's mob save keys that Helix reads back.
const (
	mobKeyCustomName        = "CustomName"
	mobKeyCustomNameVisible = "CustomNameVisible"
	mobKeyPersistent        = "Persistent"
	mobKeyMainhand          = "Mainhand"
	mobKeyOffhand           = "Offhand"
	mobKeyArmor             = "Armor"
	mobKeyFire              = "Fire"
	mobKeySurface           = "Surface"
	mobKeyNaturalSpawn      = "NaturalSpawn"
	mobKeyDead              = "Dead"
)

// mobFlagKeys are the saved bytes Actor::saveWithoutId writes from actor flags.
var mobFlagKeys = []struct {
	key  string
	flag int
}{
	{"Sheared", protocol.EntityDataFlagSheared}, {"Sitting", protocol.EntityDataFlagSitting},
	{"IsBaby", protocol.EntityDataFlagBaby}, {"IsTamed", protocol.EntityDataFlagTamed},
	{"IsTrusting", protocol.EntityDataFlagTrusting}, {"IsOrphaned", protocol.EntityDataFlagOrphaned},
	{"IsAngry", protocol.EntityDataFlagAngry}, {"Saddled", protocol.EntityDataFlagSaddled},
	{"Chested", protocol.EntityDataFlagChested}, {"ShowBottom", protocol.EntityDataFlagShowBottom},
	{"IsGliding", protocol.EntityDataFlagGliding}, {"IsSwimming", protocol.EntityDataFlagSwimming},
	{"IsEating", protocol.EntityDataFlagEating}, {"IsScared", protocol.EntityDataFlagScared},
}

// mobDataKeys are the saved values Actor::saveWithoutId writes from actor data; Color and
// Color2 are bytes, the rest ints.
var mobDataKeys = []struct {
	key   string
	data  uint32
	asByt bool
}{
	{"Color", protocol.EntityDataKeyColorIndex, true}, {"Color2", protocol.EntityDataKeyColorTwoIndex, true},
	{"Strength", protocol.EntityDataKeyStrength, false}, {"StrengthMax", protocol.EntityDataKeyStrengthMax, false},
	{"Variant", protocol.EntityDataKeyVariant, false}, {"MarkVariant", protocol.EntityDataKeyMarkVariant, false},
	{"SkinID", protocol.EntityDataKeySkinID, false},
}

// saveEntity is the mob as BDS saves it with its chunk. A dead or removed mob is not saved.
func (o *Mob) saveEntity(e *world.Entity) (chunk.Entity, bool) {
	if o.gone || o.m.Dead() {
		return chunk.Entity{}, false
	}
	prevAttrs := o.saved["Attributes"]
	data := maps.Clone(o.saved)
	if data == nil {
		data = map[string]any{}
	}
	for k, v := range o.bucketData() {
		data[k] = v
	}
	o.saveActor(e, data)
	o.saveLinks(data)
	o.saveMobState(data)
	o.saveEquipment(data)
	o.saveMobItems(data)
	data["Attributes"] = o.saveAttributes(prevAttrs)
	o.saveBreeding(data)
	o.saveTrades(data)
	o.saveDweller(data)
	if o.stand != nil {
		o.saveArmorStand(data)
	}
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
}

// saveActor writes Actor::saveWithoutId's fields: ID, place, motion, name and dimension.
func (o *Mob) saveActor(e *world.Entity, data map[string]any) {
	pos := e.Position()
	pitch, yaw, _, _ := o.m.Rotation()
	vel := o.m.Body().Vel
	data["UniqueID"] = e.UniqueID()
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Rotation"] = []float32{yaw, pitch}
	data["Motion"] = []float32{vel.X, vel.Y, vel.Z}
	if name := o.m.NameTag(); name != "" {
		data[mobKeyCustomName] = name
	} else {
		delete(data, mobKeyCustomName)
	}
	data[mobKeyCustomNameVisible] = boolByte(o.m.Flags().Has(mob.FlagAlwaysShowName))
	data["OnGround"] = boolByte(o.m.Body().OnGround)
	if _, ok := data["FallDistance"]; !ok {
		data["FallDistance"] = float32(0)
	}
	if r := e.Region(); r != nil {
		data["LastDimensionId"] = int32(o.s.dimensionID(r.Dimension()))
	}
}

// The rider links BDS saves on a vehicle (Actor::saveWithoutId 0xB3B6C10, read back by
// Actor::loadLinks 0xB3C1550): LinksTag, one compound per rider in seat order, with the
// rider's unique ID and its seat index (0 is the driver); none when nobody rides.
const (
	mobKeyLinks    = "LinksTag"
	mobKeyLinkID   = "entityID"
	mobKeyLinkSeat = "linkID"
)

// saveLinks writes the mob's riders.
func (o *Mob) saveLinks(data map[string]any) {
	riders := o.m.SavedRiders()
	if len(riders) == 0 {
		delete(data, mobKeyLinks)
		return
	}
	links := make([]any, 0, len(riders))
	for i, id := range riders {
		links = append(links, map[string]any{mobKeyLinkID: int64(id), mobKeyLinkSeat: int32(i)}) //nolint:gosec // few seats
	}
	data[mobKeyLinks] = links
}

// loadLinks gives the mob back the riders it was saved with.
func (o *Mob) loadLinks(data map[string]any) {
	links, _ := data[mobKeyLinks].([]any)
	if len(links) == 0 {
		return
	}
	ids := make([]behavior.ID, 0, len(links))
	for _, l := range links {
		c, _ := l.(map[string]any)
		if id, ok := c[mobKeyLinkID].(int64); ok {
			ids = append(ids, behavior.ID(id))
		}
	}
	o.m.LoadRiders(ids)
}

// saveMobState writes the mob's flag bytes, actor data values, persistence, fire, air,
// spawn facts and target.
func (o *Mob) saveMobState(data map[string]any) {
	f := o.m.Flags()
	for _, k := range mobFlagKeys {
		data[k.key] = boolByte(f.Has(k.flag))
	}
	for _, k := range mobDataKeys {
		data[k.key] = o.dataValue(k.data, k.asByt)
	}
	data[mobKeyPersistent] = boolByte(o.m.Persistent())
	data[mobKeyFire] = int16(min(o.m.FireTicks(), math.MaxInt16)) //nolint:gosec // clamped
	air, _ := o.m.Air()
	data["Air"] = air
	data[mobKeyDead] = uint8(0)
	data[mobKeySurface] = boolByte(o.surface)
	data[mobKeyNaturalSpawn] = boolByte(o.naturalSpawn)
	data["TargetID"] = int64(o.m.Target())
	o.saveEffects(data)
}

// saveEquipment writes the mob's hands and armour in BDS's lists.
func (o *Mob) saveEquipment(data map[string]any) {
	data[mobKeyMainhand] = []any{o.items.stacks[mob.EquipMainhand].nbt(0, false)}
	data[mobKeyOffhand] = []any{o.items.stacks[mob.EquipOffhand].nbt(0, false)}
	armour := make([]any, 0, 5)
	for _, slot := range []int{mob.EquipHead, mob.EquipChest, mob.EquipLegs, mob.EquipFeet, mob.EquipBody} {
		armour = append(armour, o.items.stacks[slot].nbt(0, false))
	}
	data[mobKeyArmor] = armour
}

// dataValue is one of the mob's actor data values for its save, 0 when its components wrote
// none.
func (o *Mob) dataValue(key uint32, asByte bool) any {
	var n int32
	for _, d := range o.m.ActorData() {
		if uint32(d.Key) != key { //nolint:gosec // actor data keys are small
			continue
		}
		switch v := d.Value.(type) {
		case int32:
			n = v
		case int64:
			n = int32(v) //nolint:gosec // actor data ints
		case uint8:
			n = int32(v)
		}
	}
	if asByte {
		return uint8(n) //nolint:gosec // colours are 0 to 15
	}
	return n
}

// saveAttributes writes health, movement and absorption over the saved attribute list,
// keeping the others it holds.
func (o *Mob) saveAttributes(prev any) []any {
	health := map[string]any{
		"Name": attrHealthName, "Base": o.m.MaxHealth(), "Current": o.m.HealthValue(), "Max": o.m.MaxHealth(),
		"Min": float32(0), "DefaultMax": o.m.MaxHealth(), "DefaultMin": float32(0),
	}
	speed := o.m.MovementSpeed()
	movement := map[string]any{
		"Name": "minecraft:movement", "Base": speed, "Current": speed, "Max": float32(math.MaxFloat32),
		"Min": float32(0), "DefaultMax": float32(math.MaxFloat32), "DefaultMin": float32(0),
	}
	absorption := map[string]any{
		"Name": attrAbsorptionName, "Base": float32(0), "Current": o.absorption, "Max": float32(mobAbsorptionMax),
		"Min": float32(0), "DefaultMax": float32(mobAbsorptionMax), "DefaultMin": float32(0),
	}
	out := []any{health, movement, absorption}
	if l, ok := prev.([]any); ok {
		for _, a := range l {
			if m, ok := a.(map[string]any); ok && m["Name"] != attrHealthName && m["Name"] != "minecraft:movement" && m["Name"] != attrAbsorptionName {
				out = append(out, a)
			}
		}
	}
	return out
}

// loadMob brings back a saved mob of a type the mob runtime runs. False leaves the data in
// the chunk for a later run that knows the type.
func (s *Host) loadMob(r *world.Region, at mcdb.ChunkPos, saved chunk.Entity, typ string) bool {
	pos, ok := floats(saved.Data["Pos"])
	if !ok || len(pos) != 3 {
		return false
	}
	if dead, _ := saved.Data[mobKeyDead].(uint8); dead != 0 {
		// BDS drops a dead actor's save: nothing comes back.
		return true
	}
	var yaw, pitch float32
	if v, isList := floats(saved.Data["Rotation"]); isList && len(v) == 2 {
		yaw, pitch = v[0], v[1]
	}
	feet := mgl32.Vec3{pos[0], pos[1], pos[2]}
	o, ok := s.newMobID(saved.ID, typ, feet, yaw, definition.InitLoaded, "")
	if !ok {
		return false
	}
	o.saved = maps.Clone(saved.Data)
	o.m.Restore(restoreBucketData(saved.Data))
	o.m.SetRotation(yaw, pitch)
	if v, isList := floats(saved.Data["Motion"]); isList && len(v) == 3 {
		o.m.SetVelocity(behavior.Vec3{X: v[0], Y: v[1], Z: v[2]})
	}
	if fire, _ := saved.Data[mobKeyFire].(int16); fire > 0 {
		o.m.SetFireTicks(int32(fire))
	}
	if p, _ := saved.Data[mobKeyPersistent].(uint8); p != 0 {
		o.m.SetPersistent()
	}
	if name, _ := saved.Data[mobKeyCustomName].(string); name != "" {
		o.m.SetNameTag(name)
	}
	if v, _ := saved.Data[mobKeyCustomNameVisible].(uint8); v != 0 {
		o.m.SetNameVisible(true)
	}
	for _, k := range mobFlagKeys {
		if b, _ := saved.Data[k.key].(uint8); b != 0 {
			o.m.SetFlag(k.flag, true)
		}
	}
	o.loadLinks(saved.Data)
	o.loadEquipment(saved.Data)
	o.loadMobItems(saved.Data)
	o.loadEffects(saved.Data)
	o.absorption = savedAbsorption(saved.Data["Attributes"])
	if o.stand != nil {
		o.loadArmorStand(saved.Data)
	}
	o.loadBreeding(saved.Data)
	o.loadTrades(saved.Data)
	o.loadDweller(saved.Data)
	surface, _ := saved.Data[mobKeySurface].(uint8)
	natural, _ := saved.Data[mobKeyNaturalSpawn].(uint8)
	o.surface, o.naturalSpawn = surface != 0, natural != 0
	rot := world.Rotation{Yaw: yaw, Pitch: pitch, HeadYaw: yaw}
	o.moves.start(feet, rot, actorMoveDropped, actorMoveFar)
	o.moves.head = true
	o.entity = r.AddLoadedEntity(at, o.config(feet, rot))
	return true
}

// loadEquipment puts back the saved hands and armour.
func (o *Mob) loadEquipment(data map[string]any) {
	first := func(key string) ItemStack {
		l, _ := data[key].([]any)
		if len(l) == 0 {
			return ItemStack{}
		}
		m, _ := l[0].(map[string]any)
		it := itemFromNBT(m)
		o.s.offload(it)
		return it
	}
	if it := first(mobKeyMainhand); !it.Empty() {
		o.setItem(mob.EquipMainhand, it)
	}
	if it := first(mobKeyOffhand); !it.Empty() {
		o.setItem(mob.EquipOffhand, it)
	}
	armour, _ := data[mobKeyArmor].([]any)
	for i, slot := range []int{mob.EquipHead, mob.EquipChest, mob.EquipLegs, mob.EquipFeet, mob.EquipBody} {
		if i >= len(armour) {
			break
		}
		m, _ := armour[i].(map[string]any)
		if it := itemFromNBT(m); !it.Empty() {
			o.s.offload(it)
			o.setItem(slot, it)
		}
	}
}

// The age, breeding and leash fields BDS saves: AgeableComponent's Age (never above 0) and
// GrowthPaused (0x7444D80), BreedableComponent's InLove, LoveCause and BreedCooldown
// (0x7372E40), written while the mob has the component, and Actor's LeasherID (read at
// 0xB3974B0), the holder's unique ID, -1 for none.
const (
	mobKeyAge          = "Age"
	mobKeyGrowthPaused = "GrowthPaused"
	mobKeyInLove       = "InLove"
	mobKeyLoveCause    = "LoveCause"
	mobKeyBreedCool    = "BreedCooldown"
	mobKeyLeasher      = "LeasherID"
)

func (o *Mob) saveBreeding(data map[string]any) {
	st := o.m.SaveState()
	if st.HasAge {
		data[mobKeyAge] = st.Age
		if _, ok := data[mobKeyGrowthPaused]; !ok {
			data[mobKeyGrowthPaused] = uint8(0)
		}
	} else {
		delete(data, mobKeyAge)
		delete(data, mobKeyGrowthPaused)
	}
	if st.HasBreed {
		data[mobKeyInLove] = st.InLove
		data[mobKeyLoveCause] = st.LoveCause
		data[mobKeyBreedCool] = st.Cooldown
	} else {
		delete(data, mobKeyInLove)
		delete(data, mobKeyLoveCause)
		delete(data, mobKeyBreedCool)
	}
	data[mobKeyLeasher] = int64(o.m.LeashHolder())
	saveAnger(data, st)
	o.saveTrust(data)
	o.saveCaptainAndElder(data)
}

func (o *Mob) loadBreeding(data map[string]any) {
	var st mob.SavedState
	st.Age, st.HasAge = data[mobKeyAge].(int32)
	paused, _ := data[mobKeyGrowthPaused].(uint8)
	st.GrowthPaused = paused != 0
	st.InLove, st.HasBreed = data[mobKeyInLove].(int32)
	st.Cooldown, _ = data[mobKeyBreedCool].(int32)
	st.LoveCause, _ = data[mobKeyLoveCause].(int64)
	if !st.HasBreed {
		st.LoveCause = int64(behavior.NoID)
	}
	st.Nuisances, st.HasAnger = loadAnger(data)
	o.m.RestoreState(st)
	o.loadTrust(data)
	o.loadCaptainAndElder(data)
	if id, ok := data[mobKeyLeasher].(int64); ok && behavior.ID(id) != behavior.NoID {
		o.m.SetLeashHolder(behavior.ID(id))
	}
}

// The item component fields BDS saves: Actor's canPickupItems and hasSetCanPickupItems
// (addAdditionalSaveData 0xB3990DD, 1.26.60), the spawn roll pickup_based_on_chance reads,
// and the minecraft:inventory container as ChestItems, each stack with its Slot.
const (
	mobKeyCanPickUp    = "canPickupItems"
	mobKeyCanPickUpSet = "hasSetCanPickupItems"
	mobKeyChestItems   = "ChestItems"
)

// saveMobItems writes the pick-up roll and the inventory.
func (o *Mob) saveMobItems(data map[string]any) {
	can, set := o.m.PickUpLoot()
	data[mobKeyCanPickUp] = boolByte(can)
	data[mobKeyCanPickUpSet] = boolByte(set)
	inv := o.m.Inventory()
	if inv == nil {
		return
	}
	list := make([]any, 0, len(inv))
	for k, it := range inv {
		if st := realStack(it); !st.Empty() {
			list = append(list, st.nbt(k, true))
		}
	}
	data[mobKeyChestItems] = list
}

// loadMobItems puts back the pick-up roll and the inventory.
func (o *Mob) loadMobItems(data map[string]any) {
	can, _ := data[mobKeyCanPickUp].(uint8)
	set, _ := data[mobKeyCanPickUpSet].(uint8)
	o.m.SetPickUpLoot(can != 0, set != 0)
	list, _ := data[mobKeyChestItems].([]any)
	if len(list) == 0 {
		return
	}
	var slots []behavior.Item
	for _, e := range list {
		m, _ := e.(map[string]any)
		slot, _ := m["Slot"].(uint8)
		it := itemFromNBT(m)
		if it.Empty() || int(slot) >= maxMobInventory {
			continue
		}
		o.s.offload(it)
		for len(slots) <= int(slot) {
			slots = append(slots, behavior.Item{})
		}
		slots[slot] = mobItem(o.s.freshStack(it))
	}
	o.m.SetInventory(slots)
}

// maxMobInventory bounds the slot numbers a save may name.
const maxMobInventory = 256

// The anger_level and trust keys BDS 1.26.60 saves: AngerLevelComponent's "Nuisances", a
// list of ActorId, Anger and Priority (save 0x735D5D0, load 0x7384410), and TrustComponent's
// TrustedPlayersAmount and TrustedPlayer<n> (save 0x7405E20, load 0x74061A0).
const (
	mobKeyNuisances     = "Nuisances"
	mobKeyNuisanceID    = "ActorId"
	mobKeyNuisanceAnger = "Anger"
	mobKeyNuisancePrio  = "Priority"
	mobKeyTrustedAmount = "TrustedPlayersAmount"
	mobKeyTrustedPrefix = "TrustedPlayer"
	// mobKeyCaptain is Mob::mTargetCaptainId, written only when set (Mob::addAdditionalSaveData,
	// 1.26.60); mobKeyElder is a guardian's elder flag (Guardian save 0xBD400D0).
	mobKeyCaptain = "TargetCaptainID"
	mobKeyElder   = "Elder"
)

// The types BDS makes Guardian actors, whose save writes Elder.
const (
	typeGuardian      = "minecraft:guardian"
	typeElderGuardian = "minecraft:elder_guardian"
)

// saveCaptainAndElder writes the follow_target_captain leader and a guardian's elder flag.
func (o *Mob) saveCaptainAndElder(data map[string]any) {
	if id := o.m.TargetLeader(); id != behavior.NoID {
		data[mobKeyCaptain] = int64(id)
	} else {
		delete(data, mobKeyCaptain)
	}
	if t := o.m.Type(); t == typeGuardian || t == typeElderGuardian {
		var elder uint8
		if o.m.Flag(behavior.FlagElder) {
			elder = 1
		}
		data[mobKeyElder] = elder
	}
}

// loadCaptainAndElder reads them back.
func (o *Mob) loadCaptainAndElder(data map[string]any) {
	if id, ok := data[mobKeyCaptain].(int64); ok {
		o.m.SetTargetLeader(behavior.ID(id))
	}
	if elder, ok := data[mobKeyElder].(uint8); ok && elder != 0 {
		o.m.SetFlag(behavior.FlagElder, true)
	}
}

// saveAnger writes the suspects while the mob has anger_level.
func saveAnger(data map[string]any, st mob.SavedState) {
	if !st.HasAnger {
		delete(data, mobKeyNuisances)
		return
	}
	list := make([]any, 0, len(st.Nuisances))
	for _, n := range st.Nuisances {
		prio := uint8(0)
		if n.Priority {
			prio = 1
		}
		list = append(list, map[string]any{mobKeyNuisanceID: int64(n.ID), mobKeyNuisanceAnger: n.Anger, mobKeyNuisancePrio: prio})
	}
	data[mobKeyNuisances] = list
}

// loadAnger reads the suspects; an entry needs ActorId and Anger, as BDS's load.
func loadAnger(data map[string]any) ([]state.Nuisance, bool) {
	raw, ok := data[mobKeyNuisances]
	if !ok {
		return nil, false
	}
	var out []state.Nuisance
	for _, e := range compoundList(raw) {
		id, okID := e[mobKeyNuisanceID].(int64)
		anger, okAnger := e[mobKeyNuisanceAnger].(int32)
		if !okID || !okAnger {
			continue
		}
		prio, _ := e[mobKeyNuisancePrio].(uint8)
		out = append(out, state.Nuisance{ID: behavior.ID(id), Anger: anger, Priority: prio != 0})
	}
	return out, true
}

// compoundList is an NBT list of compounds as the decoder gives it.
func compoundList(raw any) []map[string]any {
	switch l := raw.(type) {
	case []map[string]any:
		return l
	case []any:
		out := make([]map[string]any, 0, len(l))
		for _, e := range l {
			if c, ok := e.(map[string]any); ok {
				out = append(out, c)
			}
		}
		return out
	}
	return nil
}

// saveTrust writes the trusted players while the mob has minecraft:trust.
func (o *Mob) saveTrust(data map[string]any) {
	if n, ok := data[mobKeyTrustedAmount].(int32); ok {
		for i := range n {
			delete(data, mobKeyTrustedPrefix+strconv.Itoa(int(i)))
		}
	}
	delete(data, mobKeyTrustedAmount)
	ids, ok := o.m.SavedTrust()
	if !ok {
		return
	}
	data[mobKeyTrustedAmount] = int32(len(ids)) //nolint:gosec // a trusted set is small
	for i, id := range ids {
		data[mobKeyTrustedPrefix+strconv.Itoa(i)] = int64(id)
	}
}

// loadTrust reads the trusted players; entries missing are skipped, as BDS's load.
func (o *Mob) loadTrust(data map[string]any) {
	n, ok := data[mobKeyTrustedAmount].(int32)
	if !ok {
		return
	}
	for i := range n {
		if id, ok := data[mobKeyTrustedPrefix+strconv.Itoa(int(i))].(int64); ok {
			o.m.Trust(behavior.ID(id))
		}
	}
}

// attrAbsorptionName is the absorption attribute's saved name.
const attrAbsorptionName = "minecraft:absorption"

// savedAbsorption is the current absorption in a saved attribute list, 0 for none.
func savedAbsorption(list any) float32 {
	l, _ := list.([]any)
	for _, a := range l {
		if m, ok := a.(map[string]any); ok && m["Name"] == attrAbsorptionName {
			v, _ := m["Current"].(float32)
			return min(max(v, 0), mobAbsorptionMax)
		}
	}
	return 0
}
