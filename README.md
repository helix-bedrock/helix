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
go run ./cmd/helix
```

Press ctrl+c to stop the server.

## Plugins

A plugin is a struct embedding `helix.Plugin` with `Info` and `Setup`
methods (RFC 0001). `Setup` registers listeners, any number per event, with
options for priority, skipping cancelled events and filters:

```go
func (p *Protect) Setup(r *helix.Setup) {
    r.BlockBreak(p.protect, helix.OnlyBlock("minecraft:diamond_ore"), helix.Priority(event.High))
    r.PlayerJoin(p.welcome)
}
```

Pass plugins to `helix.New` (see `examples/protect`). The engine fires events
directly where things happen; an event nobody listens to costs a fraction
of a nanosecond and allocates nothing. Plugins can be enabled and disabled
while the server runs (`srv.Plugins().Disable("Protect")`). A panic in a
plugin is recovered and logged with its name; `helix.Config.PanicLimit`
disables a plugin that keeps panicking.

A plugin can also:

- schedule work: `p.After(d, fn)`, `p.Every(d, fn)`, and `p.Do(world, pos, fn)`
  or `p.DoPlayer(player, fn)` to run on the region that owns something;
- register commands: `r.Command(cmd.New(...))`, hidden while it is disabled;
- watch or change packets: `r.PacketIn` and `r.PacketOut` (these run on the
  network goroutine, not a region);
- keep data: `p.DataDir()` and `p.Config(&v)` for `plugins/<Name>/config.toml`.

Disabling a plugin stops all of it: listeners, tasks, commands and packet
hooks. The console (stdin of `cmd/helix`) runs commands and `plugins`,
`plugins enable|disable <name>` and `stop`; `/plugins` lists them in game.

Listeners run on the region the event happens in, in parallel with other
regions. A listener may use the event's `Tx` for the world, but must not keep
it, the event or the player after it returns.

## Regions

Each world is split into regions that tick in parallel, like Folia. The
regions of every world run on one shared pool with a worker per core, so a
thousand idle regions cost no goroutines. Chunks are grouped into sections (8 by 8 chunks by default) and
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

A region whose tick takes longer than 50ms fires `SlowTick`, with the time it
took, so a plugin can find and deal with what crowds it.

Plugins can open more worlds while the server runs with
`Server.LoadWorld(name, server.WorldConfig{...})` and close them with
`UnloadWorld(name)`, which first moves their players to the overworld spawn.
Transactions run on pool workers, so they must never wait on another
transaction (`world.Call`, `Task.Wait`): use `Do` and return.

## Mobs

All 92 vanilla mob types come from the definition files of BDS
1.26.52.3, through helix-data: health, speed, size, families, loot, and the
behaviours each mob has with their settings and priorities. Spawn events
pick variants such as baby animals as BDS does. Try `/summon zombie`.

Behaviours run as goals, and walking mobs find paths with an A* pathfinder
ported from Pumpkin. Ported so far: `float`, `random_stroll`,
`random_look_around`, `look_at_player`, `panic`, `hurt_by_target`,
`nearest_attackable_target`, `melee_attack`, `melee_box_attack`, `tempt`,
`avoid_mob_type`, `swell`, `ranged_attack`, `breed`, `follow_parent`,
`random_swim`, `swim_wander`, `random_hover`, `random_fly` and
`float_wander`; and the components `target_nearby_sensor`, `explode`,
`shooter` (arrows), `equipment`, `breedable` and `ageable`. So creepers
explode, skeletons shoot, animals fed their food breed and their babies grow
up, fish swim and bats fly. Swimming and flying mobs steer straight at their
target rather than pathfinding. Other behaviours are skipped, so a mob may
do less than in vanilla.

Mobs make their idle, hurt and death sounds (idle timing and variants from
`ambient_sound_interval`; mobs without it use its documented defaults, an
assumption still to check against BDS) and drop the experience their
`experience_reward` gives when a player kills them. `server/molang`
evaluates the Molang expressions this needs.

Mobs spawn around players by BDS's spawn rules (biome, light, blocks,
height, difficulty, herd size, weight, density) and despawn by their own
`minecraft:despawn` component. The spawning loop, caps and distances are
the Minecraft Wiki's Bedrock figures, not yet checked against BDS. Slimes
spawn in slime chunks below y=40; swamp slimes and spawn rules that need
mob events, villages or delays are not done yet. Set
`server.Config.DisableMobSpawning` to turn spawning off.

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

## Licence

Helix is licensed under the GPL-3.0 ([LICENSE](LICENSE)). It is based on
Dragonfly (MIT, [LICENSE-DRAGONFLY](LICENSE-DRAGONFLY)) and ports code from
Pumpkin (GPL-3.0); see [CREDITS.md](CREDITS.md).
