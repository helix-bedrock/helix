package circuit

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/redstone"
	"github.com/helix-bedrock/world/cube"
)

// Sculk sensors and shriekers as BDS 1.26.60: SculkSensorBlock (vtable 0xFC1B7B0) and
// CalibratedSculkSensorBlock (0xFBA7390) with their producer (setupRedstoneComponent: all sides,
// attachments, power up; the calibrated one gives none out of its back, where its input comes
// from), activation (0xD515AB0: active phase, strength from the vibration, a tick after the
// block's active ticks, 30 or 10) and the tick listener 0xD5148C0 (active -> cooldown at 0 for 10
// ticks -> inactive with the power off sound). Hearing vibrations is the player package's
// (player/sculk.go); this is only the block and its power. The shrieker's tick is handed to
// SculkHooks.ShriekerTick.
const (
	nameSculkSensor = "minecraft:sculk_sensor"
	nameCalibrated  = "minecraft:calibrated_sculk_sensor"
	nameShrieker    = "minecraft:sculk_shrieker"
	statePhase      = "sculk_sensor_phase"
	// The sensor's phases.
	phaseInactive int32 = 0
	phaseActive   int32 = 1
	phaseCooldown int32 = 2
	// sensorActiveTicks and calibratedActiveTicks are how long each stays active (the
	// block's field +936), sensorCooldownTicks how long the cooldown lasts.
	sensorActiveTicks     = 30
	calibratedActiveTicks = 10
	sensorCooldownTicks   = 10
	// allFaces are the six faces of a directional producer.
	allFaces uint8 = 1<<6 - 1
)

// SculkHooks are what the player package does for sculk blocks. Either may be nil.
type SculkHooks struct {
	// SensorQuiet is a sensor's cooldown ending, before it goes inactive: BDS's power off
	// sound.
	SensorQuiet func(r *world.Region, pos cube.Pos)
	// ShriekerTick is the shrieker's queued tick: it stops shrieking and responds.
	ShriekerTick func(r *world.Region, pos cube.Pos, block uint32)
}

// SetSculk sets what the player package does for sculk blocks. Call it before the world
// ticks.
func (t *Ticker) SetSculk(h SculkHooks) { t.sculk = h }

// sculkComponent makes a sensor's producer. Its strength starts at 0, as BDS's.
func (t *Ticker) sculkComponent(c *regionCircuit, p redstone.Pos, name string, states map[string]any) {
	comp := c.sys.Create(p, redstone.KindProducer, redstone.NoFace)
	if comp == nil {
		return
	}
	comp.SetAttach(true)
	comp.SetAllowUp(true)
	if name == nameCalibrated {
		comp.SetAllowedFaces(allFaces &^ (1 << calibratedBack(states)))
	}
}

// calibratedBack is the side of a calibrated sensor its input comes from and no power goes
// out of: the opposite of where it faces.
func calibratedBack(states map[string]any) redstone.Face {
	return cardinalFace(states).Opposite()
}

// ActivateSculkSensor turns the sensor at pos on with power, as BDS's activate: the active
// phase, the strength, and a tick when its active time ends. False if it is no sensor or is
// already active. Runs: owner of the region.
func (t *Ticker) ActivateSculkSensor(r *world.Region, pos cube.Pos, power int32) bool {
	block, ok := r.Block(pos, 0)
	if !ok || t.kind(block) != kindSculk || intState(t.blocks.States(block), statePhase) == phaseActive {
		return false
	}
	r.CancelTick(pos, block)
	c := t.circuitOf(r)
	on := t.with(block, statePhase, phaseActive)
	t.setByCircuit(c, pos, on)
	if comp := c.sys.Component(toPos(pos), redstone.KindProducer); comp != nil {
		comp.SetStrength(power)
	}
	ticks := int32(sensorActiveTicks)
	if t.blocks.Name(block) == nameCalibrated {
		ticks = calibratedActiveTicks
	}
	r.ScheduleTick(pos, on, ticks, 0)
	return true
}

// SculkInput is the power going into a calibrated sensor at pos from behind, 0 for any other
// block. Runs: owner of the region.
func (t *Ticker) SculkInput(r *world.Region, pos cube.Pos) int32 {
	block, ok := r.Block(pos, 0)
	if !ok || t.blocks.Name(block) != nameCalibrated {
		return 0
	}
	back := calibratedBack(t.blocks.States(block))
	behind := toPos(pos)
	switch back {
	case redstone.North:
		behind.Z--
	case redstone.South:
		behind.Z++
	case redstone.West:
		behind.X--
	case redstone.East:
		behind.X++
	}
	return int32(t.circuitOf(r).sys.Strength(behind))
}

// sculkTick moves a sensor on from active to cooldown, and from cooldown to inactive.
func (t *Ticker) sculkTick(r *world.Region, pos cube.Pos, block uint32) {
	c := t.circuitOf(r)
	switch intState(t.blocks.States(block), statePhase) {
	case phaseActive:
		r.CancelTick(pos, block)
		next := t.with(block, statePhase, phaseCooldown)
		t.setByCircuit(c, pos, next)
		if comp := c.sys.Component(toPos(pos), redstone.KindProducer); comp != nil {
			comp.SetStrength(0)
		}
		r.ScheduleTick(pos, next, sensorCooldownTicks, 0)
	case phaseCooldown:
		if t.sculk.SensorQuiet != nil {
			t.sculk.SensorQuiet(r, pos)
		}
		t.setByCircuit(c, pos, t.with(block, statePhase, phaseInactive))
	}
}

// shriekerTick hands the shrieker's tick to the player package.
func (t *Ticker) shriekerTick(r *world.Region, pos cube.Pos, block uint32) {
	if t.sculk.ShriekerTick != nil {
		t.sculk.ShriekerTick(r, pos, block)
	}
}
