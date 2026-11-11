package loot

import (
	"math/rand/v2"
	"testing"
)

type testRandom struct{ r *rand.Rand }

func (t testRandom) Intn(n int32) int32 { return t.r.Int32N(n) }
func (t testRandom) Float() float32     { return t.r.Float32() }

func TestEntityTables(t *testing.T) {
	tb, err := NewEntityTables()
	if err != nil {
		t.Fatal(err)
	}
	c := &EntityContext{Random: testRandom{rand.New(rand.NewPCG(1, 2))}, Difficulty: 3, Special: 1, KillerPlayer: true}
	bows := 0
	for range 50 {
		for _, d := range tb.Roll("loot_tables/entities/skeleton_gear.json", c, nil) {
			if d.Name == "minecraft:bow" {
				bows++
			}
		}
	}
	if bows != 50 {
		t.Errorf("skeleton gear gave %d bows in 50 rolls", bows)
	}
	if got := tb.Roll("loot_tables/entities/cow.json", c, nil); len(got) == 0 {
		t.Error("a cow killed by a player dropped nothing")
	}
}
