package player

import (
	"math/rand/v2"
	"slices"

	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/world"
	wgrand "github.com/helix-bedrock/worldgen/rand"
)

// Generated containers (village house chests, dungeon chests, temple barrels...) keep the
// loot table worldgen gave them until they are first opened or broken. Then the table is
// rolled into their own slots as BDS fills a container: a Mersenne Twister seeded with the
// saved LootTableSeed (a random seed when it is 0) rolls the table, the empty slots are
// shuffled, stacks are split while there are more empty slots than stacks, the stacks are
// shuffled and each goes into the next slot from the end of the shuffled list. Stacks with
// no slot left are lost, as in BDS. The table is then forgotten.

const (
	lootTableTag     = "LootTable"
	lootTableSeedTag = "LootTableSeed"
)

// containerLootRandom is the seeded random a container's loot is rolled and laid out with.
type containerLootRandom struct{ mt *wgrand.MT }

func (l containerLootRandom) Intn(n int32) int32   { return l.mt.IntN(n) }
func (l containerLootRandom) Int32N(n int32) int32 { return l.mt.IntN(n) }
func (l containerLootRandom) Float() float32       { return l.mt.Float() }

// below draws an index under n for a shuffle, as BDS's shuffle does: the raw output
// modulo n.
func (l containerLootRandom) below(n int) int { return int(l.mt.Uint32() % uint32(n)) } //nolint:gosec // n is a small slot or stack count

// coin is a fair coin flip.
// VERIFY: which bit BDS's coin flip reads was not checked; the top bit is used here.
func (l containerLootRandom) coin() bool { return l.mt.Uint32()>>31 != 0 }

// unpackLoot rolls this half's loot table into its slots, if it still has one.
// Runs: the region that owns the chest, with its items loaded.
func (c *Chest) unpackLoot() {
	table, _ := c.saved[lootTableTag].(string)
	if table == "" {
		return
	}
	seed, _ := c.saved[lootTableSeedTag].(int32)
	delete(c.saved, lootTableTag)
	delete(c.saved, lootTableSeedTag)
	c.chunk.Changed()
	s := c.s
	if s.entityLoot == nil || !s.entityLoot.Has(table) {
		return
	}
	if seed == 0 {
		seed = rand.Int32() //nolint:gosec // gameplay randomness, as BDS draws a seed
	}
	rnd := containerLootRandom{wgrand.NewMT(uint32(seed))} //nolint:gosec // the seed's bits as saved
	ctx := &loot.EntityContext{Random: rnd, Difficulty: s.level.Difficulty}
	var stacks []ItemStack
	for _, d := range s.entityLoot.Roll(table, ctx, nil) {
		stacks = append(stacks, s.lootStacks(d)...)
	}
	c.fillLoot(stacks, rnd)
}

// fillLoot lays rolled stacks out in this half's empty slots as BDS does.
func (c *Chest) fillLoot(stacks []ItemStack, rnd containerLootRandom) {
	slots := c.freeSlots()
	shuffleLoot(slots, rnd)
	stacks = splitLoot(stacks, len(slots), rnd)
	shuffleLoot(stacks, rnd)
	for _, st := range stacks {
		if len(slots) == 0 {
			return
		}
		last := len(slots) - 1
		c.items[slots[last]] = c.s.freshStack(st)
		slots = slots[:last]
	}
}

// freeSlots lists this half's empty slots in order.
func (c *Chest) freeSlots() []int {
	var out []int
	for i, it := range c.items[:c.halfSize()] {
		if it.Empty() {
			out = append(out, i)
		}
	}
	return out
}

// shuffleLoot shuffles s front to back, each element swapping with a random one at or
// before it.
func shuffleLoot[T any](s []T, rnd containerLootRandom) {
	for i := 1; i < len(s); i++ {
		if j := rnd.below(i + 1); j != i {
			s[i], s[j] = s[j], s[i]
		}
	}
}

// splitLoot spreads stacks over more slots when there are more empty slots than single
// stacks: a random splittable stack (more than one item) loses 1 to half its count to a new
// stack, and each part stays splittable on a coin flip while it has two or more items.
func splitLoot(stacks []ItemStack, slots int, rnd containerLootRandom) []ItemStack {
	var single, split []ItemStack
	for _, st := range stacks {
		switch {
		case st.Empty():
		case st.Count > 1:
			split = append(split, st)
		default:
			single = append(single, st)
		}
	}
	if slots-len(single) <= 0 {
		return append(single, split...)
	}
	for len(split) > 0 {
		i := int(rnd.Intn(int32(len(split)))) //nolint:gosec // a small stack count
		st := split[i]
		split = slices.Delete(split, i, i+1)
		n := uint8(1)
		if half := st.Count / 2; half > 0 {
			n = uint8(rnd.Intn(int32(half))) + 1 //nolint:gosec // under half a stack
		}
		part := st
		part.Count = n
		st.Count -= n
		for _, p := range [2]ItemStack{st, part} {
			if p.Count >= 2 && rnd.coin() {
				split = append(split, p)
			} else if !p.Empty() {
				single = append(single, p)
			}
		}
	}
	return single
}

// unpackHalves rolls the loot of both halves of a chest.
func (c *Chest) unpackHalves(r *world.Region) {
	lead, follower := c.halves(r)
	for _, h := range [...]*Chest{lead, follower} {
		if h != nil {
			h.unpackLoot()
		}
	}
}
