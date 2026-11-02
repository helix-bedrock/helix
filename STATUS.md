# What Helix does

Status as of 2026-11-02. Helix was **restarted from scratch with its own architecture.** Players can
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
  when touched, leaving what does not fit. Other players see the held item change.
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
  on the player's region tick.

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
  like) do not open.
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
- **Breaking leftovers:** broken blocks drop nothing yet (needs loot tables), and only the placing
  player blocks a placement (BDS checks every entity against the placed block's collision boxes).
  Spears and other data-driven items do not wear yet. Stacking petals and leaf litter, and BDS's
  own snow layer rule, are not done.
- **Crafting** is not done yet. **Item entities** are a first pass: they are saved with their chunk
  (not yet compared with a world BDS saved, and lost in chunks that are never saved, such as an
  infinite world's ungenerated ones), do not burn in lava or fire, are not pushed out of a block placed on them, and their
  spawn data is not yet compared with BDS. Item stack requests inside PlayerAuthInput are ignored.
- **Everything else** (mobs, combat, redstone, commands, UI) comes after core player gameplay.
  [API_DESIGN.md](https://github.com/helix-bedrock/.github/blob/main/API_DESIGN.md) lists which
  topics are designed.
- **The API is not stable yet.** Names and behaviour may still change between commits.
