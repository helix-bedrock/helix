package circuit

import (
	"fmt"
	"maps"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/redstone"
	"github.com/helix-bedrock/world/cube"
)

// Blocks that sense something rather than take power.
const (
	nameObserver         = "minecraft:observer"
	nameNote             = "minecraft:noteblock"
	nameTarget           = "minecraft:target"
	nameTrapped          = "minecraft:trapped_chest"
	nameDaylight         = "minecraft:daylight_detector"
	nameDaylightInverted = "minecraft:daylight_detector_inverted"
	nameLightPlate       = "minecraft:light_weighted_pressure_plate"
	nameHeavyPlate       = "minecraft:heavy_weighted_pressure_plate"
	nameStonePlate       = "minecraft:stone_pressure_plate"
	nameBlackstonePlate  = "minecraft:polished_blackstone_pressure_plate"
)

// statePowered is an observer's state.
const statePowered = "powered_bit"

// Ticks of an observer: how long before it changes, and how long to try again on a tick
// the circuits do not evaluate next.
const (
	observerDelay = 2
	observerRetry = 1
)

// EntityBoxFunc returns what plates see of an entity: its box, whether it is a mob, and
// false if plates do not see it at all. Runs: owner of the entity's region.
type EntityBoxFunc func(e *world.Entity) (lo, hi mgl32.Vec3, mob, ok bool)

// SetEntityBox sets how plates see entities. Call it before any player joins. Without
// it plates stay up.
func (t *Ticker) SetEntityBox(fn EntityBoxFunc) { t.entityBox = fn }

// observerFace is the way an observer looks.
func observerFace(states map[string]any) redstone.Face {
	v, _ := states[stateFacingNamed].(string)
	switch v {
	case "up":
		return redstone.Up
	case "north":
		return redstone.North
	case "south":
		return redstone.South
	case "west":
		return redstone.West
	case "east":
		return redstone.East
	}
	return redstone.Down
}

// observerHeard has an observer react when the block it looks at changed.
func (t *Ticker) observerHeard(r *world.Region, pos, from cube.Pos, block uint32) {
	states := t.blocks.States(block)
	if from != fromPos(toPos(pos).Side(observerFace(states))) || bitState(states, statePowered) {
		return
	}
	if t.circuitOf(r).sys.Component(toPos(pos), redstone.KindPulse) == nil || r.HasScheduledTick(pos, block) {
		return
	}
	r.ScheduleTick(pos, block, observerDelay, 0)
}

// observerTick turns an observer on or off, on a tick the circuits evaluate next: on
// any other it tries again a tick later.
func (t *Ticker) observerTick(c *regionCircuit, pos cube.Pos, block uint32) {
	if c.sys.SignalTick() {
		c.r.ScheduleTick(pos, block, observerRetry, 0)
		return
	}
	t.turnObserver(c, pos, block, !bitState(t.blocks.States(block), statePowered))
}

// turnObserver sets an observer's state. Turning on gives one pulse and asks for the
// tick that turns it off.
func (t *Ticker) turnObserver(c *regionCircuit, pos cube.Pos, block uint32, on bool) {
	comp := c.sys.Component(toPos(pos), redstone.KindPulse)
	if comp == nil {
		return
	}
	next := t.with(block, statePowered, flag(on))
	t.setByCircuit(c, pos, next)
	if !on {
		return
	}
	comp.SetStrength(fullPower)
	if !c.r.HasScheduledTick(pos, next) {
		delay := int32(observerDelay)
		if c.sys.SignalTick() {
			delay++
		}
		c.r.ScheduleTick(pos, next, delay, 0)
	}
}

// The part of a block a pressure plate senses.
const (
	plateInset  = 0.0625
	plateHeight = 0.25
)

// Ticks a plate waits before looking again.
const (
	plateDelay         = 20
	plateDelayWeighted = 10
)

// Entities that hold a weighted plate at full power.
const (
	lightPlateWeight = 15
	heavyPlateWeight = 150
)

// plateSoundHeight is how far up the block a plate clicks.
const plateSoundHeight = 0.1

// plateReach is how far from a plate's block to look for entities touching it.
const plateReach = 1

