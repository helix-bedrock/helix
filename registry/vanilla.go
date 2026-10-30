package registry

import (
	"fmt"

	"github.com/helix-bedrock/helix-data/data"
)

// Vanilla registers every vanilla block and item of BDS 1.26.60 from
// helix-data.
func Vanilla(s *Setup) error {
	if err := vanillaBlocks(s); err != nil {
		return err
	}
	return vanillaItems(s)
}

// vanillaItems registers every item BDS tells joining players about, with
// its network ID, version and components as BDS sends them, and its stack
// size and tags as BDS reports them.
func vanillaItems(s *Setup) error {
	reg, err := data.ItemRegistry()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	list, err := data.Items()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	info := make(map[string]int, len(list))
	for i, it := range list {
		info[it.Name] = i
	}
	for _, e := range reg {
		t := ItemType{Name: e.Name, ID: e.RuntimeID, Version: e.Version, ComponentBased: e.ComponentBased, Components: e.Data}
		if i, ok := info[e.Name]; ok {
			t.MaxStack, t.Tags = uint8(min(max(list[i].MaxStack, 0), 255)), list[i].Tags //nolint:gosec // clamped
		}
		if err := s.RegisterItem(t); err != nil {
			return err
		}
	}
	return nil
}

// vanillaBlocks registers every vanilla block: each state as BDS saves
// it, its numbers as BDS holds them at runtime, and its default state.
func vanillaBlocks(s *Setup) error {
	states, err := data.BlockStates()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	props, err := data.AllBlockProperties()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	defaults, err := data.BlockDefaults()
	if err != nil {
		return fmt.Errorf("registry: vanilla: %w", err)
	}
	// block_states.nbt lists each block's states together, in the order
	// the per-state property arrays use.
	var t BlockType
	flush := func() error {
		if t.Name == "" {
			return nil
		}
		def, ok := defaults[t.Name]
		if !ok {
			return fmt.Errorf("registry: vanilla: %s has no default state", t.Name)
		}
		t.Default = -1
		for i, st := range t.States {
			if sameStates(st.States, def) {
				t.Default = i
				break
			}
		}
		if t.Default < 0 {
			return fmt.Errorf("registry: vanilla: default state of %s is not one of its states", t.Name)
		}
		return s.RegisterBlock(t)
	}
	for _, st := range states {
		if st.Name != t.Name {
			if err := flush(); err != nil {
				return err
			}
			t = BlockType{Name: st.Name}
		}
		p, ok := props[st.Name]
		if !ok {
			return fmt.Errorf("registry: vanilla: %s has no properties", st.Name)
		}
		i := len(t.States)
		t.States = append(t.States, BlockState{States: st.Properties, Version: st.Version, Properties: Properties{
			LightEmission: p.LightEmission.At(i), LightDampening: p.LightDampening.At(i),
			FlameOdds: p.FlameOdds.At(i), BurnOdds: p.BurnOdds.At(i),
			ExplosionResistance: p.ExplosionResistance.At(i), Friction: p.Friction.At(i), DestroyTime: p.DestroyTime.At(i),
		}})
	}
	return flush()
}

// sameStates compares a state with a default read from JSON, where every
// number is a float64.
func sameStates(st map[string]any, def map[string]any) bool {
	if len(st) != len(def) {
		return false
	}
	for k, v := range st {
		d, ok := def[k]
		if !ok {
			return false
		}
		switch v := v.(type) {
		case uint8:
			if f, ok := d.(float64); !ok || f != float64(v) {
				return false
			}
		case int32:
			if f, ok := d.(float64); !ok || f != float64(v) {
				return false
			}
		case string:
			if d != v {
				return false
			}
		default:
			return false
		}
	}
	return true
}
