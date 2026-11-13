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
go run ./cmd/sim -list                          # load test scenarios
go run ./cmd/sim -scenario tnt -n 0,100,1000    # one scenario at several sizes, each on a fresh server
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
  lava fire, stair and slab shape rules. `settle.go` is Helix's own rule, not BDS's: as
  `world.Config.Settler` it finds liquid generation left beside open space
  (below or on a side) and queues its tick, inside the chunk on the loading
  worker, at the chunk's edge on its ticks once the next chunk is loaded. It
  runs on every chunk that loads, generated or saved, and skips liquid
  already ticking.
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
  Other entities live in loaded chunks only, and think and take input only
  while their chunk ticks, as BDS ticks actors (`EntityConfig.TickAnywhere`
  lifts that): `Apply` refuses a move into
  an unloaded chunk, an entity that moved into another region's section
  is handed to it at the end of the tick, and one whose chunk unloads is
  unloaded with it (`EntityConfig.Unloaded`).
  Regions tick only the phases that exist so far (queued work, player
  input, chunk sending, block ticks, visibility, movement, one flush per
  player, saving). Crowded regions spread their work over idle cores with
  `Region.Parallel` (`parallel.go`; `Config.ParallelMin`, `ParallelHelpers`,
  `Dimension.SetParallel`): every entity's `EntityConfig.Think` (mob AI,
  players' `Setup.Think` steps, the physics of items, orbs, TNT, falling
  blocks, projectiles, fireworks, carts and boats), each player's
  visibility scan, movement fan-out, block change packets and zone
  planning, random tick planning (`randomplan.go`), relighting changes far
  apart (`lightparallel.go`) and redstone's separate circuits. A parallel
  item only reads the region and changes its own state; everything else is
  applied afterwards in a fixed order, so results are the same on one core
  and many. Handlers that change blocks (queued ticks, random ticks
  themselves, block entities, weather) still run in BDS's order. Block ticks (`ticks.go`) are BDS 1.26.60's: each chunk
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
  end of each tick from a copy-on-write snapshot; every `Config.AutoSaveTicks`
  (30 s by default, spread over the ticks by chunk) each loaded chunk also
  saves its entities and tick queues, and online players are saved on the
  same interval (`Player.autoSave`), so a crash loses at most that much.
  Tick rates (`tps.go`): `TickMeter` (TPS and MSPT over the last 100 ticks,
  falling at once when ticks stop), `Region.TPS`/`MSPT`, `RegionInfo.TPS`,
  `Dimension.TPS` (mean of its regions); `Server.TPS` is the mean of every
  region, `Player.TPS` the client's own tick rate (its PlayerAuthInput
  arrivals), `Player.RegionTPS` the player's region's. `-testcmds` adds /tps.
  Generators: `Flat`
  (level.dat's layers), `Empty`, any `Generator`; a `ColumnGenerator` also
  hands over block entities, a `Spawner` answers the spawn height.
- `generator`: the world types `Config.WorldType` picks by name (`Types`,
  `Default`: vanilla, flat, void; plugins add their own `Type`). `Vanilla`
  is worldgen's 1.26.60 overworld built over the registry's own states
  (`blocks.New`), so its indices are Helix state IDs; block tags and
  translucency come from the registry (`Blocks.Tags`, `Properties.Translucency`).
  `Nether` and `End` (dimensions.go) are worldgen's Nether and End the same way;
  `End.ExitPortal` hands the engine the podium shape, which generation never places.
- `portal`: nether portal frames (BDS's PortalShape), lighting and breaking (`Ticker`
  wraps the fire ticker for neighbour changes), the `portals` level key records, finding
  or building an arrival portal (`Portals.Find`, `Make`), the End's arrival platform and
  the end portal frame ring. `end.go` (root) builds the End's exit portal on the first
  arrival.
- `properties.go`: `Config.LoadProperties`, the server settings file in
  BDS's server.properties format (`level-type`, `level-type-options` are
  Helix's own keys); cmd/helix reads it (`-config`), flags given on the
  command line win.
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
  to the movement hook; after a MovePlayer teleport its input is skipped until the client
  acknowledges it (InputFlagHandledTeleport, or 40 inputs at most: `teleportwait.go`, Helix's
  own rule), so input sent before the teleport does not pull the player back. `join.go`: the join in BDS's order and the read
  loop. `send.go`: every packet to a player goes through `send` /
  `sendNow`, which check the write and log the first failure (a closed
  connection at debug level). `data.go`: the player's BDS data, loaded
  and saved. `view.go`: what other players see (AddPlayer, MovePlayer)
  and the synched actor data BDS sends a joining player about itself
  (gravity, collision, hitbox), which the client's physics run on.
  `zones.go`: the chunks published around the player. `window.go`:
  the player's window (BDS's window ID counter, `showWindow`, `closeWindow`), opening the inventory
  (Interact) and ContainerClose as BDS answers them, and the container events (`OnContainerOpen`,
  `OnContainerClose`, `OnContainerItem`). Every window opens through `mayOpen` and `showWindow`.
  `player.go`: `Player` = session (connection, identity, skin) + entity +
  saved data. Joins in BDS's order as far as built (PlayerList broadcast,
  StartGame, registry packets, full PlayerList), shows players to each
  other with AddPlayer and MovePlayer, and saves Pos, Rotation,
  DimensionId and UniqueID back into the player's BDS data on leave.
  `abilities.go`: BDS's layered abilities (base and spectator layers),
  permission levels and the per-game-mode rules, read from and saved in
  the player's BDS data; `attributes.go`: BDS's 17 player attributes in
  its join order. `SetGameMode` sends what BDS sends: UpdatePlayerGameType to all, then the abilities.
  `item.go`, `playeritems.go`, `stackrequest.go`: the server-held items
  (BDS's saved format) and BDS's checks on stack
  requests (take, place, swap, destroy, creative items), applied all or
  nothing on a copy. The client's view of its items is never read back. A refused request resends
  all the player's slots (Helix's own rule, beyond BDS) so a client out of step recovers.
  `inventory.go`: the `Inventory` wrapper every set of slots is reached through, and the only code
  that writes InventorySlot and InventoryContent, through the per-player view (`OnInventoryView`,
  `Inventory.SetView`). Packets written in many places have one wrapper each so a protocol change is
  made once: `sound.go` (`playSound`, `soundEvent`, `blockSound`), `entitydata.go` (`actorData`,
  `actorEvent`), `send.go` (`blockPos`, own-slot sends). Add to these rather than writing those
  packets by hand.
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
  events `OnItemDrop`, `OnItemLoad`. Rules are not saved; items with a
  visibility rule or a different lifetime never merge, a view does not stop a merge. Packets about an entity go through `Entity.Send`,
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
  swing relay); numbers from helix-data's `bds/item_combat.json`. `shield.go`: BDS's shield (`ShieldUp`, `ShieldBlock`, `OnPlayerBlock`, actor flags in the second set), `totem.go`: its totem (`Totem`, `OnPlayerTotem`, asked before health reaches 0 so those who see the player never see it dead), `armoruse.go`: using armour swaps it with what is worn (`EquipArmor`, `OnArmorEquip`). Effects (`player/effects.go`, `player/effect_vanilla.go`): all 38 of BDS 1.26.60's effects registered (ID, name, harmful, colour); hooks `Added`, `Removed`, `Tick`, `Apply` (instant), `Died`, `Hurt`, `Modifiers` (attribute modifiers on value or maximum: Speed, Slowness, Health Boost); `GiveEffect` merges as BDS's StoredEffects (stronger takes over, weaker longer one waits), `EffectInfinite`, `ApplyInstantEffect` (splash scale), `ClearEffects`; saved as BDS's ActiveEffects; Invisibility flag and the visible effects actor data. `commands/effect.go` is /effect. Potions (`player/potion.go`, `player/potioncloud.go`): BDS 1.26.60's 49 potions by ID (the item's meta; `RegisterPotion`, `Host.Potion`), drinking (32 ticks, effects, glass bottle), splash potions (thrown_potion_effect: effects on players within 4 blocks, scaled by distance; douse_fire for water) and lingering potions (spawn_aoe_cloud: `AreaEffectCloud`, saved with its chunk as BDS saves it). Effects reach players only; mobs have none yet. `eat.go`: items used in the air, so far foods, read from each
  item's own data. `bucket.go` fills and pours; `world.Region.SetBlock` keeps a water source in the second layer of a block
  that can hold it. All are `Systems` hooks (MoveFlags, Jump, Fall, FallDamage, MoveCost, Hurt, TakeHit, HitParts, Exhaust,
  FoodTick, Attack, AttackParts, Knockback, UseItem, FoodOf, Eat), with events `OnHit`,
  `OnItemUse`, `OnItemEat`. `hit.go`: every hurt is a `Hit` of parts (Bonuses, Reductions, After; each
  `Off` or `Scale`d, plus `PartCustom`), built by AttackParts/HitParts, changed in one `OnHit`, resolved in
  BDS's order; `Player.EditEnchantment` gives one player its own copy of an enchantment. `elytra.go`: gliding (`Systems.StartGlide`, `GlideTick`, `GlideCrash`, `OnGlide`): BDS's start and stop rules, elytra wear, wall crashes. `firework.go`: firework rockets (`FireworkRocket`, `Host.LaunchFirework`, `Systems.Firework`, `OnFirework`): use while gliding (glide boost by `MovementEffect`), use on a block, dispensers, the explosion's particles and damage. `portal.go`: the player's portal time and cooldowns, moving between dimensions (ChangeDimension), arriving at a found or built portal, eyes of ender in end portal frames, lighting nether portals. `tuning.go`: every number of hunger, health, armour, falling and knockback as a `Tune`, read in three levels: the
  player's own (`Player.SetTuning`, `ClearTuning`, `ClearAllTuning`; not saved), the server's while running (the same on `Host`
  and `Server`), the default (vanilla's, or `Setup.SetTuning`). Vanilla's systems read `Player.Tuning` instead of constants; a new
  number of these systems gets a Tune too. `-testcmds` adds /tune. `death.go`: BDS's death (`Die`: items thrown unless
  `keepinventory`, death event and message) and its Respawn packet sequence
  (`Respawn`), a first pass without the spawn search. Events: `OnBlockBreak`, `OnBlockPlace` (cancellable),
  on the minimal event core (named priorities, registration order, no
  allocation without listeners, panics recovered per plugin). Block
  changes, and any packet given to `Region.Broadcast`, reach every
  player whose client has the chunk, in other regions too.
- `sim`: the load test harness (PRIORITIES item L). `Start` runs a real `helix.Server`
  (flat world by default, temporary folder, offline) with a square held loaded and ticking
  by a ticket; a `Scenario` adds one kind of load (`Start` once, `Tick` every 50 ms for
  loads that end by themselves) or bots (`Bot`, n bots each on its own 50 ms client tick).
  Bots (`bot.go`) log in like vanilla clients over `network.Memory` (the in-process
  transport, given to the server through `Config.Transports`), decode only the packets
  they need, and time each block action until its block update arrives. `Run`/`Sweep`
  warm up, measure through the world's tick observer (`Dimension.SetTickObserver`,
  `TickStats`, the `Phase` constants) and runtime/metrics, and give a `Report` (region
  tick median/p99/worst, late ticks, time per phase, cost per unit, allocations per tick,
  GC CPU share, live heap, action delays, moves seen per bot). Scenarios: idle, items,
  tnt, falling, fireworks, mobs, clouds, redstone (observer clocks), players, breakers,
  miners, explorers (vanilla terrain: generation, saving, reloading). A mechanic that
  can lag a server gets a scenario here. `cmd/sim` runs them and prints a table (`-json`,
  `-log`, `-memprofile`). Region ticks are timed with `world/clock*.go`'s fine clock:
  Go's own clock moves in 0.5 ms steps on Windows.
