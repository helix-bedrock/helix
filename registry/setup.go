// Package registry holds every type the client learns once when it joins.
// For now that is blocks and items; biomes and entity types follow.
//
// Everything is registered through one [Setup] at boot: vanilla, built-in
// features and plugins alike. [Setup.Freeze] then builds the tables the
// server runs on. What the client is told at join (block states) cannot
// change after that; the numbers BDS keeps per state (light, friction,
// hardness, ...) can, through [Blocks.UpdateProperties].
package registry

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// Names of blocks the registry needs. Neither can be deleted.
const (
	// Air is the block of every empty position.
	Air = "minecraft:air"
	// Unknown replaces deleted blocks and saved states the registry does
	// not know, as BDS does.
	Unknown = "minecraft:unknown"
)

// Properties are the numbers BDS keeps for one block state.
type Properties struct {
	// LightEmission is the light level the state gives off, 0 to 15.
	LightEmission uint8
	// LightDampening is how much light the state takes away, 0 to 15.
	LightDampening uint8
	// FlameOdds is how readily fire spreads onto the state.
	FlameOdds uint16
	// BurnOdds is how readily fire destroys the state.
	BurnOdds uint16
	// ExplosionResistance is the state's resistance to explosions.
	ExplosionResistance float32
	// Friction is the state's friction; higher is more slippery.
	Friction float32
	// DestroyTime is the state's hardness; -1 means unbreakable.
	DestroyTime float32
}

// BlockState is one state of a block type.
type BlockState struct {
	// States are the state's property values by name, as saved and sent:
	// uint8 (booleans are bytes), int32 or string.
	States map[string]any
	// Version is the block version the state is saved with.
	Version int32
	// Properties are the state's numbers.
	Properties Properties
}

// BlockType is a block and all of its states.
type BlockType struct {
	// Name is the namespaced name, such as "minecraft:stone".
	Name string
	// States lists every state. No two may have the same values.
	States []BlockState
	// Default is the index in States of the state the block is placed in
	// and that replaces it when it is deleted.
	Default int
}

// ErrFrozen is returned by Setup calls after Freeze.
var ErrFrozen = errors.New("registry: setup is frozen")

// Setup collects registrations at boot. It is used by one goroutine at a
// time and is done once Freeze has run.
type Setup struct {
	log   *slog.Logger
	owner string
	s     *setupState
}

type setupState struct {
	order   []string
	blocks  map[string]BlockType
	owners  map[string]string
	deleted map[string]string

	itemOrder    []string
	items        map[string]ItemType
	itemOwners   map[string]string
	deletedItems map[string]bool

	frozen bool
}

// NewSetup returns an empty Setup. Registrations made through it are
// logged as made by owner, for example "minecraft" for vanilla.
func NewSetup(log *slog.Logger, owner string) *Setup {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Setup{log: log, owner: owner, s: &setupState{
		blocks: map[string]BlockType{}, owners: map[string]string{}, deleted: map[string]string{},
		items: map[string]ItemType{}, itemOwners: map[string]string{}, deletedItems: map[string]bool{},
	}}
}

// As returns a view of the same Setup whose registrations are logged as
// made by owner, usually a plugin name.
func (s *Setup) As(owner string) *Setup {
	return &Setup{log: s.log, owner: owner, s: s.s}
}

// RegisterBlock adds a block type. Registering a name that exists replaces
// the old type, and the log says who replaced whom. Registering a deleted
// name brings it back.
func (s *Setup) RegisterBlock(t BlockType) error {
	if s.s.frozen {
		return ErrFrozen
	}
	if err := checkBlockType(t); err != nil {
		return err
	}
	if _, ok := s.s.blocks[t.Name]; ok {
		s.log.Info("block replaced", "block", t.Name, "by", s.owner, "was", s.s.owners[t.Name])
	} else {
		s.s.order = append(s.s.order, t.Name)
	}
	delete(s.s.deleted, t.Name)
	s.s.blocks[t.Name] = t
	s.s.owners[t.Name] = s.owner
	return nil
}

// DeleteBlock removes a block type from the whole server. Saved states of
// it load as replacement's default state; an empty replacement means
// Unknown, as BDS. Air and Unknown cannot be deleted.
func (s *Setup) DeleteBlock(name, replacement string) error {
	if s.s.frozen {
		return ErrFrozen
	}
	if name == Air || name == Unknown {
		return fmt.Errorf("registry: delete block %s: it cannot be deleted", name)
	}
	if _, ok := s.s.blocks[name]; !ok {
		return fmt.Errorf("registry: delete block %s: not registered", name)
	}
	if replacement == "" {
		replacement = Unknown
	}
	if replacement == name {
		return fmt.Errorf("registry: delete block %s: it cannot replace itself", name)
	}
	delete(s.s.blocks, name)
	s.s.deleted[name] = replacement
	s.log.Info("block deleted", "block", name, "by", s.owner, "replacement", replacement)
	return nil
}

// checkBlockType returns an error for a type the tables cannot hold.
func checkBlockType(t BlockType) error {
	if !strings.Contains(t.Name, ":") {
		return fmt.Errorf("registry: block %q: name needs a namespace", t.Name)
	}
	if len(t.States) == 0 {
		return fmt.Errorf("registry: block %s: no states", t.Name)
	}
	if t.Default < 0 || t.Default >= len(t.States) {
		return fmt.Errorf("registry: block %s: default state %d of %d", t.Name, t.Default, len(t.States))
	}
	seen := make(map[string]struct{}, len(t.States))
	for _, st := range t.States {
		for k, v := range st.States {
			switch v.(type) {
			case uint8, int32, string:
			default:
				return fmt.Errorf("registry: block %s: state %s has type %T, want uint8, int32 or string", t.Name, k, v)
			}
		}
		k := stateKey(t.Name, st.States)
		if _, dup := seen[k]; dup {
			return fmt.Errorf("registry: block %s: duplicate state %v", t.Name, st.States)
		}
		seen[k] = struct{}{}
	}
	return nil
}
