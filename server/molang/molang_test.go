package molang

import (
	"math/rand/v2"
	"testing"
)

func TestEval(t *testing.T) {
	q := map[string]float64{"last_hit_by_player": 1, "is_baby": 0, "equipment_count": 2, "player_level": 30}
	env := Env{Query: func(n string) float64 { return q[n] }}
	cases := map[string]float64{
		"50":                                              50,
		"query.last_hit_by_player ? 5 : 0":                5,
		"query.last_hit_by_player ? 5: 0":                 5,
		"!query.last_hit_by_player ? 5 : 0":               0,
		"query.last_hit_by_player ? Math.Random(1,3) : 0": 1,
		"query.last_hit_by_player ? 5 + (query.equipment_count * Math.Random(1,3)) : 0":                        7,
		"query.last_hit_by_player ? (query.is_baby ? 12 : 5) + (Math.die_roll(query.equipment_count,1,3)) : 0": 7,
		"!query.is_baby && query.last_hit_by_player ? Math.Random(1,3) : 0":                                    1,
		"Math.Min(query.player_level * 7, 100)":                                                                100,
		"1 + 2 * 3 - 4 / 2":                                                                                    5,
		"(1 + 2) * 3 >= 9 && 2 != 3":                                                                           1,
		"-2 * -3":                                                                                              6,
	}
	for src, want := range cases {
		e, err := Parse(src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if got := e.Eval(env); got != want {
			t.Errorf("%s = %v, want %v", src, got, want)
		}
	}
}

func TestRandomStaysInRange(t *testing.T) {
	e, err := Parse("Math.random_integer(1, 3)")
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(1, 2))
	seen := map[float64]bool{}
	for range 200 {
		v := e.Eval(Env{Rand: r})
		if v < 1 || v > 3 || v != float64(int(v)) {
			t.Fatalf("random_integer(1,3) gave %v", v)
		}
		seen[v] = true
	}
	if len(seen) != 3 {
		t.Fatalf("saw %v", seen)
	}
}

func TestBadInput(t *testing.T) {
	for _, src := range []string{"", "1 +", "(1", "foo", "math.random(1,2", "?:"} {
		if _, err := Parse(src); err == nil {
			t.Errorf("%q parsed", src)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add("query.last_hit_by_player ? Math.Random(1,3) : 0")
	f.Add("(1+2)*3")
	f.Fuzz(func(_ *testing.T, src string) {
		if e, err := Parse(src); err == nil {
			e.Eval(Env{Rand: rand.New(rand.NewPCG(1, 1))})
		}
	})
}
