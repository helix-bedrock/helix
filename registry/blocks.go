package registry

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/helix-bedrock/helix-data/blockstate"
	"github.com/helix-bedrock/world/chunk"
)

// Blocks is the frozen block table. Every block state has a dense ID from 0
// to Count()-1; the states of one block have consecutive IDs. Its methods
// are safe for use by many goroutines at once.
type Blocks struct {
	names      []string // per block
	firstState []uint32 // per block
	defaults   []uint32 // per block
	byName     map[string]uint32

	stateBlock []uint32 // per state: its block
	states     []map[string]any
	versions   []int32
	hashes     []uint32
	byHash     map[uint32]uint32
	byKey      map[string]uint32

	// replaced maps deleted block names to their replacement's default
	// state.
	replaced     map[string]uint32
	air, unknown uint32

	// props is swapped whole on every change, so a reader sees one
	// consistent table.
	props atomic.Pointer[[]Properties]
}

var _ chunk.BlockRegistry = (*Blocks)(nil)

// Freeze builds the block table from everything registered and ends the
// Setup: later calls return ErrFrozen. States get IDs in registration
// order.
func (s *Setup) Freeze() (*Blocks, error) {
	if s.s.frozen {
		return nil, ErrFrozen
	}
	b := &Blocks{byName: map[string]uint32{}, byHash: map[uint32]uint32{}, byKey: map[string]uint32{}, replaced: map[string]uint32{}}
	var props []Properties
	for _, name := range s.s.order {
		t, ok := s.s.blocks[name]
		if !ok {
			continue // deleted
		}
		idx := uint32(len(b.names))    //nolint:gosec // far below 2^32 blocks
		first := uint32(len(b.states)) //nolint:gosec // far below 2^32 states
		b.names = append(b.names, name)
		b.firstState = append(b.firstState, first)
		b.defaults = append(b.defaults, first+uint32(t.Default)) //nolint:gosec // checked by RegisterBlock
		b.byName[name] = idx
		for _, st := range t.States {
			id := uint32(len(b.states)) //nolint:gosec // far below 2^32 states
			h, err := blockstate.NetworkHash(blockstate.State{Name: name, Properties: st.States})
			if err != nil {
				return nil, fmt.Errorf("registry: freeze: %w", err)
			}
			if other, dup := b.byHash[h]; dup {
				return nil, fmt.Errorf("registry: freeze: %s %v has the network hash of %s %v", name, st.States, b.names[b.stateBlock[other]], b.states[other])
			}
			b.byHash[h] = id
			b.byKey[stateKey(name, st.States)] = id
			b.stateBlock = append(b.stateBlock, idx)
			b.states = append(b.states, maps.Clone(st.States))
			b.versions = append(b.versions, st.Version)
			b.hashes = append(b.hashes, h)
			props = append(props, st.Properties)
		}
	}
	var ok bool
	if b.air, ok = b.Block(Air); !ok {
		return nil, fmt.Errorf("registry: freeze: %s is not registered", Air)
	}
	if b.unknown, ok = b.Block(Unknown); !ok {
		return nil, fmt.Errorf("registry: freeze: %s is not registered", Unknown)
	}
	for name := range s.s.deleted {
		// Follow replacements that were deleted too.
		r, seen := name, map[string]bool{}
		for !seen[r] {
			seen[r] = true
			next, gone := s.s.deleted[r]
			if !gone {
				break
			}
			r = next
		}
		id, ok := b.Block(r)
		if !ok {
			return nil, fmt.Errorf("registry: freeze: replacement %s of deleted block %s is not registered", r, name)
		}
		b.replaced[name] = id
	}
	b.props.Store(&props)
	s.s.frozen = true
	return b, nil
}

// Count returns the number of block states.
func (b *Blocks) Count() int { return len(b.states) }

// Air returns the ID of air.
func (b *Blocks) Air() uint32 { return b.air }

// Unknown returns the ID of minecraft:unknown.
func (b *Blocks) Unknown() uint32 { return b.unknown }

// Block returns the ID of a block's default state.
func (b *Blocks) Block(name string) (uint32, bool) {
	i, ok := b.byName[name]
	if !ok {
		return 0, false
	}
	return b.defaults[i], true
}

// StateID returns the ID of a block state, or false if the registry does
// not know it.
func (b *Blocks) StateID(name string, states map[string]any) (uint32, bool) {
	id, ok := b.byKey[stateKey(name, states)]
	return id, ok
}

