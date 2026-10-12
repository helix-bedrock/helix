# Helix

Helix is a Minecraft: Bedrock Edition server written in Go, built for
region-based multi-threading.

## Based on Dragonfly

Helix started as a copy of [Dragonfly](https://github.com/df-mc/dragonfly)
by the df-mc contributors, taken on 2026-10-12 at commit
[`1793369709ce`](https://github.com/df-mc/dragonfly/commit/1793369709ceed934675d69e86ff6c828e4216c8).
The first commit of this repository is that code, renamed to Helix and
otherwise unchanged. Everything after it is Helix's own work. See
[CREDITS.md](CREDITS.md).

## Running

Requires Go 1.26 or later.

```shell
go run main.go
```

Press ctrl+c to stop the server.

## Regions

Each world is split into regions that tick in parallel, one goroutine each,
like Folia. Chunks are grouped into sections (8 by 8 chunks by default) and
every section belongs to one region. Regions stay at least two sections
apart: when players bring them closer, they merge; when a region's areas
drift apart, it splits. A region only touches its own chunks and entities,
so ticks never wait for each other. Set `RegionSize` under `[World]` in
`config.toml` to change the section size; 1 or 2 makes region borders
common, for testing them. The debug dashboard draws the region map.

Work that needs the whole world at once (commands, which may target any
player, and sleeping through the night) runs as a global task: every region
merges into one first, and they split apart again on their own. Something
that reaches into another region's chunk, such as a big explosion, sees it
as unloaded that once and makes the two regions merge.

## Debugging

Set an address in `config.toml` to open the debug dashboard:

```toml
[Debug]
  Address = "localhost:8080"
```

Then open `http://localhost:8080` in a browser. It shows every packet sent
and received (filter by player, packet name and direction), packet counts,
each world's tick time split into its parts, which blocks scheduled updates
(water, lava, redstone) spent time on, and Go's profiler at
`/debug/pprof/`. `/record?seconds=60` downloads a recording of every packet
for a minute (`&full=1` includes whole packets) to send with a bug report.
Keep the address on localhost: the dashboard shows every player's packets.