- Packet path: the movement fan-out (`Region.encodeOutbox`) encodes each move once per
  tick with a `packet.RawWriter` and every player whose connection takes raw bytes
  (`Viewer.SetRaw`, `network.Conn.RawOK`: latest version, no packet-out hook) gets the
  same bytes; the others get the packet. PlayerAuthInput structs are reused: the
  connection reads into released ones, and the region tick releases each packet after
  its handler (`Player.releasePacket`), so a handler must not keep a packet.
- `cmd/helix`: the server program. `-tick-distance` sets `Config.TickDistance`. `-testcmds` adds a stand-in /gamemode
  (and /gm), listed to the client with an AvailableCommands packet on its
  first input, for in-game testing until commands are designed, a countdown
  name tag over dropped items through an item view, and /itemtest to try the
  item API (private drops, lifetimes per player's drops and server-wide).
- `player/enchant.go`: `Enchantment` (every vanilla one from helix-data's `bds/enchantments.json`, on the struct plugins use; a table swapped whole for `Host.EditEnchantment`, `SetEnchantment`, `RemoveEnchantment`; read through `gameplay.enchant`), BDS's add rule (one activation list compared, same level goes one up), rules on `Setup` and `Host` (`SetItemEnchantable`, `SetItemRepair`, `SetEnchantPathBlock`, `SetEnchantBookshelf`, `SetEnchantMaterial`). Nothing reads a vanilla ID: flags on the entry (`Knockback`, `SilkTouch`, `Fortune`, `StaysWorn`, `VanishesOnDeath`, `Curse`, `Mends`), so a removed enchantment does nothing. `Systems.EnchantRequire` decides level and lapis; when it differs from what the client takes, the lapis slot is settled after the request and resent. `player/enchanttable.go`: BDS's Random (MT19937), seed, bookshelves, costs and draw, the table's window and action (`Systems.EnchantOptions`, `OnEnchantOptions`, `OnEnchant`, `Host.EnchantItem` for later loot). `player/anvil.go`: anvil (`Systems.Anvil`, `OnAnvil`; `Systems.AnvilWear` and `TuneAnvilWearChance` for its wear) and grindstone (`Systems.Grindstone`, `OnGrindstone`) worked out by the server alone, BDS's repairable component rules read from the item registry, and Mending. A craft's level, seed and experience changes wait in `craftState.after` until the whole request passes. `-testcmds` adds /enchanttest.
- `fall`: gravity for blocks as BDS 1.26.60 (`Table`, `Ticker` in the default chain under
  `circuit`): sand, red sand, gravel, concrete powder, anvils and the dragon egg fall when the
  block under them is free to fall into (lowest of a column first), snow layers when the block
  right under them changes; landing places the block, or adds snow to snow (rest on top).
  `player/fallingblock.go` is the entity (`FallingBlock`, `Host.StartFalling`,
  `SpawnFallingBlock`, `OnBlockFall`; breaks into its item when it cannot land, saved with its
  chunk). `fall/scaffolding.go`: scaffolding's stability (0 on a block that holds things on top,
  the same as the scaffolding below, else one more than the steadiest side neighbour, up to 7),
  kept in its state on place and neighbour changes; at 7 a new one falls and a settled one breaks
  with its drops (`Ticker.SetBreak`), so a tower whose base goes comes down. `player/scaffolding.go`
  is the item: using it on scaffolding builds on top of the column. Not done: anvil damage and
  landing sound, concrete powder hardening, pointed dripstone, suspicious sand and gravel,
  entities that break falling blocks.
- `blockuse`: what vanilla doors, trapdoors, gates, levers and buttons do when clicked,
  with its `Ticker` for buttons popping out and doors breaking.
- `direction`: every direction beyond `cube`'s faces and positions: `Cardinal` and the numberings block states use for it, the orders BDS walks a block's sides in (`Updates`, `Spread`, `Clockwise`, `Sides`), yaw to direction, axes and BDS's table sine. Systems name what they mean here instead of keeping tables of numbers; a test pins each numbering and order to BDS's.
- `circuit` (written from BDS's code and not fully tested: only the basics have been tried in game, nothing compared with BDS): vanilla's redstone blocks on the `redstone` library, one System per region (`Ticker`, a `world.RegionTicker`); its components move with their chunks on merges and splits. `comparator.go` has what wire connects to, what comparators read (`SignalEntity` for block entities that work it out themselves) and the comparator's block entity (`Ticker.Entities` wraps the world's list). `piston.go` has pistons and the blocks they move, as block entities (`SetEntityPush` is how they move entities: `Host.PistonPush` moves a player on the server and sends `CorrectPlayerMovePrediction`); `TakeBlockEntity` moves one without spilling it. Hoppers, dispensers and droppers are in `player/machine.go`, as `Chest`s with a `machineSpec`, since they share the chest's slots and window.
- `player/bed.go`: beds and straw beds (`bedEntity`, the colour block entity; placing; using; sleeping,
  the sleep counter and the night skip on the global owner's tick; the bed respawn point, saved under
  BDS's Spawn keys with its dimension). `player/respawnanchor.go`: respawn anchors (glowstone charges
  to 4, a charged one sets the spawn in the Nether and explodes elsewhere when respawn blocks
  explode, calmed by water around it; respawning takes a charge and stands the player by BDS's
  search on solid blocks, helix-data's `bds/block_solid.json`; only a player who dies in the
  Nether respawns at it, as BDS checks the spawn against the dimension of death). `player/shelf.go`: shelves (`Shelf` block entity saved as BDS's "Shelf",
  `ShelfAt`; clicking the front swaps the held item with a slot; a powered one swaps its whole row, up to
  three shelves facing the same way, with the hotbar: `player/shelfrow.go`, rows linked by
  `circuit/shelf.go` on power changes; BDS's row repair without a power change is not ported). `blockuse/bed.go` has the halves (`BedOther`, `BedHeadPos`) and the blockuse
  Ticker removes a half whose other half went. `player/cushion.go`: cushions (`Cushion`, placing,
  breaking, the rest check, riders kept across leaving and restarts as `LinksTag`, `reseat` on join).
- `player/bookshelf.go`: chiseled bookshelves (`Bookshelf` block entity saved as BDS's
  "ChiseledBookshelf", `BookshelfAt`; clicking a slot on the front takes its book or puts in one
  of the held item tagged minecraft:bookshelf_books, `books_stored` shows them, the comparator
  reads the last slot used plus one, books drop when it goes). `player/lectern.go`: lecterns
  (`Lectern` block entity saved as BDS's "Lectern", `LecternAt`; a book and quill or written book
  goes on an empty one, clicking one with a book opens window type 25, LecternUpdate sets the
  page and gives a redstone pulse through `circuit/lectern.go`, hitting or breaking it knocks the
  book off, the comparator reads how far through the book it is). `player/book.go`: writing in
  books and quills and signing them (BookEdit on the packet's inventory slot: replace, add, delete
  and swap pages, 50 at most; signing makes one written book with author, xuid, title, generation 0). `player/lightningrod.go` and
  `weather/rod.go`: lightning rods (a strike goes to the nearest rod within 64 blocks that is
  open to the sky and rained on; a struck rod gives power for 8 ticks, `circuit/lightningrod.go`;
  the strike cleans oxidation off the copper it stands on and copper around it, `player/copper.go`;
  Channeling tridents on a rod call lightning). Copper ageing, axes and waxing are not built.
- `player/void.go`: the void as BDS's OutOfWorldSystem (below the dimension's min height minus 16; `Systems.OutOfWorld`, `Systems.Void`, `OnVoid`, `TuneVoidDamage`, `OutOfWorlder` for an owner's own answer). Every entity's tick calls `Host.checkVoid`; a new entity kind calls it too.
- `player/explosion.go`: BDS's explosion as a struct anyone can run (`Explosion`, `Host.Explode`, `Systems.Explode`, `OnExplosionStart` (before anything), `OnExplosion` (once the blocks are known, `Keep`), `OnExplosionHit` (per entity reached), what set it off (`Kind`, `FromBlock`, `Block`, `BlockPos`, `Attacker`), block behaviour `Exploded`, entity owner `Blastable`). `player/primedtnt.go`: lit TNT (`PrimedTNT`, `Host.SpawnTNT`, `OnTNTPrime`, saved with its chunk) and vanilla's TNT block as a block behaviour.
- `circuit/config.go`: `Config`, every number redstone runs by, BDS's by default (`DefaultConfig`), and `Settings`, which holds it for the regions to read while `Server.SetRedstone` swaps it. A new redstone number goes here, not in a constant. The circuit library's own numbers are `redstone.Rules`.
- `player/fishing.go`: fishing rods and the fishing hook as BDS 1.26.60 (`FishingHook`, `Player.FishingHook`, `OnFishCatch`): casting and reeling in (rod wear 3 for an entity, 1 for a catch, 2 in the ground), floating and bobbing, the lure and bite timers with their bubbles (Lure, rain, ocean, sky), hooking players and mobs, the catch from the hook's loot table (jungle in jungles, Luck of the Sea and luck) with experience. `player/motion.go`: `Moving` (`Velocity`, `SetVelocity`, `Pull`) on players and mobs, and `PullVelocity`, BDS's pull toward a point; use it for any pull that is not a hit's knockback. `player/attach.go`: `AttachPoint` and `Player.AttachPos`, BDS's attach points (ActorLocation) on a player: feet, body, weapon, head, drop, explosion, eyes, breathing, mouth; start and aim things from these, not the eyes (hook cast and throw sounds from the drop point, bow sound from the weapon point, pulls toward the head). Mobs have none yet. `player/itemstorage.go`: storage items (any item with minecraft:storage_item; vanilla's are the bundles), as BDS 1.26.60: what one holds lives in its stack (`ItemStack.Stored`), saved as BDS's storage_item_component_content + bundle_weight, sent with a runtime bundle_id; stack requests on container 63 (dynamic) with BDS's weight, slot, banned/allowed and nesting rules, checked on the request's copy; the client is sent each one's slots in window 125 (InventoryContent once, then InventorySlot per change) as BDS's DynamicContainerTracker; a destroyed one on the ground spills. Storage rules come from helix-data's item definitions (`registry.ItemType.Storage`).
- `player/projectile.go`: everything shot or thrown as one `Projectile` with a `ProjectileSpec` read from its definition (`Host.SpawnProjectile`); `throwItem`, `releaseBow` and `dispenseProjectile` start them. Like every entity that is not a player's, they stop where chunks do not tick, as BDS's do.
- `circuit/target.go`: target blocks (`Ticker.HitTarget`, reached by `Host.HitTarget`); projectiles call it once they exist, and `-testcmds` adds /targettest.
- `daytime`: the overworld clock (`Clock`, worked out from the world tick so any core reads it without a lock), BDS's sun angle and sky darkening, and what a daylight sensor reads. `world/light.go`: sky and block light as BDS 1.26.60's relighter and height map, kept per sub chunk (`Region.Light`, `SkyLight`, `BlockLight`, `Brightness`; `Config.Light` and `Config.Sky` replace the rules), worked out on the loading worker and relit at the end of the tick after block changes. `circuit/daylight.go`: daylight sensors' block entity. `circuit/button.go`: buttons breaking off and arrows on wood buttons. `player/clock.go`: SyncWorldClocks.
- `player/furnace.go`: furnaces, blast furnaces and smokers, as a `Chest` with a `machineSpec` and a `Furnace` (tick, fuel, smelting, lit swap through `world.BlockEntityKeeper`, experience, hopper sides, window data); `crafting.Table.Smelt` finds their recipes.
- `player/campfire.go`: campfires and soul campfires as BDS 1.26.60 (`Campfire` block entity, `CampfireAt`, `OnCampfireCook`): food on four slots cooks 600 ticks while lit and drops out cooked, flint and steel or Fire Aspect lights, shovels put out, a lit one burns players and mobs (1, soul 2, then 8 s on fire; Frost Walker boots stop it), something burning lights an unlit one. `fire/campfire.go`: lighting, putting out and water (`LightCampfire`, `DouseCampfire`, `SetCampfireWet`).
- `player/breath.go`: breathing and drowning. Players run the entity library's minecraft:breathable component (`state.Breathable`, the same one mobs run, `TickFor` with a `state.Breather`), from the player definition: 15 s of air, 2 damage a second once gone (drowning with the head under water, else suffocation), 3.75 s to breathe back in; Respiration, Water Breathing, Conduit Power; air saved under BDS's `Air`, sent as actor data with the breathing flag (`Player.Breathable`, `Setup.SetBreathable`).
- `player/shears.go`: shears carving pumpkins (one seed, wear 1). `player/mobinteract.go`: using an item on a mob runs its minecraft:interact (`mob.Mob.Interact`) and does the item work (hurt_item, use_item, spawn_items, play_sounds, swing): shearing sheep, snow golems, bogged, mooshrooms. Beehives and dispensers shearing are not built.
- `player/mobbucket.go`: buckets past water and lava, as BDS 1.26.60's BucketItem: a water bucket takes cod, salmon, tropical fish, pufferfish, axolotls and tadpoles and an empty one the sulfur cube (the mob's group stack and health go in the bucket's tag, `mob.Mob.Stack`/`Restore`); pouring brings the mob back (with water for water animals); powder snow buckets; milk (drunk in 32 ticks, clears effects) and milking through minecraft:interact's transform_to_item. `bucket.go`'s water boils away in the Nether.
- `player/mace.go`: the mace (`MaceBonus` in the base damage; after a hit with over 1.5 blocks of fall: the fall stops, smash sound and dust, nearby players and mobs pushed away). Wind Burst not built.
- `player/trident.go`: tridents (thrown as thrown_trident carrying the item, saved under `Trident`; Riptide sound only, the launch is the client's; Loyalty return, Impaling, Channeling). `ProjectileSpec.StopOnHurt` reads stop_on_hurt.
- `player/brewing.go`: brewing stands as BDS 1.26.60, as a `Chest` with a `machineSpec` and a `BrewingStand` (`Chest.BrewingStand`, `Systems.BrewFuel`, `Systems.BrewResult`, `OnBrew`): blaze powder gives 20 brews, a brew takes 400 ticks and stops when the ingredient changes, every bottle with a mix becomes its result, the block shows the bottles, window data, BDS's slot rules for the window and hoppers (top: ingredient; sides: bottles or fuel; bottom takes bottles). `crafting/brewing.go`: the recipes from the CraftingData packet (`Table.Brew`, `HasBrew`, `BrewIngredient`, `BrewBottle`), BDS's PotionBrewing mix order. Drinking potions is not built.
- `commands/`: Helix's built-in commands, one file each (`give.go`), public API only, `Register` called by `server.go` for the owner "minecraft". New ones go here as their own file plus an entry in `all`.
- `command/`: the command system, no player or world imports: `Spec`/`Define` (struct overloads read by reflection in `params.go`), `Builder`, `Manager` (copy-on-write table behind an atomic pointer, `Register` by owner, `Wrap`, `Execute` with panic recovery), `parse.go` (scanner, selectors, overload matching, BDS's error keys from en_US.lang), `packet.go` (per-source AvailableCommands, soft enums), `vanilla_enums.go` (generated from a dump of BDS 1.26.60's enums: regenerate, do not edit). `player/commands.go`: `CommandSource`, `SourcePlayer`, selectors over `Player.State` (`player/seen.go`, a seqlock any region may read, published at the end of each player tick), the Item/Block/EntityType/Biome lists, sending the list and handling CommandRequest. `cmd/helix/testcmd_register.go` puts the `-testcmds` stand-ins on it.
- `weather`: BDS's weather: `Weather` (rain and thunder levels, timers, /weather, sleep clearing it, level.dat's four values) ticked on the player package's global owner, and `Ticker`, the overworld's `world.Config.Weather` (lightning, ice, snow on every ticking chunk; `RainingAt` and friends, which farm, fire and players read). `player/lightning.go` is the bolt, `player/fog.go` the /fog stack, `commands/weather.go` and `commands/fog.go` the commands.
- `fire`: fire and soul fire (`Ticker`, wrapping `liquid` in the default ticker chain; `Rules` from level.dat, `PrimeFunc` for TNT) and lava setting fire on random ticks. `player/fire.go`: flint and steel (`lightFire`) and putting fire out when breaking starts (`putOutFire`). Block flags fire reads come from `registry.Properties` (`FlameOdds`, `BurnOdds`, `LavaFlammable`, `Material`) and `registry.Biome.Humid`.
- `player/sign.go`: signs and hanging signs (`signEntity`, BDS's FrontText/BackText/IsWaxed block entity, placing for both items, `useSign` with dyes, glow ink, ink and honeycomb, the editor lock and `handleBlockActorData` for the client's edit, `SignAt`, `SetSignAt`); `attach` holds standing and wall signs like banners and ceiling hanging signs like lanterns.
- `player/banner.go`: banners (`bannerEntity`, BDS's Base/Type/Patterns block entity, placing, drops through `blockEntityDrops`, `BannerAt`, `SetBannerAt`, `BannerTable` from helix-data's `bds/banner.json`, changed through `Setup.Banners`); `attach` holds them (solid material below or behind). `player/loom.go`: the loom's window and action (`Systems.Loom`, `OnLoom`), UI slots 9, 10, 11. `player/shieldbanner.go`: BDS's shield + banner special recipe (`Systems.ShieldBanner`, `OnShieldBanner`), reached through `crafting.Table.Special` (multi recipes by network ID to UUID) from `craftRecipe`; other special recipes go to `craftSpecial` too, which picks each one's system by UUID. `player/craftrepair.go`: BDS's repair item special recipe (`Systems.CraftRepair`, `OnCraftRepair`), reusing the anvil's repairable component rules and the enchantment add rule for curses. `Player.SetHeldDamage`, `Held`, `MaxDamage` and `ItemStack.Damage` set and read wear directly (no Unbreaking roll, never breaks); `-testcmds` adds /damageitem.
- `circuit/bell.go`: bells (`Ticker.RingBell`, `bellEntity`). `player/crafter.go`: crafters, as a `Chest` with a `machineSpec` like hoppers (`Chest.craft`, `crafterTakes`, `handleToggleCrafterSlot`).
- `circuit/rails.go`, `player/minecart.go`: rails as BDS's `BaseRailBlock::Rail` (shapes, power, detector) and all four minecarts as one `Minecart` with a `cartSpec`, on the same `Chest` slots as machines (`Chest.cart`; `Player.windowChest` finds what is open). Hoppers see carts through `Host.cartsAt`.
- `player/painting.go`: paintings (`Painting`, `Motive`, BDS's 51 motives in its order, `Setup.SetMotives`, `Host.SpawnPainting`): placing picks a random biggest motive that fits (solid wall behind, no block boxes, no other painting), the survive check every 80 to 119 ticks, breaking with BDS's level events and no item (as BDS does in game), saved as BDS saves them. `player/itemframe.go`: item frames and glow item frames (`ItemFrame` block entity, `FrameAt`, `Put`, `Turn`, `Take`): clicking puts in or turns the item, starting to break takes it out, the item drops when the frame goes; `attach` holds frames on solid material and `circuit` reads them only when they face the comparator's way. `player/goathorn.go`: goat horns (the call by aux value, the 140 tick cooldown, `Player.Cooldown`, `StartCooldown`).
- `player/living.go`: `Living`, what players and mobs share for the world's hazards (`TakeDamage`, fire timer, `Wet`, blast box). Fire and lava blocks, burning, cactus and berry bushes (`Host.tickHazards`), landing (`Host.landed` for mobs; players keep `Systems.Fall`), lightning, explosions and projectiles hurt any `Living`. A new hazard is written against `Living` so it hurts mobs as well as players. Mobs also push each other and are pushed by players (`mob/push.go`, entity's `Mob::pushActors`).
- `player/mobequip.go`: mob equipment and death drops: the minecraft:equipment table rolled once when a mob is created (not loaded), items put where BDS's Actor::equip puts them, MobEquipment/MobArmourEquipment, shooter shots at the target (`Mob.Shoot`), equipment drop chances, minecraft:loot and experience_reward on death (doMobLoot). `loot/entity.go` rolls entity loot tables as BDS 1.26.60 (`EntityTables`, `EntityContext`, `SpecialMultiplier`); `mob/equipment.go` is the mob's view of its slots and the ranged-attack hooks.
- `player/mobsave.go`: mobs saved with their chunk in BDS 1.26.60's actor format (Actor::saveWithoutId and Mob's fields: identifier, definitions, UniqueID, Pos, Rotation, Motion, CustomName, Persistent, flag bytes, actor data values, Fire, Air, Mainhand/Offhand/Armor, Attributes); fields Helix does not run are kept from the loaded data and written back; `loadEntity` loads every type the mob runtime runs. Age (Age, GrowthPaused), breeding (InLove, LoveCause, BreedCooldown) and LeasherID are saved; anger timers and effects are not yet. Every dimension now has the entity loader and the block entities. `player/spawner.go`: monster spawners (`MobSpawner` block entity, BDS's BaseMobSpawner tick, save fields and defaults; spawns pass the entity library's `spawn.CheckSpawnRules`) and spawn eggs (on a spawner they set its mob in any game mode, elsewhere they spawn the mob facing the player). Name tags: `player/mobinteract.go`'s `nameMob` and `mob.Mob.Name` (BDS's NameableComponent: name, can-show flag, persistent, name_actions). `mob/breed.go` + `player/mobbreed.go`: feeding (ageable then breedable, BDS order), mating in Apply (babies through Hooks.Born, persistent; on_bred experience), leads on mobs. `player/leashknot.go`: leash knots on fences and walls (tie leashed mobs on use, break on hit/use or when the fence goes).
- `player/pushout.go`: BDS's push out of blocks, which only items and experience orbs run; `Properties.PushesOut` and `Stairs` are the block facts it reads.
- `ride`: riding for anything rideable: seats, seat offset, who may get on, links, where a rider lands. It knows nothing of carts or players: a vehicle keeps a `ride.Vehicle`, a rider implements `ride.Rider`, and `player/riding.go` is the player's side of it (the `vehicle` interface, `mount`, `letOff`).
- `connect`: the arms of fences, panes, bars and trip wire, kept in their block states since 1.26, from helix-data's rules, and stair corners (`stairs.go`, BDS's corner rule).
- Block entities: `world/blockentity.go` keeps them with their chunk (`BlockEntity`, optional
  `BlockEntityTicker`, `BlockEntityPlacer`, `BlockEntityRemover`, `BlockEntityBinder` with its `ChunkHandle`; `Dimension.SetBlockEntities` names who
  makes them; those it does not take stay saved as they were). `player/chest.go` runs chests, ender chests
  (items in `playerItems.ender`, reached as `Player.EnderChest`), hoppers, dispensers, droppers, crafters
  and carts' slots: BDS 1.26.60's pairing, lid and window, with `playerItems.open` as a stack request's copy
  of the open container (container `ContainerLevelEntity`), and the chest API (`Chest.Inventory`, `Kind`,
  `OnChestPair`, `OnChestSpill`, name and pairing). A `Chest` marks its chunk for saving through the
  `world.ChunkHandle` it is bound to, so `Inventory` methods need no region. `player/shulker.go` is what a
  shulker box does differently, as a `Chest` with `shulker` set (container `ContainerShulkerBox` in stack
  requests). `ContainerOpenEvent`, `ContainerItemEvent` and the methods, which fire no events, are in
  `window.go` and `inventory.go`. `player/blockdrop.go`: what a player's break drops (`Systems.Drops`, from
  the `loot` package, BDS's rule per block), `OnBlockDrops`, `Setup.SetBlockDrops`.
  `player/xporb.go`: experience orbs (`XPOrb`, BDS's sizes, merging, pull and touch; saved with the chunk) and
  levels (`AddExperience`, `OnExperience`). `-testcmds` adds /chesttest to try the container events in game (`price`, `noopen`, `nobang`, `playersonly` too).

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
