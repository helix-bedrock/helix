package player

import (
	"net"
	"time"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/cmd"
	"github.com/helix-bedrock/helix/server/hook"
	"github.com/helix-bedrock/helix/server/item"
	"github.com/helix-bedrock/helix/server/player/skin"
	"github.com/helix-bedrock/helix/server/session"
	"github.com/helix-bedrock/helix/server/world"
)

// Events holds the listener lists of player events (Helix, RFC 0001). They
// replace Dragonfly's per-player Handler: the player calls them directly
// where things happen, and plugins register through the helix package. All
// players of a server share one Events.
//
// Listeners run on the region the player is in, with its Tx.
type Events struct {
	Join            hook.List[JoinEvent]
	Quit            hook.List[QuitEvent]
	Move            hook.List[MoveEvent]
	Jump            hook.List[JumpEvent]
	Teleport        hook.List[TeleportEvent]
	ChangeWorld     hook.List[ChangeWorldEvent]
	ToggleSprint    hook.List[ToggleSprintEvent]
	ToggleSneak     hook.List[ToggleSneakEvent]
	Chat            hook.List[ChatEvent]
	FoodLoss        hook.List[FoodLossEvent]
	Heal            hook.List[HealEvent]
	Hurt            hook.List[HurtEvent]
	SetOnFire       hook.List[SetOnFireEvent]
	Death           hook.List[DeathEvent]
	Respawn         hook.List[RespawnEvent]
	SkinChange      hook.List[SkinChangeEvent]
	FireExtinguish  hook.List[FireExtinguishEvent]
	StartBreak      hook.List[StartBreakEvent]
	BlockBreak      hook.List[BlockBreakEvent]
	BlockPlace      hook.List[BlockPlaceEvent]
	BlockPick       hook.List[BlockPickEvent]
	ItemUse         hook.List[ItemUseEvent]
	ItemUseOnBlock  hook.List[ItemUseOnBlockEvent]
	ItemUseOnEntity hook.List[ItemUseOnEntityEvent]
	ItemRelease     hook.List[ItemReleaseEvent]
	ItemConsume     hook.List[ItemConsumeEvent]
	AttackEntity    hook.List[AttackEntityEvent]
	ExperienceGain  hook.List[ExperienceGainEvent]
	PunchAir        hook.List[PunchAirEvent]
	SignEdit        hook.List[SignEditEvent]
	Sleep           hook.List[SleepEvent]
	LecternPageTurn hook.List[LecternPageTurnEvent]
	ItemDamage      hook.List[ItemDamageEvent]
	ItemPickup      hook.List[ItemPickupEvent]
	HeldSlotChange  hook.List[HeldSlotChangeEvent]
	ItemDrop        hook.List[ItemDropEvent]
	Transfer        hook.List[TransferEvent]
	Command         hook.List[CommandEvent]
	Diagnostics     hook.List[DiagnosticsEvent]
}

// noEvents is used by players created without Events.
var noEvents = &Events{}

// Base is embedded in every player event.
type Base struct {
	// Tx is the transaction of the player's region. It is valid only while
	// the listener runs.
	Tx *world.Tx
	// Player is the player the event is about.
	Player *Player
}

// WorldName returns the name of the player's world, for the OnlyWorld
// filter.
func (b *Base) WorldName() string {
	if b.Tx == nil {
		return ""
	}
	return b.Tx.World().Name()
}

func (p *Player) base() Base { return Base{Tx: p.tx, Player: p} }

func blockName(b world.Block) string {
	if b == nil {
		return "minecraft:air"
	}
	name, _ := b.EncodeBlock()
	return name
}

// JoinEvent is called when a player has joined and spawned.
type JoinEvent struct{ Base }

// QuitEvent is called when a player leaves.
type QuitEvent struct{ Base }

// MoveEvent is called when a player moves or turns.
type MoveEvent struct {
	hook.Cancellable
	Base
	Position mgl64.Vec3
	Rotation cube.Rotation
}

// JumpEvent is called when a player jumps.
type JumpEvent struct{ Base }

// TeleportEvent is called when a player is teleported.
type TeleportEvent struct {
	hook.Cancellable
	Base
	Position mgl64.Vec3
	Rotation cube.Rotation
}

// ChangeWorldEvent is called after a player moved to another world. Before
// may be nil.
type ChangeWorldEvent struct {
	Base
	Before, After *world.World
}

// ToggleSprintEvent is called when a player starts or stops sprinting.
type ToggleSprintEvent struct {
	hook.Cancellable
	Base
	Sprinting bool
}

// ToggleSneakEvent is called when a player starts or stops sneaking.
type ToggleSneakEvent struct {
	hook.Cancellable
	Base
	Sneaking bool
}

// ChatEvent is called when a player chats. Message may be changed.
type ChatEvent struct {
	hook.Cancellable
	Base
	Message *string
}

// FoodLossEvent is called when a player's food level drops. To may be
// changed.
type FoodLossEvent struct {
	hook.Cancellable
	Base
	From int
	To   *int
}

// HealEvent is called when a player heals. Health may be changed.
type HealEvent struct {
	hook.Cancellable
	Base
	Health *float64
	Source world.HealingSource
}

// HurtEvent is called when a player is about to take damage. Damage and
// AttackImmunity may be changed.
type HurtEvent struct {
	hook.Cancellable
	Base
	Damage *float64
	// Immune is true if the player is still immune from a recent hit.
	Immune         bool
	AttackImmunity *time.Duration
	Source         world.DamageSource
}

