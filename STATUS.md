# What Helix does

Status as of 2026-11-08. Helix was **restarted from scratch with its own architecture.** Everything
is written from BDS 1.26.60's code, so it should behave as vanilla does; the marks say how far each
part has been checked.

| Mark | Means |
|---|---|
| ✅ | Works, and checked in game or against BDS (the basics at least; not every case) |
| 🧪 | Built from BDS's code, not tested in game yet |
| 🟡 | Partly built: some of it is missing |
| ⬜ | Not built yet |

The full notes for each part (API names, what is assumed, what differs from BDS) are at the
[bottom of this page](#full-notes).

## Server and world

- ✅ Joining: NetherNet, Xbox Live login, encryption, resource packs
- 🧪 Signing in to Microsoft's signaling service (only run against a fake service)
- ⬜ RakNet on by default (it is off)
- ✅ Opening and saving BDS worlds, in BDS's own format
- ✅ Flat and void worlds
- ✅ Vanilla terrain generation in the overworld (the worldgen library) and world types picked in `server.properties`
- ✅ Loading, sending and saving chunks; keeping chunks loaded without a player
- ✅ Regions ticking on many cores, merging and splitting as players move
- ✅ Registry: blocks, items, biomes, entities, creative items and recipes, byte for byte BDS's
- 🟡 Block ticks: random and scheduled ticks run; many blocks' tick behaviour is added, not all
- 🧪 World time and the day cycle (`/time` with `-testcmds`)
- 🧪 Sky light and block light, kept per block as BDS keeps them (torches, lanterns, lava and every block that gives off light)
- ⬜ Weather
- 🧪 The void (damage below the world, items removed)

## Players

- ✅ Spawning, seeing each other, the player list
- ✅ Abilities, game modes, permission levels, attributes
- 🧪 Sneaking, sprinting, swimming, gliding, crawling
- ✅ Fall damage, hunger and exhaustion
- ✅ Health, natural regeneration, starving
- ✅ Hitting other players: damage, critical hits, knockback, armour
- 🧪 Damage pipeline: every hurt is made of parts plugins can turn off or scale, one `OnHit` event, per-player enchantments
- ✅ Totems
- 🧪 Shields, putting armour on by using it
- 🧪 Death, death messages, respawning
- 🟡 Respawning at beds and respawn anchors, experience drops on death
- 🧪 Riding (minecarts and boats so far)
- ⬜ Server-side movement checks (left to the optional anti-cheat module)

## Items and inventory

- ✅ Inventory, held by the server: every change the client asks for is checked
- ✅ Creative inventory
- 🧪 Pick block
- 🧪 Dropping and picking up items, items floating and joining up
- 🧪 Eating food
- 🧪 Tools wearing out and breaking
- 🧪 Buckets (water and lava)
- 🧪 Experience orbs and levels
- ✅ Bows and arrows
- 🟡 Snowballs, eggs, ender pearls: built, but not working in game yet
- 🧪 Nether and End, nether and end portals, moving between dimensions
- ⬜ Potions, milk, crossbows, tridents, mace, fishing

## Blocks

- 🧪 Breaking blocks (speed, tools, enchantments, effects)
- 🧪 Placing blocks, facing and turning as BDS places them
- 🧪 Block drops and experience, for all 1,480 blocks
- 🧪 Water and lava flowing, making obsidian, cobblestone, stone and basalt
- 🧪 Doors, trapdoors, fence gates, levers, buttons
- 🧪 Fences, panes, bars, stair corners
- ⬜ Walls joining up
- 🧪 Torches, lanterns, ladders falling off without support
- 🧪 Beds, straw beds and cushions, sleeping through the night
- 🧪 Banners (standing and on walls, patterns kept)
- 🧪 Signs and hanging signs (placing, writing both sides, dyes, glow ink, wax)
- 🧪 Jukeboxes and music discs
- 🧪 Items and orbs pushed out of blocks
- 🧪 Fire: spreading, burning blocks away, burning out, soul fire, lava setting fire, flint and steel, putting fire out
- 🧪 Burning: players and dropped items hurt by fire and lava, set on fire, put out by water; netherite does not burn
- ⬜ Cauldrons, crops growing

## Containers and crafting

- ✅ Chests, trapped chests, copper chests, double chests
- 🧪 Ender chests
- ✅ Shulker boxes (items kept when broken)
- ✅ Furnaces, blast furnaces, smokers
- 🧪 Hoppers, dispensers, droppers, crafters
- 🧪 Chest and hopper minecart windows
- ⬜ Barrels, loot tables in chests
- ✅ Crafting in the inventory and on the crafting table, and from the recipe book
- 🟡 Special recipes: shields with banners and repairs are built; fireworks, map copies, banner copies and the rest are not
- 🧪 Repairing two items in the crafting grid
- ✅ Smithing tables: upgrades and armour trims
- ⬜ Stonecutter, cartography table, brewing stand
- ⬜ Unlocking recipes (the recipe book may show few or none)

## Enchanting and repairing

- ✅ Enchanting tables (the lead reports them working in game)
- 🧪 Anvils: repairing, merging, books, renaming, anvils wearing
- 🧪 Grindstones
- 🧪 Looms
- 🟡 Enchantment effects: most built; Fire Aspect, Smite, Bane, Depth Strider, Frost Walker and a few more are not

## Redstone

- ✅ Wire, repeaters, torches, lamps, copper bulbs, comparators
- ✅ Pistons and sticky pistons
- ✅ Observers, pressure plates, note blocks, daylight sensors, target blocks
- ✅ Tripwire, bells, trapped chests
- 🧪 TNT and explosions
- ✅ Rails and minecarts (plain, chest, hopper, TNT)
- 🧪 Boats and chest boats (every wood and the bamboo raft; an empty boat floats, falls and slides on its own)
- ⬜ Command blocks and command block minecarts

Redstone basically fully works in game, but it was written from BDS's code and is not fully tested:
it is not yet compared with BDS side by side, so timing, big circuits and edge cases are unproven.

## Commands

- 🧪 Command system (`command` package): commands as Go structs or built in code, no namespace prefix
- 🧪 Suggestions as the client shows BDS's: enums, live (soft) enums, per-player lists, items and blocks
- 🧪 BDS's prebuilt lists (game modes, effects, enchantments, entity types, biomes, game rules...)
- 🧪 Selectors (@a @p @r @s @e @initiator) over every player in every region
- 🧪 BDS's error messages, permission levels, aliases, middleware
- 🧪 Reaching any player from any region: `Player.State` to read, `Player.Run` to act, `Host.Find`, `FindName`, `FindXUID`

## Not started


- ⬜ Mobs ([entity](https://github.com/helix-bedrock/entity))
- 🧪 `/give` (in the `commands` package; plugins can remove or replace any built-in command)
- 🧪 `/teleport` and `/tp` (BDS's ten overloads, selectors, ~ and ^, rotation and facing, other dimensions)
- ⬜ The other vanilla commands (/gamemode and the rest; test stand-ins with `-testcmds`)
- ⬜ Forms and other UI

[API_DESIGN.md](https://github.com/helix-bedrock/.github/blob/main/API_DESIGN.md) lists which
topics are designed. **The API is not stable yet:** names and behaviour may still change between
commits.

## Full notes

<details>
<summary>Every part in detail</summary>

- **/teleport (written from BDS 1.26.60's code, not tested in game):** `commands/teleport.go`, BDS's TeleportCommand with its alias /tp and its ten overloads in BDS's order: to a position, to a position with yRot and xRot (~ from the victim's own), to a position facing a position or an entity, the same four with a victim first, to an entity, and a victim to an entity; checkForBlocks and forceProvidedPositionOnDimensionChange are taken as BDS has them. Positions are read from the runner (~ and ^ work), selectors pick players (@s, @p, @a, @r, @e for players, names, r, rm, x, y, z, m, l, lm, c). A victim gets off what it rides, keeps its rotation unless told, and lands with its fall distance reset; going to a player in another dimension changes dimension and puts the victim at that player's place. Messages: commands.tp.success.coordinates (names, x y z to two places) and commands.tp.success, commands.generic.tooManyTargets for a destination selector that finds more than one, noTargetMatch. New player API: `Player.Teleport(dim, feet, rot)`, `Player.Dimension`, `player.EyeHeight`. Not built: checkForBlocks refusing a spot inside blocks (commands.tp.safeTeleportFail), commands.tp.far, entities other than players, and whether BDS centres whole x and z coordinates (Helix does not).
- **Built-in commands and reaching players (written from BDS 1.26.60's code, not tested in game):** built-in commands live in the `commands` package, one file each, on the public API only, registered for the owner "minecraft" by the server; a plugin removes one with `player.Commands(setup).Remove` or replaces it by registering the same name. `/give <player> <itemName> [amount] [data]` follows BDS's GiveCommand: amount 1 to 32767 and data 0 to 32767 with BDS's too small and too big messages, whole stacks into the inventory and the rest dropped at the player, "commands.give.success" to the runner and "commands.give.successRecipient" to each player; the components argument is not built, and the item's name in the message is the name typed (BDS's item display name not read). Operators only (game directors), as in BDS: with `-testcmds`, `/optest` makes you one (`/optest off` undoes it) until Helix has an ops list. New player API: `Player.Run` runs a function on the player's own region at its next tick (the way to reach a player in another region; panics are caught and logged), `Host.FindName`, `Host.FindXUID`, `Host.NewItem`, `Host.MaxStack`, `Player.Drop`, `Player.Message`, `Player.MessageTranslated` and `Player.SetPermission`.
- **Commands (written from BDS 1.26.60's code, not tested in game):** the `command` package. A command is a `Spec` of struct overloads (exported fields are the arguments: int, float, bool, string, `Text`, `Position`, `BlockPosition`, `Targets`, `Literal`, `Item`, `Block`, any enum type, `Optional[T]`) or made with `NewBuilder` (`Param`, `Opt`, `Lit`, `Args`). There is no namespace prefix: a plugin's `home` is /home, and registering a taken name replaces it, logged with both owners. `player.RegisterCommand`, `player.Commands` and `Host.Commands` give the `Manager`: `Register`, `Add`, `Remove`, `Command`, `Commands`, `Wrap` (middleware around every command), `SetEnumOptions`, `RefreshEnum`, `Execute`, `Packet`. Enums are string types with `Type()`; `Options(src)` gives per-player options and `Dynamic()` makes a soft enum whose new options reach players at once (UpdateSoftEnum). BDS 1.26.60's own lists, dumped from its memory (`enum_probe.py`, private), are prebuilt with constants (`EnumItem`, `EnumGameMode`...): game modes, difficulties, dimensions, enchantments, effects, damage causes, equipment slots, game rules, easings, structure rotation and mirror, weather, HUD, permission levels, times of day, fill and setblock modes; Item, Block, EntityType and Biome are filled from the registry in BDS's order (full name then short; short first for entity types). Players get only the commands their command permission allows, at join after the recipes (where BDS sends it is not checked) and again when anything changes. Parsing gives BDS's messages (unknown command, syntax error with the place, not a number, not true or false, no player, no targets); with several overloads the error is the one that got furthest. A panic in a command is caught, logged with its owner, and reported as BDS's unknown error. Output goes back in a CommandOutput. Selectors resolve players only (no other entities yet) with name, type, m, r, rm, x, y, z, l, lm and c, reading every player through the new `Player.State`, which any region may read. `RelativeFloat` is a number that takes ~ (BDS's RelativeFloat, sent as the value type). Not done yet: most vanilla commands, the console as a source, BDS's exact parser (its grammar tables) and selector arguments beyond those, ^ coordinates checked against BDS, whether BDS also puts a command's own name in its alias enum, and the syntax error's context, which shows the whole line where BDS may cut it.

### What works

- **Networking:** NetherNet (RakNet is off by default), and the login the way BDS 1.26.60 does it (Xbox Live
  checks, encryption, resource packs). NetherNet's HTTP server, its limits, its timing, the status reply, public addresses and `server-udp-ports` (`-udp-ports`) were compared with the BDS code and match it. NetherNet LAN signaling is on, and each connection has its own UDP port as in BDS; `-udp-port` puts them all on one port instead. Signing in to Microsoft's signaling service is written from the BDS code (`-nethernet-id`, `-application-id`, `-application-tenant-id`, `AppCertificate.pfx`) but has only run against a fake service. Still different: WebRTC is pion where BDS has libwebrtc. Stricter than BDS on purpose, against floods and oversized input only: a message is cut at the 256 KiB announced in SDP and a packet at 16 MiB, at most 1024 offers are negotiated at once, one address may hold 16 of the 64 HTTP connections, and LAN discovery takes packets from local networks only. One-segment packets are received with no copy, and SCTP skips its checksum when the client offers that.
- **Worlds:** the world folder (`worlds/Bedrock level`, BDS's layout) opens at boot through the
  `world` library's stores. Players spawn at the level.dat spawn, or where a BDS world saved them.
  Flat and void worlds generate.
- **World types and settings:** `server.properties` (BDS's format; `-config` names another file,
  written with the defaults if missing) holds `server-name`, `max-players`, `online-mode`,
  `view-distance`, `tick-distance`, `level-name`, `level-seed` and Helix's own `level-type` and
  `level-type-options`. `level-type` is `vanilla` (BDS 1.26.60's overworld from the worldgen library,
  built over Helix's own block and biome registry), `flat`, `void`, or a type a plugin adds to
  `Config.Generators` (`generator.Type`). Empty means what level.dat says; a new world is vanilla.
  The options are a worldgen JSON file for vanilla (biomes, features, structures, terrain) and the
  flat layers for flat. Structure block entities (chests, beds, bells, spawners) come with their
  chunks; chests keep their loot table and seed but are not filled when opened yet. Springs come with the
  block tick BDS's spring feature leaves pending (one tick after generation, on the chunk's queue), so
  they pour out of cave walls in Helix's own liquid ticks. Unchanged
  generated chunks are not saved, as BDS. Players start on the ground of the spawn column
  (`world.Spawner`): BDS's spawn search is not ported. The Nether and the End open beside the
  overworld with worldgen's vanilla terrain (also in a flat world, as BDS; a void world's stay void);
  the End crystals worldgen asks for are not spawned (no End crystal entity yet). Text seeds are refused: how BDS turns text into a number
  is not read. Vanilla terrain through Helix's registry is not yet compared block for block with a
  BDS world of the same seed; a check of 64 chunks gave real terrain at about 25 ms a chunk.
- **Chunks:** each player's simulation zone (tick-distance) stays loaded and changed chunks are
  saved at the end of each tick. The view zone (view-distance) is loaded, encoded once, sent in
  BDS's sub-chunk request mode and released. A ticket (`KeepLoaded` on a box, a radius or a list of
  chunks) keeps chunks loaded without a player, ticking or frozen, until it is released.
- **Regions:** loaded chunks belong to regions that tick in parallel at 20 TPS each and merge and
  split as players move (`TickCores` sets how many cores tick). A player who moves far away, such
  as by a teleport, goes straight to the region there instead of merging the two.
- **Block ticks:** random ticks at the level's random tick speed and the two scheduled tick queues,
  loaded from and saved to the chunk, on a world tick kept in level.dat.
- **Players and entities:** players see each other (AddPlayer within BDS's 57-chunk disc,
  `EntityViewRadius2`, changeable while running), MovePlayer for their movement, and the player
  list. Other entities stay in loaded chunks, pass to the region they move into and unload with
  their chunk; where they were is saved when they leave. The void is BDS's (not tested in game yet):
  more than 16 blocks below the dimension's lowest block, so it moves with a deeper world. Players
  there take 4 void damage a tick, creative and spectator too, as in BDS; everything else (items,
  orbs, TNT, carts, projectiles, cushions) is removed. `Systems.OutOfWorld` and `Systems.Void`
  replace the check and what it does, `OnVoid` can stop or change it, `TuneVoidDamage` sets the
  damage, and an entity owner can answer with its own `OutOfWorld`. Trident Loyalty is not built.
- **Abilities:** permission levels, game mode and attributes follow BDS 1.26.60 (layered abilities,
  its per-game-mode rules, its saved format) and are sent at join in BDS's order.
- **Inventory:** items are loaded from and saved in BDS's format. Pick block works as BDS does (not yet tested in game; a block's data on the item is not built). Every inventory change the client
  asks for is checked as BDS checks it (stack network IDs, counts, stack sizes, armour slots and
  offhand items as BDS lists them, creative-only destroy and creative items) before it happens.
  Stricter than BDS on purpose: what a client says it holds is only compared, never kept, so a
  dropped item is always cut from the server's own stack. A drop asking for more than the stack
  holds is refused (BDS clamps it), and a legacy transaction must be exactly one inventory slot
  plus one world drop.
- **Item drops and pick-up (not yet tested in game):** Q and dropping from the inventory screen
  throw items as BDS does. Items fall, float in water and lava, drift with the current, slow down on the block under them,
  join nearby stacks, vanish after 6000 ticks, and go to the offhand and inventory in BDS's order
  when touched, leaving what does not fit. Other players see the held item change. Plugins can decide
  who sees an item (others are sent nothing about it and cannot pick it up), show each player its own
  name or item, change the real stack, and set when it vanishes, per item or for the whole server.
- **Registry:** built at boot from BDS 1.26.60's data: 23,960 states of 1,480 blocks with their
  network hashes and BDS's runtime numbers (light, flammability, explosion resistance, friction,
  destroy time, and BDS's collision boxes for every state: data for the 888 blocks whose shape is
  fixed and BDS's own rules for the rest (stairs, slabs, walls, fences, trapdoors, cauldrons and
  about 40 more). Doors, shulker boxes, scaffolding, powder snow, end gateways and piston parts
  need the world or the colliding entity and are only marked so far), 2,080 items, 89 biomes with BDS's numeric IDs and 138 entity types with their
  definitions, plus the creative inventory and recipes, which can change while running. Joining
  players get the registry packets; untouched, each is byte for byte BDS's.
- **Plugins:** can add, replace and delete any registry entry in `Config.Setup`. World packets go
  through a handler table plugins can replace or wrap, per packet or per system (movement so far),
  on the player's region tick. Every set of item slots is an `Inventory` (a player's inventory, armour,
  offhand, UI slots and ender chest, chests, shulker boxes, hoppers, dispensers, droppers, crafters and
  carts) that shows its changes to whoever looks (`Player.Inventory`, `Chest.Inventory`, `SetItem`,
  `AddItem`, `RemoveItem`, `Clear`, `Viewers`). Every window (own inventory, containers, carts and screens
  such as crafting tables and anvils) fires `OnContainerOpen`, which can keep it shut per player, and
  `OnContainerClose`; `OnContainerItem` fires for each slot a player's request changes. What each player
  sees of any item can be changed per player, one slot or a whole window at a time, without changing the
  server's item (`OnInventoryView`, `Inventory.SetView`, `Refresh`, `RefreshSlots`, `RefreshFor`,
  `Player.RefreshItems`, `ItemStack.WithLore`); what the player was shown is resent after it moves items.
  `OnExplosionStart` can stop any explosion before it does anything or change it (`BreaksBlocks`,
  damage, knockback), and `OnTNTPrime` any TNT from lighting. Every explosion says what set it off
  (`Explosion.Kind`, the block it came from, the player behind it), `OnExplosionHit` can change or
  stop what each entity it reaches gets (only players hurt, say) and `ExplosionEvent.Keep` spares a
  block. Chests also have pairing and spilling events and their name and pair (`Host.ChestAt`,
  `Host.WithChest`, `Chest`). None of this is tested in game yet; what other players see a player hold
  or wear has no per-player view yet.
  Beds, straw beds and cushions are written from BDS 1.26.60's code (not tested in game yet). Beds have
  their block entity (the colour the client draws them with), BDS's placing rule (facing, both halves
  free and held, no end portal near) and break as one; sleeping checks distance, the night and room to
  get up, sets the bed as the respawn point, and once enough players (`playerssleepingpercentage`) have
  slept 100 ticks the night is skipped. Outside the overworld a bed explodes and a straw bed falls apart
  (`respawnblocksexplode`). A straw bed never sets the spawn and is used up when its sleeper gets up.
  Cushions go on any top face at the exact height clicked, turned to the nearest quarter, never on or
  into another cushion; one hit breaks them; a seated player stays seated through leaving and a restart
  (`Host.SpawnCushion`, `Cushion`, `Player.Sleeping`, `Player.StopSleeping`). Not built: monsters
  keeping a player awake (there are none yet), weather clearing on the night skip, beds bouncing.
  Experience has `OnExperience`, `Player.AddExperience`, `SetLevel` and `Host.SpawnExperience`. Block breaks say what is left behind and
  how much the tool wears, and `OnBlockDrops` and `Setup.SetBlockDrops` decide what comes out, where
  and how fast (not yet tested in game).

- **Breaking and placing (first pass, not yet tested in game):** block breaking the way BDS
  1.26.60 does it with server-authoritative breaking: cracks and BDS's hit, break and place sounds for everyone near, the reach,
  the break speed from the tool, Efficiency, Aqua Affinity, Haste, Conduit Power, Mining Fatigue,
  being in the air or under water, BDS's short pause after each broken block, and BDS's resend when the client got it wrong. Placing puts the
  held block item's block where the player clicked (replacing grass, water and the other blocks BDS
  lets you build over), with the place sound, and uses up one item. Tools wear as in BDS
  (Unbreaking included) and break when worn out; effects run out.
  Every step, every enchantment and effect, and the player's own break speed and reach can be
  changed by plugins; `OnBlockBreak` and `OnBlockPlace` events can cancel or change them.
- **Player physics data and the inventory window (not yet tested in game):** the player gets its
  own synched actor data on joining, entry for entry as BDS 1.26.60 sends it (gravity and
  collision flags, the 0.6 by 1.8 box, air supply), so the client's own physics run; before
  this the player floated. Opening the inventory (Interact) and closing a container answer as
  BDS does.

- **Using items, hitting, health and hunger (built from BDS 1.26.60, not yet tested in game):** foods are eaten
  by holding the use button, with hunger, saturation, leftovers such as bowls and effects. Players hit each other with BDS's
  checks and damage: the held item, Sharpness, Strength, Weakness, critical hits, knockback, the Knockback enchantment and
  sprinting. Armour, toughness and armour enchantments reduce damage, a hit gives 10 ticks where only harder hits count, and
  absorption goes first. A full player regains health and an empty one starves, by difficulty. Empty buckets take
  liquids, and a block that can hold water keeps the source it is placed in. Not done yet: potions, milk, bows,
  tridents, mace, most item cooldowns, interacting with entities, mobs.
- **Elytra and fireworks (built 2026-11-08, checked in game by the lead the same day; what other players see not checked yet):** gliding follows BDS 1.26.60: a start from the
  client is refused (and sent back) on the ground, riding, in water, after rising 0.15 or more, or without an elytra
  that still flies (damage below max - 1); gliding stops when that changes. The elytra takes 1 damage every 20 gliding
  ticks (Unbreaking counts). Hitting a wall hurts (speed lost x 10 - 3, fly-into-wall) with the fall sounds; BDS
  measures the speed in its own glide simulation, Helix from the client's movements. The glide physics run on the
  client. Rockets (`FireworkRocket`, `Host.LaunchFirework`): used while gliding they ride with the player and send the
  glide boost (`MovementEffect`) for their lifetime; used on a block they fly up from the click; dispensers shoot
  them out of their face. Lifetime 10 x (Flight + 1) + rand(6) + rand(7); the explosion shows the particles to all
  who see it (also on a gliding player) and, with stars, hurts 5 + 2 per star: the rider in full, players within 5
  blocks it sees by distance. Hooks: `Systems.StartGlide`, `GlideTick`, `GlideCrash`, `Firework`; events `OnGlide`,
  `OnFirework`. **Not built (known gaps):** rockets shot from crossbows (BDS's `fireworksRocketServerProjectileHit`);
  crafting firework rockets and firework stars (BDS makes them by its own special recipes); sculk vibrations (BDS sends
  game events when a rocket launches and explodes and every 10 ticks of gliding, which sculk sensors, and through them
  redstone, would pick up; Helix has no sculk sensors or game events yet); flying rockets are not saved with the chunk
  (BDS saves them); BDS's telemetry/achievement message for star rockets with trail and twinkle; a player-ability
  check on launching from a block whose meaning was not found; `/give` with firework data is not checked.
- **Damage pipeline (built 2026-11-08, not yet tested in game):** every hurt of a player (melee, arrows, falls,
  fire, the void, explosions, starving, Thorns) is a `Hit`: a base and lists of parts (bonuses: melee enchantments,
  Strength and Weakness, critical hit, damage scale; reductions: damage scale, helmet, armour, toughness, Breach,
  protection enchantments, Resistance, hurt time, absorption; after: knockback, Thorns, armour and weapon wear,
  exhaustion). One `OnHit` event lets a plugin turn any part off, scale it or add its own, then the engine works
  the damage out in BDS 1.26.60's order and caps. `Player.EditEnchantment` gives one player its own copy of an
  enchantment (say weaker Sharpness). `OnPlayerAttack` and `OnPlayerHurt` are gone; `OnHit` replaces both. A plain
  hurt (`Systems.Hurt`) still takes a damage and source. Damage sensors, mobs and the mace are not built.
- **Shields, totems and putting armour on by using it (built from BDS 1.26.60, not yet tested in game):** sneaking raises a shield in the
  offhand or hand, and after 5 ticks it stops hits from in front, wears down, shakes and pushes the attacker back; an axe puts it on
  cooldown for 5 seconds. A totem in either hand saves a player from a killing hit, except from the void: 1 health, its effects and
  its animation. Regeneration and Absorption now work, for golden apples too. Using a piece of armour swaps it with what is worn,
  unless the worn piece has the Curse of Binding. Plugins can change the numbers, replace each step and listen to each.
- **Changing health, hunger and combat numbers (not yet tested in game):** a plugin can change how fast hunger drains, how
  health recovers and starving hurts, the most health and hunger, food's worth, armour and protection added or taken away,
  fall damage, hit damage and every part of knockback. A value can be set for one player or the whole server, and clearing it
  goes back to the default. Values for one player are not saved.
- **Joining and game modes (not yet tested in game):** the spawn position, difficulty, commands flag and game rules are
  sent in BDS's order, and a game mode change sends what BDS sends.
- **Movement state, falling and hunger (first pass, not yet tested in game):** sneaking,
  sprinting, swimming, gliding and crawling are taken from the client's start and stop input and
  shown to other players. Fall distance follows BDS (cleared in water, on ladders and vines, while
  gliding or slow falling, and when not moving down), and landing deals BDS's fall damage
  (distance minus 3, minus the Jump Boost level) with the fall sounds. Sprinting, swimming, jumps,
  sprint jumps, breaking blocks and taking damage cost BDS's exhaustion from the player
  definition, which burns saturation and then hunger (not on peaceful, not in creative). Health
  goes down and the player is shown hurt. Each step is a system plugins can replace.
- **Placement states (not yet tested in game):** placed blocks face, turn and split into halves
  as BDS 1.26.60 places them: its placement traits (furnaces, chests, doors, observers, slabs,
  amethyst...) and its own rules for stairs, logs, trapdoors, pistons, dispensers, barrels, glazed
  terracotta, skulls, buttons, levers, torches, ladders, lanterns, bells, grindstones, hoppers, end
  rods, cocoa, tripwire hooks, vines and leaves. Whether a wall holds a torch, ladder or lantern is
  judged from collision boxes (BDS asks each block).
- **Water and lava (not yet tested in game):** liquids flow as BDS 1.26.60's: they spread one
  depth per block (lava two, every 30 ticks; water every 5) towards the nearest way down, fall,
  dry up when their source goes, make infinite water between two sources, and lava turns to
  obsidian, cobblestone, stone or basalt where it meets water or blue ice, with the fizz sound.
  Liquids flow into waterloggable blocks' second layer and replace plants, torches and the like
  (no drops yet). Breaking or placing a block wakes the liquids beside it. Buckets pour water
  and lava (water into waterloggable blocks) and become empty buckets outside creative.
- **Fire (not yet tested in game):** written from BDS 1.26.60's FireBlock, SoulFireBlock, lava's
  random tick and FlintAndSteelItem (package `fire`, `fire.Ticker`, `Server.Fire`, `SetRules`). Fire
  ages, ticks every 30 to 39 ticks, burns the six blocks around it by their burn odds (turning them to
  fire or air, lighting campfires, priming TNT), and lights empty blocks from one below to four above
  by the flame odds around them, more on harder difficulties and half as much in humid biomes. It
  burns forever on netherrack, magma and bedrock with `infiniburn_bit`, goes out without a block to
  stand on or burn, and turns to soul fire on soul sand and soul soil; soul fire stays only there.
  `dofiretick` off stops all of it. Lava's random tick, and every lava tick (so soon after lava is
  placed or flows), tries ten blocks around it and sets fire next to the first lava-flammable one (BDS's own flag per block, now in helix-data's `block_properties.json`
  as `lava_flammable`; it is not the flame odds). Flint and steel sets fire on the clicked face and
  wears; starting to break a block puts out fire on the clicked face. Not done: rain putting fire out
  (no weather yet), fire charges, mobs burning (there are no mobs yet), bees in a
  burning hive.
- **Burning (not yet tested in game):** from BDS 1.26.60 (`player/burn.go`). Fire and lava hurt a
  player inside them each tick (1, soul fire 2, as `death.attack.inFire`) and set it on fire for 8
  seconds, 15% less per level of Fire Protection worn; lava also deals 4 a tick (the player's
  `minecraft:hurt_on_condition`, `death.attack.lava`). The hurt cooldown lets only the larger hit
  through. On fire, the player takes 1 every 20 ticks (`death.attack.onFire`) until the time runs
  out; water puts it out unless the player is still in lava, with the fizz sound and smoke (for
  burning items too). Fire Resistance stops the damage.
  Creative and spectator players are not set on fire (assumed, not read). `Player.SetOnFire`,
  `Extinguish`, `OnFire`. Dropped items have 5 health and take the same fire damage, so they burn
  away in five ticks, fizzing each tick in lava or fire; netherite items and ancient debris (BDS's fire-resistant flag, helix-data's
  `bds/item_flags.json`) do not burn. Not done: rain putting fire out, the contents of a burnt
  bundle or shulker box spilling.
- **Creative inventory:** items can be taken from the creative menu (the client's results
  message after a creative take is accepted).

- **Portals and dimensions (built 2026-11-08 from BDS 1.26.60's code, not tested in game):** the
  Nether and the End are open and players move between them (`player/portal.go`, the `portal`
  package, `end.go`). Portals take no one until their client said it finished joining (SetLocalPlayerAsInitialised), so a player who rejoins standing in a portal is not moved mid-join. A portal built for an arrival sets all its blocks in each of BDS's four passes before updating any neighbours, as BDS does with flag 2; updating them block by block let the half-built portal break itself, which also dropped its record so a second traveller got a new portal elsewhere (fixed 2026-11-08, not yet rechecked in game). Checked in game 2026-11-08: lighting a nether portal and travelling overworld to Nether and back work (End portals, ender pearls and a second player watching not yet checked). A teleport MovePlayer always carries its teleport data (cause; the client disconnects with BadPacket, codeword "Block", without it, as its MovePlayer check requires for mode teleport): portal arrivals send cause 0 as BDS's transfer does, ender pearls cause Projectile with source 0 (BDS sends the pearl's actor type, not looked up yet). A dimension change first closes the old dimension's viewer so nothing more from there reaches the client (the likely cause, not yet confirmed in game, of clients disconnecting with "Block" after a portal trip), then sends what BDS's PlayerDimensionTransferer sends: ChangeDimension, then the new chunks, and the dimension change done action only once the player is placed (no PlayStatus PlayerSpawn, which BDS sends only on the first spawn, and no StopSound, which its transfer steps do not send). Nether portals: fire inside an empty obsidian frame (flint and steel, or fire placed or spreading
  there in the overworld or the Nether, as BDS's fire onPlace) (2 to 21 wide, 3 to
  21 tall, corners not needed) fills it with portal blocks (all set before any neighbour update, as BDS does, so the portal does not break itself while filling); a frame that breaks or loses a portal block
  breaks the portal. A survival or adventure player goes through after standing 80 ticks in it, a
  creative one at once, with BDS's cooldowns (10 ticks, 300 after a trip) and the time falling by 4 a
  tick outside. x and z are divided or multiplied by 8 (y kept, within 32 million); the arrival takes
  the nearest recorded portal within 128 blocks, else builds one the way BDS does (the nearest spot within
  16 blocks with room for it, else in the open at y 70 or more on an obsidian platform with BDS's
  netherrack ledge). Portals are recorded in the world's `portals` key (field names BDS's; not compared
  byte for byte with a BDS world). End portals: an eye of ender goes into a frame (sound, used up outside
  creative); a ring of twelve frames with eyes, each facing the middle, opens the 3 by 3 end portal. Into the End you land on
  BDS's 5 by 5 obsidian platform at 100, 50, 0; the first arrival builds the exit portal (podium) closed,
  as BDS's dragon fight does, and keeps where it is in the End's level key. Out of the End you go to
  your bed or the world spawn. Dying in the Nether or the End respawns you in the overworld. The
  player's dimension is saved. **Not built:** the credits on leaving the End, the dragon and opening the
  exit portal, fire charges lighting portals, mobs, items and other entities going through portals, the yaw turn when the portal's axis
  differs, zombified piglins from portals, and the level-setting check BDS makes before lighting.

### What is left

- **World generation:** BDS's spawn search, filling structure chests from their loot tables when
  first opened (the engine's job, not worldgen's), text seeds, and the mobs structures spawn.
- **Block behaviour:** water and lava are the only blocks with behaviour so far. Lava does not set
  fire, blocks liquids replace drop nothing, stairs and slabs let water out on every side (BDS
  decides by their shape), and placing a waterloggable block into water does not keep the water.
  Walls, fences, panes, redstone wire, snow layers, scaffolding, petals, glow lichen
  and dripstone are placed in their item's state (their state needs the neighbours), the second
  door half is not placed, and clicking blocks that open or toggle (chests, doors, levers) is not
  done. Buckets do not pick liquids up.
- **Movement:** the client's position is taken as it is. BDS's server-side simulation and
  corrections will come with the optional anti-cheat module. Other containers (chests and the
  like, other than chests) do not open.
- **Block use (first pass, not yet tested in game):** clicking a block follows BDS's order: the
  `OnBlockUse` event, the block's use, then the held item, and a held click stays with whichever the
  first click did. Doors, trapdoors, fence gates, levers and buttons work (new `blockuse` package). A door, trapdoor or gate opened by hand no longer shuts itself straight away: redstone took its new open state as power and then shut it when it found none (fixed 2026-11-05, not yet re-tested in game).
  Doors place both halves with BDS's hinge rule and fall apart without their other half or support.
  Buttons pop out after 20 or 30 ticks. No redstone output yet. Other blocks with a use (furnaces,
  beds...) do not react yet, so a block in hand is placed against them.
- **Fences, panes and bars (not yet tested in game):** since 1.26 their arms are in the block's own
  state, so the server works them out on placing and when a neighbour changes (new `connect` package,
  from BDS's per-state rules in helix-data's `bds/connections.json`). Walls are not done yet.
  Stairs work the same way for their corner (`minecraft:corner`, a state since 1.26.50), from BDS
  1.26.60's rule (`connect/stairs.go`): a stair facing across in front makes an outer corner, one
  behind makes an inner corner, on the same half only, unless the block on the other side is the very
  same stair. Not tested in game.
- **Block entities and chests (first pass, not yet tested in game):** block entities now live with
  their chunk while it is loaded (`world.BlockEntity`): made when their block is set, removed when it
  changes type, ticked, saved in BDS's format and sent to clients in the chunk and when they change.
  Ones the engine does not run yet keep their saved data untouched. Chests, trapped chests and copper
  chests follow BDS 1.26.60: they pair with the chest beside them facing the same way, open unless the
  block above stops the lid (BDS's list, then collision boxes), share one window between everyone
  using them, move items with BDS's checks, re-pair after a load, and throw their items when broken.
  Ender chests open the same way but never pair, and show each player their own items, which are saved
  on the player in BDS's `EnderChestInventory` and stay through death.
  Not done yet: loot tables, cats on chests, redstone from
  trapped chests, barrels.
- **Death and respawn (first pass, not yet tested in game):** at 0 health the player dies as BDS
  1.26.60's order has it: without `keepinventory`, vanishing items go and armour, offhand, cursor and
  inventory are thrown at random (items kept on death stay), others see the death, the death screen
  and the death message show, and the respawn button runs BDS's Respawn packets and brings the player
  back with full health and hunger. Not done yet: experience drops, beds and respawn anchors, BDS's
  search for a safe spot within `spawnradius` (Helix uses the world spawn itself) and waiting for the
  chunks there. Which attributes reset is a first reading.
- **Health:** Armour, Feather Falling, Protection, Resistance, absorption, the
  invulnerability ticks after a hit, natural regeneration and starving are not done. Blocks
  that change a fall (slime, hay, beds) are not ported; "in water" is the feet or eye block
  holding water, not BDS's exact liquid-height check. Elytra durability is not done.
- **Breaking leftovers:** only the placing
  player blocks a placement (BDS checks every entity against the placed block's collision boxes).
  Spears and other data-driven items do not wear yet. Stacking petals and leaf litter, and BDS's
  own snow layer rule, are not done.
- **Block drops (first pass, not yet tested in game):** survival players breaking a block with the
  right tool get BDS 1.26.60's drops for all 1,480 blocks (new `loot` package, from helix-data's
  `bds/block_drops.json` and its block loot tables). Plugins can change all of it (see Plugins above).
  Blocks give BDS's experience orbs, which follow the nearest player and raise its level (how
  far they spread at first and what BDS sends about them are not compared yet). Not done yet: items built from a block entity (the plain item drops), chests filling
  from `LootTable`, enchantment loot functions, drops from anything but a player breaking the block. The
  speed items leave a broken block at was not read from BDS.
- **Enchanting (first pass; the lead reports it working in game, anvil wear and the edge cases not separately confirmed):** all 42 of BDS 1.26.60's enchantments are registered on the same
  `Enchantment` struct plugins use, from helix-data's `bds/enchantments.json`: max level, weight, slots, group, costs,
  compatibility and every hook can be changed or removed in `Setup`, or while running with `Host.EditEnchantment`, `SetEnchantment`
  and `RemoveEnchantment`; a removed one does nothing on items, Silk Touch and curses included. `Systems.EnchantRequire` sets what a table's option
  asks for (level, levels taken, lapis), `SetEnchantMaterial` the item in place of lapis, `Systems.Bookshelves` the shelf count, and the anvil's and grindstone's numbers are Tunes. Anvils wear as BDS's do, and a name over 30 characters is refused (`TuneAnvilNameLength`, the limit of the game's own text box; BDS has none).
  Items have `Enchants`, `WithEnchant`, `WithoutEnchant`, `RepairCost`; players `EnchantSeed` and `SetEnchantOptions`. **Enchanting tables** (BDS's seed and draw, bookshelves, lapis and levels, `OnEnchantOptions`,
  `OnEnchant`), **anvils** (materials, merging, books, names, the price of earlier work, `OnAnvil`) and **grindstones**
  (curses kept, experience back, `OnGrindstone`) work, each with a `Systems` hook. **Effects built:** Thorns, Breach, Density, Impaling in water and
  Mending, with Sharpness, Efficiency, Unbreaking, Aqua Affinity, Silk Touch, Fortune and the armour kinds from before. Not done yet:
  anvils on maps, Fire Aspect (needs burning), Impaling in rain, Smite and Bane (no mobs),
  bows, crossbows, tridents, fishing, Depth Strider, Respiration, Soul Speed, Swift Sneak, Frost Walker and Fire Protection's burn
  time, and enchant loot functions. Run with `-testcmds`, `/enchanttest` gives levels and changes a maximum.