// plateRule is how a plate reads what stands on it.
type plateRule struct {
	// weight is the entities at full power of a weighted plate, 0 for one that is
	// simply on.
	weight int32
	// mobs says it ignores anything but mobs.
	mobs bool
}

// plateRuleOf is the rule of the plate called name.
func plateRuleOf(name string) plateRule {
	switch name {
	case nameLightPlate:
		return plateRule{weight: lightPlateWeight}
	case nameHeavyPlate:
		return plateRule{weight: heavyPlateWeight}
	case nameStonePlate, nameBlackstonePlate:
		return plateRule{mobs: true}
	}
	return plateRule{}
}

// signalOf is the power a plate gives with this value in its state.
func (p plateRule) signalOf(value int32) int32 {
	if p.weight == 0 {
		return onOff(value == 1)
	}
	return value
}

// valueOf is the state of a plate giving this power.
func (p plateRule) valueOf(power int32) int32 {
	if p.weight == 0 && power > 0 {
		return 1
	}
	return power
}

// delay is how long a plate waits before looking again.
func (p plateRule) delay() int32 {
	if p.weight == 0 {
		return plateDelay
	}
	return plateDelayWeighted
}

// plateState is the power the plate in this state gives.
func (t *Ticker) plateState(block uint32) int32 {
	return plateRuleOf(t.blocks.Name(block)).signalOf(intState(t.blocks.States(block), stateSignal))
}

// plateSignal is the power a plate should give for what stands on it now.
func (t *Ticker) plateSignal(c *regionCircuit, pos cube.Pos, rule plateRule) int32 {
	if t.entityBox == nil {
		return 0
	}
	x, y, z := float32(pos[0]), float32(pos[1]), float32(pos[2])
	c.found = c.r.AppendEntitiesIn(c.found[:0], x-plateReach, z-plateReach, x+1+plateReach, z+1+plateReach)
	var count int32
	for _, e := range c.found {
		lo, hi, mob, ok := t.entityBox(e)
		if !ok || (rule.mobs && !mob) {
			continue
		}
		if hi[0] > x+plateInset && lo[0] < x+1-plateInset && hi[1] > y && lo[1] < y+plateHeight &&
			hi[2] > z+plateInset && lo[2] < z+1-plateInset {
			count++
		}
	}
	if rule.weight == 0 {
		return onOff(count > 0)
	}
	count = min(count, rule.weight)
	return int32(math.Ceil(float64(count) / float64(rule.weight) * fullPower))
}

// updatePlate sets a plate's state, clicks if it went on or off, and while it gives
// power has it look again later.
func (t *Ticker) updatePlate(c *regionCircuit, pos cube.Pos, block uint32, rule plateRule, old, now int32) {
	next := t.with(block, stateSignal, rule.valueOf(now))
	if next != block && c.r.SetBlock(pos, 0, next) {
		c.r.UpdateNeighbours(pos)
	}
	if (old > 0) != (now > 0) {
		event := packet.SoundEventPressurePlateClickOff
		if now > 0 {
			event = packet.SoundEventPressurePlateClickOn
		}
		c.r.Broadcast(pos, &packet.LevelSoundEvent{
			SoundType: event,
			Position:  mgl32.Vec3{float32(pos[0]) + 0.5, float32(pos[1]) + plateSoundHeight, float32(pos[2]) + 0.5},
			ExtraData: int32(t.blocks.Hash(next)), //nolint:gosec // hashes are sent as int32
		}, nil)
	}
	if now > 0 {
		c.r.ScheduleTick(pos, next, rule.delay(), 0)
	}
}

// plateTick looks again at what stands on a plate, and breaks one with nothing under
// it.
func (t *Ticker) plateTick(c *regionCircuit, pos cube.Pos, block uint32) {
	if !t.attach.DiodeSurvives(c.r, pos) {
		t.fall(c.r, pos, block)
		return
	}
	rule := plateRuleOf(t.blocks.Name(block))
	t.updatePlate(c, pos, block, rule, t.plateState(block), t.plateSignal(c, pos, rule))
}

