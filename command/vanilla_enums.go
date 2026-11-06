// Code generated from a dump of the enums BDS 1.26.60 registers (enum_probe.py,
// private). Dump again for a new version rather than editing by hand.

package command

// The names of the enums BDS sends, for SetEnumOptions and plugin enums that share them.
const (
	EnumItem              = "Item"
	EnumBlock             = "Block"
	EnumBoolean           = "Boolean"
	EnumGameMode          = "GameMode"
	EnumDifficulty        = "Difficulty"
	EnumDimension         = "Dimension"
	EnumEntityType        = "EntityType"
	EnumBiome             = "Biome"
	EnumEnchant           = "Enchant"
	EnumEffect            = "Effect"
	EnumDamageCause       = "DamageCause"
	EnumEquipmentSlot     = "EntityEquipmentSlot"
	EnumBoolGameRule      = "BoolGameRule"
	EnumIntGameRule       = "IntGameRule"
	EnumEasing            = "Easing"
	EnumStructureRotation = "Rotation"
	EnumMirror            = "Mirror"
	EnumWeather           = "WeatherType"
	EnumHudElement        = "HudElement"
	EnumHudVisibility     = "HudVisibility"
	EnumPermissionLevel   = "PermissionLevel"
	EnumTimeSpec          = "TimeSpec"
	EnumFillMode          = "FillMode"
	EnumSetBlockMode      = "SetBlockMode"
)

// GameMode is a game mode, from BDS's GameMode list.
type GameMode string

// Type is BDS's name of the list.
func (GameMode) Type() string { return EnumGameMode }

// Difficulty is a difficulty, from BDS's Difficulty list.
type Difficulty string

// Type is BDS's name of the list.
func (Difficulty) Type() string { return EnumDifficulty }

// Dimension is a dimension, from BDS's Dimension list.
type Dimension string

// Type is BDS's name of the list.
func (Dimension) Type() string { return EnumDimension }

// EntityType is an entity type, filled from the registry, from BDS's EntityType list.
type EntityType string

// Type is BDS's name of the list.
func (EntityType) Type() string { return EnumEntityType }

// Biome is a biome, filled from the registry, from BDS's Biome list.
type Biome string

// Type is BDS's name of the list.
func (Biome) Type() string { return EnumBiome }

// Enchant is an enchantment, from BDS's Enchant list.
type Enchant string

// Type is BDS's name of the list.
func (Enchant) Type() string { return EnumEnchant }

// Effect is an effect, from BDS's Effect list.
type Effect string

// Type is BDS's name of the list.
func (Effect) Type() string { return EnumEffect }

// DamageCause is a damage cause, from BDS's DamageCause list.
type DamageCause string

// Type is BDS's name of the list.
func (DamageCause) Type() string { return EnumDamageCause }

// EquipmentSlot is an equipment slot, from BDS's EntityEquipmentSlot list.
type EquipmentSlot string

// Type is BDS's name of the list.
func (EquipmentSlot) Type() string { return EnumEquipmentSlot }

// BoolGameRule is a true or false game rule, from BDS's BoolGameRule list.
type BoolGameRule string

// Type is BDS's name of the list.
func (BoolGameRule) Type() string { return EnumBoolGameRule }

// IntGameRule is a number game rule, from BDS's IntGameRule list.
type IntGameRule string

// Type is BDS's name of the list.
func (IntGameRule) Type() string { return EnumIntGameRule }

// Easing is a camera easing, from BDS's Easing list.
type Easing string

// Type is BDS's name of the list.
func (Easing) Type() string { return EnumEasing }

// StructureRotation is a structure rotation, from BDS's Rotation list.
type StructureRotation string

// Type is BDS's name of the list.
func (StructureRotation) Type() string { return EnumStructureRotation }

// Mirror is a structure mirror, from BDS's Mirror list.
type Mirror string

// Type is BDS's name of the list.
func (Mirror) Type() string { return EnumMirror }

// Weather is a weather, from BDS's WeatherType list.
type Weather string

// Type is BDS's name of the list.
func (Weather) Type() string { return EnumWeather }

// HudElement is a HUD element, from BDS's HudElement list.
type HudElement string

// Type is BDS's name of the list.
func (HudElement) Type() string { return EnumHudElement }

// HudVisibility is hide or reset, from BDS's HudVisibility list.
type HudVisibility string

// Type is BDS's name of the list.
func (HudVisibility) Type() string { return EnumHudVisibility }

// PermissionLevel is a permission level, from BDS's PermissionLevel list.
type PermissionLevel string

// Type is BDS's name of the list.
func (PermissionLevel) Type() string { return EnumPermissionLevel }

// TimeSpec is a named time of day, from BDS's TimeSpec list.
type TimeSpec string

// Type is BDS's name of the list.
func (TimeSpec) Type() string { return EnumTimeSpec }

// FillMode is a fill mode, from BDS's FillMode list.
type FillMode string

// Type is BDS's name of the list.
func (FillMode) Type() string { return EnumFillMode }

