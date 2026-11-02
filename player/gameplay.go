package player

import (
	"maps"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/world/cube"
)

// Systems are the steps of a player's block breaking and placing, its
// movement state, health and food. Each
// starts as vanilla's (BDS 1.26.60) and may be replaced in Setup, or
// wrapped by keeping the old value and calling it:
//
//	sys := s.Systems()
//	next := sys.BreakTime
//	sys.BreakTime = func(b *helix.BreakInfo) float32 { return next(b) * 2 }
//
// They run on the player's region tick.
type Systems struct {
	// Breaking.
	BreakStart  BreakStartFunc
	BreakTime   BreakTimeFunc
	ToolSpeed   ToolSpeedFunc
	BreakTick   BreakTickFunc
	BreakFinish BreakFinishFunc
	Wear        WearFunc
	// Placing.
	PlaceState PlaceStateFunc
	PlaceCheck PlaceCheckFunc
	Place      PlaceFunc
	// Movement state, after the movement handler.
	MoveFlags  MoveFlagsFunc
	Jump       JumpFunc
	Fall       FallFunc
	FallDamage FallDamageFunc
	MoveCost   MoveCostFunc
	// Health and food.
	Hurt       HurtFunc
	Exhaust    ExhaustFunc
	Exhaustion ExhaustionValues
	Die        DieFunc
	Respawn    RespawnFunc
	// Items in the world.
	DropItem DropItemFunc
	PickUp   PickUpFunc
	// ItemTag gives items a name tag, updated once a second. Nil, vanilla's,
	// shows none. It is sent to clients only, never saved.
	ItemTag ItemTagFunc
}

// ItemTagFunc returns the text of an item's name tag.
type ItemTagFunc func(i *ItemActor) string

// MoveFlagsFunc sets the player's movement states (sneaking, sprinting...)
// from its PlayerAuthInput input flags.
type MoveFlagsFunc func(p *Player, in protocol.InputFlags)

// JumpFunc runs when the player starts a jump.
type JumpFunc func(p *Player)

// FallFunc updates the player's fall distance after a movement and lands
// it.
type FallFunc func(p *Player, m *MoveInfo)

// FallDamageFunc lands the player after falling distance blocks.
type FallDamageFunc func(p *Player, distance float32)

// MoveCostFunc returns the exhaustion a movement costs.
type MoveCostFunc func(p *Player, m *MoveInfo) float32

// HurtFunc deals damage to the player; false if it was not hurt.
type HurtFunc func(p *Player, damage float32, cause DamageCause) bool

// ExhaustFunc adds exhaustion to the player and burns its food.
type ExhaustFunc func(p *Player, amount float32)

// BlockBehaviour is what a block type does. Every method is optional: a
// behaviour implements the interfaces below it needs, and vanilla's
// default applies to the rest.
type BlockBehaviour any

// PlacementStater picks the state a block is placed in (BDS's
// getPlacementBlock): facing, half, axis and so on.
type PlacementStater interface {
	PlacementState(pl *Placement) (uint32, bool)
}

// Replaceable reports whether a block is replaced by a block placed on it,
// such as air (BDS's canBeBuiltOver).
type Replaceable interface {
	Replaceable(block uint32) bool
}

// gameplay is the frozen gameplay setup.
type gameplay struct {
	sys      Systems
	enchants map[int16]*Enchantment
	effects  map[int32]*Effect
	blocks   map[string]BlockBehaviour
	mining   *mining
	// durability, replaceables and notReplacedBy are helix-data's
	// bds/break_place.json.
	durability    map[string]data.Durability
	replaceables  map[string]bool
	notReplacedBy map[string]map[string]bool
	// placement is helix-data's bds/placement.json.
	placement map[string]data.Placement
	breakEv   listeners[BlockBreakEvent]
	placeEv   listeners[BlockPlaceEvent]
}

// gameState is the gameplay part of the Setup while it is open.
type gameState struct {
	sys      Systems
	enchants map[int16]*Enchantment
	effects  map[int32]*Effect
	blocks   map[string]BlockBehaviour
	breakEv  listeners[BlockBreakEvent]
	placeEv  listeners[BlockPlaceEvent]
	// out is the gameplay freeze fills; vanilla's steps read its tables,
	// so they can be wrapped before the freeze.
	out *gameplay
}

func newGameState() *gameState {
	return &gameState{enchants: map[int16]*Enchantment{}, effects: map[int32]*Effect{}, blocks: map[string]BlockBehaviour{}, out: &gameplay{}}
}

