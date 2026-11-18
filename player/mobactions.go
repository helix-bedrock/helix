package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/helix/mob"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// The engine side of the mob actions in mob/actions.go: minecraft:teleport's trail and
// sounds, minecraft:spawn_entity's items and entities, the game events mobs post, and the
// Respiration their armour gives, as BDS 1.26.60 does them.

// The teleport trail's numbers.
const (
	teleportDirScale = 0.2
	teleportCount    = int32(128)
)

// gameEventIDs are BDS 1.26.60's game event IDs by name (the GameEvent table, initializer).
var gameEventIDs = map[string]int32{
	"block_activate": 1, "block_attach": 2, "block_change": 3, "block_close": 4, "block_deactivate": 5,
	"block_destroy": 6, "block_detach": 7, "block_open": 8, "block_place": 9, "bounce": 10,
	"container_close": 11, "container_open": 12, "dispense_fail": 13, "drink": 14, "eat": 15,
	"elytra_glide": 16, "entity_damage": 17, "entity_die": 18, "entity_dismount": 19,
	"entity_interact": 20, "entity_mount": 21, "entity_move": 22, "entity_place": 23,
	"entity_roar": 24, "entity_act": 25, "equip": 26, "explode": 27, "flap": 28, "fluid_pickup": 29,
	"fluid_place": 30, "hit_ground": 31, "item_interact_finish": 32, "item_interact_start": 33,
	"lightning_strike": 34, "multi_item_swap": 35, "note_block_play": 36, "piston_contract": 37,
	"piston_extend": 38, "prime_fuse": 39, "projectile_land": 40, "projectile_shoot": 41,
	"sculk_touch": 57, "sculk_sensor_tendrils_clicking": 58, "shear": 59, "shriek": 60,
	"single_item_swap": 61, "splash": 62, "swim": 63, "teleport": 64, "unequip": 65,
}

// gameEventID is the ID of a game event by name, with or without "minecraft:".
func gameEventID(name string) (int32, bool) {
	if len(name) > len("minecraft:") && name[:len("minecraft:")] == "minecraft:" {
		name = name[len("minecraft:"):]
	}
	id, ok := gameEventIDs[name]
	return id, ok
}

// Teleported implements mob.TeleportHooks: the trail for those who see where the mob
// arrived, LevelEvent 1018 where it left, the teleport game event there, and 1018 again
// where it arrived unless it is silent.
func (m *Mob) Teleported(o *mob.Mob, from behavior.Vec3) {
	r := m.entity.Region()
	if r == nil {
		return
	}
	to := mglVec(o.Pos())
	start := mglVec(from)
	end := cube.Pos{int(math.Floor(float64(to[0]))), int(math.Floor(float64(to[1]))), int(math.Floor(float64(to[2])))}
	w, h := o.Size()
	if pk := levelEventGeneric(packet.LevelEventParticlesTeleportTrail, map[string]any{
		"Startx": start[0], "Starty": start[1], "Startz": start[2],
		"Endx": float32(end[0]) + 0.5, "Endy": float32(end[1]) + 0.5, "Endz": float32(end[2]) + 0.5,
		"Variationx": w, "Variationy": h, "DirScale": float32(teleportDirScale), "Count": teleportCount,
	}); pk != nil {
		r.Broadcast(end, pk, nil)
	}
	levelEvent(r, vecPos(start), start, packet.LevelEventSoundEndermanTeleport, 0)
	m.s.postMobGameEvent(r, gameEventTeleport, start, m)
	if !o.Flag(behavior.FlagSilent) {
		levelEvent(r, end, to, packet.LevelEventSoundEndermanTeleport, 0)
	}
}

// postMobGameEvent posts a game event a mob caused.
func (s *Host) postMobGameEvent(r *world.Region, id int32, at mgl32.Vec3, src *Mob) {
	if s.vibrationIgnored(r, id, nil, 0, false) {
		return
	}
	r.PostGameEvent(world.GameEvent{ID: id, Pos: at, Source: src})
}

// GameEvent implements mob.GameEventHooks.
func (m *Mob) GameEvent(_ *mob.Mob, name string, p behavior.Vec3) {
	r := m.entity.Region()
	id, ok := gameEventID(name)
	if r == nil || !ok {
		return
	}
	m.s.postMobGameEvent(r, id, mglVec(p), m)
}

// SpawnItem implements mob.SpawnHooks: one of the item thrown out at the mob, with its 10
// tick pick-up delay.
func (m *Mob) SpawnItem(o *mob.Mob, item string) bool {
	r := m.entity.Region()
	if r == nil {
		return false
	}
	if _, ok := m.s.reg.Items.ByName(item); !ok {
		return false
	}
	m.s.spawnMobDrop(r, ItemStack{Name: item, Count: 1}, mglVec(o.Pos()))
	return true
}

// SpawnEntity implements mob.SpawnHooks as BDS's ActorSpawnHelper: born puts
// the newborn in the middle of the mob's block facing the mob's way, spawned at
// the mob with its rotation, summoned at the mob facing nowhere in particular.
// transformed is taken as spawned: its helper was not read.
func (m *Mob) SpawnEntity(o *mob.Mob, typ, event, method string) *mob.Mob {
	r := m.entity.Region()
	if r == nil {
		return nil
	}
	init, ev, ok := mob.SpawnInit(method, event)
	if !ok {
		return nil
	}
	pos := mglVec(o.Pos())
	_, yaw, _, _ := o.Rotation()
	switch method {
	case "born":
		pos = mgl32.Vec3{
			float32(math.Floor(float64(pos[0]))) + 0.5,
			float32(math.Floor(float64(pos[1]))) + 0.5,
			float32(math.Floor(float64(pos[2]))) + 0.5,
		}
	case "summoned":
		yaw = 0
	}
	c, ok := m.s.newMob(typ, pos, yaw, init, ev)
	if !ok {
		return nil
	}
	c.add(r.Dimension())
	return c.m
}

// Respiration implements mob.ArmorEnchantHooks: the highest Respiration level of the armour
// the mob wears. Runs: the mob's think, reading only its own items.
func (m *Mob) Respiration(*mob.Mob) int32 {
	best := int32(0)
	for slot := mob.EquipHead; slot <= mob.EquipFeet; slot++ {
		for _, e := range m.items.stacks[slot].Enchants() {
			if e.ID == enchantRespiration {
				best = max(best, int32(e.Level))
			}
		}
	}
	return best
}

var (
	_ mob.TeleportHooks     = (*Mob)(nil)
	_ mob.SpawnHooks        = (*Mob)(nil)
	_ mob.GameEventHooks    = (*Mob)(nil)
	_ mob.ArmorEnchantHooks = (*Mob)(nil)
)
