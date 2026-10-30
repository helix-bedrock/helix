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
```

Local development uses a `go.work` (never committed) that points at the
sibling library folders, so unreleased library changes are used.

## Layout

- `helix` (root): `Server`, the main struct, and its `Config`.
- `network`: transports (NetherNet, RakNet) and the login sequence as named,
  replaceable steps, in BDS 1.26.60's order.
- `registry`: the one boot `Setup` (register, replace, delete) and the
  frozen tables. Blocks so far: dense state IDs, network hashes, a per-state
  property table swapped whole on live changes, and `chunk.BlockRegistry`
  for the world library. Items: BDS's network IDs, versions and
  components, stack sizes and tags, and the ItemRegistry packet built once.
  Biomes: BDS's numeric IDs, climate and tags, `Resolve` for deleted or
  unknown saved IDs (plains). Entity types: identifiers, runtime IDs and
  behaviour pack definitions (engine hooks come with entities). Creative
  inventory and recipes are `Live` packets swapped on change, re-sent to
  online players. Unchanged, every packet is BDS's own; once a plugin
  changes biomes or entity types they are rebuilt (tests check the rebuild
  of vanilla is byte for byte BDS's). `Vanilla` loads helix-data.
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
  57-chunk disc (spawn packets on entering, RemoveActor on leaving), and
  each entity's movement is built once per tick for everyone who sees it.
  Regions tick only the phases that exist so far (queued work, player
  input, chunk sending, visibility, movement, one flush per player,
  saving). Loads, generation, network encoding and
  saves run on a worker pool, jobs for one chunk in order on one worker. Each player is a `Viewer`
  with two zones: the simulation zone is held loaded, the view zone is
  sent and released. Encoded chunks (`NetChunk`) are shared by every
  player and kept in a byte-budget cache. Changed chunks are saved at the
  end of each tick from a copy-on-write snapshot. Generators: `Flat`
  (level.dat's layers), `Empty`; vanilla generation is not hooked up, so
  an infinite world's unsaved chunks are empty and never saved.
- `setup.go`: `Setup`, the one boot builder: the registry's `Setup` plus
  the packet handler table. `Handle[T]` replaces the handler of a packet
  type, `WrapHandle[T]` wraps it; system hooks (`Movement`,
  `WrapMovement`) swap one vanilla system without rewriting the packet.
  Vanilla registers through it first (`vanillaHandlers`), then
  `Config.Setup`; frozen into a slice indexed by packet ID.
- `input.go`: each player's ordered input queue (network goroutine to
  region tick, bounded, two swapped buffers), drained in the region's
  player input phase through the frozen handlers. Vanilla's
  PlayerAuthInput handler drops impossible values (NaN, infinite, beyond
  the world limit) and hands movement to the movement hook.
- `global.go`: the global owner (RFC 0002), one goroutine at 20 TPS that
  owns the player list and publishes a copy for readers.
- `player.go`: `Player` = session (connection, identity, skin) + entity +
  saved data. Joins in BDS's order as far as built (PlayerList broadcast,
  StartGame, registry packets, full PlayerList), shows players to each
  other with AddPlayer and MovePlayer, and saves Pos, Rotation,
  DimensionId and UniqueID back into the player's BDS data on leave. No
  inventory, abilities, game mode changes or block changes yet.
- `cmd/helix`: the server program.

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
5. Never trust a player. Every packet and every field in it is assumed to
   be a lie until checked against server state: values (NaN, ranges,
   sizes, IDs), rates and claims (where it is, what it broke or used).
   BDS's own checks are the baseline; a stricter check must never refuse
   what BDS accepts. The optional anti-cheat is separate.

## Gotchas

- Topics `API_DESIGN.md` does not mark as decided (event list, custom
  events, entity visibility API, commands, permissions, config file) are
  designed with the owner first; ask before adding public packages for them.
