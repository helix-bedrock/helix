// Package blockuse is what vanilla's doors, trapdoors, fence gates, levers and
// buttons do when used, as BDS 1.26.60 does it: their state, their sounds and
// the blocks that react later. Power itself is the circuit package's.
package blockuse

import (
	"maps"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// States used here.
const (
	stateOpen     = "open_bit"
	stateUpper    = "upper_block_bit"
	stateHinge    = "door_hinge_bit"
	statePressed  = "button_pressed_bit"
	stateCardinal = direction.CardinalState
	stateDelay    = "repeater_delay"
	stateSubtract = "output_subtract_bit"
)

// repeaterDelays is how many delay settings a repeater has.
const repeaterDelays = 4

// Ticks a pressed button waits before it pops out.
const (
	buttonTicksStone = 20
	buttonTicksWood  = 30
)

// kind is the vanilla block class a block belongs to.
type kind uint8

const (
	kindNone kind = iota
	kindDoor
	kindTrapdoor
	kindGate
	kindLever
	kindButton
	kindRepeater
	kindComparator
)

// kindOf returns the class of the block called name.
func kindOf(name string) kind {
	switch {
	case strings.HasSuffix(name, "_door"):
		return kindDoor
	case strings.HasSuffix(name, "trapdoor"):
		return kindTrapdoor
	case strings.HasSuffix(name, "fence_gate"):
		return kindGate
	case name == "minecraft:lever":
		return kindLever
	case strings.HasSuffix(name, "_button"):
		return kindButton
	case strings.HasSuffix(name, "_repeater"):
		return kindRepeater
	case strings.HasSuffix(name, "_comparator"):
		return kindComparator
	}
	return kindNone
}

// Usable reports whether block is one this package gives a use to, so a click
// on it is taken even when it does nothing.
func Usable(blocks *registry.Blocks, block uint32) bool {
	return kindOf(blocks.Name(block)) != kindNone
}

// Use does what using block at pos does, for a user looking along yaw. False if
// the block did not take the click, so the held item is used instead: iron doors
// and trapdoors only react to redstone. Runs: owner of the region.
func Use(blocks *registry.Blocks, r *world.Region, pos cube.Pos, block uint32, yaw float32) bool {
	name := blocks.Name(block)
	switch kindOf(name) {
	case kindDoor:
		return name != "minecraft:iron_door" && useDoor(blocks, r, pos, block)
	case kindTrapdoor:
		if name == "minecraft:iron_trapdoor" {
			return false
		}
		open := bit(blocks, block, stateOpen)
		next := with(blocks, block, stateOpen, flag(!open))
		setAndUpdate(r, pos, next)
		sound(blocks, r, pos, choose(open, packet.SoundEventTrapdoorClose, packet.SoundEventTrapdoorOpen), block)
	case kindGate:
		useGate(blocks, r, pos, block, yaw)
	case kindLever:
		open := bit(blocks, block, stateOpen)
		setAndUpdate(r, pos, with(blocks, block, stateOpen, flag(!open)))
		sound(blocks, r, pos, choose(open, packet.SoundEventButtonClickOff, packet.SoundEventButtonClickOn), block)
	case kindButton:
		if !bit(blocks, block, statePressed) {
			next := with(blocks, block, statePressed, flag(true))
			setAndUpdate(r, pos, next)
			sound(blocks, r, pos, packet.SoundEventButtonClickOn, block)
			r.ScheduleTick(pos, next, buttonTicks(name), 0)
		}
	case kindRepeater:
		delay, _ := blocks.States(block)[stateDelay].(int32)
		setAndUpdate(r, pos, with(blocks, block, stateDelay, (delay+1)%repeaterDelays))
	case kindComparator:
		subtract := bit(blocks, block, stateSubtract)
		next := with(blocks, block, stateSubtract, flag(!subtract))
		sound(blocks, r, pos, choose(subtract, packet.SoundEventPowerOn, packet.SoundEventPowerOff), next)
		setAndUpdate(r, pos, next)
	default:
		return false
	}
	return true
}

// useGate opens or closes a gate. Opened from the side it would swing towards,
// it turns to swing away from the user.
func useGate(blocks *registry.Blocks, r *world.Region, pos cube.Pos, block uint32, yaw float32) {
	open := bit(blocks, block, stateOpen)
	next := block
	if !open {
		d := direction.FromYaw(yaw)
		if c, ok := cardinal(blocks, block); ok && c == d.Opposite() {
			next = with(blocks, next, stateCardinal, d.String())
		}
	}
	setAndUpdate(r, pos, with(blocks, next, stateOpen, flag(!open)))
	sound(blocks, r, pos, choose(open, packet.SoundEventFenceGateClose, packet.SoundEventFenceGateOpen), block)
}

// useDoor flips the door's lower half, whichever half was used, and sends the
// upper half again so clients draw it with the lower one's new state.
func useDoor(blocks *registry.Blocks, r *world.Region, pos cube.Pos, block uint32) bool {
	lower, lowerPos := block, pos
	if bit(blocks, block, stateUpper) {
		lowerPos = pos.Side(cube.FaceDown)
		var ok bool
		if lower, ok = r.Block(lowerPos, 0); !ok || blocks.Name(lower) != blocks.Name(block) {
			return true
		}
	}
	open := bit(blocks, lower, stateOpen)
	setAndUpdate(r, lowerPos, with(blocks, lower, stateOpen, flag(!open)))
	upperPos := lowerPos.Side(cube.FaceUp)
	if upper, ok := r.Block(upperPos, 0); ok {
		r.SetBlock(upperPos, 0, upper)
		r.UpdateNeighbours(upperPos)
	}
	sound(blocks, r, lowerPos, choose(open, packet.SoundEventDoorClose, packet.SoundEventDoorOpen), lower)
	return true
}

// SetPowered opens or closes a door, trapdoor or fence gate because of redstone,
// if it is not that way already, with its sound. A gate keeps its direction.
// Runs: owner of the region.
func SetPowered(blocks *registry.Blocks, r *world.Region, pos cube.Pos, block uint32, on bool) {
	switch kindOf(blocks.Name(block)) {
	case kindDoor:
		lower, lowerPos := block, pos
		if bit(blocks, block, stateUpper) {
			lowerPos = pos.Side(cube.FaceDown)
			var ok bool
			if lower, ok = r.Block(lowerPos, 0); !ok {
				return
			}
		}
		if bit(blocks, lower, stateOpen) != on {
			useDoor(blocks, r, lowerPos, lower)
		}
	case kindTrapdoor:
		if bit(blocks, block, stateOpen) != on {
			setAndUpdate(r, pos, with(blocks, block, stateOpen, flag(on)))
			sound(blocks, r, pos, choose(on, packet.SoundEventTrapdoorOpen, packet.SoundEventTrapdoorClose), block)
		}
	case kindGate:
		if bit(blocks, block, stateOpen) != on {
			setAndUpdate(r, pos, with(blocks, block, stateOpen, flag(on)))
			sound(blocks, r, pos, choose(on, packet.SoundEventFenceGateOpen, packet.SoundEventFenceGateClose), block)
		}
	}
}

// Sound plays a block's sound at its centre to everyone near.
func Sound(blocks *registry.Blocks, r *world.Region, pos cube.Pos, event string, block uint32) {
	sound(blocks, r, pos, event, block)
}

// IsDoor reports whether block is a door, which is placed in two halves.
func IsDoor(blocks *registry.Blocks, block uint32) bool {
	return kindOf(blocks.Name(block)) == kindDoor
}

// DoorFits reports whether a door may stand at pos: its upper half goes in the
// block above, which replaceable must allow, on a block that holds it up.
func DoorFits(blocks *registry.Blocks, r *world.Region, pos cube.Pos, replaceable func(block uint32) bool) bool {
	above, ok := r.Block(pos.Side(cube.FaceUp), 0)
	return ok && replaceable(above) && holdsDoor(blocks, r, pos)
}

// PlaceDoor sets both halves of the door with lower state block at pos. The
// hinge goes on the side away from a neighbouring door, else on the side with
// more solid blocks. Runs: owner of the region.
func PlaceDoor(blocks *registry.Blocks, r *world.Region, pos cube.Pos, block uint32) bool {
	if !r.SetBlock(pos, 0, block) {
		return false
	}
	upper := with(blocks, block, stateUpper, flag(true))
	c, ok := cardinal(blocks, block)
	upper = with(blocks, upper, stateHinge, flag(ok && hingeOnRight(blocks, r, pos, c)))
	upperPos := pos.Side(cube.FaceUp)
	r.SetBlock(upperPos, 0, upper)
	r.UpdateNeighbours(pos)
	r.UpdateNeighbours(upperPos)
	return true
}

// hingeOnRight picks the hinge from the blocks either side of a door facing d:
// its right neighbour is the one in direction d, its left the one opposite.
func hingeOnRight(blocks *registry.Blocks, r *world.Region, pos cube.Pos, d direction.Cardinal) bool {
	side := func(f cube.Face) (door bool, solid int) {
		at := pos.Side(f)
		for range 2 {
			if b, ok := r.Block(at, 0); ok {
				door = door || IsDoor(blocks, b)
				if blocks.Properties(b).SolidMaterial {
					solid++
				}
			}
			at = at.Side(cube.FaceUp)
		}
		return door, solid
	}
	leftDoor, leftSolid := side(d.Opposite().Face())
	rightDoor, rightSolid := side(d.Face())
	if leftDoor && !rightDoor {
		return true
	}
	return rightSolid > leftSolid
}

// holdsDoor reports whether the block under pos holds a door up.
func holdsDoor(blocks *registry.Blocks, r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos.Side(cube.FaceDown), 0)
	return ok && blocks.Properties(b).SolidMaterial
}

