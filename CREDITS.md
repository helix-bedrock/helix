# Credits

## Dragonfly

Helix is based on [Dragonfly](https://github.com/df-mc/dragonfly),
Copyright (c) the df-mc contributors, used under the MIT licence in
[LICENSE](LICENSE).

- Copied: 2026-10-12
- Source commit: `1793369709ceed934675d69e86ff6c828e4216c8` (2026-09-20)
- Changes in the first commit: module path renamed from
  `github.com/df-mc/dragonfly` to `github.com/helix-bedrock/helix`, the
  default server name, the README and CI. All other code is Dragonfly's as
  it was at that commit.

Every later commit is Helix's own and is listed in the git history.

## Data

Block states come from [helix-data](https://github.com/helix-bedrock/helix-data), dumped from the Bedrock Dedicated Server, instead of Dragonfly's `block_states.nbt`. Item and creative data are still Dragonfly's (`server/world/vanilla_items.nbt`, `server/item/creative/creative_items.nbt`).

## gophertunnel

Parts of [gophertunnel](https://github.com/Sandertv/gophertunnel) (MIT,
Copyright (c) Sandertv), v1.62.0, which Dragonfly depended on:

| Helix path | gophertunnel path | Notes |
|---|---|---|
| `server/internal/text` | `minecraft/text` | Copied unchanged |
| `server/resource` | `minecraft/resource` | Copied unchanged |
| `server/network/gamedata.go` | `minecraft/game_data.go` | `GameData` copied |
| `server/network/conn.go`, `listener.go` | `minecraft/conn.go`, `listener.go` | Server-side login, resource pack sending, start game and spawn sequence, and the server list answer, rewritten on Helix's own `protocol`, `raknet` and `nethernet` libraries |

Login verification (Xbox Live tokens and the legacy chain) is Helix's own,
in `github.com/helix-bedrock/protocol/login` and `protocol/auth`.

## Dragonfly pull requests

Open (unmerged) Dragonfly pull requests ported into Helix after the base
copy, each in its own commit naming the PR and its author. All MIT.

| PR | Author | Change |
|---|---|---|
| #1381 | THEBOSS9345 | Chunk callbacks no longer run nested (loader deadlock) |
| #1419 | schphe | Loader calls the viewer with its lock released (deadlock) |
| #1400 | schphe | Chunks with moved entities, scheduled ticks or changed block entities are saved |
| #1412 | schphe | Entities no longer leak when their chunk unloads |
| #1413 | schphe | Scheduled block updates no longer run twice |
| #1416 | schphe | Entities stop ticking once closed |
| #1415 | schphe | Fireworks do not hurt through walls |
| #1417 | schphe | Inventory slot change data race |
| #1418 | schphe | Oversized stacks are dropped whole |
| #1408 | schphe | Chest pairing no longer duplicates contents |
| #1401 | schphe | Two item stack request duplications |
| #1392 | schphe | Refused slot writes no longer destroy items |
| #1399 | schphe | No fall damage in mid-air past an overhang |
| #1391 | schphe | Book title and author length bounded |
| #1390 | schphe | Smithing with an empty slot refused |
| #1422 | schphe | Sub chunk layer loop and compaction |
| #1474 | HashimTheArab | Stairs corner continuation |
| #1461 | HashimTheArab | Door placement and hinges |
| #1460 | HashimTheArab | Cake eating and candle ignition |
| #1442 | HashimTheArab | Item use keeps inventory changes made during it |
| #1440 | HashimTheArab | Sword and shears mining speeds |
| #1348 | HashimTheArab | Explosion knockback |
| #1346 | HashimTheArab | Bedrock arrow damage |
| #1333 | HashimTheArab | Entity network offsets |
| #1453 | EinBexiii | Four entity bugs |
| #1454 | EinBexiii | Items pushed out of blocks |
| #1445 | didntpot | User data and multi recipes craftable |
| #1365 | FDUTCH | Vanilla shulker box drops |
| #1275 | RestartFU | Nil block entity NBT |
| #1289 | root-nat | Inventory resync after a release that consumes nothing |
| #1471 | MEMOxiiii | Spectator mode matches vanilla |
| #1378 | THEBOSS9345 | Fall damage world setting |
| #1379 | THEBOSS9345 | Rotation when teleporting |
