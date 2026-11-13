package circuit

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/redstone"
	"github.com/helix-bedrock/world/cube"
)

// The blocks of a tripwire.
const (
	nameHook     = "minecraft:tripwire_hook"
	nameTripWire = "minecraft:trip_wire"
)

// Their states.
const (
	stateDirection = "direction"
	stateAttached  = "attached_bit"
	stateDisarmed  = "disarmed_bit"
)

// BDS's numbers for a tripwire.
const (
	// hookNoSource says no wire asked a hook to look.
	hookNoSource = -1
	// tripHeight is the height of the part of a wire's block that senses.
	tripHeight = 0.15625
	// nameShears is the item that cuts a wire without setting it off.
	nameShears = "minecraft:shears"
)

// The hook directions a wire tells: the hook found going south has direction north,
// the one found going west has direction east. Each hook looks along its whole wire,
// so telling one is enough.
const (
	hookFacingNorth = 2
	hookFacingEast  = 3
)

// hookSteps is a step in each value of a hook's direction.
var hookSteps = [...]cube.Pos{{0, 0, 1}, {-1, 0, 0}, {0, 0, -1}, {1, 0, 0}}

// hookHolders is the face of a hook's holder for each value of its direction.
var hookHolders = [...]redstone.Face{redstone.North, redstone.East, redstone.South, redstone.West}

// oppositeHook is the direction of a hook facing a hook of direction d.
func oppositeHook(d int32) int32 { return (d + 2) % int32(len(hookSteps)) }

// hookDirection is a hook's direction, kept in range.
func hookDirection(states map[string]any) int32 {
	d := intState(states, stateDirection)
	if d < 0 || int(d) >= len(hookSteps) {
		return 0
	}
	return d
}

// wireSeen is what a hook saw of a wire along its line.
type wireSeen struct {
	wire, disarmed bool
}

// hookLook is BDS's calculate state: a hook looks along its line for the hook facing
// it. It is attached when only wire lies between them, and on when one of those wires
// is pressed and not disarmed. source is how far along the wire that asked it to look
// is, and has it look again later. Runs: owner of the region.
func (t *Ticker) hookLook(c *regionCircuit, pos cube.Pos, block uint32, source int) {
	states := t.blocks.States(block)
	d := hookDirection(states)
	step := hookSteps[d]
	wasAttached, wasPowered := bitState(states, stateAttached), bitState(states, statePowered)
	attached, pressed, other := true, false, 0
	var seen [hookReachLimit]wireSeen
	hookReach := t.settings.Get().Tripwire.HookReach
	for i := 1; i < hookReach; i++ {
		b, ok := c.r.Block(cube.Pos{pos[0] + step[0]*i, pos[1], pos[2] + step[2]*i}, 0)
		if !ok {
			attached = false
			break
		}
		k := t.kind(b)
		if k == kindHook {
			if hookDirection(t.blocks.States(b)) == oppositeHook(d) {
				other = i
			}
			break
		}
		if k != kindTripWire && i != source {
			attached = false
			continue
		}
		s := t.blocks.States(b)
		seen[i] = wireSeen{wire: true, disarmed: bitState(s, stateDisarmed)}
		pressed = pressed || (!seen[i].disarmed && bitState(s, statePowered))
		if i == source {
			c.r.ScheduleTick(pos, block, t.settings.Get().Tripwire.Delay, 0)
			attached = attached && !seen[i].disarmed
		}
	}
	hooked := other >= 2 && attached
	on := hooked && pressed
	next := t.with(t.with(block, stateAttached, flag(hooked)), statePowered, flag(on))
	if other > 0 {
		at := cube.Pos{pos[0] + step[0]*other, pos[1], pos[2] + step[2]*other}
		t.setHook(c, at, t.with(next, stateDirection, oppositeHook(d)), on)
		t.hookSound(c.r, at, next, hooked, on, wasAttached, wasPowered)
	}
	t.hookSound(c.r, pos, next, hooked, on, wasAttached, wasPowered)
	t.setHook(c, pos, next, on)
	if other < 2 || hooked == wasAttached {
		return
	}
	for i := 1; i < other; i++ {
		if !seen[i].wire {
			continue
		}
		at := cube.Pos{pos[0] + step[0]*i, pos[1], pos[2] + step[2]*i}
		if b, ok := c.r.Block(at, 0); ok && t.kind(b) == kindTripWire {
			t.setByCircuit(c, at, t.with(b, stateAttached, flag(attached)))
		}
	}
}

// setHook sets a hook's state and the power it gives.
func (t *Ticker) setHook(c *regionCircuit, pos cube.Pos, block uint32, on bool) {
	t.setByCircuit(c, pos, block)
	if comp := c.sys.Component(toPos(pos), redstone.KindProducer); comp != nil {
		comp.SetStrength(onOff(on))
	}
}

