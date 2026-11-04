# What Helix does

Status as of 2026-11-04. Helix was **restarted from scratch with its own architecture.** Players can
join, spawn in a BDS world, see each other, use their inventory, break and place blocks, and
sneak, sprint, swim and take fall damage. Everything follows BDS 1.26.60.

## What works

- **Networking:** NetherNet and RakNet, and the login the way BDS 1.26.60 does it (Xbox Live
  checks, encryption, resource packs).
- **Worlds:** the world folder (`worlds/Bedrock level`, BDS's layout) opens at boot through the
  `world` library's stores. Players spawn at the level.dat spawn, or where a BDS world saved them.
  Flat and void worlds generate.
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
  their chunk; where they were is saved when they leave.
- **Abilities:** permission levels, game mode and attributes follow BDS 1.26.60 (layered abilities,
  its per-game-mode rules, its saved format) and are sent at join in BDS's order.
- **Inventory:** items are loaded from and saved in BDS's format. Every inventory change the client
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
  on the player's region tick. Chests and ender chests have events before anything happens (open,
  close, each slot a player changes with the inventory slots it changes, pairing, spilling) and methods
  to read and change their items, name and pair (`Host.ChestAt`, `Host.WithChest`, `Chest`, `Player.EnderItem`).
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
- **Creative inventory:** items can be taken from the creative menu (the client's results
  message after a creative take is accepted).

## What is left

- **Vanilla terrain generation** is not hooked up yet, so an infinite world shows only what it has
  saved.
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
  first click did. Doors, trapdoors, fence gates, levers and buttons work (new `blockuse` package).
  Doors place both halves with BDS's hinge rule and fall apart without their other half or support.
  Buttons pop out after 20 or 30 ticks. No redstone output yet. Other blocks with a use (furnaces,
  beds...) do not react yet, so a block in hand is placed against them.
- **Fences, panes and bars (not yet tested in game):** since 1.26 their arms are in the block's own
  state, so the server works them out on placing and when a neighbour changes (new `connect` package,
  from BDS's per-state rules in helix-data's `bds/connections.json`). Walls are not done yet.
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
  map copies, banners, repairs...), the 1,456 recipes that keep an item's data (dyeing bundles and such), and the
  other screens (furnace, stonecutter...). **Smithing tables** work for every BDS recipe, upgrades and armour trims, with a
  plugin API: `Registry.EditSmithing` and `Setup.EditSmithing` clear, remove or change recipes (`Clear`, `Remove`,
  `RemoveFor`, `Set`), and `OnSmith` changes or refuses one use; `Setup.EditTrims` and `Registry.Trims` change the
  trim patterns and materials. **All recipes** have the same API: `Registry.EditRecipes` and
  `Setup.EditRecipes` list, clear (`Clear`, `ClearBlock`, `ClearSpecial`, `ClearBrewing`), remove (`Remove`,
  `RemoveFor`) or change and add (`Set`) shaped and shapeless recipes of any block, at boot or while running. Screens
  not built yet list their recipes but cannot use them. Torches, lanterns and ladders place and fall off by BDS's rules (`attach` package, helix-data `bds/support.json`): which way they face, what may hold them, and they drop when their holder goes. A grid only makes recipes tagged for the crafting table, whatever recipe the client names. `Player.EditRecipes` takes recipes away from one player (`Remove`, `RemoveFor`, `ClearBlock`, `Clear`, `Reset`): that player gets a filtered list and the server refuses the rest. Those rules are not saved. Items left in a grid stay there when the window closes:
  no return was found in BDS, so the client is taken to move them, which is not confirmed. **Item entities** are a first pass: they are saved with their chunk
  (not yet compared with a world BDS saved, and lost in chunks that are never saved, such as an
  infinite world's ungenerated ones), do not burn in lava or fire, are not pushed out of a block placed on them, and their
  spawn data is not yet compared with BDS. Item stack requests inside PlayerAuthInput are ignored.
- **Redstone (written from BDS 1.26.60's code, not fully tested):** the lead has checked in game that wire, repeaters, torches, lamps, copper bulbs and comparators work, but only those basics. Nothing has been compared with BDS side by side, and timing, large circuits and edge cases are unproven, so do not treat redstone as one-to-one yet. The circuits are the new `redstone` library, connected by the `circuit` package: wire with BDS's connection rule, breaking a tick after losing its support, torches, repeaters, comparators, levers, buttons, redstone blocks, lamps, doors, trapdoors and fence gates. Wire, repeaters and comparators can only be placed where they survive. Items named differently from their block place it (redstone, repeaters, comparators, string, sugar cane, seeds, crops, berries, cocoa); signs and banners do not place yet, and seeds and crops have no check of what they stand on. Comparators save their output in BDS's block entity and read chests, saved containers, composters, cauldrons, cakes, candle cakes, end portal frames, beehives, respawn anchors and copper bulbs, through a solid block too, with BDS's click sound. Circuits move with their chunks when regions merge or split. Pistons and sticky pistons (BDS's push table, 12 block limit, slime and honey, blocks breaking, block entities carried along), hoppers, dispensers and droppers are built but untested in game. Pistons push dropped items, lit TNT and players out of the way and carry what stands on a block moving sideways (a straight push, not BDS's full sweep: no slime bounce, and only players stop at blocks; a player's client gets a movement correction), a moved double chest loses its partner, hoppers only work with chests, hoppers, dispensers and droppers (no furnaces, composters or minecarts), and a dispenser throws every item but TNT out as an item (no arrows, buckets and so on). Observers, pressure plates (wood for anything, stone and blackstone for mobs, weighted by count, seen by players and dropped items), note blocks (BDS's instrument table, tuning, saved note) and trapped chests are built but untested in game. Daylight sensors place, join wire and swap with their inverted form, but give no power: Helix has no world time or sky light yet. Target blocks join wire and give no power until projectiles exist. Note particles have no colour. Tripwire hooks and tripwire (attaching, setting off, cutting with and without shears, sounds) are built but untested in game. TNT and explosions are built from BDS's code but untested in game: redstone, flint and steel, fire charges, Fire Aspect, dispensers and other explosions light TNT; explosions break blocks by BDS's rays and resistance, drop items, hurt and push players, items, orbs and lit TNT, and can be run by plugins (`Explosion`, `Host.Explode`, `OnExplosion`, `Systems.Explode`, `Exploded`, `Blastable`, `Host.SpawnTNT`). Shields do not stop blasts yet, pistons push lit TNT only as straight pushes, and game rules are read when the world opens. Rails are not done: they wait for minecarts. Comparators read 0 from jukeboxes, lecterns, item frames, command blocks, crafters, chiseled bookshelves, shelves, sculk sensors, creaking hearts, copper golem statues and detector rails, whose block entities or carts are not run yet.
- **Everything else** (mobs, combat, commands, UI) comes after core player gameplay.
  [API_DESIGN.md](https://github.com/helix-bedrock/.github/blob/main/API_DESIGN.md) lists which
  topics are designed.
- **The API is not stable yet.** Names and behaviour may still change between commits.