// SetBlockMode is a setblock mode, from BDS's SetBlockMode list.
type SetBlockMode string

// Type is BDS's name of the list.
func (SetBlockMode) Type() string { return EnumSetBlockMode }

// vanillaEnums are the options BDS 1.26.60 gives the lists that need no registry.
var vanillaEnums = map[string][]string{
	EnumGameMode:          {"default", "creative", "spectator", "survival", "adventure", "d", "c", "s", "a"},
	EnumDifficulty:        {"peaceful", "easy", "normal", "hard", "p", "e", "n", "h"},
	EnumDimension:         {"overworld", "nether", "the_end"},
	EnumEnchant:           {"protection", "fire_protection", "feather_falling", "blast_protection", "projectile_protection", "thorns", "respiration", "depth_strider", "aqua_affinity", "sharpness", "smite", "bane_of_arthropods", "knockback", "fire_aspect", "looting", "efficiency", "silk_touch", "unbreaking", "fortune", "power", "punch", "flame", "infinity", "luck_of_the_sea", "lure", "frost_walker", "mending", "binding", "vanishing", "impaling", "riptide", "loyalty", "channeling", "multishot", "piercing", "quick_charge", "soul_speed", "swift_sneak", "wind_burst", "density", "breach", "lunge"},
	EnumEffect:            {"speed", "slowness", "haste", "mining_fatigue", "strength", "instant_health", "instant_damage", "jump_boost", "nausea", "regeneration", "resistance", "fire_resistance", "water_breathing", "invisibility", "blindness", "night_vision", "hunger", "weakness", "poison", "wither", "health_boost", "absorption", "saturation", "levitation", "fatal_poison", "conduit_power", "slow_falling", "bad_omen", "village_hero", "darkness", "trial_omen", "wind_charged", "weaving", "oozing", "infested", "raid_omen", "breath_of_the_nautilus", "freezing"},
	EnumDamageCause:       {"entity_attack", "contact", "campfire", "fly_into_wall", "projectile", "fire_tick", "charging", "stalactite", "thorns", "lightning", "drowning", "none", "entity_explosion", "magma", "magic", "self_destruct", "freezing", "override", "anvil", "fire", "void", "wither", "starve", "suffocation", "falling_block", "stalagmite", "fall", "fireworks", "temperature", "ram_attack", "block_explosion", "piston", "sonic_boom", "soul_campfire", "lava", "mace_smash"},
	EnumEquipmentSlot:     {"slot.weapon.mainhand", "slot.weapon.offhand", "slot.armor.head", "slot.armor.chest", "slot.armor.legs", "slot.armor.feet", "slot.armor.body", "slot.hotbar", "slot.inventory", "slot.enderchest", "slot.saddle", "slot.armor", "slot.chest", "slot.equippable"},
	EnumBoolGameRule:      {"commandblockoutput", "dodaylightcycle", "doentitydrops", "dofiretick", "recipesunlock", "dolimitedcrafting", "domobloot", "domobspawning", "dotiledrops", "doweathercycle", "drowningdamage", "falldamage", "firedamage", "keepinventory", "mobgriefing", "pvp", "showcoordinates", "showdaysplayed", "naturalregeneration", "tntexplodes", "sendcommandfeedback", "doinsomnia", "commandblocksenabled", "doimmediaterespawn", "showdeathmessages", "showtags", "freezedamage", "respawnblocksexplode", "showbordereffect", "showrecipemessages", "projectilescanbreakblocks", "tntexplosiondropdecay"},
	EnumIntGameRule:       {"maxcommandchainlength", "randomtickspeed", "functioncommandlimit", "spawnradius", "playerssleepingpercentage"},
	EnumEasing:            {"linear", "spring", "in_quad", "out_quad", "in_out_quad", "in_cubic", "out_cubic", "in_out_cubic", "in_quart", "out_quart", "in_out_quart", "in_quint", "out_quint", "in_out_quint", "in_sine", "out_sine", "in_out_sine", "in_expo", "out_expo", "in_out_expo", "in_circ", "out_circ", "in_out_circ", "in_bounce", "out_bounce", "in_out_bounce", "in_back", "out_back", "in_out_back", "in_elastic", "out_elastic", "in_out_elastic"},
	EnumStructureRotation: {"0_degrees", "90_degrees", "180_degrees", "270_degrees"},
	EnumMirror:            {"none", "x", "z", "xz"},
	EnumWeather:           {"clear", "rain", "thunder"},
	EnumHudElement:        {"all", "paperdoll", "armor", "tooltips", "touch_controls", "crosshair", "hotbar", "health", "progress_bar", "hunger", "air_bubbles", "horse_health", "status_effects", "item_text"},
	EnumHudVisibility:     {"hide", "reset"},
	EnumPermissionLevel:   {"visitor", "member", "operator"},
	EnumTimeSpec:          {"sunrise", "day", "noon", "sunset", "night", "midnight"},
	EnumFillMode:          {"outline", "hollow", "destroy", "keep"},
	EnumSetBlockMode:      {"replace", "destroy", "keep"},
}
