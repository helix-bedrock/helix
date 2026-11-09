package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// DieFunc runs when the player's health reaches 0.
type DieFunc func(p *Player, src DamageSource)

// RespawnFunc brings a dead player back at pos, its eye position.
type RespawnFunc func(p *Player, pos mgl32.Vec3)

// Where a player is on BDS's way from dying to respawning.
const (
	// respawnNone is a player who is alive.
	respawnNone = iota
	// respawnStart tells the client on the next tick that a spawn is being
	// looked for.
	respawnStart
	// respawnWaitClient waits for the client's ready Respawn, sent when the
	// player presses the button on the death screen.
	respawnWaitClient
	// respawnFind picks the spawn and tells the client.
	respawnFind
	// respawnWaitAction waits for the client's respawn PlayerAction.
	respawnWaitAction
)

// keepOnDeathTag is the item tag that keeps an item on a dying player.
const keepOnDeathTag = "minecraft:keep_on_death"

// dead reports whether the player has no health left.
func (p *Player) dead() bool { return p.Health() <= 0 }

// vanillaDie is BDS's player death: without keepinventory, vanishing items
// go, then armour, offhand, the cursor and the inventory are thrown at random;
// the death event, the death info and the death message follow, and the
// respawn sequence starts. Experience is not dropped yet.
func vanillaDie(p *Player, src DamageSource) {
	p.StopSleeping()
	p.stopUsing()
	p.lowerShield()
	p.hurt = hurtState{}
	p.Extinguish()
	p.leaveChest(p.entity.Region(), true)
	if !p.s.level.KeepInventory {
		p.dropAllOnDeath()
	}
	p.brk = breakState{}
	p.move.fall = 0
	if r := p.entity.Region(); r != nil {
		r.Broadcast(p.blockPos(), actorEvent(p.runtimeID, packet.ActorEventDeath), nil)
	}
	if key := deathMessage(src); key != "" {
		params := []string{p.name}
		if src.Attacker != nil {
			params = append(params, src.Attacker.name)
		}
		p.send(&packet.DeathInfo{Cause: key, Messages: params})
		if p.s.level.ShowDeathMessages {
			text := &packet.Text{TextType: packet.TextTypeTranslation, NeedsTranslation: true, Message: key, Parameters: params}
			p.send(text)
			p.broadcast(text)
		}
	}
	p.respawn = respawnStart
	p.flush()
}

// deathMessage is the translation key BDS picks for a cause, for the causes
// Helix deals; empty for any other. BDS also has a second key for some falls,
// whose condition is not read yet.
func deathMessage(src DamageSource) string {
	switch src.Cause {
	case DamageFall:
		return "death.attack.fall"
	case DamageEntityAttack:
		return "death.attack.player"
	case DamageStarve:
		return "death.attack.starve"
	case DamageFire:
		return "death.attack.inFire"
	case DamageFireTick:
		return "death.attack.onFire"
	case DamageLava:
		return "death.attack.lava"
	case DamageContact:
		if src.Block == nameBerryBush {
			return "death.attack.sweetBerry"
		}
		return "death.attack.cactus"
	}
	return ""
}

// dropAllOnDeath empties the player's containers in BDS's order and sends
// the client the result.
func (p *Player) dropAllOnDeath() {
	inv := &p.inventory
	p.dropSlots(inv.main[:], true)
	p.dropSlots(inv.armor[:], false)
	off := []ItemStack{inv.offhand[0]}
	p.dropSlots(off, false)
	inv.offhand[0] = off[0]
	p.dropSlots(inv.ui[cursorSlot:cursorSlot+1], false)
	p.dropSlots(inv.main[:], false)
	p.sendOwnItems()
	p.sendEquipment()
}

// dropSlots throws out the stacks in slots, leaving those kept on death.
// With vanishingOnly it instead removes the stacks with the curse of
// vanishing.
func (p *Player) dropSlots(slots []ItemStack, vanishingOnly bool) {
	for i := range slots {
		s := slots[i]
		if s.Empty() {
			continue
		}
		if _, keep := s.Tag[keepOnDeathTag]; keep {
			continue
		}
		if vanishingOnly {
			if p.s.game.itemHas(s, func(e *Enchantment) bool { return e.VanishesOnDeath }) {
				slots[i] = ItemStack{}
			}
			continue
		}
		// A drop that was refused stays in its slot, so no item is lost.
		if p.s.game.sys.DropItem(p, s, true) {
			slots[i] = ItemStack{}
		}
	}
}

