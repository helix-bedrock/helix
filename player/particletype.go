package player

// particleTypeID is the ParticleType number of a particle name as entity definitions write it
// ("mobspellambient", "dragonbreath"), and false for a name BDS does not know. The names are
// BDS 1.26.60's ParticleType reflection table, in enum order from "none" at 0,
// with the one value after portal that the table leaves unnamed (mobportal): BDS's cloud
// sends mobspellambient as 35 and its tick checks dragonbreath as 49.
// Runs: anywhere; it is read while definitions load.
func particleTypeID(name string) (int32, bool) {
	names := [...]string{
		"none", "bubble", "bubblemanual", "crit", "blockforcefield", "smoke", "explode", "evaporation",
		"flame", "candleflame", "lava", "largesmoke", "reddust", "risingborderdust", "iconcrack",
		"snowballpoof", "largeexplode", "hugeexplosion", "breezewindexplosion", "mobflame", "heart",
		"terrain", "townaura", "portal", "", "watersplash", "watersplashmanual", "waterwake", "dripwater",
		"driplava", "driphoney", "stalactitedripwater", "stalactitedriplava", "fallingdust", "mobspell",
		"mobspellambient", "mobspellinstantaneous", "ink", "slime", "rainsplash", "villagerangry",
		"villagerhappy", "enchantingtable", "trackingemitter", "note", "witchspell", "carrotboost",
		"mobappearance", "endrod", "dragonbreath", "spit", "totem", "food", "fireworksstarter",
		"fireworks", "fireworksoverlay", "balloongas", "coloredflame", "sparkler", "conduit",
		"bubblecolumnup", "bubblecolumndown", "sneeze", "shulkerbullet", "bleach", "dragondestroyblock",
		"myceliumdust", "fallingborderdust", "campfiresmoke", "campfiresmoketall", "dragonbreathfire",
		"dragonbreathtrail", "soul", "obsidiantear", "portalreverse", "snowflake", "wax",
		"electricspark", "shriek", "sculksoul", "sonicexplosion", "dustplume", "whitesmoke",
		"vaultconnection", "windexplosion", "wolfarmorcrack", "creakingcrumble", "paleoakleaves",
		"eyeblossomopen", "eyeblossomclose", "blueflame", "greenflame", "pausemobgrowth",
		"resetmobgrowth", "sulfurcube", "orangepoplarleaves", "redpoplarleaves", "yellowpoplarleaves",
		"iceballbreak",
	}
	for i, n := range names {
		if n == name && n != "" {
			return int32(i), true //nolint:gosec // under 100 names
		}
	}
	return 0, false
}

// cloudParticle is a spawn_aoe_cloud's particle number: its definition's particle, or
// mobspellambient when it names none. A name BDS does not know is 0, none, as its lookup
// gives after logging it.
func cloudParticle(name string) int32 {
	if name == "" {
		name = cloudParticleDefault
	}
	id, _ := particleTypeID(name)
	return id
}
