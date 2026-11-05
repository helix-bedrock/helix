// Package connect is how fences, glass panes, bars and trip wire join their neighbours,
// and how stairs turn into corners, as BDS 1.26.60 does it: since 1.26 the arms and
// corners are in the block's own state, so the server works them out when the block
// is placed and when a neighbour changes. Walls have a rule of their own and are not here.
package connect

import (
	"fmt"
	"strings"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Groups a connecting block belongs to.
const (
	groupWoodFence   = 0
	groupNetherFence = 1
	groupThin        = 3
	groupTripWire    = 4
)

// noGroup marks a state that does not connect to anything.
const noGroup = -1

// arms are the connection states, with the side each looks at and the face of
// that neighbour which touches it.
var arms = [...]struct {
	state string
	side  cube.Face
	touch cube.Face
}{
	{"minecraft:connection_north", cube.FaceNorth, cube.FaceSouth},
	{"minecraft:connection_south", cube.FaceSouth, cube.FaceNorth},
	{"minecraft:connection_west", cube.FaceWest, cube.FaceEast},
	{"minecraft:connection_east", cube.FaceEast, cube.FaceWest},
}

// rule is what a state lets connect to it.
type rule struct {
	has    bool
	groups uint8
	faces  uint8
}

// Table holds every state's connection facts. It does not change after New, so
// any region may read it.
type Table struct {
	blocks *registry.Blocks
	rules  []rule
	group  []int8
	// stairIndex and stairs are the stair corner facts (stairs.go).
	stairIndex []uint16
	stairs     []stairFacts
}

// New loads BDS's rules for the states of blocks.
func New(blocks *registry.Blocks) (*Table, error) {
	conns, err := data.ConnectionData()
	if err != nil {
		return nil, err
	}
	t := &Table{blocks: blocks, rules: make([]rule, blocks.BlockCount()), group: make([]int8, blocks.BlockCount())}
	t.stairIndex, t.stairs = stairsOf(blocks)
	for id := range t.rules {
		state := uint32(id) //nolint:gosec // state IDs fit uint32
		t.group[id] = groupOf(blocks.Name(state), blocks.States(state))
		c, ok := conns[blocks.Name(state)]
		if !ok {
			continue
		}
		if len(c.States) == 0 {
			t.rules[id] = rule{has: true, groups: uint8(c.Groups), faces: uint8(c.Faces)} //nolint:gosec // bit sets of six and four bits
		}
	}
	for name, c := range conns {
		for _, s := range c.States {
			id, ok := blocks.StateID(name, typed(blocks, name, s.States))
			if !ok {
				return nil, fmt.Errorf("connect: unknown state of %s: %v", name, s.States)
			}
			if s.Groups >= 0 {
				t.rules[id] = rule{has: true, groups: uint8(s.Groups), faces: uint8(s.Faces)} //nolint:gosec // bit sets of six and four bits
			}
		}
	}
	return t, nil
}

// typed gives a state read from JSON the types the registry uses: its numbers
// are floats there.
func typed(blocks *registry.Blocks, name string, states map[string]any) map[string]any {
	def, ok := blocks.Block(name)
	if !ok {
		return states
	}
	out := make(map[string]any, len(states))
	want := blocks.States(def)
	for k, v := range states {
		f, isNum := v.(float64)
		switch {
		case !isNum:
			out[k] = v
		case isByte(want[k]):
			out[k] = uint8(f)
		default:
			out[k] = int32(f)
		}
	}
	return out
}

// isByte reports whether a state value is a byte.
func isByte(v any) bool {
	_, ok := v.(uint8)
	return ok
}

// groupOf is the group of a block that works out its arms here, by BDS's class:
// fire and chorus plants have connection states with rules of their own.
func groupOf(name string, states map[string]any) int8 {
	if _, ok := states[arms[0].state]; !ok {
		return noGroup
	}
	switch {
	case name == "minecraft:trip_wire":
		return groupTripWire
	case name == "minecraft:nether_brick_fence":
		return groupNetherFence
	case strings.HasSuffix(name, "_fence"):
		return groupWoodFence
	case strings.HasSuffix(name, "glass_pane"), strings.HasSuffix(name, "_bars"):
		return groupThin
	}
	return noGroup
}

// Connected returns block at pos with its arms (or a stair's corner) set from its
// neighbours, or block itself if it is not a block that connects. A neighbour in an
// unloaded chunk does not connect. Runs: owner of the region.
func (t *Table) Connected(r *world.Region, pos cube.Pos, block uint32) uint32 {
	if int(block) >= len(t.group) {
		return block
	}
	if t.group[block] == noGroup {
		return t.StairCorner(r, pos, block)
	}
	group := t.group[block]
	var next map[string]any
	for _, a := range arms {
		v := uint8(0)
		if n, ok := r.Block(pos.Side(a.side), 0); ok && t.accepts(n, group, a.touch) {
			v = 1
		}
		cur := t.blocks.States(block)
		if old, ok := cur[a.state].(uint8); ok && old == v {
			continue
		}
		if next == nil {
			next = make(map[string]any, len(cur))
			for k, s := range cur {
				next[k] = s
			}
		}
		next[a.state] = v
	}
	if next == nil {
		return block
	}
	if id, ok := t.blocks.StateID(t.blocks.Name(block), next); ok {
		return id
	}
	return block
}

// accepts reports whether block lets group connect on its side touch. A block
// with no rule lets everything connect but trip wire.
func (t *Table) accepts(block uint32, group int8, touch cube.Face) bool {
	if int(block) >= len(t.rules) {
		return false
	}
	c := t.rules[block]
	if !c.has {
		return group != groupTripWire
	}
	return c.groups&(1<<group) != 0 && c.faces&(1<<touch) != 0
}

// Ticker works out a connecting block's arms when a neighbour changes, and hands
// everything else to another BlockTicker.
type Ticker struct {
	world.BlockTicker
	table *Table
}

// NewTicker returns a Ticker using table and handing the rest to next.
func NewTicker(table *Table, next world.BlockTicker) *Ticker {
	return &Ticker{BlockTicker: next, table: table}
}

// NeighbourChanged sets the block's new arms. The block's neighbours are not told:
// arms do not change what connects to it.
func (t *Ticker) NeighbourChanged(r *world.Region, pos, from cube.Pos, block uint32, layer uint8) {
	if layer == 0 {
		if next := t.table.Connected(r, pos, block); next != block {
			r.SetBlock(pos, 0, next)
			block = next
		}
	}
	t.BlockTicker.NeighbourChanged(r, pos, from, block, layer)
}