// doorStands reports whether a door half at pos still has its other half and,
// for a lower half, a block holding it up.
func doorStands(blocks *registry.Blocks, r *world.Region, pos cube.Pos, block uint32) bool {
	upper := bit(blocks, block, stateUpper)
	otherPos := pos.Side(cube.FaceUp)
	if upper {
		otherPos = pos.Side(cube.FaceDown)
	} else if !holdsDoor(blocks, r, pos) {
		return false
	}
	other, ok := r.Block(otherPos, 0)
	if !ok {
		// The other half's chunk is in another region; leave the door alone.
		return true
	}
	return blocks.Name(other) == blocks.Name(block) && bit(blocks, other, stateUpper) != upper
}

// buttonTicks is how long the button called name stays in.
func buttonTicks(name string) int32 {
	if name == "minecraft:stone_button" || name == "minecraft:polished_blackstone_button" {
		return buttonTicksStone
	}
	return buttonTicksWood
}

// Ticker adds the blocks that react later to another BlockTicker: buttons pop
// out, doors fall apart.
type Ticker struct {
	world.BlockTicker
	blocks *registry.Blocks
}

// NewTicker returns a Ticker that hands everything else to next.
func NewTicker(blocks *registry.Blocks, next world.BlockTicker) *Ticker {
	return &Ticker{BlockTicker: next, blocks: blocks}
}

