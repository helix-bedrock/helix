# Helix Additional Permissions

These permissions are added to the PolyForm Noncommercial License 1.0.0 that
covers this software (see `LICENSE`). The licensor gives them on top of that
license. Where this file and the license disagree, this file wins, but only
for the permissions it lists. It never takes a right away.

## 1. Running servers

You may run the software, changed or unchanged, to operate a Minecraft:
Bedrock Edition server, even if the server makes money. For example, a server
may take donations or sell ranks, subscriptions or in-game items, as long as
it follows Mojang's Minecraft Usage Guidelines. Running a server counts as a
permitted purpose, even for a company.

## 2. Plugins

A plugin is a program that works with the software only through its public
API (its exported Go packages, its plugin interfaces, and the scripting API
once there is one), and that does not include the software's own source code
beyond what is needed to call that API.

A plugin is your own work, not a "new work" made from the software. You may
license, sell and distribute your plugins under any terms you choose. This
license applies to a plugin only where the plugin copies the software's code.

## 3. What stays forbidden

Except as permitted above, you may not, for money or anything else of value:

- sell, rent or license the software, any part of its code, or any version
  of it, changed or unchanged;
- include the software or its code in a product you sell;
- charge for access to the software's source code;
- sell hosting whose main offer is running the software for other people.

Only add-ons, meaning plugins as defined in permission 2, may be sold. The
engine itself may never be sold, by anyone.

## 4. Third-party code

Some repos contain code taken from other projects under their own licenses,
such as MIT. That code stays under its original license, and its notices are
kept in `CREDITS.md` and `licenses/`. These permissions do not change it.
