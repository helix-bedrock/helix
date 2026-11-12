package player

import (
	"maps"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
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
	pos := e.Position()
	pitch, yaw, _, _ := o.m.Rotation()
	vel := o.m.Body().Vel
	f := o.m.Flags()
	data["UniqueID"] = e.UniqueID()
	data["Pos"] = []float32{pos[0], pos[1], pos[2]}
	data["Rotation"] = []float32{yaw, pitch}
	data["Motion"] = []float32{vel.X, vel.Y, vel.Z}
	if name := o.m.NameTag(); name != "" {
		data[mobKeyCustomName] = name
	} else {
		delete(data, mobKeyCustomName)
	}
	data[mobKeyCustomNameVisible] = boolByte(f.Has(mob.FlagAlwaysShowName))
	data["OnGround"] = boolByte(o.m.Body().OnGround)
	if _, ok := data["FallDistance"]; !ok {
		data["FallDistance"] = float32(0)
	}
	if r := e.Region(); r != nil {
		data["LastDimensionId"] = int32(o.s.dimensionID(r.Dimension()))
	}
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
	data[mobKeyMainhand] = []any{o.items.stacks[mob.EquipMainhand].nbt(0, false)}
	data[mobKeyOffhand] = []any{o.items.stacks[mob.EquipOffhand].nbt(0, false)}
	armour := make([]any, 0, 5)
	for _, slot := range []int{mob.EquipHead, mob.EquipChest, mob.EquipLegs, mob.EquipFeet, mob.EquipBody} {
		armour = append(armour, o.items.stacks[slot].nbt(0, false))
	}
	data[mobKeyArmor] = armour
	data[mobKeyDead] = uint8(0)
	data[mobKeySurface] = boolByte(o.surface)
	data[mobKeyNaturalSpawn] = boolByte(o.naturalSpawn)
	data["TargetID"] = int64(o.m.Target())
	data["Attributes"] = o.saveAttributes(prevAttrs)
	return chunk.Entity{ID: e.UniqueID(), Data: data}, true
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

// saveAttributes writes health and movement over the saved attribute list, keeping the
// others it holds.
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
	out := []any{health, movement}
	if l, ok := prev.([]any); ok {
		for _, a := range l {
			if m, ok := a.(map[string]any); ok && m["Name"] != attrHealthName && m["Name"] != "minecraft:movement" {
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
	o.loadEquipment(saved.Data)
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
		return itemFromNBT(m)
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
			o.setItem(slot, it)
		}
	}
}