// stepOn is what an entity touching a plate does. A plate that was up goes down at
// once; a weighted one that is down changes its strength, and comes up only on its
// tick.
func (t *Ticker) stepOn(c *regionCircuit, pos cube.Pos, block uint32) {
	rule := plateRuleOf(t.blocks.Name(block))
	old := t.plateState(block)
	now := t.plateSignal(c, pos, rule)
	switch {
	case now <= 0 || now == old:
	case old == 0:
		c.r.CancelTick(pos, block)
		t.updatePlate(c, pos, block, rule, old, now)
	default:
		if c.r.SetBlock(pos, 0, t.with(block, stateSignal, rule.valueOf(now))) {
			c.r.UpdateNeighbours(pos)
		}
	}
}

// entitiesOnPlates finds the plates and tripwires entities touch. Runs: owner of the region.
func (t *Ticker) entitiesOnPlates(c *regionCircuit) {
	if t.entityBox == nil {
		return
	}
	c.moving = c.r.AppendEntities(c.moving[:0])
	for _, e := range c.moving {
		lo, hi, _, ok := t.entityBox(e)
		if !ok {
			continue
		}
		for x := floor(lo[0]); x <= floor(hi[0]); x++ {
			for z := floor(lo[2]); z <= floor(hi[2]); z++ {
				for y := floor(lo[1]); y <= floor(hi[1]); y++ {
					pos := cube.Pos{x, y, z}
					b, ok := c.r.Block(pos, 0)
					if !ok {
						continue
					}
					switch t.kind(b) {
					case kindPlate:
						t.stepOn(c, pos, b)
					case kindTripWire:
						if !bitState(t.blocks.States(b), statePowered) {
							t.wireUpdate(c, pos, b)
						}
					}
				}
			}
		}
	}
}

// floor is the block coordinate of v.
func floor(v float32) int { return int(math.Floor(float64(v))) }

// SetChestViewers gives a trapped chest the power of its n viewers. Runs: owner of the
// region.
func (t *Ticker) SetChestViewers(r *world.Region, pos cube.Pos, n int32) {
	if comp := t.circuitOf(r).sys.Component(toPos(pos), redstone.KindProducer); comp != nil {
		comp.SetStrength(min(n, fullPower))
	}
}

// BDS's names in a note block's block entity.
const (
	noteEntityID = "Music"
	noteTag      = "note"
)

// BDS's numbers for a note block.
const (
	noteCount = 25
	// noteMaxPitched is the last instrument that plays the note set; the heads above it
	// play noteFixed and show no particle.
	noteMaxPitched = 19
	noteFixed      = 9
)

// What a note block shows, and how far up the block.
const (
	noteParticle       = "minecraft:note_particle"
	noteParticleHeight = 1.2
)

// instrumentNone says a block lets no note block play under it.
const instrumentNone = 0xff

// loadInstruments fills what each state plays above and below a note block.
func (t *Ticker) loadInstruments(n int) error {
	table, err := data.NoteInstrumentData()
	if err != nil {
		return fmt.Errorf("note instruments: %w", err)
	}
	t.noteAbove, t.noteBelow = make([]uint8, n), make([]uint8, n)
	for id := range uint32(n) { //nolint:gosec // states fit
		name := t.blocks.Name(id)
		t.noteBelow[id] = table.Below[name]
		t.noteAbove[id] = instrumentNone
		if v, ok := table.Above[name]; ok {
			t.noteAbove[id] = v
		}
	}
	return nil
}

// instrument is what the note block at pos plays: what is above it, or with air there
// what is below it. False if it plays nothing.
func (t *Ticker) instrument(r *world.Region, pos cube.Pos) (uint8, bool) {
	above, ok := r.Block(cube.Pos{pos[0], pos[1] + 1, pos[2]}, 0)
	if !ok {
		return 0, false
	}
	if above != t.blocks.Air() {
		if int(above) >= len(t.noteAbove) || t.noteAbove[above] == instrumentNone {
			return 0, false
		}
		return t.noteAbove[above], true
	}
	below, ok := r.Block(cube.Pos{pos[0], pos[1] - 1, pos[2]}, 0)
	if !ok || int(below) >= len(t.noteBelow) {
		return 0, ok
	}
	return t.noteBelow[below], true
}

