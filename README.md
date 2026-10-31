<div align="center">

<img src=".github/helix-mark.svg" alt="Helix" width="120">

<p>
  <b>An open-source Minecraft: Bedrock Edition server engine in Go</b><br>
  Mechanics one-to-one with BDS, a rich API, every part replaceable
</p>

[![Minecraft](https://img.shields.io/badge/minecraft-v1.26.60_(Bedrock)-black)](https://feedback.minecraft.net/hc/en-us/sections/360001186971-Release-Changelogs)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)
[![Status](https://img.shields.io/badge/status-in_development-orange)](STATUS.md)

</div>

## How it is checked

Behaviour comes from the decompiled Bedrock Dedicated Server (BDS) 1.26.60, compared with other
servers (PocketMine-MP, PowerNukkitX, Allay) and checked against BDS itself: the packets a player is
sent, the world files BDS writes and what happens in game. The decompiled code is **not** in this
repo; it was only used to read how the game works.

## Why Helix?

No Bedrock server software matches the official Bedrock Dedicated Server (BDS) one-to-one yet.
Helix aims to be the first: every mechanic works exactly as it does in BDS, so a survival server
plays the same as vanilla.

At the same time, Helix is built to run any kind of server. Every mechanic is the BDS one by default
but can be replaced, and the API aims to be as rich as possible: richer than PocketMine-MP,
PowerNukkitX and Allay on Bedrock, and on par with what Paper gives Java Edition servers. Vanilla
features are modules built on the same public API plugins use, so a Skyblock or minigame server can
turn them off, keep the fast core and replace anything, down to the engine's fundamentals. Regions
tick on many cores at once, so large servers stay smooth.

## Quick Start

```bash
go run ./cmd/helix
```

The server opens the world in `worlds/Bedrock level` (BDS's layout).

## Features

- **Same as vanilla:** the login, the registry packets, the world format and the game rules follow
  BDS 1.26.60.
- **Everything replaceable:** registry entries and packet handlers can be added, replaced or wrapped
  by plugins, also while running.
- **Data-driven:** blocks, items, biomes, entities, creative items and recipes are loaded from
  Mojang's data in [helix-data](https://github.com/helix-bedrock/helix-data), not hard-coded.
- **Multi-core:** regions tick in parallel and merge and split as players move.
- **BDS worlds:** opens and saves worlds in BDS's own format, so they move between Helix and BDS.

## Roadmap

What works today and what is left is in [STATUS.md](STATUS.md).

| Part | State |
|---|---|
| Networking and login (NetherNet, RakNet, Xbox Live, encryption, resource packs) | ✅ done |
| World storage (BDS's format, pluggable stores) | ✅ done |
| Registry (blocks, items, biomes, entities, creative items, recipes) | ✅ done |
| Chunk loading, sending and saving, tickets | ✅ done |
| Regions (parallel ticking, merge and split) | ✅ done |
| Block ticks (random and scheduled) | 🟡 ticks run; no vanilla block behaviour yet |
| Players (spawn, movement, seeing each other, abilities, attributes) | 🟡 in progress |
| Inventory | 🟡 changes checked as BDS does; dropping and crafting not done |
| Vanilla terrain generation ([worldgen](https://github.com/helix-bedrock/worldgen)) | ⬜ not hooked up |
| Mobs ([entity](https://github.com/helix-bedrock/entity)), combat, redstone, commands, UI | ⬜ not started |

**The goal is to match the latest Minecraft version exactly.** After that, every time Minecraft
updates, Helix will be updated to the new version and gain everything the update adds. The project
is maintained.

## Contributing

- **Found a bug or a difference from vanilla?** Open an [issue](https://github.com/helix-bedrock/helix/issues)
- **Want to contribute code?** Read the [contributing guide](https://github.com/helix-bedrock/.github/blob/main/CONTRIBUTING.md)
  and the [AI policy](https://github.com/helix-bedrock/.github/blob/main/AI_POLICY.md)

## License

Helix is licensed under [GPL-3.0](LICENSE). Ported code and its sources are listed in
[CREDITS.md](CREDITS.md).
