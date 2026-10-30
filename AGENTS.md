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
- `cmd/helix`: the server program.
- `play.go`: TEMPORARY empty-world spawn until the world and player designs
  exist.

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

## Gotchas

- Topics `API_DESIGN.md` does not mark as decided (event list, custom
  events, entity visibility API, commands, permissions, config file) are
  designed with the owner first; ask before adding public packages for them.
