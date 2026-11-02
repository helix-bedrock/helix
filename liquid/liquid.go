// Package liquid is vanilla water and lava: how they spread, fall, dry up,
// make infinite sources and turn lava into stone, as BDS 1.26.60 does.
//
// In BDS 1.26.60 minecraft:water and minecraft:lava are the liquids that
// flow, at every depth. minecraft:flowing_water and minecraft:flowing_lava
// are older names: placed or ticked, they turn into water or lava with
// the same depth.
package liquid

import (
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Kind is a liquid.
type Kind int8

// The liquids.
const (
	Water Kind = iota
	Lava
	kindCount
)

// Liquid block names.
const (
	waterName        = "minecraft:water"
	lavaName         = "minecraft:lava"
	flowingWaterName = "minecraft:flowing_water"
	flowingLavaName  = "minecraft:flowing_lava"
	depthState       = "liquid_depth"
)

const (
	// maxDepth is the highest liquid_depth; 8 and above is falling liquid.
	maxDepth = 15
	// falling is added to a depth for liquid that falls.
	falling = 8
	// noCost is a direction's spread cost when the liquid cannot go there.
	noCost = 1000
	// waterSlopeReach and lavaSlopeReach are how many blocks out the
	// liquids look for a way down.
	waterSlopeReach, lavaSlopeReach = 4, 2
)

// noBlock marks a liquid depth with no state.
const noBlock = ^uint32(0)

// liquid is a liquid's blocks: its states by depth, its block type and
// that of its older flowing name.
type liquid struct {
	states        [maxDepth + 1]uint32
	typ, flowType uint32
	material      uint8
}

// blockInfo is what Liquids knows of a block state.
type blockInfo struct {
	// kind is the liquid of a liquid state, -1 for other blocks.
	kind Kind
	// flowing is set for the older flowing names.
	flowing bool
	// depth is the state's liquid_depth.
	depth int8
}

// Liquids is BDS's water and lava. It is a world.BlockTicker that ticks
// only liquids; it keeps no state between calls except per-region working
// state, so one Liquids serves every dimension. Its methods run on region
// ticks.
type Liquids struct {
	blocks *registry.Blocks
	info   []blockInfo
	liq    [kindCount]liquid
	// UltraWarm is set for an ultra-warm dimension (the nether): lava
	// spreads further and faster there.
	UltraWarm                                          bool
	stone, cobble, obsidian, basalt, blueIce, soulSoil uint32
}

// local is the per-region working state of Liquids.
type local struct {
	// solidifying is set while lava solidifies; checks it causes wait in
	// queue, as BDS does.
	solidifying bool
	queue       []solidifyCheck
	rng         *rand.Rand
}

// solidifyCheck is a lava block at pos to check against its neighbour.
type solidifyCheck struct {
	pos, from cube.Pos
}

// New returns the liquids of the registry's blocks.
func New(blocks *registry.Blocks) *Liquids {
	l := &Liquids{blocks: blocks, info: make([]blockInfo, blocks.Count())}
	for i := range l.info {
		l.info[i].kind = -1
	}
	for k, names := range [kindCount][2]string{Water: {waterName, flowingWaterName}, Lava: {lavaName, flowingLavaName}} {
		lq := &l.liq[k]
		for i := range lq.states {
			lq.states[i] = noBlock
		}
		for f, name := range names {
			def, ok := blocks.Block(name)
			if !ok {
				continue
			}
			typ, _ := blocks.Type(def)
			if f == 0 {
				lq.typ, lq.material = typ, blocks.Properties(def).Material
			} else {
				lq.flowType = typ
			}
			for d := range int32(maxDepth + 1) {
				id, ok := blocks.StateID(name, map[string]any{depthState: d})
				if !ok || int(id) >= len(l.info) {
					continue
				}
				l.info[id] = blockInfo{kind: Kind(k), flowing: f == 1, depth: int8(d)}
				if f == 0 {
					lq.states[d] = id
				}
			}
		}
	}
	l.stone = l.named("minecraft:stone")
	l.cobble = l.named("minecraft:cobblestone")
	l.obsidian = l.named("minecraft:obsidian")
	l.basalt = l.named("minecraft:basalt")
	l.blueIce = l.named("minecraft:blue_ice")
	l.soulSoil = l.named("minecraft:soul_soil")
	return l
}

func (l *Liquids) named(name string) uint32 {
	if b, ok := l.blocks.Block(name); ok {
		return b
	}
	return noBlock
}

// Liquid reports the liquid of block and its depth.
func (l *Liquids) Liquid(block uint32) (Kind, int, bool) {
	if int(block) >= len(l.info) || l.info[block].kind < 0 {
		return 0, 0, false
	}
	i := l.info[block]
	return i.kind, int(i.depth), true
}

// Block returns the state of liquid k at depth (0 a source, 8 and up
// falling).
func (l *Liquids) Block(k Kind, depth int) (uint32, bool) {
	if k < 0 || k >= kindCount || depth < 0 || depth > maxDepth || l.liq[k].states[depth] == noBlock {
		return 0, false
	}
	return l.liq[k].states[depth], true
}

// TickDelay is how many ticks liquid k waits between spreads: 5 for
// water, 30 for lava, 10 in an ultra-warm dimension.
func (l *Liquids) TickDelay(k Kind) int32 { return TickDelay(k, l.UltraWarm) }

// TickDelay is how many ticks liquid k waits between spreads, in an
// ultra-warm dimension or not.
func TickDelay(k Kind, ultraWarm bool) int32 {
	switch {
	case k == Water:
		return 5
	case k == Lava && ultraWarm:
		return 10
	case k == Lava:
		return 30
	}
	return 0
}

// RandomTicks is false: lava's random tick sets fire, which is not
// ported.
func (*Liquids) RandomTicks(uint32, uint8) bool { return false }

// RandomTick does nothing.
func (*Liquids) RandomTick(*world.Region, cube.Pos, uint32, uint8) {}

// QueuedTick runs a liquid's tick: an older flowing name first turns into
// the liquid, without telling its neighbours.
func (l *Liquids) QueuedTick(r *world.Region, pos cube.Pos, block uint32) {
	if int(block) >= len(l.info) || l.info[block].kind < 0 {
		return
	}
	i := l.info[block]
	if i.flowing {
		if !l.unflow(r, pos, i) {
			return
		}
	}
	l.tick(r, pos, i.kind)
}

// Placed runs a liquid's onPlace: lava next to water solidifies, and an
// older flowing name turns into the liquid and is ticked.
func (l *Liquids) Placed(r *world.Region, pos cube.Pos, block uint32, _ uint8) {
	if int(block) >= len(l.info) || l.info[block].kind < 0 {
		return
	}
	i := l.info[block]
	l.solidifyAround(r, pos, i.kind)
	if !i.flowing {
		return
	}
	if li, ok := l.at(l.liquidAt(r, pos)); !ok || li.kind != i.kind || !li.flowing {
		return
	}
	if l.unflow(r, pos, i) {
		r.ScheduleTick(pos, l.liq[i.kind].states[i.depth], l.TickDelay(i.kind), 0)
	}
}

// NeighbourChanged runs a liquid's neighborChanged: lava next to water
// solidifies, and a liquid is ticked unless a tick is already due when
// this one would be. Older flowing names only solidify.
func (l *Liquids) NeighbourChanged(r *world.Region, pos, _ cube.Pos, block uint32, _ uint8) {
	if int(block) >= len(l.info) || l.info[block].kind < 0 {
		return
	}
	i := l.info[block]
	l.solidifyAround(r, pos, i.kind)
	if i.flowing {
		return
	}
	lb := l.liquidAt(r, pos)
	if li, ok := l.at(lb); !ok || li.kind != i.kind || li.flowing {
		return
	}
	delay := l.TickDelay(i.kind)
	if !r.ScheduledTickAt(pos, lb, delay) {
		r.ScheduleTick(pos, lb, delay, 0)
	}
}

// unflow turns the older flowing name at pos into its liquid, keeping the
// depth, without telling the neighbours. False if nothing was there.
func (l *Liquids) unflow(r *world.Region, pos cube.Pos, i blockInfo) bool {
	to := l.liq[i.kind].states[i.depth]
	if to == noBlock {
		return false
	}
	return r.SetBlock(pos, l.layerOf(r, pos, l.liq[i.kind].flowType), to)
}

func (l *Liquids) at(block uint32) (blockInfo, bool) {
	if int(block) >= len(l.info) || l.info[block].kind < 0 {
		return blockInfo{}, false
	}
	return l.info[block], true
}

// liquidAt is BDS's getLiquidBlock: the block in the second layer at pos
// if there is one, else the block on layer 0.
func (l *Liquids) liquidAt(r *world.Region, pos cube.Pos) uint32 {
	air := l.blocks.Air()
	if b, ok := r.Block(pos, 1); ok && b != air {
		return b
	}
	b, _ := r.Block(pos, 0)
	return b
}

// layerOf is the layer a liquid of type typ at pos is on: 0 if the block
// on layer 0 is of that type, else the second layer.
func (l *Liquids) layerOf(r *world.Region, pos cube.Pos, typ uint32) uint8 {
	b, _ := r.Block(pos, 0)
	if t, _ := l.blocks.Type(b); t == typ {
		return 0
	}
	return 1
}

// material is the material of block.
func (l *Liquids) material(block uint32) uint8 { return l.blocks.Properties(block).Material }

// depthOf is the depth of a liquid of k's material at pos, -1 if the
// liquid there is another or none.
func (l *Liquids) depthOf(r *world.Region, pos cube.Pos, k Kind) int {
	b := l.liquidAt(r, pos)
	if l.material(b) != l.liq[k].material {
		return -1
	}
	if i, ok := l.at(b); ok {
		return int(i.depth)
	}
	return 0
}

// set puts block at pos on layer and, as BDS's update flag does, tells the
// neighbours.
func (l *Liquids) set(r *world.Region, pos cube.Pos, layer uint8, block uint32) bool {
	if !r.SetBlock(pos, layer, block) {
		return false
	}
	r.UpdateNeighbours(pos)
	return true
}

// fizz plays the fizz of lava cooling at pos.
func (l *Liquids) fizz(r *world.Region, pos cube.Pos) {
	rng := l.local(r).rng
	a, b := rng.Float32(), rng.Float32()
	r.Broadcast(pos, &packet.LevelEvent{
		EventType: packet.LevelEventSoundFizz,
		Position:  mgl32.Vec3{float32(pos[0]) + 0.5, float32(pos[1]) + 0.5, float32(pos[2]) + 0.5},
		EventData: int32(((a-b)*0.8 + 2.6) * 1000),
	}, nil)
}

func (l *Liquids) local(r *world.Region) *local {
	v, _ := r.Local(l, func() any {
		return &local{rng: rand.New(rand.NewPCG(r.ID(), uint64(r.Dimension().CurrentTick())))} //nolint:gosec // gameplay randomness
	}).(*local)
	return v
}
