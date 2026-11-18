# Progress

Where each part of Helix stands. Updated 2026-11-12.

Live progress, by feature: [helixbedrock.com/progress](https://helixbedrock.com/progress/).
Engine notes with API names: [STATUS.md](STATUS.md).

| Mark | Means |
|---|---|
| ✅ | Done, or works in game |
| 🟡 | Works in part; more to add or check |
| 🧪 | Built, being tested in game |
| ⬜ | Not started |

| Part | State |
|---|---|
| Networking and login (NetherNet, RakNet, Xbox Live, encryption, resource packs) | ✅ done |
| World storage (BDS's format, pluggable stores) | ✅ done |
| Registry (blocks, items, biomes, entities, creative items, recipes) | ✅ done |
| Chunks, regions (parallel ticking, merge and split), light, weather, day cycle | ✅ done |
| Vanilla terrain ([worldgen](https://github.com/helix-bedrock/worldgen)), the Nether and the End, portals | ✅ works in game |
| Players: movement, health, hunger, combat, shields, totems, elytra, beds | ✅ works in game |
| Inventory, containers, crafting, smithing, enchanting, anvils | ✅ works in game |
| Redstone ([redstone](https://github.com/helix-bedrock/redstone)), pistons, observers, TNT, rails and minecarts | ✅ works in game |
| Farming, plants, falling blocks, fire, fishing, bundles, fireworks | ✅ works in game |
| Mobs ([entity](https://github.com/helix-bedrock/entity)): spawning, fighting, equipment, drops | 🟡 works in game; behaviour still being checked against BDS |
| Potions, effects, brewing, crossbows, tridents, mace, breeding, leads, spawners, armour stands | 🧪 built, being tested in game |
| Command system, selectors, `/give`, `/summon`, `/teleport`, `/effect` | 🟡 works; most vanilla commands still to add |
| Villages and trading, raids, Ender Dragon, Wither, structure loot, maps, beacons | ⬜ not started |
| Load testing (400 players in one region, heavy redstone, mass entities) | ⬜ not started |
| Plugin API (WASM plugins, sandbox, custom content) | ⬜ not started |

These lists only cover what we know about: there are surely mechanics we have not found yet
that still need to be added.
