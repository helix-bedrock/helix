// Package registry holds every type the client learns once when it joins.
// That is blocks, items, biomes and entity types, frozen at boot, plus the
// creative inventory and recipes, which may change while the server runs.
//
// Everything is registered through one [Setup] at boot: vanilla, built-in
// features and plugins alike. [Setup.Freeze] then builds the tables the
// server runs on. What the client is told once at join (block states,
// items, biomes, entity types) cannot change after that; the numbers BDS
// keeps per block state can, through [Blocks.UpdateProperties], and so can
// the creative inventory and recipes, through [Live.Update].
package registry

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/helix-bedrock/protocol/packet"
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
	// LavaFlammable is set when lava lights fire next to the state, BDS's own flag beside the
	// odds.
	LavaFlammable bool
	// Translucency is how much light and sight pass through, 0 (opaque) to 1, as BDS keeps it
	// for the block; world generation reads it.
	Translucency float32
	// ExplosionResistance is the state's resistance to explosions.
	ExplosionResistance float32
	// Friction is the state's friction; higher is more slippery.
	Friction float32
	// DestroyTime is the state's hardness; -1 means unbreakable.
	DestroyTime float32
	// Collision is the state's collision boxes inside its block, as BDS
	// stores them; none means nothing collides with it. Shared between
	// states: never change the slice, replace it.
	Collision []Box
	// CollisionComputed is set when the state's shape depends on more than
	// its own state (doors need their other half) or BDS's rule for it is
	// not ported yet; Collision is then empty. Shapes BDS computes from
	// the state alone (stairs, slabs, fences, walls...) are in Collision.
	CollisionComputed bool
	// Climbable is BDS's climbable flag: a mob or player in the block can
	// climb, and its fall distance is cleared (ladders, vines).
	Climbable bool
	// Material is BDS's MaterialType of the block (0 air, 5 water, 6 lava).
	Material uint8
	// PushesOut is BDS's flag for a block that pushes items and orbs out of itself and
	// counts as a closed side when they look for a way out.
	PushesOut bool
	// Stairs is BDS's flag for stairs, which push them out more slowly.
	Stairs bool
	// HalfSlab is BDS's half slab flag (its slab block sets it for single slabs only).
	// It stops full sunlight in a column like a block that dampens light.
	HalfSlab bool
	// SolidMaterial is set when the block's material is solid.
	SolidMaterial bool
	// LiquidReaction is what a liquid flowing into the block does.
	LiquidReaction LiquidReaction
	// LavaBlocking is set when the block stops lava flowing into it. Lava
	// asks this instead of LiquidReaction.
	LavaBlocking bool
	// CanContainLiquid is set when the block can hold a liquid in its
	// second layer (waterlogged).
	CanContainLiquid bool
}

// The Materials liquids look at.
const (
	MaterialWater        = 5
	MaterialLava         = 6
	MaterialPowderSnow   = 14
	MaterialBubbleColumn = 18
)

// LiquidReaction is what a liquid flowing into a block does, as BDS names
// them in on_liquid_touches.
type LiquidReaction uint8

// The liquid reactions, in BDS's order.
const (
	// LiquidBroken: the liquid replaces the block.
	LiquidBroken LiquidReaction = iota
	// LiquidPopped: the liquid replaces the block, which drops.
	LiquidPopped
	// LiquidBlocking: the liquid cannot flow into the block.
	LiquidBlocking
	// LiquidNoReaction: the liquid goes in the block's second layer.
	LiquidNoReaction
)

// liquidReaction is the reaction helix-data names name.
func liquidReaction(name string) (LiquidReaction, bool) {
	switch name {
	case "broken":
		return LiquidBroken, true
	case "popped":
		return LiquidPopped, true
	case "blocking":
		return LiquidBlocking, true
	case "no_reaction":
		return LiquidNoReaction, true
	}
	return 0, false
}

// Box is a collision box inside a block: min x, y, z, then max x, y, z.
type Box [6]float32

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
	// Tags are the block's tags as BDS holds them, some with the minecraft:
	// namespace and some without ("minecraft:is_axe_item_destructible",
	// "wood").
	Tags []string
	// Definition is what the client is told about a block it does not
	// have built in, in StartGame: components, states, geometry. BDS
	// sends one for each data-driven vanilla block; nil for the others.
	Definition map[string]any
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
	// definitionOrder is the order BDS sends block definitions in.
	definitionOrder []string
	order           []string
	blocks          map[string]BlockType
	owners          map[string]string
	deleted         map[string]string

	itemOrder    []string
	items        map[string]ItemType
	itemOwners   map[string]string
	deletedItems map[string]bool

	biomeOrder        []string
	biomes            map[string]Biome
	biomeOwners       map[string]string
	deletedBiomes     map[string]deletedBiome
	biomesChanged     bool
	vanillaBiomes     *packet.BiomeDefinitionList
	vanillaBiomeNames map[string]bool

	entityOrder     []string
	entities        map[string]EntityType
	entityOwners    map[string]string
	deletedEntities map[string]bool
	entitiesChanged bool
	vanillaEntities *packet.AvailableActorIdentifiers

	creative *packet.CreativeContent
	crafting *packet.CraftingData
	// trims is the armour trim patterns and materials.
	trims *packet.TrimData
	// smithingEdits run on the recipes at freeze, once items and blocks are known.
	smithingEdits []func(*Smithing) error
	recipeEdits   []func(*Recipes) error

	voxelShapes *packet.VoxelShapes
	jigsaw      *packet.JigsawStructureData

	// vanillaOwner is the owner of the Setup NewSetup returned: changes by
	// anyone else make the registry differ from BDS's packets.
	vanillaOwner string
	frozen       bool
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
		biomes: map[string]Biome{}, biomeOwners: map[string]string{}, deletedBiomes: map[string]deletedBiome{}, vanillaBiomeNames: map[string]bool{},
		entities: map[string]EntityType{}, entityOwners: map[string]string{}, deletedEntities: map[string]bool{},
		vanillaOwner: owner,
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
