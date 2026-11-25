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
go test ./...
go run ./cmd/helix   # start a server with the default configuration
go test ./world -run '^$' -bench RegionLayout -benchtime 1x   # 400 fake players per region layout (~4 min)
go run ./cmd/sim -list                          # load test scenarios
go run ./cmd/sim -scenario tnt -n 0,100,1000    # one scenario at several sizes, each on a fresh server
```

Local development uses a `go.work` (never committed) that points at the
sibling library folders, so unreleased library changes are used.

## Layout

Per-file detail is in [`LAYOUT.md`](LAYOUT.md): search it for the package or
file you touch before changing it, and update its entry when behaviour changes.

- root: `Server`, `Config`, `Setup` (boot builder), `world.go` + `worldformat.go`
  (open the world, BDS conversion, `-export-bds`), `properties.go`.
- `network`: NetherNet/RakNet transports, login steps in BDS order.
- `registry`: boot `Setup`, frozen tables (blocks, collision, items, biomes,
  entity types, creative, recipes), `Vanilla` loads helix-data.
- `world`: dimensions, regions, scheduler, chunks in memory, loading/saving.
- `player`: everything a player does, one file per job (containers, block
  entities, items, mobs, projectiles, explosions, ...).
- Block systems: `liquid`, `fall`, `fire`, `farm`, `drip`, `connect`,
  `blockuse`, `portal`, `circuit` (redstone, rails, bells, targets).
- Game systems: `mob`, `ride`, `attach`, `effect`, `loot`, `crafting`,
  `village`, `weather`, `daytime`, `direction`, `generator`.
- `command` (system) and `commands` (built-ins, one file each).
- `sim` (load tests), `mobdebug`, `cmd/helix` (server program), `cmd/sim`.

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
- Storage: Helix's own format per world (`world/hx`), converting to and
  from BDS worlds without loss; pluggable stores.

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
   the truth: the server's state decides, with BDS's own checks and
   corrections as the base. Where a cheat or exploit works on BDS, Helix
   closes it with its own stricter rule (marked "Helix's own rule, beyond
   BDS") that never refuses legit play; inventory, items, enchanting and
   client sounds are always checked against server state. Anti-cheat checks
   (movement first, then reach, kill aura, CPS, packet limits) live in one
   anti-cheat module with a shared check interface, not as one-off code.

## Gotchas

- Topics `API_DESIGN.md` does not mark as decided (event list, custom
  events, entity visibility API, commands, permissions, config file) are
  designed with the owner first; ask before adding public packages for them.
