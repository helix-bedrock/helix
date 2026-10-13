package world

import (
	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/hook"
	"time"
)

// Events holds the listener lists of world events (Helix, RFC 0001). The
// engine calls them directly where things happen; plugins register through
// the helix package. A World gets them from Config.Events, usually shared by
// every world of a server.
//
// Listeners run on the region the event happens in, with that region's Tx.
type Events struct {
	LiquidFlow     hook.List[LiquidFlowEvent]
	LiquidDecay    hook.List[LiquidDecayEvent]
	LiquidHarden   hook.List[LiquidHardenEvent]
	Sound          hook.List[SoundEvent]
	FireSpread     hook.List[FireSpreadEvent]
	BlockBurn      hook.List[BlockBurnEvent]
	CropTrample    hook.List[CropTrampleEvent]
	LeavesDecay    hook.List[LeavesDecayEvent]
	PortalCreate   hook.List[PortalCreateEvent]
	PortalActivate hook.List[PortalActivateEvent]
	EntitySpawn    hook.List[EntitySpawnEvent]
	EntityDespawn  hook.List[EntityDespawnEvent]
	Explosion      hook.List[ExplosionEvent]
	RedstoneUpdate hook.List[RedstoneUpdateEvent]
	Close          hook.List[CloseEvent]
	SlowTick       hook.List[SlowTickEvent]
}

// Events returns the world's event lists.
func (w *World) Events() *Events { return w.conf.Events }

// txWorld is embedded in world events for the OnlyWorld filter.
type txWorld struct {
	// Tx is the transaction of the region the event happens in. It is valid
	// only while the listener runs.
	Tx *Tx
}

// WorldName returns the name of the world the event happens in.
func (e *txWorld) WorldName() string {
	if e.Tx == nil {
		return ""
	}
	return e.Tx.World().Name()
}

func nameOf(b Block) string {
	if b == nil {
		return "minecraft:air"
	}
	name, _ := b.EncodeBlock()
	return name
}

// LiquidFlowEvent is called when a liquid flows from one block into another.
type LiquidFlowEvent struct {
	hook.Cancellable
	txWorld
	From, Into cube.Pos
	Liquid     Liquid
	Replaced   Block
}

// BlockName returns the liquid's name, for the OnlyBlock filter.
func (e *LiquidFlowEvent) BlockName() string { return nameOf(e.Liquid) }

// LiquidDecayEvent is called when a liquid gets lower or disappears.
type LiquidDecayEvent struct {
	hook.Cancellable
	txWorld
	Pos           cube.Pos
	Before, After Liquid
}

// BlockName returns the liquid's name, for the OnlyBlock filter.
func (e *LiquidDecayEvent) BlockName() string { return nameOf(e.Before) }

// LiquidHardenEvent is called when a liquid hardens, such as lava turning
// into obsidian.
type LiquidHardenEvent struct {
	hook.Cancellable
	txWorld
	Pos                       cube.Pos
	Hardened, Other, NewBlock Block
}

// BlockName returns the new block's name, for the OnlyBlock filter.
func (e *LiquidHardenEvent) BlockName() string { return nameOf(e.NewBlock) }

// SoundEvent is called when a sound plays.
type SoundEvent struct {
	hook.Cancellable
	txWorld
	Sound    Sound
	Position mgl64.Vec3
}

// FireSpreadEvent is called when fire spreads to another block.
type FireSpreadEvent struct {
	hook.Cancellable
	txWorld
	From, To cube.Pos
}

// BlockBurnEvent is called when fire burns a block away.
type BlockBurnEvent struct {
	hook.Cancellable
	txWorld
	Pos cube.Pos
}

// CropTrampleEvent is called when an entity tramples farmland.
type CropTrampleEvent struct {
	hook.Cancellable
	txWorld
	Pos cube.Pos
}

// LeavesDecayEvent is called when leaves decay.
type LeavesDecayEvent struct {
	hook.Cancellable
	txWorld
	Pos cube.Pos
}

// PortalCreateEvent is called when a portal is built.
type PortalCreateEvent struct {
	hook.Cancellable
	txWorld
	Dimension Dimension
	Positions []cube.Pos
}

// PortalActivateEvent is called when a portal frame is filled.
type PortalActivateEvent struct {
	hook.Cancellable
	txWorld
	Dimension Dimension
	Positions []cube.Pos
}

// EntitySpawnEvent is called when an entity is added to a world.
type EntitySpawnEvent struct {
	txWorld
	Entity Entity
}

// EntityDespawnEvent is called when an entity is removed from a world.
type EntityDespawnEvent struct {
	txWorld
	Entity Entity
}

// ExplosionEvent is called when something explodes. Entities, Blocks,
// ItemDropChance and SpawnFire may be changed.
type ExplosionEvent struct {
	hook.Cancellable
	txWorld
	Source         ExplosionSource
	Entities       *[]Entity
	Blocks         *[]cube.Pos
	ItemDropChance *float64
	SpawnFire      *bool
}

