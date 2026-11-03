# AGENTS.md

Rules for AI coding agents working in this repo. Also read the org-wide rules in
`../.github/AGENT_RULES.md` (online:
<https://github.com/helix-bedrock/.github/blob/main/AGENT_RULES.md>). Where they
disagree, this file wins for this repo.

## What this repo is

Helix, the Minecraft: Bedrock Edition server engine. It was restarted from
scratch on 2026-10-29 with its own architecture; the old Dragonfly fork is not
part of it. It builds on the Helix libraries (protocol, raknet, nethernet,
world, worldgen, entity, helix-data). When the engine needs something a library
should do, change the library.

## Commands

```bash
go build ./...
go vet ./...
golangci-lint run
go test -race ./...
go run ./cmd/helix   # start a server with the default configuration
go test ./world -run '^$' -bench RegionLayout -benchtime 1x   # 400 fake players per region layout (~4 min)
```

Local development uses a `go.work` (never committed) that points at the
sibling library folders, so unreleased library changes are used.

## Layout

- `helix` (root): `Server`, the main struct, and its `Config`.
- `network`: transports (NetherNet, RakNet) and the login sequence as named,
  replaceable steps, in BDS 1.26.60's order.
- `registry`: the one boot `Setup` (register, replace, delete) and the
  frozen tables. Blocks so far: dense state IDs, network hashes, a per-state
  property table swapped whole on live changes (collision boxes
  from helix-data's `bds/collision.json` included, plus BDS's shape rules
  that read only the state, in `collision.go` and `collision_more.go`;
  `CollisionComputed` marks the states that need the world, a block
  entity or the colliding entity: doors (`Door`), shulker boxes,
  scaffolding, powder snow, end gateways, piston parts), BDS's climbable flag
  and material per block, its liquid facts (reaction, can contain liquid,
  solid material; helix-data's `bds/liquids.json`), and `chunk.BlockRegistry`
  for the world library. Items: BDS's network IDs, versions and
  components, stack sizes and tags, and the ItemRegistry packet built once.
  Biomes: BDS's numeric IDs, climate and tags, `Resolve` for deleted or
  unknown saved IDs (plains). Entity types: identifiers, runtime IDs and
  behaviour pack definitions (engine hooks come with entities). Creative
  inventory and recipes are `Live` packets swapped on change, re-sent to
  online players. Unchanged, every packet is BDS's own; once a plugin
  changes biomes or entity types they are rebuilt (tests check the rebuild
  of vanilla is byte for byte BDS's). `Vanilla` loads helix-data.
- `liquid`: water and lava as BDS 1.26.60 (`Liquids`, the default
  `Config.BlockTicker`): the tick (depth from the neighbours, infinite
  water, falling, spreading along the shortest way down), lava solidifying,
  and the old `flowing_*` names turning into water and lava. Not ported:
  lava fire, drops of blocks liquids replace, stair and slab shape rules.
- `world.go`: opens the world folder and its stores (`world/mcdb`; the
  world and player stores can be replaced through `Config.Storage`) with
  the block registry.
- `world` (package): the world in memory. A `Dimension` per dimension,
  split into regions (RFC 0002): groups of sections (8 by 8 chunks) that
  each own their chunks and viewers and tick at 20 TPS on a shared
  `Scheduler`, earliest deadline first. Regions merge when they come
  within `MergeDistance` sections and split when their sections part,
  only between ticks: a region that must merge with one still ticking
  parks until it ends, so no tick waits. `RunAt` runs code on the owner
  of a chunk, loading it. `Entity` is the part every entity shares (IDs,
  position, rotation), owned by its region and indexed by chunk; a
  player's entity follows its viewer and sees every entity in BDS's
  57-chunk disc by default (`EntityViewRadius2`, changeable while running
  through `SetEntityViewRadius2`, never wider than the gap between
  regions; spawn packets on entering, RemoveActor on leaving), and
  each entity's movement is built once per tick for everyone who sees it.
  Other entities live in loaded chunks only: `Apply` refuses a move into
  an unloaded chunk, an entity that moved into another region's section
  is handed to it at the end of the tick, and one whose chunk unloads is
  unloaded with it (`EntityConfig.Unloaded`).
  Regions tick only the phases that exist so far (queued work, player
  input, chunk sending, block ticks, visibility, movement, one flush per
  player, saving). Block ticks (`ticks.go`) are BDS 1.26.60's: each chunk
  in a simulation zone ticks once per world tick, random ticks first
  (BDS's count and position generator, `RandomTickSpeed`), then its two
  queues (pending ticks and the random ticking queue, up to 100 each, in
  time then priority order, waiting while chunks within 8 blocks are not
  loaded, run only if the block type is still there). Blocks get their
  behaviour through `Config.Ticker` (`BlockTicker`): ticks, `Placed`
  (BDS's onPlace, from `Region.SetBlock` when the state changes) and
  `NeighbourChanged` (from `Region.UpdateNeighbours`, BDS's order -x +x
  +z -z +y -y, layer 0 then the second layer). Gameplay that changes a
  block calls `SetBlock` then `UpdateNeighbours`, as BDS's update flag.
  `SetBlock` of air on layer 0 moves a block's second layer (water in a
  waterlogged block) down, as BDS's chunk does. `Region.ScheduleTick`
  adds ticks; `Region.Local` keeps a system's per-region working state. The queues are saved with the chunk; the world tick clock
  lives on the `Scheduler` and in level.dat's currentTick. Tickets
  (`ticket.go`, RFC 0003): `Dimension.KeepLoaded(area, ticking)` holds
  every chunk of an `Area` (`Chunks`, `Box`, `Radius`) as a simulation
  zone does, claiming sections so regions form around it, until
  `Ticket.Release`; a ticking ticket's chunks get block ticks after the
  players' zones, a frozen one's only stay loaded. A region keeps its
  ticket holds in ticket order; they move with their sections on merges
  and splits, and the release goes to every region. A viewer whose centre
  moves more than `MergeDistance` sections from every section of its
  region (a teleport) is handed, with its player's entity and its queued
  work, to the region owning the new section or a new one, as entities
  are handed off, instead of merging the two regions. Loads, generation, network encoding and
  saves run on a worker pool, jobs for one chunk in order on one worker. Each player is a `Viewer`
  with two zones: the simulation zone is held loaded, the view zone is
  sent and released. Encoded chunks (`NetChunk`) are shared by every
  player and kept in a byte-budget cache. Changed chunks are saved at the
  end of each tick from a copy-on-write snapshot. Generators: `Flat`
  (level.dat's layers), `Empty`; vanilla generation is not hooked up, so
  an infinite world's unsaved chunks are empty and never saved.
- `setup.go`: `Setup`, the one boot builder: the registry's `Setup` plus
  `Player`, the player package's Setup. Vanilla registers through both
  first, then `Config.Setup`.
- `player` (package): everything a player is and does, one file per job.
  `host.go`: the `Host` the server builds once the world is open
  (`NewHost`, from the frozen player Setup); it admits players
  (`Admit`, with the duplicate and full checks on the global owner),
  finds and broadcasts to them, and disconnects and saves them on
  `Close`. `list.go`: the global owner (RFC 0002), one goroutine at 20
  TPS that owns the player list and publishes a copy for readers.
  `setup.go`: the packet handler table: `Handle[T]` replaces the handler
  of a packet type, `WrapHandle[T]` wraps it; system hooks (`Movement`,
  `WrapMovement`) swap one vanilla system without rewriting the packet;
  frozen into a slice indexed by packet ID. `input.go`: each player's
  ordered input queue (network goroutine to region tick, bounded, two
  swapped buffers), drained in the region's player input phase through
  the frozen handlers; vanilla's PlayerAuthInput handler hands movement
  to the movement hook. `join.go`: the join in BDS's order and the read
  loop. `send.go`: every packet to a player goes through `send` /
  `sendNow`, which check the write and log the first failure (a closed
  connection at debug level). `data.go`: the player's BDS data, loaded
  and saved. `view.go`: what other players see (AddPlayer, MovePlayer)
  and the synched actor data BDS sends a joining player about itself
  (gravity, collision, hitbox), which the client's physics run on.
  `zones.go`: the chunks published around the player. `container.go`:
  opening the inventory (Interact) and ContainerClose as BDS answers
  them, with BDS's window ID counter.
  `player.go`: `Player` = session (connection, identity, skin) + entity +
  saved data. Joins in BDS's order as far as built (PlayerList broadcast,
  StartGame, registry packets, full PlayerList), shows players to each
  other with AddPlayer and MovePlayer, and saves Pos, Rotation,
  DimensionId and UniqueID back into the player's BDS data on leave.
  `abilities.go`: BDS's layered abilities (base and spectator layers),
  permission levels and the per-game-mode rules, read from and saved in
  the player's BDS data; `attributes.go`: BDS's 17 player attributes in
  its join order. `SetGameMode` sends what BDS sends: UpdatePlayerGameType to all, then the abilities.
  `item.go`, `inventory.go`, `stackrequest.go`: the server-held inventory
  (BDS's saved format and join packets) and BDS's checks on stack
  requests (take, place, swap, destroy, creative items), applied all or
  nothing on a copy. The client's view of its items is never read back.
  `drop.go`: drops from stack requests and the legacy transaction, stricter
  than BDS: client items are compared, never kept (lead's decision, core
  architecture, not anti-cheat). `Give`, pick-up and held item updates.
  `itemactor.go`: item entities with BDS 1.26.60's tick (`DropItem`, `PickUp`
  systems), saved with their chunk in BDS's format. World gained
  `EntityConfig.Owner`, `Region.AppendEntitiesIn`, and for saving `EntityConfig.Save`
  and `Dimension.SetEntityLoader` (saved entities nothing takes, such as mobs, stay
  in the chunk's data untouched). `itemview.go`: the plugin API on an item:
  `SetVisibility` (per player; refused players get no packets and cannot pick it
  up), `SetView` (per player name and shown item, server unchanged), `SetStack`
  (the real item), `SetLifetime` and the server-wide `Host.SetItemLifetime`,
  events `OnItemDrop`, `OnItemLoad`. Rules are not saved and
  items with one never merge. Packets about an entity go through `Entity.Send`,
  which reaches only those who see it, never `Region.Broadcast`; the world's
  `EntityConfig.VisibleTo` and `SpawnFor` work for any entity.
  Armour and offhand take the items BDS allows (registry `Wearable`,
  `ArmorSlot`, `Offhand`, from helix-data's `item_equipment.json`).
  `crafting` (package): the recipes by network ID from the `CraftingData` packet and BDS's fit rule; knows nothing
  of players. `player/craft.go`: the crafting table's window and the craft actions of a stack request (`OnCraft`).
  `player/smith.go`: the smithing table's action, upgrades and armour trims (`OnSmith`; registry `Trims`, helix-data's `bds/trim.json`); `registry/smithing.go`: `EditSmithing` and `Smithing`, the
  plugin API over the recipes (clear, remove, change, for an item), boot or live; `registry/recipes.go`: `EditRecipes` and
  `Recipes`, the same for shaped and shapeless recipes of every block. `attach/`: torches, lanterns and ladders: which faces of each block hold things (helix-data `bds/support.json`), where they may go, which way they face, and falling off when the holder goes. `player/recipes.go`: `Player.EditRecipes`, rules taking recipes from one player, applied to its packet and checked on every craft. `-testcmds` adds /smithtest and /recipetest.
  `breaking.go`, `placing.go`, `mining.go`, `gameplay.go`, `event.go`:
  block breaking and placing as BDS 1.26.60 does them (research notes are
  private). PlayerAuthInput's block actions run before movement, in BDS's
  order (server-authoritative breaking: crack LevelEvents, reach 6.7 / 12
  to the block corner, progress per input tick, break at 0.99999, the
  resend of four blocks after a refused prediction; in creative and for
  blocks with no break time the block breaks as soon as breaking starts, as
  BDS's GameMode start does; after a block breaks, breaking stays on at that
  position, with a 250 ms cooldown before the next block makes progress; one
  extra beyond BDS as read so far: LevelEvent 3602 when the break rate changes,
  so the crack keeps up after landing). Every step is a hook
  in `Setup.Systems()` (BreakStart, BreakTime, ToolSpeed, BreakTick,
  BreakFinish, PlaceState, PlaceCheck, Place) to replace or wrap. The
  break speed is assembled from parts that can each be changed:
  the tool rule (helix-data's mining table), each enchantment's
  `ToolSpeed` (Efficiency) and `Underwater` (Aqua Affinity), each
  effect's `BreakSpeed` and `BreakRate` (Haste, Conduit Power, Mining
  Fatigue, applied twice as BDS does), and the player's `SetBreakSpeed`
  and `SetReachScale`. Enchantments and effects are registered entries
  (`RegisterEnchantment`, `RegisterEffect`). Per-block behaviour is set
  with `SetBlockBehaviour` (optional interfaces: `PlacementStater`,
  which wins over vanilla's placement; vanilla's is `placestate.go`:
  BDS's placement traits and getPlacementBlock rules per block from
  helix-data's `bds/placement.json`; `bucket.go` pours water and lava,
  `Replaceable`; without one, a block is built over if it carries BDS's
  replaceable component, from helix-data's `bds/break_place.json`). Held
  tools wear by BDS's rule per item from the same table (the `Wear`
  hook; Unbreaking is the enchantment's `DamageChance`), and break with
  the break sound. Effects count down each tick and are removed when
  they run out. `movestate.go`, `fall.go`, `hunger.go`, `health.go`:
  the movement states (sneak, sprint, swim, glide, crawl) from the
  client's start and stop input flags (BDS decides them in its own
  simulation, which belongs to the anti-cheat module), shown to others
  as actor flags; after each movement BDS's fall distance (cleared in
  water, on climbable blocks, gliding, slow falling) and fall damage, and
  BDS's exhaustion for moving, jumping, mining and damage (values from
  the player definition), burning saturation then hunger. `health.go`: BDS's hurt (armour and toughness, armour
  enchantments, each effect's `Resist`, 10 hurt ticks letting through only what is above the last hurt, absorption,
  armour wear). `hunger.go` also has the food tick: regaining health and starving. `attack.go`: players hitting players
  (BDS's checks, damage from the attribute, item, enchantments and effects, critical hits, knockback, item wear, arm
  swing relay); numbers from helix-data's `bds/item_combat.json`. `shield.go`: BDS's shield (`ShieldUp`, `ShieldBlock`, `OnPlayerBlock`, actor flags in the second set), `totem.go`: its totem (`Totem`, `OnPlayerTotem`, asked before health reaches 0 so those who see the player never see it dead), `armoruse.go`: using armour swaps it with what is worn (`EquipArmor`, `OnArmorEquip`). Effects have `Added`, `Removed` and `Tick` hooks (Regeneration, Absorption). `eat.go`: items used in the air, so far foods, read from each
  item's own data. `bucket.go` fills and pours; `world.Region.SetBlock` keeps a water source in the second layer of a block
  that can hold it. All are `Systems` hooks (MoveFlags, Jump, Fall, FallDamage, MoveCost, Hurt, ReduceDamage, Exhaust,
  FoodTick, Attack, AttackDamage, Knockback, UseItem, FoodOf, Eat), with events `OnPlayerAttack`,
  `OnPlayerHurt`, `OnItemUse`, `OnItemEat`. `tuning.go`: every number of hunger, health, armour, falling and knockback as a `Tune`, read in three levels: the
  player's own (`Player.SetTuning`, `ClearTuning`, `ClearAllTuning`; not saved), the server's while running (the same on `Host`
  and `Server`), the default (vanilla's, or `Setup.SetTuning`). Vanilla's systems read `Player.Tuning` instead of constants; a new
  number of these systems gets a Tune too. `-testcmds` adds /tune. `death.go`: BDS's death (`Die`: items thrown unless
  `keepinventory`, death event and message) and its Respawn packet sequence
  (`Respawn`), a first pass without the spawn search. Events: `OnBlockBreak`, `OnBlockPlace` (cancellable),
  on the minimal event core (named priorities, registration order, no
  allocation without listeners, panics recovered per plugin). Block
  changes, and any packet given to `Region.Broadcast`, reach every
  player whose client has the chunk, in other regions too.
- `cmd/helix`: the server program. `-testcmds` adds a stand-in /gamemode
  (and /gm), listed to the client with an AvailableCommands packet on its
  first input, for in-game testing until commands are designed, a countdown
  name tag over dropped items through an item view, and /itemtest to try the
  item API (private drops, lifetimes per player's drops and server-wide).
- `player/enchant.go`: `Enchantment` (every vanilla one from helix-data's `bds/enchantments.json`, on the struct plugins use; a table swapped whole for `Host.EditEnchantment`, `SetEnchantment`, `RemoveEnchantment`; read through `gameplay.enchant`), BDS's add rule (one activation list compared, same level goes one up), rules on `Setup` and `Host` (`SetItemEnchantable`, `SetItemRepair`, `SetEnchantPathBlock`, `SetEnchantBookshelf`, `SetEnchantMaterial`). Nothing reads a vanilla ID: flags on the entry (`Knockback`, `SilkTouch`, `Fortune`, `StaysWorn`, `VanishesOnDeath`, `Curse`, `Mends`), so a removed enchantment does nothing. `Systems.EnchantRequire` decides level and lapis; when it differs from what the client takes, the lapis slot is settled after the request and resent. `player/enchanttable.go`: BDS's Random (MT19937), seed, bookshelves, costs and draw, the table's window and action (`Systems.EnchantOptions`, `OnEnchantOptions`, `OnEnchant`, `Host.EnchantItem` for later loot). `player/anvil.go`: anvil (`Systems.Anvil`, `OnAnvil`; `Systems.AnvilWear` and `TuneAnvilWearChance` for its wear) and grindstone (`Systems.Grindstone`, `OnGrindstone`) worked out by the server alone, BDS's repairable component rules read from the item registry, and Mending. A craft's level, seed and experience changes wait in `craftState.after` until the whole request passes. `-testcmds` adds /enchanttest.
- `blockuse`: what vanilla doors, trapdoors, gates, levers and buttons do when clicked,
  with its `Ticker` for buttons popping out and doors breaking.
- `direction`: every direction beyond `cube`'s faces and positions: `Cardinal` and the numberings block states use for it, the orders BDS walks a block's sides in (`Updates`, `Spread`, `Clockwise`, `Sides`), yaw to direction, axes and BDS's table sine. Systems name what they mean here instead of keeping tables of numbers; a test pins each numbering and order to BDS's.
- `connect`: the arms of fences, panes and bars, kept in their block states since 1.26, from helix-data's rules.
- Block entities: `world/blockentity.go` keeps them with their chunk (`BlockEntity`, optional
  `BlockEntityTicker`, `BlockEntityPlacer`, `BlockEntityRemover`; `Dimension.SetBlockEntities` names who
  makes them; those it does not take stay saved as they were). `player/chest.go` is the only one run so far:
  chests and ender chests (items in `Inventory.ender`), BDS 1.26.60's pairing, lid and window, with `Inventory.open` as a stack request's copy of the open
  chest (container `ContainerLevelEntity`). `player/chestapi.go` is their plugin API: events `OnChestOpen`,
  `OnChestClose`, `OnChestItem` (per slot a request changes, after BDS's checks; cancelling refuses the whole
  request, changes make the player get its items resent), `OnChestPair`, `OnChestSpill`, and methods on `Chest`
  and `Player`, which fire no events. `player/blockdrop.go`: what a player's break drops (`Systems.Drops`, from
  the `loot` package, BDS's rule per block), `OnBlockDrops`, `Setup.SetBlockDrops`.
  `player/xporb.go`: experience orbs (`XPOrb`, BDS's sizes, merging, pull and touch; saved with the chunk) and
  levels (`AddExperience`, `OnExperience`). `-testcmds` adds /chesttest to try the chest events in game.

## Design

The engine's design is in the "Engine design" section of
`../.github/API_DESIGN.md` (registry, world in memory, regions, players and
entities, events, storage, build order). Read it before adding anything. In
short:

- One `Setup` at boot; blocks, items, biomes and entity types are frozen
  then; behaviour and properties can be replaced or wrapped while running.
- Two zones per player: the simulation zone is loaded and ticked, the view
  zone is sent and released. Client-side chunk generation on for vanilla
  worlds.
- Regions per RFC 0002, with parallel work inside crowded regions and
  fixed-order apply; encode once, send to many.
- A player is session + entity + profile.
- Events: priorities `First` to `Last` then `Monitor`, no allocations,
  parallel listeners by default.
- Storage: one BDS-layout LevelDB per world by default, pluggable stores.

Build order: storage (`world` library), registry, world in memory, regions,
players (replaces `play.go`).

## Invariants: do not break these

1. Vanilla behaviour is one-to-one with BDS 1.26.60 (redstone included,
   required). Extra checks beyond BDS are allowed only when they never refuse
   what BDS accepts, and are documented as such.
2. Every login refusal uses BDS's disconnect reason for that case.
3. Never panic. Bad input from clients returns an error.
4. No felt lag with 400 players in one spot: no steady-state allocations on
   hot paths, no waiting inside a tick, proven by benchmarks.
5. Never assume the player is right. What a client says is a request, not
   the truth: the server's state decides, the way BDS does it, with BDS's
   own checks and corrections. This is not an anti-cheat: no checks beyond
   BDS's by default and nothing strict. An anti-cheat comes later as a
   separate, optional module.

## Gotchas

- Topics `API_DESIGN.md` does not mark as decided (event list, custom
  events, entity visibility API, commands, permissions, config file) are
  designed with the owner first; ask before adding public packages for them.