// QueuedTick pops a pressed button out.
func (t *Ticker) QueuedTick(r *world.Region, pos cube.Pos, block uint32) {
	if kindOf(t.blocks.Name(block)) != kindButton {
		t.BlockTicker.QueuedTick(r, pos, block)
		return
	}
	now, ok := r.Block(pos, 0)
	if !ok || kindOf(t.blocks.Name(now)) != kindButton || !bit(t.blocks, now, statePressed) {
		return
	}
	setAndUpdate(r, pos, with(t.blocks, now, statePressed, flag(false)))
	sound(t.blocks, r, pos, packet.SoundEventButtonClickOff, now)
}

// NeighbourChanged breaks a door that lost its other half or its support.
func (t *Ticker) NeighbourChanged(r *world.Region, pos, from cube.Pos, block uint32, layer uint8) {
	if layer != 0 || !IsDoor(t.blocks, block) || doorStands(t.blocks, r, pos, block) {
		t.BlockTicker.NeighbourChanged(r, pos, from, block, layer)
		return
	}
	setAndUpdate(r, pos, t.blocks.Air())
	r.Broadcast(pos, &packet.LevelEvent{
		EventType: packet.LevelEventParticlesDestroyBlock, Position: centre(pos),
		EventData: int32(t.blocks.Hash(block)), //nolint:gosec // hashes are sent as int32
	}, nil)
}

// setAndUpdate sets a block and tells its neighbours.
func setAndUpdate(r *world.Region, pos cube.Pos, block uint32) {
	if old, ok := r.Block(pos, 0); ok && old != block && r.SetBlock(pos, 0, block) {
		r.UpdateNeighbours(pos)
	}
}

// sound plays a block's sound at its centre to everyone near.
func sound(blocks *registry.Blocks, r *world.Region, pos cube.Pos, event string, block uint32) {
	r.Broadcast(pos, &packet.LevelSoundEvent{
		SoundType: event, Position: centre(pos),
		ExtraData: int32(blocks.Hash(block)), //nolint:gosec // hashes are sent as int32
	}, nil)
}

// centre is the middle of the block at pos.
func centre(pos cube.Pos) mgl32.Vec3 {
	return mgl32.Vec3{float32(pos[0]) + 0.5, float32(pos[1]) + 0.5, float32(pos[2]) + 0.5}
}

// choose returns a if c, else b.
func choose(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}

// flag is a bit state's value.
func flag(on bool) uint8 {
	if on {
		return 1
	}
	return 0
}

// bit reports a bit state, false if block does not have it.
func bit(blocks *registry.Blocks, block uint32, key string) bool {
	v, _ := blocks.States(block)[key].(uint8)
	return v != 0
}

// cardinal is a block's direction; false if it has none.
func cardinal(blocks *registry.Blocks, block uint32) (direction.Cardinal, bool) {
	v, _ := blocks.States(block)[stateCardinal].(string)
	return direction.Parse(v)
}

// with returns block with one state changed, or block if it has no such state.
func with(blocks *registry.Blocks, block uint32, key string, v any) uint32 {
	states := blocks.States(block)
	if _, ok := states[key]; !ok {
		return block
	}
	next := maps.Clone(states)
	next[key] = v
	id, ok := blocks.StateID(blocks.Name(block), next)
	if !ok {
		return block
	}
	return id
}
