package circuit

import (
	"maps"
	"strings"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/redstone"
	"github.com/helix-bedrock/world/cube"
)

// How wire connects to a block, from BDS's shouldConnectToRedstone.
type join uint8

const (
	joinNone join = iota
	// joinAll connects on every side.
	joinAll
	// joinLine connects along the block's direction, as a repeater.
	joinLine
	// joinFacing connects where the block's facing points the way wire runs: an
	// observer's or a piston's back.
	joinFacing
)

// joinOf is how wire connects to the block called name. Levers, buttons, plates
// and the block of redstone connect through their producer component in BDS.
func joinOf(name string) join {
	switch name {
	case nameWire, nameTorch, nameTorchOff, nameCompare, nameCompareOff, nameBlock, nameLever,
		"minecraft:jukebox", "minecraft:lectern", "minecraft:daylight_detector",
		"minecraft:daylight_detector_inverted", "minecraft:detector_rail", "minecraft:trapped_chest",
		"minecraft:bell", "minecraft:tripwire_hook", "minecraft:target", "minecraft:sculk_sensor",
		"minecraft:calibrated_sculk_sensor":
		return joinAll
	case nameRepeater, nameRepeaterOff:
		return joinLine
	case "minecraft:observer", "minecraft:piston", "minecraft:sticky_piston":
		return joinFacing
	}
	switch {
	case strings.HasSuffix(name, "_button"), strings.HasSuffix(name, "_pressure_plate"),
		strings.HasSuffix(name, "lightning_rod"):
		return joinAll
	}
	return joinNone
}

// signalSource is whether the block called name gives power. A conductor that does
// stops nothing: wire steps past it.
func signalSource(name string) bool {
	switch name {
	case "minecraft:bell", "minecraft:piston", "minecraft:sticky_piston":
		return false
	}
	return joinOf(name) != joinNone
}

// What a comparator reads from a block, from BDS's getComparatorSignal.
type signal uint8

const (
	signalNone signal = iota
	// signalContainer is how full its block entity's items are.
	signalContainer
	signalComposter
	signalCauldron
	signalCake
	signalCandleCake
	signalPortalFrame
	signalHive
	signalAnchor
	signalBulb
	// signalUnbuilt has a signal in BDS from a block entity not run yet. It reads as
	// 0, but still stops a comparator looking past it.
	signalUnbuilt
)

// Sizes of the containers a comparator reads.
const (
	sizeHopper  = 5
	sizeFurnace = 3
	sizeBrewing = 5
	sizeDropper = 9
	sizeChest   = 27
	sizePot     = 1
)

// signalOf is what a comparator reads from the block called name.
func signalOf(name string) signal {
	if containerSize(name) != 0 {
		return signalContainer
	}
	switch name {
	case "minecraft:composter":
		return signalComposter
	case "minecraft:cauldron":
		return signalCauldron
	case "minecraft:cake":
		return signalCake
	case "minecraft:end_portal_frame":
		return signalPortalFrame
	case "minecraft:beehive", "minecraft:bee_nest":
		return signalHive
	case "minecraft:respawn_anchor":
		return signalAnchor
	case "minecraft:jukebox", "minecraft:lectern", "minecraft:frame", "minecraft:glow_frame",
		"minecraft:detector_rail", "minecraft:command_block", "minecraft:chain_command_block",
		"minecraft:repeating_command_block", "minecraft:crafter", "minecraft:creaking_heart",
		"minecraft:chiseled_bookshelf", "minecraft:sculk_sensor", "minecraft:calibrated_sculk_sensor":
		return signalUnbuilt
	}
	switch {
	case strings.HasSuffix(name, "candle_cake"):
		return signalCandleCake
	case strings.HasSuffix(name, "copper_bulb"):
		return signalBulb
	case strings.HasSuffix(name, "_shelf"), strings.HasSuffix(name, "copper_golem_statue"):
		return signalUnbuilt
	}
	return signalNone
}

