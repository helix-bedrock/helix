# helix

Helix: an open-source Minecraft: Bedrock Edition server engine in Go.

**Status as of 2026-10-30:** restarted from scratch with its own architecture.
Networking works: NetherNet and RakNet, and the login the way BDS 1.26.60 does
it (Xbox Live checks, encryption, resource packs). The world folder
(`worlds/Bedrock level`, BDS's layout) opens at boot through the `world`
library's stores; players spawn at the level.dat spawn, or where a BDS world
saved them, in an empty world until chunks are sent. The block registry is
built at boot from BDS 1.26.60's data: 23,960 states of 1,480 blocks with
their network hashes and BDS's runtime numbers (light, flammability,
explosion resistance, friction, destroy time), and 2,080 items with the
network IDs, versions and components BDS sends; plugins can add, replace
and delete both in `Config.Setup`. Joining players get BDS's item list.

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
