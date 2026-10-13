// Command events generates event/event.go and setup_events.go from one
// table, so every engine event has a plugin alias and a Setup method.
//
// Run from the module root: go run ./internal/gen/events
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
)

type ev struct {
	name  string // alias in package event and Setup method name
	pkg   string // player, world or inventory
	typ   string // engine type
	list  string // field of the engine's Events
	about string // doc
}

var events = []ev{
	{"PlayerJoin", "player", "JoinEvent", "Join", "a player has joined and spawned"},
	{"PlayerQuit", "player", "QuitEvent", "Quit", "a player leaves"},
	{"PlayerMove", "player", "MoveEvent", "Move", "a player moves or turns"},
	{"PlayerJump", "player", "JumpEvent", "Jump", "a player jumps"},
	{"PlayerTeleport", "player", "TeleportEvent", "Teleport", "a player is teleported"},
	{"PlayerChangeWorld", "player", "ChangeWorldEvent", "ChangeWorld", "a player moved to another world"},
	{"PlayerToggleSprint", "player", "ToggleSprintEvent", "ToggleSprint", "a player starts or stops sprinting"},
	{"PlayerToggleSneak", "player", "ToggleSneakEvent", "ToggleSneak", "a player starts or stops sneaking"},
	{"Chat", "player", "ChatEvent", "Chat", "a player chats"},
	{"PlayerFoodLoss", "player", "FoodLossEvent", "FoodLoss", "a player's food level drops"},
	{"PlayerHeal", "player", "HealEvent", "Heal", "a player heals"},
	{"PlayerHurt", "player", "HurtEvent", "Hurt", "a player is about to take damage"},
	{"PlayerSetOnFire", "player", "SetOnFireEvent", "SetOnFire", "a player is set on fire"},
	{"PlayerDeath", "player", "DeathEvent", "Death", "a player dies"},
	{"PlayerRespawn", "player", "RespawnEvent", "Respawn", "a player respawns"},
	{"PlayerSkinChange", "player", "SkinChangeEvent", "SkinChange", "a player changes skin"},
	{"PlayerSleep", "player", "SleepEvent", "Sleep", "a player goes to sleep"},
	{"PlayerTransfer", "player", "TransferEvent", "Transfer", "a player is sent to another server"},
	{"PlayerDiagnostics", "player", "DiagnosticsEvent", "Diagnostics", "a client sends performance figures"},
	{"PlayerCommand", "player", "CommandEvent", "Command", "a player runs a command"},
	{"FireExtinguish", "player", "FireExtinguishEvent", "FireExtinguish", "a player puts out a fire"},
	{"BlockStartBreak", "player", "StartBreakEvent", "StartBreak", "a player starts breaking a block"},
	{"BlockBreak", "player", "BlockBreakEvent", "BlockBreak", "a player breaks a block"},
	{"BlockPlace", "player", "BlockPlaceEvent", "BlockPlace", "a player places a block"},
	{"BlockPick", "player", "BlockPickEvent", "BlockPick", "a player picks a block"},
	{"ItemUse", "player", "ItemUseEvent", "ItemUse", "a player uses the held item in the air"},
	{"ItemUseOnBlock", "player", "ItemUseOnBlockEvent", "ItemUseOnBlock", "a player uses the held item on a block"},
	{"ItemUseOnEntity", "player", "ItemUseOnEntityEvent", "ItemUseOnEntity", "a player uses the held item on an entity"},
	{"ItemRelease", "player", "ItemReleaseEvent", "ItemRelease", "a player releases a held item"},
	{"ItemConsume", "player", "ItemConsumeEvent", "ItemConsume", "a player eats or drinks"},
	{"ItemDamage", "player", "ItemDamageEvent", "ItemDamage", "a player's item loses durability"},
	{"ItemPickup", "player", "ItemPickupEvent", "ItemPickup", "a player picks up an item"},
	{"ItemDrop", "player", "ItemDropEvent", "ItemDrop", "a player drops an item"},
	{"HeldSlotChange", "player", "HeldSlotChangeEvent", "HeldSlotChange", "a player changes the selected hotbar slot"},
	{"AttackEntity", "player", "AttackEntityEvent", "AttackEntity", "a player attacks an entity"},
	{"ExperienceGain", "player", "ExperienceGainEvent", "ExperienceGain", "a player gains experience"},
	{"PunchAir", "player", "PunchAirEvent", "PunchAir", "a player swings at nothing"},
	{"SignEdit", "player", "SignEditEvent", "SignEdit", "a player edits a sign"},
	{"LecternPageTurn", "player", "LecternPageTurnEvent", "LecternPageTurn", "a player turns a lectern's page"},
	{"LiquidFlow", "world", "LiquidFlowEvent", "LiquidFlow", "a liquid flows into a block"},
	{"LiquidDecay", "world", "LiquidDecayEvent", "LiquidDecay", "a liquid gets lower or disappears"},
	{"LiquidHarden", "world", "LiquidHardenEvent", "LiquidHarden", "a liquid hardens"},
	{"Sound", "world", "SoundEvent", "Sound", "a sound plays"},
	{"FireSpread", "world", "FireSpreadEvent", "FireSpread", "fire spreads to another block"},
	{"BlockBurn", "world", "BlockBurnEvent", "BlockBurn", "fire burns a block away"},
	{"CropTrample", "world", "CropTrampleEvent", "CropTrample", "an entity tramples farmland"},
	{"LeavesDecay", "world", "LeavesDecayEvent", "LeavesDecay", "leaves decay"},
	{"PortalCreate", "world", "PortalCreateEvent", "PortalCreate", "a portal is built"},
	{"PortalActivate", "world", "PortalActivateEvent", "PortalActivate", "a portal frame is filled"},
	{"EntitySpawn", "world", "EntitySpawnEvent", "EntitySpawn", "an entity is added to a world"},
	{"EntityDespawn", "world", "EntityDespawnEvent", "EntityDespawn", "an entity is removed from a world"},
	{"Explosion", "world", "ExplosionEvent", "Explosion", "something explodes"},
	{"RedstoneUpdate", "world", "RedstoneUpdateEvent", "RedstoneUpdate", "the redstone engine changes a block"},
	{"SlowTick", "world", "SlowTickEvent", "SlowTick", "a region's tick took longer than 50ms"},
	{"WorldClose", "world", "CloseEvent", "Close", "a world is about to close"},
	{"InventoryTake", "inventory", "TakeEvent", "Take", "a stack is taken from an inventory slot"},
	{"InventoryPlace", "inventory", "PlaceEvent", "Place", "a stack is placed in an inventory slot"},
	{"InventoryDrop", "inventory", "DropEvent", "Drop", "a stack is dropped out of an inventory slot"},
	{"PacketIn", "network", "PacketInEvent", "PacketIn", "a client sends a packet. It runs on the network goroutine, not a region"},
	{"PacketOut", "network", "PacketOutEvent", "PacketOut", "a packet is sent to a client. It runs on the network goroutine, not a region"},
}

