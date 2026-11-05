package player

import (
	"maps"
	"sync/atomic"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
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
	// EnchantOptions makes an enchanting table's options, Anvil and Grindstone their
	// screens' results.
	EnchantOptions EnchantOptionsFunc
	// EnchantRequire says what picking a table's option asks of the player, Bookshelves
	// how many shelves count around a table.
	EnchantRequire EnchantRequireFunc
	Bookshelves    BookshelvesFunc
	Anvil          AnvilFunc
	// AnvilWear runs after an anvil at pos was used.
	AnvilWear  func(p *Player, r *world.Region, pos cube.Pos)
	Grindstone GrindstoneFunc
	// Breaking.
	BreakStart  BreakStartFunc
	BreakTime   BreakTimeFunc
	ToolSpeed   ToolSpeedFunc
	BreakTick   BreakTickFunc
	BreakFinish BreakFinishFunc
	Wear        WearFunc
	// Drops is what a broken block drops.
	Drops DropsFunc
	// Placing.
	PlaceState PlaceStateFunc
	PlaceCheck PlaceCheckFunc
	Place      PlaceFunc
	// UseBlock is a click on a block, before the held item is used.
	UseBlock UseBlockFunc
	// Movement state, after the movement handler.
	MoveFlags  MoveFlagsFunc
	Jump       JumpFunc
	Fall       FallFunc
	FallDamage FallDamageFunc
	MoveCost   MoveCostFunc
	// Health and food.
	Hurt         HurtFunc
	ReduceDamage ReduceDamageFunc
	Exhaust      ExhaustFunc
	FoodTick     FoodTickFunc
	Die          DieFunc
	// Explode runs explosions.
	Explode ExplodeFunc
	Respawn RespawnFunc
	// OutOfWorld says whether an entity is in the void, Void what the void does to it.
	OutOfWorld  OutOfWorldFunc
	Void        VoidFunc
	ShieldUp    ShieldUpFunc
	ShieldBlock ShieldBlockFunc
	Totem       TotemFunc
	// Hitting.
	Attack       AttackFunc
	AttackDamage AttackDamageFunc
	Knockback    KnockbackFunc
	// Using items in the air.
	UseItem    UseItemFunc
	EquipArmor EquipArmorFunc
	FoodOf     FoodOfFunc
	Eat        EatFunc
	// Items in the world.
	DropItem DropItemFunc
	PickUp   PickUpFunc
	// ItemLifetime is how many ticks old items vanish at, from the start. It
	// can change while running with Host.SetItemLifetime.
	ItemLifetime int32
}

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
type HurtFunc func(p *Player, damage float32, src DamageSource) bool

// ReduceDamageFunc is what is left of damage after what the player wears and has:
// armour, its enchantments and effects.
type ReduceDamageFunc func(p *Player, damage float32, src DamageSource) float32

