# helix

Helix: an open-source Minecraft: Bedrock Edition server engine in Go.

**Status as of 2026-10-30:** restarted from scratch with its own architecture.
Networking works: NetherNet and RakNet, and the login the way BDS 1.26.60 does
it (Xbox Live checks, encryption, resource packs). The world folder
(`worlds/Bedrock level`, BDS's layout) opens at boot through the `world`
library's stores; players spawn at the level.dat spawn, or where a BDS world
saved them, and get the world's chunks: the `world` package keeps each
player's simulation zone (tick-distance) loaded and saves changed chunks
at the end of each tick, while the view zone (view-distance) is loaded,
encoded once, sent in BDS's sub-chunk request mode and released. Loaded
chunks belong to regions that tick in parallel at 20 TPS each and merge
and split as players move (`TickCores` sets how many cores tick). Players
see each other: AddPlayer within BDS's 57-chunk disc (a setting,
`EntityViewRadius2`, changeable while running), MovePlayer for
their movement, and the player list; other entities stay in loaded
chunks, pass to the region they move into and unload with their chunk; where they were is saved when they
leave. Abilities, permission levels, game mode and attributes follow BDS
1.26.60 (layered abilities, its per-game-mode rules, its saved format) and
are sent at join in BDS's order. The inventory is the server's:
items are loaded from and saved in BDS's format, and every inventory
change the client asks for is checked as BDS checks it (stack network
IDs, counts, stack sizes, armour slots and offhand items as BDS lists
them, creative-only destroy and creative items) before it happens;
dropping and crafting are not done yet. World packets go through a handler table plugins can replace or
wrap, per packet or per system (movement so far), on the player's region
tick. Flat and
void worlds generate; vanilla terrain generation is not hooked up yet, so
an infinite world shows only what it has saved. The block registry is
built at boot from BDS 1.26.60's data: 23,960 states of 1,480 blocks with
their network hashes and BDS's runtime numbers (light, flammability,
explosion resistance, friction, destroy time), 2,080 items, 89 biomes with
BDS's numeric IDs and 138 entity types with their definitions, plus the
creative inventory and recipes, which can change while running. Plugins
can add, replace and delete any of them in `Config.Setup`. Joining players
get the registry packets; untouched, each is byte for byte BDS's.

Part of [Helix](https://github.com/helix-bedrock), an open-source Minecraft: Bedrock
Edition server engine in Go.

## Run

```bash
go run ./cmd/helix
```

## Contributing

See the [contributing guide](https://github.com/helix-bedrock/.github/blob/main/CONTRIBUTING.md).
AI-assisted contributions follow the
[AI policy](https://github.com/helix-bedrock/.github/blob/main/AI_POLICY.md).

## License

GPL-3.0. See [`LICENSE`](LICENSE). Ported code and its sources are listed
in [`CREDITS.md`](CREDITS.md).
