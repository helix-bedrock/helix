# helix

Helix: an open-source Minecraft: Bedrock Edition server engine in Go.

**Status as of 2026-10-29:** restarted from scratch with its own architecture.
Networking works: NetherNet and RakNet, and the login the way BDS 1.26.60 does
it (Xbox Live checks, encryption, resource packs). Players spawn in an empty
world until the world is added.

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