// FoodTickFunc is the player's hunger once a tick: regaining health and starving.
type FoodTickFunc func(p *Player)

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
	sys Systems
	// enchants is swapped whole when a plugin changes an enchantment while running.
	enchants atomic.Pointer[enchantTable]
	// enchantRules is what enchanting reads about items and blocks, swapped whole too.
	enchantRules atomic.Pointer[enchanting]
	effects      map[int32]*Effect
	blocks       map[string]BlockBehaviour
	mining       *mining
	// durability, replaceables and notReplacedBy are helix-data's
	// bds/break_place.json.
	durability    map[string]data.Durability
	replaceables  map[string]bool
	notReplacedBy map[string]map[string]bool
	// placement is helix-data's bds/placement.json.
	placement map[string]data.Placement
	breakEv   listeners[BlockBreakEvent]
	placeEv   listeners[BlockPlaceEvent]
	useEv     listeners[BlockUseEvent]
	// combat is helix-data's bds/item_combat.json.
	combat *combatTable
	// tuning is the default of every Tune, which clearing goes back to.
	tuning tuneTable
	// The events of using items, hitting and being hurt.
	itemUseEv listeners[ItemUseEvent]
	itemEatEv listeners[ItemEatEvent]
	attackEv  listeners[PlayerAttackEvent]
	hurtEv    listeners[PlayerHurtEvent]
	// explosionEv is explosions.
	explosionEv listeners[ExplosionEvent]
	// The events of shields, totems and armour.
	blockEv      listeners[PlayerBlockEvent]
	totemEv      listeners[PlayerTotemEvent]
	armorEquipEv listeners[ArmorEquipEvent]
	// The events of enchanting tables, anvils and grindstones.
	enchantOptionsEv listeners[EnchantOptionsEvent]
	enchantEv        listeners[EnchantEvent]
	anvilEv          listeners[AnvilEvent]
	grindstoneEv     listeners[GrindstoneEvent]
	// itemDropEv and itemLoadEv are the item events.
	itemDropEv listeners[ItemDropEvent]
	itemLoadEv listeners[ItemLoadEvent]
	// The chest events, and the event about what a broken block drops.
	containerOpenEv  listeners[ContainerOpenEvent]
	containerCloseEv listeners[ContainerCloseEvent]
	containerItemEv  listeners[ContainerItemEvent]
	inventoryViewEv  listeners[InventoryViewEvent]
	explosionStartEv listeners[ExplosionStartEvent]
	explosionHitEv   listeners[ExplosionHitEvent]
	voidEv           listeners[VoidEvent]
	tntPrimeEv       listeners[TNTPrimeEvent]
	craftEv          listeners[CraftEvent]
	smithEv          listeners[SmithEvent]
	chestPairEv      listeners[ChestPairEvent]
	chestSpillEv     listeners[ChestSpillEvent]
	dropsEv          listeners[BlockDropsEvent]
	xpEv             listeners[ExperienceEvent]
	// dropRules replace blocks' drop rules; a nil rule drops nothing.
	dropRules map[string]loot.Rule
}

// gameState is the gameplay part of the Setup while it is open.
type gameState struct {
	sys      Systems
	enchants map[int16]*Enchantment
	// enchanting is what enchanting reads about items and blocks.
	enchanting *enchanting
	// The events of enchanting tables, anvils and grindstones.
	enchantOptionsEv listeners[EnchantOptionsEvent]
	enchantEv        listeners[EnchantEvent]
	anvilEv          listeners[AnvilEvent]
	grindstoneEv     listeners[GrindstoneEvent]
	effects          map[int32]*Effect
	blocks           map[string]BlockBehaviour
	breakEv          listeners[BlockBreakEvent]
	placeEv          listeners[BlockPlaceEvent]
	useEv            listeners[BlockUseEvent]
	// The events of using items, hitting and being hurt.
	itemUseEv   listeners[ItemUseEvent]
	itemEatEv   listeners[ItemEatEvent]
	attackEv    listeners[PlayerAttackEvent]
	explosionEv listeners[ExplosionEvent]
	hurtEv      listeners[PlayerHurtEvent]
	// The events of shields, totems and armour.
	blockEv      listeners[PlayerBlockEvent]
	totemEv      listeners[PlayerTotemEvent]
	armorEquipEv listeners[ArmorEquipEvent]
	// itemDropEv and itemLoadEv are the item events.
	itemDropEv listeners[ItemDropEvent]
	itemLoadEv listeners[ItemLoadEvent]
	// The chest events, and the event about what a broken block drops.
	containerOpenEv  listeners[ContainerOpenEvent]
	containerCloseEv listeners[ContainerCloseEvent]
	containerItemEv  listeners[ContainerItemEvent]
	inventoryViewEv  listeners[InventoryViewEvent]
	explosionStartEv listeners[ExplosionStartEvent]
	explosionHitEv   listeners[ExplosionHitEvent]
	voidEv           listeners[VoidEvent]
	tntPrimeEv       listeners[TNTPrimeEvent]
	craftEv          listeners[CraftEvent]
	smithEv          listeners[SmithEvent]
	chestPairEv      listeners[ChestPairEvent]
	chestSpillEv     listeners[ChestSpillEvent]
	dropsEv          listeners[BlockDropsEvent]
	xpEv             listeners[ExperienceEvent]
	// dropRules replace blocks' drop rules; a nil rule drops nothing.
	dropRules map[string]loot.Rule
	// tuning is the default of every Tune.
	tuning tuneTable
	// out is the gameplay freeze fills; vanilla's steps read its tables,
	// so they can be wrapped before the freeze.
	out *gameplay
}

