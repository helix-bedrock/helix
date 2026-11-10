package player

import (
	"strings"

	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// What the mob filter tests ask the engine: the world around a mob (mob.Env) and a player
// (mob.PlayerInfo). Read while mobs think: only reads.

// BiomeTags are the tags of the biome at pos, vanilla's from helix-data.
func (s *Host) BiomeTags(r *world.Region, pos cube.Pos) ([]string, bool) {
	id, ok := r.Biome(pos)
	if !ok {
		return nil, false
	}
	if sg := s.spawning; sg != nil {
		if tags, ok := sg.tags[id]; ok {
			return tags, true
		}
	}
	b, ok := s.reg.Biomes.ByID(id)
	return b.Tags, ok
}

// BiomeName is the name of the biome at pos.
func (s *Host) BiomeName(r *world.Region, pos cube.Pos) (string, bool) {
	id, ok := r.Biome(pos)
	if !ok {
		return "", false
	}
	b, ok := s.reg.Biomes.ByID(id)
	return b.Name, ok
}

// DayTime is the overworld clock's time.
func (s *Host) DayTime() int32 {
	if s.clock == nil {
		return 0
	}
	return s.clock.Time()
}

// GameRule is a boolean game rule from the level's list, by lower case name.
func (s *Host) GameRule(name string) (value, ok bool) {
	for _, g := range s.level.GameRules {
		if strings.EqualFold(g.Name, name) {
			v, ok := g.Value.(bool)
			return v, ok
		}
	}
	return false, false
}

// --- mob.PlayerInfo ---

func (a playerActor) Sneaking() bool  { return a.p.Moving(MoveSneaking) }
func (a playerActor) Sprinting() bool { return a.p.Moving(MoveSprinting) }

// HasAbility answers the ability names BDS's has_ability test takes.
func (a playerActor) HasAbility(name string) bool {
	ab, ok := filterAbilities[name]
	return ok && a.p.abilities.Flag(ab)
}

var filterAbilities = map[string]Ability{
	"build": AbilityBuild, "mine": AbilityMine, "doorsandswitches": AbilityDoorsAndSwitches,
	"opencontainers": AbilityOpenContainers, "attackplayers": AbilityAttackPlayers,
	"attackmobs": AbilityAttackMobs, "op": AbilityOperatorCommands, "teleport": AbilityTeleport,
	"invulnerable": AbilityInvulnerable, "flying": AbilityFlying, "mayfly": AbilityMayFly,
	"instabuild": AbilityInstantBuild, "lightning": AbilityLightning, "mute": AbilityMuted,
	"worldbuilder": AbilityWorldBuilder, "noclip": AbilityNoClip,
}

// Equipment appends the items in the slots a filter domain names.
func (a playerActor) Equipment(domain string, dst []string) []string {
	p := a.p
	add := func(s ItemStack) {
		if !s.Empty() {
			dst = append(dst, s.Name)
		}
	}
	armour := func(slot int) {
		if inv := p.Armour(); inv != nil && slot < inv.Size() {
			add(inv.Item(slot))
		}
	}
	switch domain {
	case "hand", "main_hand":
		add(p.Held())
	case "offhand":
		if inv := p.Offhand(); inv != nil && inv.Size() > 0 {
			add(inv.Item(0))
		}
	case "head":
		armour(0)
	case "torso":
		armour(1)
	case "leg":
		armour(2)
	case "feet":
		armour(3)
	case "armor":
		for i := range 4 {
			armour(i)
		}
	case "inventory":
		for _, s := range p.inventories().main.Items() {
			add(s)
		}
	default:
		dst = a.Equipment("offhand", dst)
		dst = a.Equipment("armor", dst)
		dst = a.Equipment("inventory", dst)
	}
	return dst
}

// ItemTags are an item's tags from the registry.
func (a playerActor) ItemTags(item string) []string {
	t, ok := a.p.s.reg.Items.ByName(item)
	if !ok {
		return nil
	}
	return t.Tags
}