// tickRespawn moves the respawn sequence on, once a tick. BDS looks for a
// safe place within the spawn radius and waits for the chunks there; Helix
// uses the world spawn itself so far.
func (p *Player) tickRespawn() {
	switch p.respawn {
	case respawnStart:
		p.send(&packet.Respawn{Position: p.entity.Position(), State: packet.RespawnStateSearchingForSpawn})
		p.respawn = respawnWaitClient
	case respawnFind:
		spawn := p.respawnPoint()
		p.moveZones(spawn)
		p.send(&packet.Respawn{Position: spawn, State: packet.RespawnStateSearchingForSpawn},
			&packet.Respawn{Position: spawn, State: packet.RespawnStateReadyToSpawn})
		p.respawn = respawnWaitAction
	default:
		return
	}
	p.flush()
}

// handleRespawn is vanilla's Respawn handler: the client saying it is ready
// lets the sequence go on, as BDS checks the runtime ID.
func handleRespawn(p *Player, pk *packet.Respawn) {
	if pk.State == packet.RespawnStateClientReadyToSpawn && pk.EntityRuntimeID == p.runtimeID && p.respawn == respawnWaitClient {
		p.respawn = respawnFind
	}
}

// handlePlayerAction is vanilla's PlayerAction handler. Only the respawn is
// built: BDS takes it from a dead player whose spawn was found.
func handlePlayerAction(p *Player, pk *packet.PlayerAction) {
	if pk.ActionType == protocol.PlayerActionDimensionChangeDone {
		p.portal.changing = false
		return
	}
	if pk.ActionType == protocol.PlayerActionStopSleeping && pk.EntityRuntimeID == p.runtimeID {
		p.StopSleeping()
		return
	}
	if pk.ActionType != protocol.PlayerActionRespawn || pk.EntityRuntimeID != p.runtimeID || p.respawn != respawnWaitAction || !p.dead() {
		return
	}
	if p.dim != p.s.overworld && p.s.overworld != nil {
		// Death outside the overworld respawns there: the body resets where it is,
		// then the player moves across.
		p.s.game.sys.Respawn(p, p.entity.Position())
		p.toOverworldSpawn(true)
		return
	}
	p.s.game.sys.Respawn(p, p.respawnPoint())
}

// respawnPoint is where a dead player comes back: beside its bed, else the world spawn.
func (p *Player) respawnPoint() mgl32.Vec3 {
	if at, ok := p.bedRespawn(p.entity.Region()); ok {
		return at
	}
	spawn, _ := p.worldSpawn()
	return spawn
}

// vanillaRespawn is BDS's respawn as far as built: the body resets, the
// player moves to the spawn, is told its health, and those who see it get
// the spawn-alive event. Which attributes BDS resets is a first reading:
// health, hunger, saturation and exhaustion to their defaults, effects gone.
func vanillaRespawn(p *Player, pos mgl32.Vec3) {
	p.respawn = respawnNone
	for _, e := range p.Effects() {
		p.RemoveEffect(e.ID)
	}
	p.setAttributes(attrHealth, p.attributes.maximum(attrHealth), attrHunger, p.attributes.maximum(attrHunger),
		attrSaturation, playerAttributes[attrSaturation].def, attrExhaustion, playerAttributes[attrExhaustion].def)
	p.move = moveState{}
	p.food = foodState{}
	p.s.handlers.movement(p, moveTo(pos, p.entity.Rotation(), p.entity.Tick()))
	p.send(&packet.SetHealth{Health: int32(p.Health())})
	if r := p.entity.Region(); r != nil {
		r.Broadcast(p.blockPos(), actorEvent(p.runtimeID, packet.ActorEventSpawnAlive), nil)
	}
	p.flush()
}
