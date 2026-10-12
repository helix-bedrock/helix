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
