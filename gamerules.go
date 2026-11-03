package helix

import (
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/world/mcdb/leveldat"
)

// gameRules are level.dat's game rules as the client is told them at join. Which rules
// BDS lists and in what order is not captured yet, so this is every rule level.dat
// keeps, by name.
func gameRules(d *leveldat.Data) []protocol.GameRule {
	return []protocol.GameRule{
		{Name: "commandblockoutput", CanBeModifiedByPlayer: true, Value: d.CommandBlockOutput},
		{Name: "commandblocksenabled", CanBeModifiedByPlayer: true, Value: d.CommandBlocksEnabled},
		{Name: "dodaylightcycle", CanBeModifiedByPlayer: true, Value: d.DoDayLightCycle},
		{Name: "doentitydrops", CanBeModifiedByPlayer: true, Value: d.DoEntityDrops},
		{Name: "dofiretick", CanBeModifiedByPlayer: true, Value: d.DoFireTick},
		{Name: "doimmediaterespawn", CanBeModifiedByPlayer: true, Value: d.DoImmediateRespawn},
		{Name: "doinsomnia", CanBeModifiedByPlayer: true, Value: d.DoInsomnia},
		{Name: "domobloot", CanBeModifiedByPlayer: true, Value: d.DoMobLoot},
		{Name: "domobspawning", CanBeModifiedByPlayer: true, Value: d.DoMobSpawning},
		{Name: "dotiledrops", CanBeModifiedByPlayer: true, Value: d.DoTileDrops},
		{Name: "doweathercycle", CanBeModifiedByPlayer: true, Value: d.DoWeatherCycle},
		{Name: "drowningdamage", CanBeModifiedByPlayer: true, Value: d.DrowningDamage},
		{Name: "falldamage", CanBeModifiedByPlayer: true, Value: d.FallDamage},
		{Name: "firedamage", CanBeModifiedByPlayer: true, Value: d.FireDamage},
		{Name: "freezedamage", CanBeModifiedByPlayer: true, Value: d.FreezeDamage},
		{Name: "functioncommandlimit", CanBeModifiedByPlayer: true, Value: uint32(d.FunctionCommandLimit)}, //nolint:gosec // game rule values are small
		{Name: "keepinventory", CanBeModifiedByPlayer: true, Value: d.KeepInventory},
		{Name: "locatorbar", CanBeModifiedByPlayer: true, Value: d.LocatorBar},
		{Name: "maxcommandchainlength", CanBeModifiedByPlayer: true, Value: uint32(d.MaxCommandChainLength)}, //nolint:gosec // game rule values are small
		{Name: "mobgriefing", CanBeModifiedByPlayer: true, Value: d.MobGriefing},
		{Name: "naturalregeneration", CanBeModifiedByPlayer: true, Value: d.NaturalRegeneration},
		{Name: "projectilescanbreakblocks", CanBeModifiedByPlayer: true, Value: d.ProjectilesCanBreakBlocks},
		{Name: "pvp", CanBeModifiedByPlayer: true, Value: d.PVP},
		{Name: "randomtickspeed", CanBeModifiedByPlayer: true, Value: uint32(d.RandomTickSpeed)}, //nolint:gosec // game rule values are small
		{Name: "recipesunlock", CanBeModifiedByPlayer: true, Value: d.RecipesUnlock},
		{Name: "respawnblocksexplode", CanBeModifiedByPlayer: true, Value: d.RespawnBlocksExplode},
		{Name: "sendcommandfeedback", CanBeModifiedByPlayer: true, Value: d.SendCommandFeedback},
		{Name: "showbordereffect", CanBeModifiedByPlayer: true, Value: d.ShowBorderEffect},
		{Name: "showcoordinates", CanBeModifiedByPlayer: true, Value: d.ShowCoordinates},
		{Name: "showdaysplayed", CanBeModifiedByPlayer: true, Value: d.ShowDaysPlayed},
		{Name: "showdeathmessages", CanBeModifiedByPlayer: true, Value: d.ShowDeathMessages},
		{Name: "showrecipemessages", CanBeModifiedByPlayer: true, Value: d.ShowRecipeMessages},
		{Name: "showtags", CanBeModifiedByPlayer: true, Value: d.ShowTags},
		{Name: "spawnradius", CanBeModifiedByPlayer: true, Value: uint32(d.SpawnRadius)}, //nolint:gosec // game rule values are small
		{Name: "tntexplodes", CanBeModifiedByPlayer: true, Value: d.TNTExplodes},
		{Name: "tntexplosiondropdecay", CanBeModifiedByPlayer: true, Value: d.TNTExplosionDropDecay},
	}
}