- **Crafting** is a first pass from BDS 1.26.60: the inventory's small grid and the crafting table's large
  one, by hand and from the recipe book, with `OnCraft` to change the results or refuse. Recipes come from the
  `CraftingData` packet, so plugins change them through `Registry.Crafting`. Not done yet: which recipes a player
  has unlocked (the recipe book may show few or none), `doLimitedCrafting`, BDS's 14 special recipes (fireworks,
  map copies, banner copies...; the shield and banner one and the repair one are built), the 1,456 recipes that keep an item's data (dyeing bundles and such), and the
  other screens (furnace, stonecutter...). **Smithing tables** work for every BDS recipe, upgrades and armour trims, with a
  plugin API: `Registry.EditSmithing` and `Setup.EditSmithing` clear, remove or change recipes (`Clear`, `Remove`,
  `RemoveFor`, `Set`), and `OnSmith` changes or refuses one use; `Setup.EditTrims` and `Registry.Trims` change the
  trim patterns and materials. **All recipes** have the same API: `Registry.EditRecipes` and
  `Setup.EditRecipes` list, clear (`Clear`, `ClearBlock`, `ClearSpecial`, `ClearBrewing`), remove (`Remove`,
  `RemoveFor`) or change and add (`Set`) shaped and shapeless recipes of any block, at boot or while running. Screens
  not built yet list their recipes but cannot use them. Torches, lanterns and ladders place and fall off by BDS's rules (`attach` package, helix-data `bds/support.json`): which way they face, what may hold them, and they drop when their holder goes. A grid only makes recipes tagged for the crafting table, whatever recipe the client names. `Player.EditRecipes` takes recipes away from one player (`Remove`, `RemoveFor`, `ClearBlock`, `Clear`, `Reset`): that player gets a filtered list and the server refuses the rest. Those rules are not saved. Items left in a grid stay there when the window closes:
  no return was found in BDS, so the client is taken to move them, which is not confirmed. **Item entities** are a first pass: they are saved with their chunk
  (not yet compared with a world BDS saved), do not burn in lava or fire, and their
  spawn data is not yet compared with BDS. Item stack requests inside PlayerAuthInput are ignored.
  Every broken container (chests, barrels, hoppers, dispensers, droppers, crafters, furnaces, carts)
  drops each slot as one whole item through one helper (`Host.spillSlots`), not tested in game. BDS's
  code throws chest, barrel and hopper slots in piles of 10 to 30, but in game BDS ends with one
  stack, so Helix drops them whole by the lead's choice. Items lying within BDS's merge box (0.5
  past their own, every 25 ticks) join up to the item's max stack; a plugin's item view does not
  stop a merge.
