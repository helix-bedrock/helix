// Package circuit connects vanilla's redstone blocks to the redstone library, as
// BDS 1.26.60's blocks do: which component each block makes, and what it does when
// its power changes. Each region has a System of its own; regions that merge or
// split move their components with their chunks, so nothing a player sees changes.
package circuit

import (
	"fmt"
	"maps"
	"strings"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/attach"
	"github.com/helix-bedrock/helix/blockuse"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/redstone"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// States used here.
const (
	stateSignal   = "redstone_signal"
	stateTorch    = "torch_facing_direction"
	stateCardinal = "minecraft:cardinal_direction"
	stateDelay    = "repeater_delay"
	stateSubtract = "output_subtract_bit"
	stateLit      = "output_lit_bit"
	stateOpen     = "open_bit"
	statePressed  = "button_pressed_bit"
	stateFacing   = "facing_direction"
	stateLever    = "lever_direction"
	// stateFacingNamed is the facing state of observers.
	stateFacingNamed = "minecraft:facing_direction"
)

// Blocks that come in a lit and an unlit form.
const (
	nameWire        = "minecraft:redstone_wire"
	nameBlock       = "minecraft:redstone_block"
	nameTorch       = "minecraft:redstone_torch"
	nameTorchOff    = "minecraft:unlit_redstone_torch"
	nameLamp        = "minecraft:redstone_lamp"
	nameLampOn      = "minecraft:lit_redstone_lamp"
	nameRepeater    = "minecraft:powered_repeater"
	nameRepeaterOff = "minecraft:unpowered_repeater"
	nameCompare     = "minecraft:powered_comparator"
	nameCompareOff  = "minecraft:unpowered_comparator"
	nameLever       = "minecraft:lever"
)

// lampOffDelay is how many ticks a lamp stays lit after losing power.
const lampOffDelay = 4

// fullPower is the strength of something simply on.
const fullPower = 15

// kind is the vanilla block class a block belongs to.
type kind uint8

const (
	kindNone kind = iota
	kindWire
	kindTorch
	kindBlock
	kindLamp
	kindRepeater
	kindComparator
	kindLever
	kindButton
	kindDoor
	kindTrapdoor
	kindGate
)

// kindOf returns the class of the block called name.
func kindOf(name string) kind {
	switch name {
	case nameWire:
		return kindWire
	case nameTorch, nameTorchOff:
		return kindTorch
	case nameBlock:
		return kindBlock
	case nameLamp, nameLampOn:
		return kindLamp
	case nameRepeater, nameRepeaterOff:
		return kindRepeater
	case nameCompare, nameCompareOff:
		return kindComparator
	case nameLever:
		return kindLever
	}
	switch {
	case strings.HasSuffix(name, "_button"):
		return kindButton
	case strings.HasSuffix(name, "_door"):
		return kindDoor
	case strings.HasSuffix(name, "trapdoor"):
		return kindTrapdoor
	case strings.HasSuffix(name, "fence_gate"):
		return kindGate
	}
	return kindNone
}

// componentKind is the component a block class makes.
func componentKind(k kind) redstone.Kind {
	switch k {
	case kindWire:
		return redstone.KindWire
	case kindTorch:
		return redstone.KindTorch
	case kindRepeater:
		return redstone.KindRepeater
	case kindComparator:
		return redstone.KindComparator
	case kindBlock, kindLever, kindButton:
		return redstone.KindProducer
	}
	return redstone.KindConsumer
}

// FallFunc removes a block that lost what held it and throws out its drops.
type FallFunc func(r *world.Region, pos cube.Pos, block uint32)

// Ticker gives a world its circuits and hands every other block matter to the
// BlockTicker it embeds.
type Ticker struct {
	world.BlockTicker
	blocks *registry.Blocks
	items  *registry.Items
	attach *attach.Table
	fall   FallFunc
	// These are by block state. They are made once and only read, as regions tick
	// in parallel.
	power []redstone.Power
	kinds []kind
	// joins is how wire connects to a state, stops whether a state is a conductor
	// that is no signal source, which keeps wire from stepping past it.
	joins []join
	stops []bool
	// signals is what a comparator reads from a state.
	signals []signal
}

// localKey finds a region's circuit state on it.
type localKey struct{}

// regionCircuit is one region's System and what it asks of the region.
type regionCircuit struct {
	t      *Ticker
	r      *world.Region
	sys    *redstone.System
	seeded bool
	// recheck asks for paths stopped at chunks a merge brought in to be tried again.
	recheck bool
}

// NewTicker returns a Ticker with BDS's conductivity.
func NewTicker(reg *registry.Registry, attached *attach.Table, fall FallFunc, next world.BlockTicker) (*Ticker, error) {
	blocks := reg.Blocks
	table, err := data.RedstoneConductivity()
	if err != nil {
		return nil, fmt.Errorf("circuit: %w", err)
	}
	t := &Ticker{BlockTicker: next, blocks: blocks, items: reg.Items, attach: attached, fall: fall}
	n := blocks.Count()
	t.power, t.kinds, t.stops = make([]redstone.Power, n), make([]kind, n), make([]bool, n)
	t.joins, t.signals = make([]join, n), make([]signal, n)
	for id := range uint32(n) { //nolint:gosec // states fit
		name := blocks.Name(id)
		t.kinds[id] = kindOf(name)
		t.joins[id] = joinOf(name)
		t.signals[id] = signalOf(name)
		answer := table[name].Power
		if c := table[name]; c.State != "" {
			answer = c.Values[fmt.Sprint(blocks.States(id)[c.State])]
		}
		switch answer {
		case "conductor":
			t.power[id], t.stops[id] = redstone.PowerConductor, !signalSource(name)
		case "insulator":
			t.power[id] = redstone.PowerInsulator
		}
	}
	return t, nil
}

// kind returns the class of a state.
func (t *Ticker) kind(block uint32) kind {
	if int(block) < len(t.kinds) {
		return t.kinds[block]
	}
	return kindNone
}

// circuitOf returns r's circuit state. Runs: owner of the region.
func (t *Ticker) circuitOf(r *world.Region) *regionCircuit {
	v := r.Local(localKey{}, func() any {
		return &regionCircuit{t: t, r: r, sys: redstone.New()}
	})
	c, _ := v.(*regionCircuit)
	return c
}

// BeforeBlockTicks seeds a new System, applies queued changes and, every
// redstone tick, evaluates. Runs: owner of the region.
func (t *Ticker) BeforeBlockTicks(r *world.Region) {
	c := t.circuitOf(r)
	if !c.seeded {
		c.seeded = true
		r.EachChunk(func(pos mcdb.ChunkPos) { t.seedChunk(c, pos) })
	}
	if c.recheck {
		c.recheck = false
		c.sys.RecheckWaiting(c)
	}
	c.sys.UpdateDependencies(c)
	if c.sys.Tick() {
		c.sys.Evaluate(c)
	}
}

// RegionsMerged moves src's circuits into dst. A region that never ticked has
// nothing to move: its chunks are seeded with dst's next tick.
func (t *Ticker) RegionsMerged(dst, src *world.Region) {
	d, s := t.circuitOf(dst), t.circuitOf(src)
	d.sys.Merge(s.sys)
	d.seeded = d.seeded && s.seeded
	d.recheck = true
}

// RegionSplit moves the circuits of the chunks that left r to n.
func (t *Ticker) RegionSplit(r, n *world.Region, in func(pos mcdb.ChunkPos) bool) {
	from, to := t.circuitOf(r), t.circuitOf(n)
	from.sys.SplitOff(to.sys, func(origin redstone.Pos) bool {
		return in(mcdb.ChunkPos{origin.X >> 4, origin.Z >> 4})
	})
	to.seeded = from.seeded
}

// ChunkTicked tells the blocks of a chunk what changed. Runs: owner of the region.
func (t *Ticker) ChunkTicked(r *world.Region, pos mcdb.ChunkPos) {
	c := t.circuitOf(r)
	c.sys.UpdateChunk(redstone.Pos{X: pos[0] << 4, Z: pos[1] << 4}, c)
}

// ChunkArrived makes the components of a chunk that just loaded. Runs: owner of
// the region.
func (t *Ticker) ChunkArrived(r *world.Region, pos mcdb.ChunkPos) {
	c := t.circuitOf(r)
	if !c.seeded {
		return
	}
	t.seedChunk(c, pos)
	c.sys.ChunkLoaded(redstone.Pos{X: pos[0] << 4, Z: pos[1] << 4})
}

// isRedstone reports whether a state is a block with a component.
func (t *Ticker) isRedstone(block uint32) bool { return t.kind(block) != kindNone }

// seedChunk makes a component for each redstone block in a chunk.
func (t *Ticker) seedChunk(c *regionCircuit, pos mcdb.ChunkPos) {
	c.r.FindBlocks(pos, t.isRedstone, func(p cube.Pos, block uint32) {
		t.setup(c, p, block, true)
	})
}

// Placed keeps the circuit in step with a block change, unless the circuit made
// it. A new block replaces the component of the old one and changes the paths
// through its position.
func (t *Ticker) Placed(r *world.Region, pos cube.Pos, block uint32, layer uint8) {
	t.BlockTicker.Placed(r, pos, block, layer)
	if layer != 0 {
		return
	}
	c := t.circuitOf(r)
	if c.sys.Locked() {
		return
	}
	p := toPos(pos)
	k := t.kind(block)
	old := c.sys.At(p)
	wasWire := old != nil && old.Kind() == redstone.KindWire
	if old != nil && (k == kindNone || old.Kind() != componentKind(k)) {
		c.sys.Remove(p)
	}
	if k != kindNone {
		t.setup(c, pos, block, false)
	}
	c.sys.Invalidate(p)
	switch {
	case k == kindWire:
		t.refreshWire(c, pos)
		t.tickWireAround(c, pos, false)
	case wasWire:
		t.tickWireAround(c, pos, true)
	}
}

// NeighbourChanged lets a burnt out torch try again, has wire look at its
// connections and whatever lost its support fall, each a tick later as BDS does.
func (t *Ticker) NeighbourChanged(r *world.Region, pos, from cube.Pos, block uint32, layer uint8) {
	if layer == 0 {
		switch t.kind(block) {
		case kindTorch:
			if comp := t.circuitOf(r).sys.Component(toPos(pos), redstone.KindTorch); comp != nil {
				comp.ResetBurnOut()
			}
		case kindWire:
			if !t.attach.WireSurvives(r, pos) || t.wireState(r, pos, block) != block {
				r.ScheduleTick(pos, block, 1, 0)
			}
		case kindRepeater, kindComparator:
			if !t.attach.DiodeSurvives(r, pos) {
				r.ScheduleTick(pos, block, 1, 0)
			}
		}
	}
	t.BlockTicker.NeighbourChanged(r, pos, from, block, layer)
}

// tickWireAround has the wire on the levels above and below pos look at its
// connections next tick: it is not next to pos, so it hears nothing else. When
// wire went away every one does; when wire came only those that would change.
func (t *Ticker) tickWireAround(c *regionCircuit, pos cube.Pos, removed bool) {
	for _, f := range wireSides {
		n := fromPos(toPos(pos).Side(f.face))
		for _, dy := range [...]int{1, -1} {
			at := cube.Pos{n[0], n[1] + dy, n[2]}
			b, ok := c.r.Block(at, 0)
			if ok && t.kind(b) == kindWire && (removed || t.wireState(c.r, at, b) != b) {
				c.r.ScheduleTick(at, b, 1, 0)
			}
		}
	}
}

// wireSides are the four sides of wire with their states, in the order BDS reads
// them.
var wireSides = [...]struct {
	state string
	face  redstone.Face
}{
	{"redstone_north", redstone.North},
	{"redstone_east", redstone.East},
	{"redstone_south", redstone.South},
	{"redstone_west", redstone.West},
}

// Wire connection values.
const (
	wireNone = iota
	wireSide
	wireUp
)

// wireNames are the state values of a connection.
var wireNames = [...]string{"none", "side", "up"}

// wireState is the state wire at pos should have. With one side joined the wire
// runs on straight through; with none it joins all four.
func (t *Ticker) wireState(r *world.Region, pos cube.Pos, block uint32) uint32 {
	var v [4]int
	for i, f := range wireSides {
		v[i] = t.wireConnection(r, pos, f.face)
	}
	n, e, s, w := v[0], v[1], v[2], v[3]
	if n|s|v[3] == 0 {
		w = wireSide
	}
	if v[1]|n|s == 0 {
		e = wireSide
	}
	if n|v[1]|v[3] == 0 {
		n = wireSide
	}
	if s|v[1]|v[3] == 0 {
		s = wireSide
	}
	next := maps.Clone(t.blocks.States(block))
	for i, val := range [...]int{n, e, s, w} {
		next[wireSides[i].state] = wireNames[val]
	}
	if id, ok := t.blocks.StateID(t.blocks.Name(block), next); ok {
		return id
	}
	return block
}

// refreshWire gives the wire at pos the connections it should have.
func (t *Ticker) refreshWire(c *regionCircuit, pos cube.Pos) {
	block, ok := c.r.Block(pos, 0)
	if !ok || t.kind(block) != kindWire {
		return
	}
	if id := t.wireState(c.r, pos, block); id != block {
		t.setByCircuit(c, pos, id)
	}
}

// wireConnection is how wire at pos joins the block on side f. Wire one level up
// comes first, unless a conductor above stops it, then what is on the side, then
// wire one level down, unless a conductor on the side stops it.
func (t *Ticker) wireConnection(r *world.Region, pos cube.Pos, f redstone.Face) int {
	side := fromPos(toPos(pos).Side(f))
	aboveSide := cube.Pos{side[0], side[1] + 1, side[2]}
	above, _ := r.Block(cube.Pos{pos[0], pos[1] + 1, pos[2]}, 0)
	if !t.stopsWire(above) {
		if b, ok := r.Block(aboveSide, 0); ok && t.kind(b) == kindWire && t.attach.WireSurvives(r, aboveSide) {
			if s, ok := r.Block(side, 0); ok && t.attach.Supports(s, cube.Face(f.Opposite())) {
				return wireUp
			}
			return wireSide
		}
	}
	b, ok := r.Block(side, 0)
	if !ok {
		return wireNone
	}
	if t.joinsWire(b, f) {
		return wireSide
	}
	if t.stopsWire(b) {
		return wireNone
	}
	if under, ok := r.Block(cube.Pos{side[0], side[1] - 1, side[2]}, 0); ok && t.kind(under) == kindWire {
		return wireSide
	}
	return wireNone
}

// stopsWire is whether a state keeps wire from running up or down past it.
func (t *Ticker) stopsWire(block uint32) bool {
	return int(block) < len(t.stops) && t.stops[block]
}

// joinsWire is whether wire connects to a state on its side f.
func (t *Ticker) joinsWire(block uint32, f redstone.Face) bool {
	if int(block) >= len(t.joins) {
		return false
	}
	switch t.joins[block] {
	case joinAll:
		return true
	case joinLine:
		d := cardinalFace(t.blocks.States(block))
		return f == d || f == d.Opposite()
	case joinFacing:
		states := t.blocks.States(block)
		v, ok := states[stateFacing].(int32)
		if !ok {
			v, _ = states[stateFacingNamed].(int32)
		}
		return faceFromIndex(v) == f
	}
	return false
}

// QueuedTick breaks wire, a repeater or a comparator that lost its support, joins
// wire up, and turns a lit lamp with no power off.
func (t *Ticker) QueuedTick(r *world.Region, pos cube.Pos, block uint32) {
	switch t.kind(block) {
	case kindWire:
		if !t.attach.WireSurvives(r, pos) {
			t.fall(r, pos, block)
			return
		}
		t.refreshWire(t.circuitOf(r), pos)
	case kindRepeater, kindComparator:
		if !t.attach.DiodeSurvives(r, pos) {
			t.fall(r, pos, block)
		}
	case kindLamp:
		c := t.circuitOf(r)
		if t.blocks.Name(block) == nameLampOn && c.sys.Strength(toPos(pos)) == 0 {
			t.setByCircuit(c, pos, t.named(nameLamp, block))
		}
	default:
		t.BlockTicker.QueuedTick(r, pos, block)
	}
}

// setup makes the component of block at pos, or brings an existing one up to date
// with the block's state. load says the block came with its chunk rather than
// being placed.
func (t *Ticker) setup(c *regionCircuit, pos cube.Pos, block uint32, load bool) {
	p := toPos(pos)
	name := t.blocks.Name(block)
	states := t.blocks.States(block)
	switch t.kind(block) {
	case kindWire:
		// A new component starts at no power, and the block follows it.
		if comp := c.sys.Create(p, redstone.KindWire, redstone.Down); comp != nil {
			if int32(comp.Strength()) != intState(states, stateSignal) {
				t.setByCircuit(c, pos, t.with(block, stateSignal, int32(comp.Strength())))
			}
		}
	case kindTorch:
		comp := c.sys.Component(p, redstone.KindTorch)
		if comp == nil {
			comp = c.sys.Create(p, redstone.KindTorch, torchFace(states))
			if comp != nil {
				comp.SetTorchOn(name == nameTorch)
			}
		}
	case kindBlock:
		if comp := c.sys.Create(p, redstone.KindProducer, redstone.NoFace); comp != nil {
			comp.SetStrength(fullPower)
		}
	case kindLamp:
		if comp := c.sys.Create(p, redstone.KindConsumer, redstone.Down); comp != nil {
			comp.SetSideInput(true)
			comp.SetFromCapacitor(false)
		}
	case kindRepeater:
		if comp := c.sys.Create(p, redstone.KindRepeater, cardinalFace(states)); comp != nil {
			comp.SetAttach(true)
			comp.SetDelay(intState(states, stateDelay))
			if name == nameRepeater {
				comp.SetStrength(fullPower)
			}
		}
	case kindComparator:
		created := c.sys.Component(p, redstone.KindComparator) == nil
		if comp := c.sys.Create(p, redstone.KindComparator, cardinalFace(states)); comp != nil {
			comp.SetAttach(true)
			comp.SetSubtract(bitState(states, stateSubtract))
			e := t.ensureComparator(c.r, pos)
			if created && load {
				comp.LoadSignal(e.signal)
			}
		}
	case kindLever:
		if comp := c.sys.Create(p, redstone.KindProducer, leverFace(states)); comp != nil {
			comp.SetStrength(onOff(bitState(states, stateOpen)))
			comp.SetAttach(true)
			comp.SetAllowUp(true)
		}
	case kindButton:
		dir := faceFromIndex(intState(states, stateFacing)).Opposite()
		if comp := c.sys.Create(p, redstone.KindProducer, dir); comp != nil {
			comp.SetStrength(onOff(bitState(states, statePressed)))
			comp.SetAttach(true)
			comp.SetAllowUp(true)
		}
	case kindDoor:
		if comp := c.sys.Create(p, redstone.KindConsumer, redstone.Down); comp != nil {
			comp.SetStrength(onOff(bitState(states, stateOpen)))
			comp.SetHalfPulse(true)
		}
	case kindTrapdoor:
		if comp := c.sys.Create(p, redstone.KindConsumer, redstone.Down); comp != nil {
			comp.SetStrength(onOff(bitState(states, stateOpen)))
			comp.SetAllowUp(true)
			comp.SetHalfPulse(true)
			comp.SetIgnoreFirst(true)
		}
	case kindGate:
		if comp := c.sys.Create(p, redstone.KindConsumer, redstone.Down); comp != nil {
			comp.SetStrength(onOff(bitState(states, stateOpen)))
			comp.SetAllowUp(true)
		}
	}
}

// Loaded implements redstone.World.
func (c *regionCircuit) Loaded(p redstone.Pos) bool {
	_, ok := c.r.Block(fromPos(p), 0)
	return ok
}

// ChunksLoaded implements redstone.World.
func (c *regionCircuit) ChunksLoaded(p redstone.Pos, radius int32) bool {
	for x := p.X - radius; x <= p.X+radius; x += 16 {
		for z := p.Z - radius; z <= p.Z+radius; z += 16 {
			if !c.Loaded(redstone.Pos{X: x, Y: p.Y, Z: z}) {
				return false
			}
		}
	}
	return true
}

// BlockPower implements redstone.World.
func (c *regionCircuit) BlockPower(p redstone.Pos) redstone.Power {
	block, ok := c.r.Block(fromPos(p), 0)
	if !ok {
		return redstone.PowerNone
	}
	if int(block) < len(c.t.power) {
		return c.t.power[block]
	}
	return redstone.PowerNone
}

// PowerChanged implements redstone.World: the block's onRedstoneUpdate.
func (c *regionCircuit) PowerChanged(p redstone.Pos, strength, previous int16, first bool) {
	t, pos := c.t, fromPos(p)
	block, ok := c.r.Block(pos, 0)
	if !ok {
		return
	}
	on := strength > 0
	name := t.blocks.Name(block)
	switch t.kind(block) {
	case kindWire:
		t.setByCircuit(c, pos, t.with(block, stateSignal, int32(strength)))
	case kindTorch:
		t.updateTorch(c, pos, block, name, on)
	case kindLamp:
		if on {
			c.r.CancelTick(pos, block)
			if name != nameLampOn {
				t.setByCircuit(c, pos, t.named(nameLampOn, block))
			}
		} else {
			c.r.ScheduleTick(pos, block, lampOffDelay, 0)
		}
	case kindRepeater:
		if want := choose(on, nameRepeater, nameRepeaterOff); want != name {
			t.setByCircuit(c, pos, t.named(want, block))
		}
	case kindComparator:
		t.updateComparator(c, pos, block, strength, previous)
	case kindDoor:
		if !first {
			t.updateDoor(c, pos, block, p, on)
		}
	case kindTrapdoor, kindGate:
		if !first {
			c.sys.Lock(true)
			blockuse.SetPowered(t.blocks, c.r, pos, block, on)
			c.sys.Lock(false)
		}
	}
}

// updateTorch swaps a torch's lit and unlit forms; burning out fizzes.
func (t *Ticker) updateTorch(c *regionCircuit, pos cube.Pos, block uint32, name string, on bool) {
	if on {
		if name != nameTorch {
			t.setByCircuit(c, pos, t.named(nameTorch, block))
		}
		return
	}
	if name == nameTorchOff {
		return
	}
	if comp := c.sys.Component(toPos(pos), redstone.KindTorch); comp != nil && comp.BurntOut() {
		blockuse.Sound(t.blocks, c.r, pos, packet.SoundEventFizz, block)
	}
	t.setByCircuit(c, pos, t.named(nameTorchOff, block))
}

// updateDoor opens a door when either half has power.
func (t *Ticker) updateDoor(c *regionCircuit, pos cube.Pos, block uint32, p redstone.Pos, on bool) {
	other := redstone.Up
	if bitState(t.blocks.States(block), "upper_block_bit") {
		other = redstone.Down
	}
	if !on && c.sys.Strength(p.Side(other)) > 0 {
		on = true
	}
	c.sys.Lock(true)
	blockuse.SetPowered(t.blocks, c.r, pos, block, on)
	c.sys.Lock(false)
}

// setByCircuit sets a block because of the circuit, so it does not rebuild it.
func (t *Ticker) setByCircuit(c *regionCircuit, pos cube.Pos, block uint32) {
	c.sys.Lock(true)
	if c.r.SetBlock(pos, 0, block) {
		c.r.UpdateNeighbours(pos)
	}
	c.sys.Lock(false)
}

// with returns block with one state changed.
func (t *Ticker) with(block uint32, key string, v any) uint32 {
	states := t.blocks.States(block)
	if _, ok := states[key]; !ok {
		return block
	}
	next := maps.Clone(states)
	next[key] = v
	if id, ok := t.blocks.StateID(t.blocks.Name(block), next); ok {
		return id
	}
	return block
}

// named returns the state of the block called name with block's states.
func (t *Ticker) named(name string, block uint32) uint32 {
	if id, ok := t.blocks.StateID(name, t.blocks.States(block)); ok {
		return id
	}
	return block
}

// toPos and fromPos change between the engine's positions and the library's.
func toPos(p cube.Pos) redstone.Pos {
	return redstone.Pos{X: int32(p[0]), Y: int32(p[1]), Z: int32(p[2])} //nolint:gosec // block positions fit
}

func fromPos(p redstone.Pos) cube.Pos { return cube.Pos{int(p.X), int(p.Y), int(p.Z)} }

// torchFace is BDS's component direction of a torch: the side its holder is on,
// down for the floor.
func torchFace(states map[string]any) redstone.Face {
	v, _ := states[stateTorch].(string)
	switch v {
	case "west":
		return redstone.West
	case "east":
		return redstone.East
	case "north":
		return redstone.North
	case "south":
		return redstone.South
	}
	return redstone.Down
}

// cardinalFace is the face of a cardinal direction state.
func cardinalFace(states map[string]any) redstone.Face {
	v, _ := states[stateCardinal].(string)
	switch v {
	case "south":
		return redstone.South
	case "west":
		return redstone.West
	case "east":
		return redstone.East
	}
	return redstone.North
}

// leverFace is the side a lever's holder is on.
func leverFace(states map[string]any) redstone.Face {
	v, _ := states[stateLever].(string)
	switch v {
	case "east":
		return redstone.West
	case "west":
		return redstone.East
	case "south":
		return redstone.North
	case "north":
		return redstone.South
	case "up_north_south", "up_east_west":
		return redstone.Down
	}
	return redstone.Up
}

// faceFromIndex is a facing_direction value as a face.
func faceFromIndex(i int32) redstone.Face {
	if i < 0 || i >= int32(redstone.NoFace) {
		return redstone.Down
	}
	return redstone.Face(i) //nolint:gosec // checked above
}

// intState reads a whole number state, 0 if it is missing.
func intState(states map[string]any, key string) int32 {
	v, _ := states[key].(int32)
	return v
}

// bitState reads a bit state, false if it is missing.
func bitState(states map[string]any, key string) bool {
	v, _ := states[key].(uint8)
	return v != 0
}

// onOff is the strength of something on or off.
func onOff(on bool) int32 {
	if on {
		return fullPower
	}
	return 0
}

// flag is a bit state's value.
func flag(on bool) uint8 {
	if on {
		return 1
	}
	return 0
}

// choose returns a if c, else b.
func choose(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}