// SetOnFireEvent is called when a player is set on fire. Duration may be
// changed.
type SetOnFireEvent struct {
	hook.Cancellable
	Base
	Duration *time.Duration
}

// DeathEvent is called when a player dies. KeepInventory may be changed.
type DeathEvent struct {
	Base
	Source        world.DamageSource
	KeepInventory *bool
}

// RespawnEvent is called when a player respawns. Position and World may be
// changed.
type RespawnEvent struct {
	Base
	Position *mgl64.Vec3
	World    **world.World
}

// SkinChangeEvent is called when a player changes skin. Skin may be
// changed.
type SkinChangeEvent struct {
	hook.Cancellable
	Base
	Skin *skin.Skin
}

// FireExtinguishEvent is called when a player puts out a fire.
type FireExtinguishEvent struct {
	hook.Cancellable
	Base
	Pos cube.Pos
}

// StartBreakEvent is called when a player starts breaking a block.
type StartBreakEvent struct {
	hook.Cancellable
	Base
	Pos   cube.Pos
	Block world.Block
}

// BlockName returns the block's name, for the OnlyBlock filter.
func (e *StartBreakEvent) BlockName() string { return blockName(e.Block) }

// BlockBreakEvent is called when a player breaks a block. Drops and XP may
// be changed.
type BlockBreakEvent struct {
	hook.Cancellable
	Base
	Pos   cube.Pos
	Block world.Block
	Drops *[]item.Stack
	XP    *int
}

// BlockName returns the block's name, for the OnlyBlock filter.
func (e *BlockBreakEvent) BlockName() string { return blockName(e.Block) }

// BlockPlaceEvent is called when a player places a block.
type BlockPlaceEvent struct {
	hook.Cancellable
	Base
	Pos   cube.Pos
	Block world.Block
}

// BlockName returns the block's name, for the OnlyBlock filter.
func (e *BlockPlaceEvent) BlockName() string { return blockName(e.Block) }

// BlockPickEvent is called when a player picks a block (middle click).
type BlockPickEvent struct {
	hook.Cancellable
	Base
	Pos   cube.Pos
	Block world.Block
}

// BlockName returns the block's name, for the OnlyBlock filter.
func (e *BlockPickEvent) BlockName() string { return blockName(e.Block) }

// ItemUseEvent is called when a player uses the held item in the air.
type ItemUseEvent struct {
	hook.Cancellable
	Base
}

// ItemUseOnBlockEvent is called when a player uses the held item on a
// block, before the block is activated or the item placed.
type ItemUseOnBlockEvent struct {
	hook.Cancellable
	Base
	Pos      cube.Pos
	Face     cube.Face
	ClickPos mgl64.Vec3
}

// ItemUseOnEntityEvent is called when a player uses the held item on an
// entity.
type ItemUseOnEntityEvent struct {
	hook.Cancellable
	Base
	Entity world.Entity
}

// ItemReleaseEvent is called when a player releases a held item, such as a
// drawn bow.
type ItemReleaseEvent struct {
	hook.Cancellable
	Base
	Item     item.Stack
	Duration time.Duration
}

// ItemConsumeEvent is called when a player eats or drinks.
type ItemConsumeEvent struct {
	hook.Cancellable
	Base
	Item item.Stack
}

// AttackEntityEvent is called when a player attacks an entity. Force,
// Height and Critical may be changed.
type AttackEntityEvent struct {
	hook.Cancellable
	Base
	Entity   world.Entity
	Force    *float64
	Height   *float64
	Critical *bool
}

// ExperienceGainEvent is called when a player gains experience. Amount may
// be changed.
type ExperienceGainEvent struct {
	hook.Cancellable
	Base
	Amount *int
}

// PunchAirEvent is called when a player swings at nothing.
type PunchAirEvent struct {
	hook.Cancellable
	Base
}

// SignEditEvent is called when a player edits a sign.
type SignEditEvent struct {
	hook.Cancellable
	Base
	Pos              cube.Pos
	FrontSide        bool
	OldText, NewText string
}

// SleepEvent is called when a player goes to sleep.
type SleepEvent struct {
	hook.Cancellable
	Base
	SendReminder *bool
}

// LecternPageTurnEvent is called when a player turns a lectern's page.
// NewPage may be changed.
type LecternPageTurnEvent struct {
	hook.Cancellable
	Base
	Pos     cube.Pos
	OldPage int
	NewPage *int
}

// ItemDamageEvent is called when an item of the player loses durability.
// Damage may be changed.
type ItemDamageEvent struct {
	hook.Cancellable
	Base
	Item   item.Stack
	Damage *int
}

// ItemPickupEvent is called when a player picks up an item. Item may be
// changed.
type ItemPickupEvent struct {
	hook.Cancellable
	Base
	Item *item.Stack
}

// HeldSlotChangeEvent is called when a player changes the selected hotbar
// slot.
type HeldSlotChangeEvent struct {
	hook.Cancellable
	Base
	From, To int
}

// ItemDropEvent is called when a player drops an item.
type ItemDropEvent struct {
	hook.Cancellable
	Base
	Item item.Stack
}

// TransferEvent is called when a player is sent to another server. Address
// may be changed.
type TransferEvent struct {
	hook.Cancellable
	Base
	Address *net.UDPAddr
}

// CommandEvent is called when a player runs a command.
type CommandEvent struct {
	hook.Cancellable
	Base
	Command cmd.Command
	Args    []string
}

// DiagnosticsEvent is called when the client sends performance figures.
type DiagnosticsEvent struct {
	Base
	Diagnostics session.Diagnostics
}

// Events returns the event lists the player calls.
func (p *Player) Events() *Events { return p.ev }