// containerSize is how many slots the block called name has, 0 if it is no
// container a comparator reads. BDS divides by this whatever the slots hold.
func containerSize(name string) int {
	switch name {
	case "minecraft:hopper":
		return sizeHopper
	case "minecraft:furnace", "minecraft:lit_furnace", "minecraft:blast_furnace",
		"minecraft:lit_blast_furnace", "minecraft:smoker", "minecraft:lit_smoker":
		return sizeFurnace
	case "minecraft:brewing_stand":
		return sizeBrewing
	case "minecraft:dispenser", "minecraft:dropper":
		return sizeDropper
	case "minecraft:chest", "minecraft:trapped_chest", "minecraft:ender_chest", "minecraft:barrel":
		return sizeChest
	case "minecraft:decorated_pot":
		return sizePot
	}
	if strings.HasSuffix(name, "shulker_box") || strings.HasSuffix(name, "copper_chest") {
		return sizeChest
	}
	return 0
}

// ContainerSum adds up how full a container is, as BDS does, in 32 bit floats so
// its rounding is kept.
type ContainerSum struct {
	sum float32
	any bool
}

// unknownStack is the stack size BDS divides by for a stack with no item.
const unknownStack = 255

// signalSteps is how many strengths a part-full container spreads over.
const signalSteps = 14

// Add counts a stack of count items that fit maxStack to a slot.
func (s *ContainerSum) Add(count, maxStack int) {
	if count <= 0 {
		return
	}
	if maxStack <= 0 {
		maxStack = unknownStack
	}
	s.sum += float32(count) / float32(maxStack)
	s.any = true
}

// Signal is the strength for a container of size slots.
func (s *ContainerSum) Signal(size int) int32 {
	if size <= 0 || !s.any {
		return 0
	}
	// Whole part of a positive number, plus one for holding anything.
	return int32(s.sum/float32(size)*signalSteps) + 1
}

// SignalEntity is a block entity that works out its own comparator signal, as a
// chest does from its items and its other half.
type SignalEntity interface {
	ComparatorSignal(r *world.Region, pos cube.Pos) int32
}

// readSignal is what a comparator at pos with direction dir reads: the block
// behind it, or the one past that if the first is solid and no signal source. No
// signal at all is reported as false, which is not the same as 0.
func (t *Ticker) readSignal(r *world.Region, pos cube.Pos, dir redstone.Face) (int32, bool) {
	at := fromPos(toPos(pos).Side(dir))
	b, ok := r.Block(at, 0)
	if !ok {
		return 0, false
	}
	if t.hasSignal(b) {
		return t.blockSignal(r, at, b), true
	}
	if !t.attach.Solid(b) || signalSource(t.blocks.Name(b)) {
		return 0, false
	}
	at = fromPos(toPos(at).Side(dir))
	b, ok = r.Block(at, 0)
	if !ok || !t.hasSignal(b) {
		return 0, false
	}
	return t.blockSignal(r, at, b), true
}

// hasSignal is whether a comparator reads a state.
func (t *Ticker) hasSignal(block uint32) bool {
	return int(block) < len(t.signals) && t.signals[block] != signalNone
}

// anchorCharges is a respawn anchor's highest charge.
const anchorCharges = 4

// Comparator strengths of simple blocks.
const (
	candleCakeSignal = 14
	cakeFull         = 14
)

// blockSignal is the signal of the block at pos.
func (t *Ticker) blockSignal(r *world.Region, pos cube.Pos, block uint32) int32 {
	states := t.blocks.States(block)
	switch t.signals[block] {
	case signalContainer:
		return t.containerSignal(r, pos, block)
	case signalComposter:
		return intState(states, "composter_fill_level")
	case signalCauldron:
		return (intState(states, "fill_level") + 1) / 2
	case signalCake:
		return cakeFull - 2*intState(states, "bite_counter")
	case signalCandleCake:
		return candleCakeSignal
	case signalPortalFrame:
		return onOff(bitState(states, "end_portal_eye_bit"))
	case signalHive:
		return intState(states, "honey_level")
	case signalAnchor:
		return int32(float32(intState(states, "respawn_anchor_charge")) / anchorCharges * fullPower)
	case signalBulb:
		return onOff(bitState(states, "lit"))
	}
	return 0
}

// containerSignal asks a block entity that runs, or adds up the items of one kept
// as it was saved.
func (t *Ticker) containerSignal(r *world.Region, pos cube.Pos, block uint32) int32 {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return 0
	}
	if s, ok := be.(SignalEntity); ok {
		return s.ComparatorSignal(r, pos)
	}
	list, _ := be.SaveData(pos)["Items"].([]any)
	var sum ContainerSum
	for _, v := range list {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}
		name, _ := item["Name"].(string)
		count, _ := item["Count"].(uint8)
		maxStack := 0
		if it, ok := t.items.ByName(name); ok {
			maxStack = int(it.MaxStack)
		}
		sum.Add(int(count), maxStack)
	}
	return sum.Signal(containerSize(t.blocks.Name(block)))
}

