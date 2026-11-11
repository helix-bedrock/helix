package mob

import "testing"

func TestMolangVanilla(t *testing.T) {
	for _, src := range []string{
		"query.last_hit_by_player ? 5 + (query.equipment_count * Math.Random(1,3)) : 0",
		"query.last_hit_by_player ? (query.is_baby ? 12 : 5) + (Math.die_roll(query.equipment_count,1,3)) : 0",
		"query.has_any_family('blaze', 'creeper') ? -90 : 0",
		"math.clamp(query.property('minecraft:creaking_swaying_ticks') + 1, 0, 6)",
		"2.0 - math.clamp(query.anger_level / 80 * 1.5, 0, 1.5)",
		"query.anger_level(this) >= 40", "!query.is_on_ground", "query.last_hit_by_player ? 5: 0",
		"Math.Min(query.player_level * 7, 100)", "query.had_component_group('has_nectar')",
		"!query.is_baby && query.last_hit_by_player ? Math.Random(1,3) : 0",
	} {
		if _, err := parseMolang(src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	e, _ := parseMolang("1 + 2 * 3 >= 7 ? 4 : 5")
	if v := e.eval(nil); v != 4 {
		t.Errorf("got %v", v)
	}
}