// hookSound plays what changed about a hook, one sound at most.
func (t *Ticker) hookSound(r *world.Region, pos cube.Pos, block uint32, attached, on, wasAttached, wasOn bool) {
	var event string
	switch {
	case on && !wasOn:
		event = packet.SoundEventPowerOn
	case !on && wasOn:
		event = packet.SoundEventPowerOff
	case attached && !wasAttached:
		event = packet.SoundEventAttach
	case !attached && wasAttached:
		event = packet.SoundEventDetach
	default:
		return
	}
	r.Broadcast(pos, &packet.LevelSoundEvent{
		SoundType: event,
		Position:  mgl32.Vec3{float32(pos[0]) + 0.5, float32(pos[1]) + plateSoundHeight, float32(pos[2]) + 0.5},
		ExtraData: int32(t.blocks.Hash(block)), //nolint:gosec // hashes are sent as int32
	}, nil)
}

// hookHeld reports whether a hook has its holder.
func (t *Ticker) hookHeld(r *world.Region, pos cube.Pos, block uint32) bool {
	f := hookHolders[hookDirection(t.blocks.States(block))]
	b, ok := r.Block(fromPos(toPos(pos).Side(f)), 0)
	return ok && t.attach.Supports(b, cube.Face(f.Opposite()))
}

// hookTick has a hook look again, and breaks one that lost its holder.
func (t *Ticker) hookTick(c *regionCircuit, pos cube.Pos, block uint32) {
	t.hookLook(c, pos, block, hookNoSource)
	if !t.hookHeld(c.r, pos, block) {
		now, _ := c.r.Block(pos, 0)
		t.fall(c.r, pos, now)
	}
}

// tellHooks has the hooks at the ends of a wire's line look. BDS looks south and west.
func (t *Ticker) tellHooks(c *regionCircuit, pos cube.Pos) {
	t.tellHook(c, pos, cube.Pos{0, 0, 1}, hookFacingNorth)
	t.tellHook(c, pos, cube.Pos{-1, 0, 0}, hookFacingEast)
}

// tellHook follows the wire from pos in one direction to a hook facing back along it.
func (t *Ticker) tellHook(c *regionCircuit, pos, step cube.Pos, facing int32) {
	hookReach := t.settings.Get().Tripwire.HookReach
	for i := 1; i < hookReach; i++ {
		at := cube.Pos{pos[0] + step[0]*i, pos[1], pos[2] + step[2]*i}
		b, ok := c.r.Block(at, 0)
		if !ok {
			return
		}
		switch t.kind(b) {
		case kindTripWire:
			continue
		case kindHook:
			if hookDirection(t.blocks.States(b)) == facing {
				t.hookLook(c, at, b, i)
			}
		}
		return
	}
}

// wireUpdate sets a wire pressed if anything touches it, tells the hooks when that
// changed, and while it is pressed has it look again later.
func (t *Ticker) wireUpdate(c *regionCircuit, pos cube.Pos, block uint32) {
	pressed := t.wireTouched(c, pos)
	if pressed != bitState(t.blocks.States(block), statePowered) {
		t.setByCircuit(c, pos, t.with(block, statePowered, flag(pressed)))
		t.tellHooks(c, pos)
	}
	if pressed {
		now, _ := c.r.Block(pos, 0)
		c.r.ScheduleTick(pos, now, t.settings.Get().Tripwire.Delay, 0)
	}
}

// wireTouched reports whether anything touches the wire at pos.
func (t *Ticker) wireTouched(c *regionCircuit, pos cube.Pos) bool {
	if t.entityBox == nil {
		return false
	}
	x, y, z := float32(pos[0]), float32(pos[1]), float32(pos[2])
	c.found = c.r.AppendEntitiesIn(c.found[:0], x-plateReach, z-plateReach, x+1+plateReach, z+1+plateReach)
	for _, e := range c.found {
		lo, hi, _, ok := t.entityBox(e)
		if ok && hi[0] > x && lo[0] < x+1 && hi[1] > y && lo[1] < y+tripHeight && hi[2] > z && lo[2] < z+1 {
			return true
		}
	}
	return false
}

// DestroyBlock is a player about to break a block with the item called item in hand:
// a wire goes off for shears, and is set off by anything else. Runs: owner of the
// region.
func (t *Ticker) DestroyBlock(r *world.Region, pos cube.Pos, block uint32, item string) {
	if t.kind(block) != kindTripWire {
		return
	}
	c := t.circuitOf(r)
	t.setByCircuit(c, pos, t.with(block, statePowered, flag(item != nameShears)))
	t.tellHooks(c, pos)
}