// noteEntity is a note block's block entity: the note it is set to, and whether it
// has power, so it only plays as power comes.
type noteEntity struct {
	saved   map[string]any
	note    uint8
	powered bool
}

// SaveData is what BDS saves of a note block.
func (e *noteEntity) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(e.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = noteEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	m[noteTag] = e.note
	return m
}

// NetworkData is the same: BDS sends it as it saves it.
func (e *noteEntity) NetworkData(pos cube.Pos) map[string]any { return e.SaveData(pos) }

// loadNote brings back a note block's block entity.
func loadNote(saved map[string]any) *noteEntity {
	note, _ := saved[noteTag].(uint8)
	kept := maps.Clone(saved)
	delete(kept, noteTag)
	return &noteEntity{saved: kept, note: note}
}

// ensureNote returns the block entity of the note block at pos, making one if the
// world holds none.
func (t *Ticker) ensureNote(r *world.Region, pos cube.Pos) *noteEntity {
	if be, ok := r.BlockEntity(pos); ok {
		if e, ok := be.(*noteEntity); ok {
			return e
		}
	}
	e := &noteEntity{}
	r.SetBlockEntity(pos, e)
	return e
}

// playNote sends what BDS sends for a note: the block event, the sound and the
// particle. It sends no particle colour, whose format was not read.
func (t *Ticker) playNote(r *world.Region, pos cube.Pos, note uint8) {
	instrument, ok := t.instrument(r, pos)
	if !ok {
		return
	}
	r.Broadcast(pos, &packet.BlockEvent{
		Position:  protocol.BlockPos{int32(pos[0]), int32(pos[1]), int32(pos[2])}, //nolint:gosec // block coordinates fit int32
		EventType: int32(instrument), EventData: int32(note),
	}, nil)
	pitch := int32(note)
	if instrument > noteMaxPitched {
		pitch = noteFixed
	}
	r.Broadcast(pos, &packet.LevelSoundEvent{
		SoundType: packet.SoundEventNote,
		Position:  mgl32.Vec3{float32(pos[0]), float32(pos[1]), float32(pos[2])},
		ExtraData: int32(instrument)<<8 | pitch,
	}, nil)
	if instrument <= noteMaxPitched {
		r.Broadcast(pos, &packet.SpawnParticleEffect{
			EntityUniqueID: -1,
			Position:       mgl32.Vec3{float32(pos[0]) + 0.5, float32(pos[1]) + noteParticleHeight, float32(pos[2]) + 0.5},
			ParticleName:   noteParticle,
		}, nil)
	}
}

// notePower plays a note block as power comes to it.
func (t *Ticker) notePower(r *world.Region, pos cube.Pos, on bool) {
	e := t.ensureNote(r, pos)
	if e.powered == on {
		return
	}
	e.powered = on
	if on {
		t.playNote(r, pos, e.note)
	}
}

// UseBlock is a player using a block the circuits answer for: a note block goes up a
// note and plays, a daylight sensor swaps with its inverted form. False if it is
// neither. Runs: owner of the region.
func (t *Ticker) UseBlock(r *world.Region, pos cube.Pos, block uint32) bool {
	switch t.kind(block) {
	case kindNote:
		e := t.ensureNote(r, pos)
		e.note = (e.note + 1) % noteCount
		r.SaveBlockEntity(pos)
		t.playNote(r, pos, e.note)
		return true
	case kindDaylight:
		next := choose(t.blocks.Name(block) == nameDaylight, nameDaylightInverted, nameDaylight)
		if r.SetBlock(pos, 0, t.named(next, block)) {
			r.UpdateNeighbours(pos)
		}
		return true
	}
	return false
}

// AttackBlock is a player starting to break a block: a note block plays. Runs: owner
// of the region.
func (t *Ticker) AttackBlock(r *world.Region, pos cube.Pos, block uint32) {
	if t.kind(block) == kindNote {
		t.playNote(r, pos, t.ensureNote(r, pos).note)
	}
}