// RedstoneUpdateEvent is called when the redstone engine changes a block.
type RedstoneUpdateEvent struct {
	hook.Cancellable
	txWorld
	Update RedstoneUpdate
}

// CloseEvent is called on every region of a world just before it closes,
// for last work in it.
type CloseEvent struct {
	txWorld
}

// SlowTickEvent is called on a region whose tick took longer than
// Config.TickBudget, one tick (50ms) by default, so the server can no longer keep up there. Took
// is how long the tick took. Listeners can look at the region through Tx,
// for example to find what crowds it.
type SlowTickEvent struct {
	txWorld
	Region uint64
	Took   time.Duration
}

// The functions below fire an event from code outside this package (blocks,
// portals). Each returns true if a listener cancelled the action, and builds
// the event only if someone listens.

func evs(tx *Tx) *Events { return tx.w.conf.Events }

// LiquidFlow fires LiquidFlowEvent.
func LiquidFlow(tx *Tx, from, into cube.Pos, l Liquid, replaced Block) (cancelled bool) {
	if !evs(tx).LiquidFlow.Active() {
		return false
	}
	return evs(tx).LiquidFlow.Fire(&LiquidFlowEvent{txWorld: txWorld{tx}, From: from, Into: into, Liquid: l, Replaced: replaced})
}

// LiquidDecay fires LiquidDecayEvent.
func LiquidDecay(tx *Tx, pos cube.Pos, before, after Liquid) (cancelled bool) {
	if !evs(tx).LiquidDecay.Active() {
		return false
	}
	return evs(tx).LiquidDecay.Fire(&LiquidDecayEvent{txWorld: txWorld{tx}, Pos: pos, Before: before, After: after})
}

// LiquidHarden fires LiquidHardenEvent.
func LiquidHarden(tx *Tx, pos cube.Pos, hardened, other, newBlock Block) (cancelled bool) {
	if !evs(tx).LiquidHarden.Active() {
		return false
	}
	return evs(tx).LiquidHarden.Fire(&LiquidHardenEvent{txWorld: txWorld{tx}, Pos: pos, Hardened: hardened, Other: other, NewBlock: newBlock})
}

// FireSpread fires FireSpreadEvent.
func FireSpread(tx *Tx, from, to cube.Pos) (cancelled bool) {
	if !evs(tx).FireSpread.Active() {
		return false
	}
	return evs(tx).FireSpread.Fire(&FireSpreadEvent{txWorld: txWorld{tx}, From: from, To: to})
}

// BlockBurn fires BlockBurnEvent.
func BlockBurn(tx *Tx, pos cube.Pos) (cancelled bool) {
	if !evs(tx).BlockBurn.Active() {
		return false
	}
	return evs(tx).BlockBurn.Fire(&BlockBurnEvent{txWorld: txWorld{tx}, Pos: pos})
}

// CropTrample fires CropTrampleEvent.
func CropTrample(tx *Tx, pos cube.Pos) (cancelled bool) {
	if !evs(tx).CropTrample.Active() {
		return false
	}
	return evs(tx).CropTrample.Fire(&CropTrampleEvent{txWorld: txWorld{tx}, Pos: pos})
}

// LeavesDecay fires LeavesDecayEvent.
func LeavesDecay(tx *Tx, pos cube.Pos) (cancelled bool) {
	if !evs(tx).LeavesDecay.Active() {
		return false
	}
	return evs(tx).LeavesDecay.Fire(&LeavesDecayEvent{txWorld: txWorld{tx}, Pos: pos})
}

// PortalCreate fires PortalCreateEvent.
func PortalCreate(tx *Tx, dim Dimension, positions []cube.Pos) (cancelled bool) {
	if !evs(tx).PortalCreate.Active() {
		return false
	}
	return evs(tx).PortalCreate.Fire(&PortalCreateEvent{txWorld: txWorld{tx}, Dimension: dim, Positions: positions})
}

// PortalActivate fires PortalActivateEvent.
func PortalActivate(tx *Tx, dim Dimension, positions []cube.Pos) (cancelled bool) {
	if !evs(tx).PortalActivate.Active() {
		return false
	}
	return evs(tx).PortalActivate.Fire(&PortalActivateEvent{txWorld: txWorld{tx}, Dimension: dim, Positions: positions})
}

// Explosion fires ExplosionEvent.
func Explosion(tx *Tx, src ExplosionSource, entities *[]Entity, blocks *[]cube.Pos, itemDropChance *float64, spawnFire *bool) (cancelled bool) {
	if !evs(tx).Explosion.Active() {
		return false
	}
	return evs(tx).Explosion.Fire(&ExplosionEvent{txWorld: txWorld{tx}, Source: src, Entities: entities, Blocks: blocks, ItemDropChance: itemDropChance, SpawnFire: spawnFire})
}