- **Rails and minecarts (written from BDS 1.26.60's code, not tested in game):** rail, powered, detector and activator rails: shapes and slopes from the rails around (`circuit/rails.go`), falling without a floor, power along powered and activator rails for nine rails, comparators reading a chest cart on a detector rail. Minecart, chest, hopper and TNT minecarts (`player/minecart.go`): BDS's rail movement, slopes, powered rail boost, brake and start against a block, slowdown by rider or by how full, riding with walking forward, players pushing carts and carts pushing carts, placing, dispensing, breaking with drops, saving with the chunk. Hoppers take from and give to carts in the block's space, so a moving cart counts and a cart on a block above the hopper does not. Hopper carts take from containers and items above, stopped by a powered activator rail; TNT carts light on one. Not done: command block carts, water speed, the entitydrops game rule. The powered rail boost was checked again against 1.26.60 and matches: 0.06 along the way it moves above speed 0.01, otherwise 0.02 away from a block. Read from BDS but unsure: nothing found that carries rail power up a slope, so power stops at a slope unless the lead sees otherwise; the hopper cart's 4 tick timer; what stops a cart being placed (only other carts here).
- **Projectiles (written from BDS 1.26.60's code, not tested in game):** snowballs, eggs and ender pearls thrown, bows with arrows, and dispensers shooting snowballs, eggs and arrows (`player/projectile.go`). One `Projectile` with a `ProjectileSpec` read from each entity's definition, flying as BDS's ProjectileSystem does: blocks and players along the path, nearest first, slowing and gravity. Bows: draw time, full draw critical, Power, Punch, Infinity, wear, offhand arrows first. Arrows stick, shake, fall when the block goes, last a minute, can be picked up (creative only for Infinity or creative shots), and save with the chunk. Pearls teleport and cost 5 fall damage. They reach the client as BDS sends them: AddActor with the velocity (invisible until the end of the second tick), then only MoveActorDelta with the changed fields under BDS's limits and per player by distance (Ticks 0 near by), on the ground once stuck, and ActorEvent 39 for the shake. Clients fly them themselves; BDS sends a projectile no SetActorMotion or MotionPredictionHints. Beyond BDS, by the project lead's choice: they keep flying past the loaded chunks, holding the chunks ahead of them and staying shown to those who see them (`ProjectileSpec.StayInLoaded` turns this off); they are removed 64 blocks below the world (assumed). Target blocks take their hits. Not done: crossbows, tridents, potions, tipped arrow effects, Flame (Helix has no burning), hit particles, egg chicks and endermites (no mobs), hitting anything but players and blocks, lighting TNT. Assumed, not read from BDS: throws start at the eyes for every kind, a stuck arrow waits out its shake before pick up, the knockback is a melee hit's first push along the flight, and what a dispenser uses for an arrow's numbers.
- **World time (written from BDS's code, not tested in game):** the overworld clock (`daytime` package, `Server.Clock`) runs by the `dodaylightcycle` game rule, is read from and saved in level.dat's Time and the WorldClocks key, and clients get it in SyncWorldClocks at join and every 256 ticks. With `-testcmds`, `/time set`, `add` and `query` change it, as BDS's command moves it (a named time goes to its next occurrence), and `Server.SetTime` does so for plugins. Not done: the real `/time` command, sleeping skipping the night, weather.
- **Light (written from BDS 1.26.60's code, not tested in game):** `world/light.go` keeps sky and block light for every block, a nibble each per sub chunk (one value while a sub chunk is all the same), as BDS's SubChunkRelighter does. Each block's light and dampening come from helix-data (BDS's own numbers); a cell counts the larger of its two layers, and light entering a cell loses its dampening, at least 1. Sky light is 15 down to BDS's height map, the cell above the highest block that dampens light or is a half slab (read from 1.26.60's height map code and its slab block, which gives the half slab flag to single slabs only); below that it only spreads, so it falls off even straight down under leaves or water. Light is not saved, as in BDS: the loading worker lights a new chunk on its own, the region lets light across its sides when it joins, and a block change relights what it touches at the end of the tick (or at once when light is read). Chunks not loaded let no light through. Clicking a light block with a light block never places one; in creative it moves the block to the next level, 15 going round to 0 (BDS 1.26.60's light block click). With `-testcmds`, `/lighttest` shows the server's sky and block light where you stand. API: `Region.Light`, `SkyLight`, `BlockLight`, `Brightness` (with the night's darkening from `daytime.SkyDarken`); `world.Config.Light` (`LightRules`) replaces the rules and `Config.Sky` turns sky light on or off. Assumed, not read from BDS: only the overworld has sky light (the Nether and the End have none); BDS has a second height map that lets water through, used only when a dimension option is on, which Helix does not use. Not done: light is not sent to players (the client works out its own), mobs and ice melting do not read it yet, and a chunk unloading does not take back light it let into its neighbours, as in BDS.
- **Being killed:** level.dat is written every 30 seconds while running (how often BDS does was not read), so a killed server loses little of the world tick and the time. A chunk saved with a tick clock ahead of the world tick moves its queue back to it, so its ticks do not wait out what was lost (beyond BDS).
- **Redstone settings:** every number redstone runs by can be changed through `Config.Redstone` (`circuit.Config`), at boot or while running with `Server.SetRedstone`; `circuit.DefaultConfig` is BDS's. Hoppers: `MoveCooldown`, `PickupCooldown`, `FirstItemCooldown`, `ItemsPerMove`, `ChainDepth` and the pick up box. Also circuit interval, signal reach, torch burn out, rail reach, load radius, hopper carts, dispensers, lamps, observers, plates, target blocks, pistons, tripwire, detector rails, bells, crafters and TNT. Hoppers now wait 4 ticks after a move, read from BDS's hopper constructor (0xd415b30 sets the field its move copies into the cooldown); Helix had assumed 8. Static read only, not timed on a running BDS. Moving items and picking up share one cooldown in BDS; they are two settings here with the same default. Block shapes, the 0 to 15 signal strength the block state holds and repeater delays of 1 to 4 are not settings.
- **Chest and hopper minecart windows (written from BDS 1.26.60's code, not tested in game):** clicking the cart opens it as BDS does: a chest cart opens a plain container window and a hopper cart a hopper window, naming the block the cart is in and the cart. Helix had sent the cart window types. Refused without the open containers ability, for a dead player, with a window open, and past 12 blocks measured to the middle of the cart's block. Every take is checked against the server's slots, including two players on one stack, a slot past the hopper cart's five, a cart that broke, and a cart that left the player's region or unloaded, whose items in memory may already be saved. Helix also opens a cart from an Interact aimed at it, which BDS does not (it opens the player's own inventory unless riding something with slots); kept because the client was seen asking this way.
- **Doors and other shapes that need the world (written from BDS 1.26.60's code, not tested in game):** every moving entity, explosion ray, arrow and dismount now collides with a door's panel, worked out from both its halves (`player/collision.go`). Scaffolding holds only what is wholly above it, a shulker box is a whole block. Not done: an open shulker's lid box, powder snow (its box depends on the entity's fall distance), piston arms, moving blocks and the end gateway have no box for these yet.
- **Shulker boxes (written from BDS 1.26.60's code, not tested in game):** they open like chests, on the same `Chest` API and events. Each opens towards the side it was placed on and needs the lower half of that block free; it never pairs, and its lid moves at half a chest's speed with its own sounds. No shulker box can go in one, by a player or a hopper. Breaking one keeps the items and name in the dropped box and placing that box brings them back; in creative, only a box with items in it drops. Not done: the lid pushing entities and its taller collision box, dispensers placing boxes, recipes keeping the items when dyeing, washing in a cauldron. Assumed, not traced: nothing spills when a box goes some other way than a break. Not identified: a value in BDS's box that is 1 where a chest's is 64 (vtable slots 22 and 77), not used.
- **Jukebox songs (written from BDS 1.26.60's code, not tested in game):** the song is sent as a sound with a handle to each player in reach (view radius in blocks plus 46, along the ground), with `RecordStarted` when it starts; one who comes later gets it from where it has got to, one who leaves reach and everyone at the end or eject get it stopped. As in BDS a jukebox loaded from disk keeps its disc but does not play. Not done: the note particle's colour, and a sound event BDS sends each second (id 441, not identified).
- **Pushed out of blocks (written from BDS 1.26.60's code, not tested in game):** items and experience orbs inside a block's collision box are pushed out (`player/pushout.go`, helix-data `bds/block_push.json`): the nearest open side, 0.25 blocks a tick, 0.1 for stairs. With every side closed and a plain block they rise through the blocks with collision off until out. BDS's code runs this for those two only, so other entities do not get it.
- **Boats (written from BDS 1.26.60's code, not tested in game):** every boat, chest boat and bamboo raft (woods 0 oak to 10 poplar as BDS registers them, `player/boat.go`). Placing is BoatItem::_useOn: only on a block's top with air above, at the point clicked, turned to the player's yaw plus 90, refused when an entity other than a dropped item is in BDS's check box, the item's name kept as the boat's. Hits are Boat::_hurt: integrity 40, back a point a tick, damage times ten off, broken at 0 or at once by a creative player; breaking drops the boat's own item (named as the boat) unless creative or doentitydrops is off, and a chest boat spills its items first. Riding uses entities/boat.json's seats (one in the middle, two side by side, the raft's higher, a chest boat's one front seat), rotation locked to 90 and turned -90. The driver's client predicts the boat, as in BDS (whose server ignores a client's MoveActorAbsolute in 1.26.60); Helix takes the boat's place from the driver's PlayerAuthInput, whose position is the boat's own when the input names it as the client-predicted vehicle (measured in game with the `-debug` boat trace: the reported y is the boat's float height; not yet found in BDS's code), turned by its vehicle rotation (taken as pitch then yaw, not yet checked). Chest boats open as a plain container window like a chest cart (BDS 0x737E270) when used crouching or asked for by Interact, 27 slots, saved with the boat. An empty boat moves by itself (`player/boatphysics.go`) as BDS 1.26.60's systems run it: the buoyancy timer (IncreaseBuoyancyTimer, speed times 30 plus 1, times 0.05, big waves 0.03 odds at 10 times), gravity ((vy - 0.04) * 0.98), the float (BuoyancyFloat: how far below its waterline, the waves' (sin + 1) * 0.035, a climb of at most vy * 0.7 + 0.05), BoatMoveFriction (0.9 in water, the block it rests on while on the ground, none in the air), then a move through the blocks' collision boxes; explosions push it. A boat's position is BDS's: 0.375 (Boat::reloadHardcoded's height offset) above the bottom of its box, added when it is placed as Actor::moveTo does, and saved that way; boats saved before this sit 0.375 too low once. Every boat sends BDS's buoyancy JSON in actor data 121 (BuoyancyData, written by JsonCpp's FastWriter), which the client needs to float a boat, its own driven one included. Whether it floats compares its position with BDS's liquid height (getLiquidBlockHeight, a source reaching the block's top, a ninth higher than Helix's other liquid surface). Which order BDS runs the friction against the move is not read yet (Helix slows it first, as Java did); waves is BDS's movement type 1 (checked in the JSON writer). A player who leaves the game in a boat keeps the seat (saved in BDS's LinksTag, the cushion's way, now shared in `player/keepseat.go`) and is put back when it returns near the boat. With `-debug` an empty boat logs its position for two seconds after its driver gets out, to compare the server's float with the client's. Not built: lava damage, pulling mobs in, dispensers placing boats, hoppers taking from chest boats, the paddles other players see, keeping riders across a restart.
- **Riding (written from BDS 1.26.60's code, not tested in game):** its own package, `ride`, for anything with a rideable component, used by minecarts first and meant for mobs later. Who may get on (seats free, family, width, crouching), which seat each rider gets by how many ride, seat offset with the rider's height added, position turned with the vehicle, seat data and links, and where a rider lands (`ExitVehicleSystem`: beside, behind and ahead of the vehicle, on its level, a block up, a block down, else on top). The server puts a rider in its seat and riders use no hunger. Not done: the `on_top_center` and `look_direction` dismount modes (they land on top), `rotate_rider_by` expressions, pulling mobs in, steering, comparing what is sent with BDS. One block property BDS lets a rider land in is not identified yet.
- **Furnaces, blast furnaces and smokers (written from BDS 1.26.60's code, not tested in game):** `player/furnace.go`, as a `Chest` with a `machineSpec` like hoppers, with BDS's numbers in helix-data's `bds/furnace.json`. One item smelts in 200 ticks in a furnace and 100 in a blast furnace or smoker, from the recipes BDS sends for each block. Fuel burns for the cook time times the item's share from BDS (coal and charcoal 8 cooks, coal blocks 80, logs, planks and other wood 1.5, sticks and saplings 0.5, lava buckets 100 and leave the bucket; nether wood is no fuel), so a blast furnace burns fuel twice as fast. A furnace lights when it has something to smelt, cools by 2 ticks a tick when it cannot cook, starts again when the input changes, swaps between its lit and unlit block keeping its slots, plays its sound every 20 to 99 ticks while lit, and turns a bucket in the fuel slot into a water bucket when it dries a sponge. Players get the experience of what they take out at once, as orbs, rounded at random; items hoppers take out store theirs, which a broken furnace gives as orbs with what its result slot is worth. Hoppers feed the input from above and fuel from the sides, and take the result and empty buckets from below; players can only put fuel in the fuel slot and nothing in the result. Saved as BDS saves it (BurnTime, CookTime, BurnDuration, StoredXPInt). API: `Systems.FurnaceFuel`, `FurnaceResult` (return false to stop an item smelting, per furnace if wanted) and `FurnaceXP`; `OnFurnaceBurn` (change the burn time, cancel, or clear `Consume` for free fuel) and `OnFurnaceSmelt` (change the result, cancel, or keep the input); `Chest.Furnace()` reads and sets burn time, cook progress, stored experience and the cook speed (`SetCookTicks`, not saved). Not done: the fuel aux value BDS also sends to the window, the achievements BDS sends, and recipes placed from the recipe book into the furnace. The window's data keys are the protocol package's, not checked against BDS.
- **Repairing two items in the crafting grid (written from BDS 1.26.60's code, not tested in game):** `player/craftrepair.go`, BDS's repair item special recipe (found by its UUID in CraftingData, so the grid and the recipe book both work). Exactly two items, of one kind that has durability, one of each, anywhere in the inventory's or a table's grid. The result is a new item of that kind whose durability is the two items' added up plus a twentieth of the most, capped at full; it keeps nothing of the inputs (enchantments, names, earlier anvil work, trims all go) except curses, which both items pass on. An item whose repairable component names its own kind (only spears in vanilla) is instead repaired through that rule, each way round, keeping the less damaged one with all its data, plus the other's curses. Anvils (material, two of one item, books) and grindstones were already built (`player/anvil.go`). API: `Systems.CraftRepair`, `OnCraftRepair`. To try repairs, `Player.SetHeldDamage` (with `Held`, `MaxDamage`, `ItemStack.Damage`) sets the damage of the item in hand, and with `-testcmds` `/damageitem <amount>`, `max` or `fix` does it from the game (never breaking the item, refusing one that cannot be damaged).
- **Signs and hanging signs (written from BDS 1.26.60's code; the lead checked writing, glow ink, ink sacs and dyes in game on 2026-11-05, nothing compared with BDS side by side):** `player/sign.go`. All 13 woods' standing, wall and hanging signs place as BDS's SignItem and HangingSignItem do: a sign never under a block, standing on top (16 ways), on the side clicked otherwise; a hanging sign under a block (attached and turned 16 ways when sneaking or when the block above has no full underside, else 4 ways) or hung from any other face across it, turned to face the player. Placing opens the editor on the front. The block entity saves FrontText, BackText (text, owner, colour, glow, outline, formatting) and IsWaxed as BDS does, and is sent with LockedForEditingBy. Clicking a sign opens the side the player sees (BDS's rule from the sign's rotation, taken from the block's middle rather than the sign's shape); honeycomb waxes it (then only the waxed sound plays), a glow ink sac makes that side glow, an ink sac takes the glow off, a dye colours the text with BDS's colour table, each only when there is text and something changes, and each used up outside creative. Adventure players cannot change signs, and a sign open in someone's editor is theirs until they finish. The client's edit is taken per side whose text changed, owned by the editor's XUID; Helix takes it only from the player editing (BDS's packet handler before that was not read). Standing and wall signs fall without solid material below or behind, ceiling hanging signs without something to hang from (a lantern's rule, BDS's support check read only roughly), side hanging signs never. API: `SignAt`, `SetSignAt`. Not built: signs that come with text on the item, a hanging sign lining up with one above it, text filtering, signs saved before 1.20's two sides.
- **Banners and the loom (written from BDS 1.26.60's code, not tested in game):** `player/banner.go` and `player/loom.go`, with BDS's pattern table, dyes and pattern items in helix-data's `bds/banner.json` (dumped from BDS's memory). A banner goes standing on top of a block (turned the 16 ways a sign turns, by where the player looks) or hanging on the side clicked, never under a block, and only against a block whose material is solid; it falls off when that block goes. Its block entity saves Base, Type and Patterns as BDS does and is sent to clients so they draw it. Breaking it drops the banner with its colour as the aux value and its patterns and type in the tag, which placing brings back; pick block does the same. The loom opens its window and works out the result itself from the slots the server holds: a known pattern, a normal (not ominous) banner with fewer than 6 patterns, one of the 16 dyes (ink sacs, lapis, bone meal and cocoa beans are not dyes here, as in BDS), enough banners and dye for how many the client asks to make at once, and the pattern's own banner pattern item for creeper, skull, flower, Mojang, bricks, bordure, piglin, globe, flow and guster (never used up), nothing in that slot for the rest. One banner and one dye are used per banner made. As in BDS the loom also accepts the base and illager patterns with no item. API: `Systems.Loom` (what a loom makes), `OnLoom` (change or refuse a result), `Setup.Banners` (change the patterns, dyes, pattern items and the limit), `BannerAt` and `SetBannerAt`. **Shields with banners:** a shield and a banner anywhere in the crafting grid make the shield with the banner's colour (Base) and patterns, keeping the shield's own data, as BDS's shield recipe does (found by the UUID BDS sends it under in CraftingData, so the recipe book and grid both work); a shield that already has a banner takes none, a banner with more than 6 patterns is refused, and an ominous banner gives the illager pattern in white. Other items in the grid are not looked at, as in BDS, and are used up. API: `Systems.ShieldBanner`, `OnShieldBanner`. Not built: washing a pattern off a banner or shield in a cauldron (Helix has no cauldrons), the other banner crafting recipes (copying, adding a pattern on the crafting table), ominous banners from raids and loot (an ominous banner loaded from a world keeps its type).
- **Redstone (written from BDS 1.26.60's code, not fully tested):** the lead has checked in game that wire, repeaters, torches, lamps, copper bulbs and comparators work, but only those basics. Nothing has been compared with BDS side by side, and timing, large circuits and edge cases are unproven, so do not treat redstone as one-to-one yet. The circuits are the new `redstone` library, connected by the `circuit` package: wire with BDS's connection rule, breaking a tick after losing its support, torches, repeaters, comparators, levers, buttons, redstone blocks, lamps, doors, trapdoors and fence gates. Wire, repeaters and comparators can only be placed where they survive. Items named differently from their block place it (redstone, repeaters, comparators, string, sugar cane, seeds, crops, berries, cocoa); seeds and crops have no check of what they stand on. Comparators save their output in BDS's block entity and read chests, saved containers, composters, cauldrons, cakes, candle cakes, end portal frames, beehives, respawn anchors and copper bulbs, through a solid block too, with BDS's click sound. Circuits move with their chunks when regions merge or split. Pistons and sticky pistons (BDS's push table, 12 block limit, slime and honey, blocks breaking, block entities carried along), hoppers, dispensers and droppers are built but untested in game. Pistons push dropped items, lit TNT and players out of the way and carry what stands on a block moving sideways (a straight push, not BDS's full sweep: no slime bounce, and only players stop at blocks; a player's client gets a movement correction), a moved double chest loses its partner, hoppers work with chests, hoppers, dispensers, droppers and furnaces (no composters or minecarts), and a dispenser throws every item but TNT, water buckets, lava buckets and empty buckets out as an item (no arrows, fish or powder snow buckets and so on; water still pours in the nether). Observers, pressure plates (wood for anything, stone and blackstone for mobs, weighted by count, seen by players and dropped items), note blocks (BDS's instrument table, tuning, saved note) and trapped chests are built but untested in game. Daylight sensors (written from BDS's code, not tested in game) read the sky every 20 clock ticks and give power by the sun, inverted ones by what the sky lacks; weather is left out as Helix has none, and they read the stored sky light (`Region.SkyLight`). Buttons (not tested in game) can only be placed on, and fall off without, a block whose face holds things, so not on doors, and arrows press and hold wood buttons and wood plates (BDS's box for buttons; a projectile's box on plates is assumed centred on it). Target blocks are built from BDS's code: a projectile's hit (`Host.HitTarget`) gives 1 to 15 by how near the centre of the face it lands, for 8 ticks or 20 for an arrow or trident, and `/targettest` with `-testcmds` does the same without one. A punch does nothing, as in BDS. Not ported: a flag BDS sets on the target's component whose meaning was not found, and its telemetry and script events. Note particles have no colour. Tripwire hooks and tripwire (attaching, setting off, cutting with and without shears, sounds) are built but untested in game. TNT and explosions are built from BDS's code but untested in game: redstone, flint and steel, fire charges, Fire Aspect, dispensers and other explosions light TNT; explosions break blocks by BDS's rays and resistance, drop items, hurt and push players, items, orbs and lit TNT, and can be run by plugins (`Explosion`, `Host.Explode`, `OnExplosion`, `Systems.Explode`, `Exploded`, `Blastable`, `Host.SpawnTNT`). Shields do not stop blasts yet, pistons push lit TNT only as straight pushes, and game rules are read when the world opens. Jukeboxes and music discs are built from BDS's code but untested in game: a disc goes in and plays, a click throws it out, the jukebox gives full power while the song lasts, a comparator reads the disc's level from BDS's table, and breaking it drops the disc. Hoppers do not feed jukeboxes (not checked in BDS). Water and lava flowing in front of an observer fire it, as in BDS; a block set to the state it already has no longer wakes observers. Comparators read 0 from lecterns, item frames, command blocks, crafters, chiseled bookshelves, shelves, sculk sensors, creaking hearts, copper golem statues and detector rails, whose block entities or carts are not run yet. Bells ring when clicked from the right side or when power comes, change between side and between-two with their holders, and fall without one. Crafters take items evenly over the enabled slots, make crafting table recipes on a redstone pulse into the container in front or out of the front, and comparators read them; recipes made in code (fireworks, banner copies) do not craft yet.
- **Everything else** (mobs, combat, commands, UI) comes after core player gameplay.
  [API_DESIGN.md](https://github.com/helix-bedrock/.github/blob/main/API_DESIGN.md) lists which
  topics are designed.
- **The API is not stable yet.** Names and behaviour may still change between commits.

</details>