// updateComparator is the block's onRedstoneUpdate: it only swaps form when output
// starts or stops, and keeps what it gives out for saving.
func (t *Ticker) updateComparator(c *regionCircuit, pos cube.Pos, block uint32, strength, previous int16) {
	if strength == previous && bitState(t.blocks.States(block), stateSubtract) {
		return
	}
	switch {
	case previous <= 0 && strength > 0:
		t.setByCircuit(c, pos, t.with(t.named(nameCompare, block), stateLit, flag(true)))
	case previous > 0 && strength == 0:
		t.setByCircuit(c, pos, t.with(t.named(nameCompareOff, block), stateLit, flag(false)))
	}
	if e, ok := t.comparatorAt(c.r, pos); ok {
		e.signal = min(int32(strength), fullPower)
		c.r.SaveBlockEntity(pos)
	}
}

// comparatorAt returns the comparator's block entity at pos.
func (t *Ticker) comparatorAt(r *world.Region, pos cube.Pos) (*comparatorEntity, bool) {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return nil, false
	}
	e, ok := be.(*comparatorEntity)
	return e, ok
}

// ensureComparator returns the block entity of the comparator at pos, making one
// if the world holds none: it is what reads the block behind.
func (t *Ticker) ensureComparator(r *world.Region, pos cube.Pos) *comparatorEntity {
	if e, ok := t.comparatorAt(r, pos); ok {
		return e
	}
	e := &comparatorEntity{t: t}
	r.SetBlockEntity(pos, e)
	return e
}

// BDS's names for a comparator's block entity.
const (
	comparatorEntityID = "Comparator"
	outputSignalTag    = "OutputSignal"
)

// comparatorEntity is a comparator's block entity: it keeps the output and feeds
// the component what is behind it.
type comparatorEntity struct {
	t      *Ticker
	saved  map[string]any
	signal int32
}

// SaveData is what BDS saves of a comparator.
func (e *comparatorEntity) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(e.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = comparatorEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	m[outputSignalTag] = e.signal
	return m
}

// NetworkData is the same: BDS sends it as it saves it.
func (e *comparatorEntity) NetworkData(pos cube.Pos) map[string]any { return e.SaveData(pos) }

// TickBlockEntity reads what is behind the comparator on the ticks BDS does. Runs:
// owner of the region.
func (e *comparatorEntity) TickBlockEntity(r *world.Region, pos cube.Pos) {
	c := e.t.circuitOf(r)
	if !c.sys.SignalTick() {
		return
	}
	block, ok := r.Block(pos, 0)
	if !ok || e.t.kind(block) != kindComparator {
		return
	}
	comp := c.sys.Component(toPos(pos), redstone.KindComparator)
	if comp == nil {
		return
	}
	if v, ok := e.t.readSignal(r, pos, comp.Direction()); ok {
		comp.SetRearSignal(max(v, 0))
	} else {
		comp.SetRearSignal(-1)
	}
}

// comparatorEntities adds comparators to the block entities another list makes.
type comparatorEntities struct {
	t    *Ticker
	next world.BlockEntities
}

// Entities returns next with comparators' block entities added. next may be nil.
func (t *Ticker) Entities(next world.BlockEntities) world.BlockEntities {
	return comparatorEntities{t: t, next: next}
}

// New returns a comparator's block entity, or asks next.
func (e comparatorEntities) New(block uint32) world.BlockEntity {
	if e.t.kind(block) == kindComparator {
		return &comparatorEntity{t: e.t}
	}
	if e.next != nil {
		return e.next.New(block)
	}
	return nil
}

// Load brings back a comparator's block entity, or asks next.
func (e comparatorEntities) Load(pos cube.Pos, saved map[string]any) (world.BlockEntity, bool) {
	if id, _ := saved["id"].(string); id == comparatorEntityID {
		v, _ := saved[outputSignalTag].(int32)
		kept := maps.Clone(saved)
		delete(kept, outputSignalTag)
		return &comparatorEntity{t: e.t, saved: kept, signal: v}, true
	}
	if e.next != nil {
		return e.next.Load(pos, saved)
	}
	return nil, false
}
