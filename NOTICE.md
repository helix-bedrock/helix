# Notice

## Minecraft and Mojang

Helix is not an official Minecraft product. It is not approved by or
associated with Mojang or Microsoft. "Minecraft" is a trademark of Mojang
Synergies AB.

The game behaviour in Helix's engine, world generation, entity and redstone
code was rebuilt in Go by studying Mojang's Bedrock Dedicated Server (BDS).
That includes reading its decompiled binary. The algorithms and the game
behaviour they produce come from Mojang's work, and Helix credits Mojang for
them. Helix's Go code was written by the Helix authors. No Mojang source or
binary code is included.

Because of this, Helix is licensed for noncommercial use only (see `LICENSE`
and `ADDITIONAL-PERMISSIONS.md`). Nobody, including the Helix authors, may
sell Helix or its code.

Anyone who runs Helix must follow Mojang's Minecraft End User License
Agreement and Minecraft Usage Guidelines:
<https://www.minecraft.net/eula> and
<https://www.minecraft.net/usage-guidelines>.

## Mojang data

Some repos ship game data that came from Mojang: block states, items, recipes,
worldgen settings and similar files, taken from BDS, Mojang's
`bedrock-samples`, or Minecraft: Java Edition. That data belongs to Mojang.
The Helix license does not cover it.

## Third-party code

Code taken from other projects, with its source, commit and license, is listed
in each repo's `CREDITS.md`, and the original license texts are in `licenses/`.
