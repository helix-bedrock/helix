package circuit

import (
	"maps"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// BDS's names for a bell.
const (
	nameBell        = "minecraft:bell"
	bellEntityID    = "Bell"
	bellRingingTag  = "Ringing"
	bellTicksTag    = "Ticks"
	bellDirTag      = "Direction"
	stateAttachment = "attachment"
	attachStanding  = "standing"
	attachHanging   = "hanging"
	attachSide      = "side"
	attachMultiple  = "multiple"
)

// bellRingTicks is where a bell's count stops it. A ringing bell counts two a tick, so
// a ring lasts 25 ticks.
const bellRingTicks = 50

// bellEntity is a bell's block entity. BDS has it look at its power each tick and ring
// as power comes; powered is not saved, so a powered bell rings again when loaded.
type bellEntity struct {
	t       *Ticker
	saved   map[string]any
	ticks   int32
	dir     int32
	ringing bool
	powered bool
}

// loadBell brings back a bell's block entity.
func (t *Ticker) loadBell(saved map[string]any) *bellEntity {
	e := &bellEntity{t: t, saved: maps.Clone(saved)}
	ringing, _ := saved[bellRingingTag].(uint8)
	e.ringing = ringing != 0
	e.ticks, _ = saved[bellTicksTag].(int32)
	e.dir, _ = saved[bellDirTag].(int32)
	for _, k := range [...]string{bellRingingTag, bellTicksTag, bellDirTag} {
		delete(e.saved, k)
	}
	return e
}

// SaveData is what BDS saves of a bell.
func (e *bellEntity) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(e.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = bellEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	m[bellRingingTag] = flag(e.ringing)
	m[bellTicksTag] = e.ticks
	m[bellDirTag] = e.dir
	return m
}

// NetworkData is the same: BDS sends it as it saves it.
func (e *bellEntity) NetworkData(pos cube.Pos) map[string]any { return e.SaveData(pos) }

// TickBlockEntity is BDS's bell tick: the count, then a ring if power just came. Power
// rings it the way its state faces.
func (e *bellEntity) TickBlockEntity(r *world.Region, pos cube.Pos) {
	e.ticks++
	if e.ringing {
		e.ticks++
	}
	if e.ticks >= bellRingTicks {
		e.ringing, e.ticks = false, 0
	}
	if e.t.circuitOf(r).sys.Strength(toPos(pos)) <= 0 {
		e.powered = false
		return
	}
	if e.powered {
		return
	}
	e.powered = true
	if block, ok := r.Block(pos, 0); ok && e.t.kind(block) == kindBell {
		e.ring(r, pos, block, intState(e.t.blocks.States(block), stateDirection))
	}
}

// ring is BDS's ring: the sound and the toggle, which tells the neighbours. Villagers
// and raiders hearing it come with mobs. BDS sends the block entity only when power rang
// it; it goes out every time here, so everyone swings the bell the same way.
func (e *bellEntity) ring(r *world.Region, pos cube.Pos, block uint32, dir int32) {
	e.ticks, e.ringing, e.dir = 0, true, dir
	r.Broadcast(pos, &packet.LevelSoundEvent{
		SoundType: packet.SoundEventBell,
		Position:  mgl32.Vec3{float32(pos[0]), float32(pos[1]), float32(pos[2])},
		ExtraData: -1,
	}, nil)
	toggled := !bitState(e.t.blocks.States(block), stateToggle)
	e.t.setByCircuit(e.t.circuitOf(r), pos, e.t.with(block, stateToggle, flag(toggled)))
	r.SendBlockEntity(pos)
}

// RingBell is a player clicking a bell, false if the click does not ring it. A bell on
// the floor rings from its front and back, one on a side from its sides, a hanging one
// from anywhere. Runs: owner of the region.
func (t *Ticker) RingBell(r *world.Region, pos cube.Pos, block uint32, yaw float32) bool {
	if t.kind(block) != kindBell {
		return false
	}
	look := lookOf(yaw)
	states := t.blocks.States(block)
	lengthwise := look.FacesZ() == direction.Cardinal(intState(states, stateDirection)).FacesZ()
	switch states[stateAttachment] {
	case attachHanging:
	case attachStanding:
		if !lengthwise {
			return false
		}
	case attachSide, attachMultiple:
		if lengthwise {
			return false
		}
	default:
		return false
	}
	t.ensureBell(r, pos).ring(r, pos, block, int32(look.Opposite()))
	return true
}

// lookOf is the way BDS's bell takes a player to look: the larger of the table's sine and
// cosine of the yaw, the cosine on a tie.
func lookOf(yaw float32) direction.Cardinal {
	units := direction.Units(yaw)
	sin, cos := direction.Sin(units), direction.Cos(units)
	if abs32(sin) <= abs32(cos) {
		if cos < 0 {
			return direction.North
		}
		return direction.South
	}
	if sin <= 0 {
		return direction.East
	}
	return direction.West
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// ensureBell returns the block entity of the bell at pos, making one if the world holds
// none.
func (t *Ticker) ensureBell(r *world.Region, pos cube.Pos) *bellEntity {
	if be, ok := r.BlockEntity(pos); ok {
		if e, ok := be.(*bellEntity); ok {
			return e
		}
	}
	e := &bellEntity{t: t}
	r.SetBlockEntity(pos, e)
	return e
}

// bellNeighbour is BDS's bell hearing a neighbour: a bell on a side takes a new holder
// behind it and becomes one between two, one between two that lost a holder goes back on
// the side it has left. With nothing holding it, it falls next tick.
func (t *Ticker) bellNeighbour(r *world.Region, pos, from cube.Pos, block uint32) {
	states := t.blocks.States(block)
	facing := direction.Cardinal(intState(states, stateDirection)).Face()
	for _, f := range direction.Updates() {
		if from.Side(f) != pos {
			continue
		}
		nb, ok := r.Block(from, 0)
		if !ok {
			return
		}
		holds := t.attach.Supports(nb, f)
		switch attachment := states[stateAttachment]; {
		case attachment == attachSide && facing == f.Opposite() && holds:
			block = t.with(block, stateAttachment, attachMultiple)
		case attachment == attachMultiple && (facing == f || facing == f.Opposite()) && !holds:
			if away, ok := direction.Of(f.Opposite()); ok {
				block = t.with(t.with(block, stateAttachment, attachSide), stateDirection, int32(away))
			}
		}
		break
	}
	t.setByCircuit(t.circuitOf(r), pos, block)
	if !t.bellHeld(r, pos, block) {
		r.ScheduleTick(pos, block, 1, 0)
	}
}

// bellHeld is BDS's canSurvive for a bell.
func (t *Ticker) bellHeld(r *world.Region, pos cube.Pos, block uint32) bool {
	states := t.blocks.States(block)
	facing := direction.Cardinal(intState(states, stateDirection)).Face()
	holds := func(at cube.Pos, face cube.Face) bool {
		b, ok := r.Block(at, 0)
		return ok && t.attach.Supports(b, face)
	}
	switch states[stateAttachment] {
	case attachHanging:
		return t.attach.CanHang(r, pos)
	case attachSide:
		return holds(pos.Side(facing.Opposite()), facing)
	case attachMultiple:
		return holds(pos.Side(facing.Opposite()), facing) || holds(pos.Side(facing), facing.Opposite())
	}
	return holds(pos.Side(cube.FaceDown), cube.FaceUp)
}