// Systems returns the breaking and placing steps to replace or wrap. Not
// usable after the Setup froze.
func (s *Setup) Systems() *Systems {
	if s.h.frozen {
		return &Systems{}
	}
	return &s.h.game.sys
}

// RegisterEnchantment adds an enchantment or replaces the one with its ID.
func (s *Setup) RegisterEnchantment(e Enchantment) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	if old, ok := s.h.game.enchants[e.ID]; ok {
		s.h.log.Info("enchantment replaced", "name", e.Name, "was", old.Name, "by", s.owner)
	}
	s.h.game.enchants[e.ID] = &e
	return nil
}

// Enchantment returns the registered enchantment with id, to change its
// fields in place.
func (s *Setup) Enchantment(id int16) (*Enchantment, bool) {
	e, ok := s.h.game.enchants[id]
	return e, ok && !s.h.frozen
}

// RegisterEffect adds an effect or replaces the one with its ID.
func (s *Setup) RegisterEffect(e Effect) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	if old, ok := s.h.game.effects[e.ID]; ok {
		s.h.log.Info("effect replaced", "name", e.Name, "was", old.Name, "by", s.owner)
	}
	s.h.game.effects[e.ID] = &e
	return nil
}

// Effect returns the registered effect with id, to change its fields in
// place.
func (s *Setup) Effect(id int32) (*Effect, bool) {
	e, ok := s.h.game.effects[id]
	return e, ok && !s.h.frozen
}

// SetBlockBehaviour sets what the block type name does, replacing its
// behaviour.
func (s *Setup) SetBlockBehaviour(name string, b BlockBehaviour) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.blocks[name] = b
	return nil
}

// OnBlockBreak listens to players breaking blocks.
func OnBlockBreak(s *Setup, fn func(e *BlockBreakEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.breakEv.add(s.owner, p, fn)
	return nil
}

// OnBlockPlace listens to players placing blocks.
func OnBlockPlace(s *Setup, fn func(e *BlockPlaceEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.placeEv.add(s.owner, p, fn)
	return nil
}

// vanillaGameplay registers BDS's enchantments, effects and steps.
func vanillaGameplay(s *Setup) error {
	for _, e := range vanillaEnchantments() {
		if err := s.RegisterEnchantment(e); err != nil {
			return err
		}
	}
	for _, e := range vanillaEffects() {
		if err := s.RegisterEffect(e); err != nil {
			return err
		}
	}
	sys, g := s.Systems(), s.h.game.out
	sys.BreakStart = func(*Player, cube.Pos, int32) bool { return true }
	sys.ToolSpeed = func(b *BreakInfo) float32 { return g.mining.toolSpeed(b) }
	sys.BreakTime = g.vanillaBreakTime
	sys.Wear = func(b *BreakInfo) int32 { return g.wear(b) }
	sys.BreakTick = vanillaBreakTick
	sys.BreakFinish = vanillaBreakFinish
	sys.PlaceState = vanillaPlaceState
	sys.PlaceCheck = vanillaPlaceCheck
	sys.Place = vanillaPlace
	sys.DropItem = vanillaDropItem
	sys.PickUp = vanillaPickUp
	sys.MoveFlags = vanillaMoveFlags
	sys.Jump = vanillaJump
	sys.Fall = vanillaFall
	sys.FallDamage = vanillaFallDamage
	sys.MoveCost = vanillaMoveCost
	sys.Hurt = vanillaHurt
	sys.Exhaust = vanillaExhaust
	sys.Die = vanillaDie
	sys.Respawn = vanillaRespawn
	ex, err := vanillaExhaustionValues()
	if err != nil {
		return err
	}
	sys.Exhaustion = ex
	return nil
}

// freezeGameplay ends the gameplay part of the Setup.
func (s *Setup) freezeGameplay() (*gameplay, error) {
	m, err := loadMining()
	if err != nil {
		return nil, err
	}
	bp, err := data.BreakPlaceData()
	if err != nil {
		return nil, err
	}
	placement, err := data.PlacementData()
	if err != nil {
		return nil, err
	}
	gs := s.h.game
	g := gs.out
	*g = gameplay{
		sys: gs.sys, enchants: maps.Clone(gs.enchants), effects: maps.Clone(gs.effects),
		blocks: maps.Clone(gs.blocks), mining: m, breakEv: gs.breakEv, placeEv: gs.placeEv,
		durability: durabilityTable(bp), replaceables: set(bp.Replaceable), notReplacedBy: map[string]map[string]bool{},
		placement: placement,
	}
	for name, by := range bp.NotReplacedBy {
		g.notReplacedBy[name] = set(by)
	}
	g.breakEv.freeze()
	g.placeEv.freeze()
	return g, nil
}