// Name returns the block name of a state, or "" for an unknown ID.
func (b *Blocks) Name(id uint32) string {
	if int(id) >= len(b.stateBlock) {
		return ""
	}
	return b.names[b.stateBlock[id]]
}

// States returns a state's property values. The map is shared: do not
// change it.
func (b *Blocks) States(id uint32) map[string]any {
	if int(id) >= len(b.states) {
		return nil
	}
	return b.states[id]
}

// Version returns the block version a state is saved with.
func (b *Blocks) Version(id uint32) int32 {
	if int(id) >= len(b.versions) {
		return 0
	}
	return b.versions[id]
}

// Hash returns a state's network hash, the ID clients get for it.
func (b *Blocks) Hash(id uint32) uint32 {
	if int(id) >= len(b.hashes) {
		return 0
	}
	return b.hashes[id]
}

// Properties returns a state's numbers as they are now.
func (b *Blocks) Properties(id uint32) Properties {
	t := *b.props.Load()
	if int(id) >= len(t) {
		return Properties{}
	}
	return t[id]
}

// PropertyTable returns the whole property table, indexed by state ID. It
// does not change: a region takes it at the start of a tick and uses it
// for the whole tick. Do not modify it.
func (b *Blocks) PropertyTable() []Properties { return *b.props.Load() }

// UpdateProperties changes the numbers of any states while the server
// runs, vanilla ones included. edit gets a copy of the table to change;
// the copy replaces the table at once, and each region picks it up at the
// start of its next tick. Concurrent updates are applied one after the
// other, never lost.
func (b *Blocks) UpdateProperties(edit func(table []Properties)) {
	for {
		old := b.props.Load()
		table := slices.Clone(*old)
		edit(table)
		if b.props.CompareAndSwap(old, &table) {
			return
		}
	}
}

// The methods below are chunk.BlockRegistry, for the world library.

// BlockCount returns the number of block states.
func (b *Blocks) BlockCount() int { return len(b.states) }

// AirRuntimeID returns the ID of air.
func (b *Blocks) AirRuntimeID() uint32 { return b.air }

// RuntimeIDToState returns a state's name and property values.
func (b *Blocks) RuntimeIDToState(id uint32) (string, map[string]any, bool) {
	if int(id) >= len(b.states) {
		return "", nil, false
	}
	return b.names[b.stateBlock[id]], b.states[id], true
}

// StateToRuntimeID returns the ID of a saved state. A state of a deleted
// block loads as its replacement; any other state the registry does not
// know loads as minecraft:unknown, as BDS loads it, so a chunk never fails
// over one block.
func (b *Blocks) StateToRuntimeID(name string, states map[string]any) (uint32, bool) {
	if id, ok := b.byKey[stateKey(name, states)]; ok {
		return id, true
	}
	if id, ok := b.replaced[name]; ok {
		return id, true
	}
	return b.unknown, true
}

// FilteringBlock returns a state's light dampening.
func (b *Blocks) FilteringBlock(id uint32) uint8 { return b.Properties(id).LightDampening }

// LightBlock returns a state's light emission.
func (b *Blocks) LightBlock(id uint32) uint8 { return b.Properties(id).LightEmission }

// HashToRuntimeID returns the state with a network hash.
func (b *Blocks) HashToRuntimeID(h uint32) (uint32, bool) {
	id, ok := b.byHash[h]
	return id, ok
}

// RuntimeIDToHash returns a state's network hash.
func (b *Blocks) RuntimeIDToHash(id uint32) (uint32, bool) {
	if int(id) >= len(b.hashes) {
		return 0, false
	}
	return b.hashes[id], true
}

// stateKey is equal for equal states whatever the map order. Value types
// are part of it: the byte 1 and the int 1 are different states.
func stateKey(name string, states map[string]any) string {
	var sb strings.Builder
	sb.WriteString(name)
	for _, k := range slices.Sorted(maps.Keys(states)) {
		sb.WriteByte(';')
		sb.WriteString(k)
		switch v := states[k].(type) {
		case uint8:
			sb.WriteString("=b")
			sb.WriteString(strconv.Itoa(int(v)))
		case int32:
			sb.WriteString("=i")
			sb.WriteString(strconv.Itoa(int(v)))
		case string:
			sb.WriteString("=s")
			sb.WriteString(v)
		default:
			fmt.Fprintf(&sb, "=?%T:%v", v, v)
		}
	}
	return sb.String()
}