func main() {
	var a bytes.Buffer
	a.WriteString(`// Code generated by internal/gen/events. DO NOT EDIT.

package event

import (
	"github.com/helix-bedrock/helix/server/item/inventory"
	"github.com/helix-bedrock/helix/server/network"
	"github.com/helix-bedrock/helix/server/player"
	"github.com/helix-bedrock/helix/server/world"
)

`)
	for _, e := range events {
		fmt.Fprintf(&a, "// %s is called when %s.\ntype %s = %s.%s\n\n", e.name, e.about, e.name, e.pkg, e.typ)
	}
	write("event/events.go", a.Bytes())

	var b bytes.Buffer
	b.WriteString(`// Code generated by internal/gen/events. DO NOT EDIT.

package helix

import "github.com/helix-bedrock/helix/event"

`)
	lists := map[string]string{"player": "r.srv.playerEvents", "world": "r.srv.worldEvents", "inventory": "r.srv.inventoryEvents", "network": "r.srv.packetEvents"}
	for _, e := range events {
		fmt.Fprintf(&b, "// %s registers a listener for when %s.\nfunc (r *Setup) %s(fn func(*event.%s), opts ...Option) {\n\tregister(r, &%s.%s, fn, opts)\n}\n\n",
			e.name, e.about, e.name, e.name, lists[e.pkg], e.list)
	}
	write("setup_events.go", b.Bytes())
}

func write(path string, src []byte) {
	out, err := format.Source(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