func newGameState() *gameState {
	return &gameState{enchanting: newEnchanting(), enchants: map[int16]*Enchantment{}, effects: map[int32]*Effect{}, blocks: map[string]BlockBehaviour{nameTNT: tntBlock{}}, dropRules: map[string]loot.Rule{}, tuning: vanillaTuning(exhaustionValues{}), out: &gameplay{}}
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

// OnBlockUse listens to players clicking blocks.
func OnBlockUse(s *Setup, fn func(e *BlockUseEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.useEv.add(s.owner, p, fn)
	return nil
}

// OnItemDrop listens to players' items entering the world.
func OnItemDrop(s *Setup, fn func(e *ItemDropEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.itemDropEv.add(s.owner, p, fn)
	return nil
}

// OnItemLoad listens to saved items coming back with their chunks.
func OnItemLoad(s *Setup, fn func(e *ItemLoadEvent), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	s.h.game.itemLoadEv.add(s.owner, p, fn)
	return nil
}

// addListener adds fn to one of the Setup's event lists.
func addListener[E any](s *Setup, l *listeners[E], fn func(e *E), p Priority) error {
	if s.h.frozen {
		return registry.ErrFrozen
	}
	l.add(s.owner, p, fn)
	return nil
}

// vanillaGameplay registers BDS's enchantments, effects and steps.
func vanillaGameplay(s *Setup) error {
	if err := vanillaEnchanting(s); err != nil {
		return err
	}
	for _, e := range vanillaEffects() {
		if err := s.RegisterEffect(e); err != nil {
			return err
		}
	}
	sys, g := s.Systems(), s.h.game.out
	sys.EnchantOptions = vanillaEnchantOptions
	sys.EnchantRequire = vanillaEnchantRequire
	sys.Bookshelves = vanillaBookshelves
	sys.Anvil = vanillaAnvil
	sys.AnvilWear = vanillaAnvilWear
	sys.Grindstone = vanillaGrindstone
	sys.BreakStart = func(*Player, cube.Pos, int32) bool { return true }
	sys.ToolSpeed = func(b *BreakInfo) float32 { return g.mining.toolSpeed(b) }
	sys.BreakTime = g.vanillaBreakTime
	sys.Wear = func(b *BreakInfo) int32 { return g.wear(b) }
	sys.BreakTick = vanillaBreakTick
	sys.BreakFinish = vanillaBreakFinish
	sys.Drops = vanillaDrops
	sys.PlaceState = vanillaPlaceState
	sys.PlaceCheck = vanillaPlaceCheck
	sys.Place = vanillaPlace
	sys.UseBlock = vanillaUseBlock
	sys.DropItem = vanillaDropItem
	sys.PickUp = vanillaPickUp
	sys.ItemLifetime = itemLifetime
	sys.MoveFlags = vanillaMoveFlags
	sys.Jump = vanillaJump
	sys.Fall = vanillaFall
	sys.FallDamage = vanillaFallDamage
	sys.MoveCost = vanillaMoveCost
	sys.Hurt = vanillaHurt
	sys.ReduceDamage = vanillaReduceDamage
	sys.Exhaust = vanillaExhaust
	sys.FoodTick = vanillaFoodTick
	sys.Attack = vanillaAttack
	sys.AttackDamage = vanillaAttackDamage
	sys.Knockback = vanillaKnockback
	sys.UseItem = vanillaUseItem
	sys.FoodOf = vanillaFoodOf
	sys.Eat = vanillaEat
	sys.Die = vanillaDie
	sys.Explode = vanillaExplode
	sys.Respawn = vanillaRespawn
	sys.OutOfWorld = vanillaOutOfWorld
	sys.Void = vanillaVoid
	sys.ShieldUp = vanillaShieldUp
	sys.ShieldBlock = vanillaShieldBlock
	sys.Totem = vanillaTotem
	sys.EquipArmor = vanillaEquipArmor
	ex, err := vanillaExhaustionValues()
	if err != nil {
		return err
	}
	s.h.game.tuning = vanillaTuning(ex)
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
	combat, err := loadCombat()
	if err != nil {
		return nil, err
	}
	gs := s.h.game
	g := gs.out
	*g = gameplay{
		tuning: gs.tuning, combat: combat, itemUseEv: gs.itemUseEv, itemEatEv: gs.itemEatEv, attackEv: gs.attackEv, hurtEv: gs.hurtEv, explosionEv: gs.explosionEv,
		sys: gs.sys, effects: maps.Clone(gs.effects),
		blocks: maps.Clone(gs.blocks), mining: m, breakEv: gs.breakEv, placeEv: gs.placeEv,
		durability: durabilityTable(bp), replaceables: set(bp.Replaceable), notReplacedBy: map[string]map[string]bool{},
		placement: placement, itemDropEv: gs.itemDropEv, itemLoadEv: gs.itemLoadEv, useEv: gs.useEv,
		containerOpenEv: gs.containerOpenEv, containerCloseEv: gs.containerCloseEv, containerItemEv: gs.containerItemEv,
		inventoryViewEv: gs.inventoryViewEv, explosionStartEv: gs.explosionStartEv, explosionHitEv: gs.explosionHitEv, voidEv: gs.voidEv, tntPrimeEv: gs.tntPrimeEv, craftEv: gs.craftEv, smithEv: gs.smithEv,
		chestPairEv: gs.chestPairEv, chestSpillEv: gs.chestSpillEv, dropsEv: gs.dropsEv, xpEv: gs.xpEv, dropRules: maps.Clone(gs.dropRules),
		blockEv: gs.blockEv, totemEv: gs.totemEv, armorEquipEv: gs.armorEquipEv,
		enchantOptionsEv: gs.enchantOptionsEv, enchantEv: gs.enchantEv, anvilEv: gs.anvilEv, grindstoneEv: gs.grindstoneEv,
	}
	g.enchants.Store(newEnchantTable(gs.enchants))
	g.enchantRules.Store(gs.enchanting)
	g.enchantOptionsEv.freeze()
	g.enchantEv.freeze()
	g.anvilEv.freeze()
	g.grindstoneEv.freeze()
	for name, by := range bp.NotReplacedBy {
		g.notReplacedBy[name] = set(by)
	}
	g.itemUseEv.freeze()
	g.itemEatEv.freeze()
	g.attackEv.freeze()
	g.hurtEv.freeze()
	g.explosionEv.freeze()
	g.blockEv.freeze()
	g.totemEv.freeze()
	g.armorEquipEv.freeze()
	g.breakEv.freeze()
	g.placeEv.freeze()
	g.useEv.freeze()
	g.itemDropEv.freeze()
	g.itemLoadEv.freeze()
	g.containerOpenEv.freeze()
	g.containerCloseEv.freeze()
	g.containerItemEv.freeze()
	g.inventoryViewEv.freeze()
	g.explosionStartEv.freeze()
	g.explosionHitEv.freeze()
	g.voidEv.freeze()
	g.tntPrimeEv.freeze()
	g.craftEv.freeze()
	g.smithEv.freeze()
	g.chestPairEv.freeze()
	g.chestSpillEv.freeze()
	g.dropsEv.freeze()
	g.xpEv.freeze()
	return g, nil
}
