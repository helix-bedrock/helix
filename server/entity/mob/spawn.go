package mob

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/server/block"
	"github.com/helix-bedrock/helix/server/block/cube"
	"github.com/helix-bedrock/helix/server/entity/path"
	"github.com/helix-bedrock/helix/server/world"
)

// Natural spawning (Helix). What may spawn where comes from BDS's spawn
// rules in helix-data. The spawning loop itself is inside BDS, not in its
// data; its numbers below are the Minecraft Wiki's for Bedrock Edition
// ("Mob spawning", read 2026-10-12) and still need checking against BDS:
//
//   - attempts: a chance of 11/2000 per ticked chunk per tick,
//   - where: 24 to 44 blocks from a player,
//   - caps per 9x9 chunks around the attempt, surface / cave: ambient 0/2,
//     animal 4/0, monster 8/16, pillager 8/8, water animal 36/0,
//   - at most 200 naturally spawned mobs in a world.
const (
	spawnChancePerChunk = 11.0 / 2000
	spawnMinDist        = 24
	spawnMaxDist        = 44
	capRadiusChunks     = 4
	globalCap           = 200
)

var populationCaps = map[string][2]int{
	"ambient":      {0, 2},
	"animal":       {4, 0},
	"cat":          {4, 0},
	"monster":      {8, 16},
	"pillager":     {8, 8},
	"water_animal": {36, 0},
}

// Spawner spawns mobs around players by BDS's spawn rules and despawns
// them. Set its Tick as world.Config.RegionTick. It holds no state that
// changes, so one Spawner serves every world and region at once.
type Spawner struct {
	rules []*spawnRule
}

type spawnRule struct {
	t        *Type
	category string
	conds    []spawnCond
}

type spawnCond struct {
	surface, underground, underwater bool
	onBlocks, notOnBlocks            []string
	light                            *[2]int
	difficulty                       [2]int
	weight                           int
	herd                             [2]int
	permute                          []permute
	biome                            json.RawMessage
	density                          [2]int // surface, underground; -1 for no limit
	height                           *[2]int
}

type permute struct {
	weight int
	id     string
}

// NewSpawner reads the spawn rules for the mob types in reg. Rules for
// mobs reg does not have, and rule conditions Helix cannot check yet (mob
// events, delays, villages), are left out.
func NewSpawner(reg world.EntityRegistry) (*Spawner, error) {
	raw, err := data.SpawnRules()
	if err != nil {
		return nil, fmt.Errorf("mob: %w", err)
	}
	s := &Spawner{}
	for id, r := range raw {
		t, ok := reg.Lookup(id)
		mt, isMob := t.(*Type)
		if !ok || !isMob {
			continue
		}
		rule, err := parseRule(mt, r)
		if err != nil {
			return nil, fmt.Errorf("mob: spawn rule of %s: %w", id, err)
		}
		if len(rule.conds) > 0 {
			s.rules = append(s.rules, rule)
		}
	}
	slices.SortFunc(s.rules, func(a, b *spawnRule) int {
		if a.t.def.ID < b.t.def.ID {
			return -1
		}
		return 1
	})
	return s, nil
}

func parseRule(t *Type, raw json.RawMessage) (*spawnRule, error) {
	var f struct {
		Rules struct {
			Description struct {
				Population string `json:"population_control"`
			} `json:"description"`
			Conditions []map[string]json.RawMessage `json:"conditions"`
		} `json:"minecraft:spawn_rules"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	r := &spawnRule{t: t, category: f.Rules.Description.Population}
	for _, c := range f.Rules.Conditions {
		if cond, ok := parseCond(c); ok {
			r.conds = append(r.conds, cond)
		}
	}
	return r, nil
}

var difficulties = map[string]int{"peaceful": 0, "easy": 1, "normal": 2, "hard": 3}

func parseCond(c map[string]json.RawMessage) (spawnCond, bool) {
	for _, unsupported := range []string{"minecraft:mob_event_filter", "minecraft:delay_filter",
		"minecraft:player_in_village_filter", "minecraft:spawn_event", "minecraft:spawns_lava"} {
		if _, ok := c[unsupported]; ok {
			return spawnCond{}, false
		}
	}
	s := spawnCond{weight: 1, herd: [2]int{1, 1}, difficulty: [2]int{0, 3}, density: [2]int{-1, -1}, biome: c["minecraft:biome_filter"]}
	_, s.surface = c["minecraft:spawns_on_surface"]
	_, s.underground = c["minecraft:spawns_underground"]
	_, s.underwater = c["minecraft:spawns_underwater"]
	s.onBlocks = blockNames(c["minecraft:spawns_on_block_filter"])
	s.notOnBlocks = blockNames(c["minecraft:spawns_on_block_prevented_filter"])
	if raw, ok := c["minecraft:brightness_filter"]; ok {
		s.light = &[2]int{int(numberField(raw, "min", 0)), int(numberField(raw, "max", 15))}
	}
	if raw, ok := c["minecraft:difficulty_filter"]; ok {
		var d struct{ Min, Max string }
		_ = json.Unmarshal(raw, &d)
		s.difficulty = [2]int{difficulties[d.Min], difficulties[d.Max]}
		if d.Max == "" {
			s.difficulty[1] = 3
		}
	}
	if raw, ok := c["minecraft:weight"]; ok {
		s.weight = int(numberField(raw, "default", 1))
	}
	if raw, ok := c["minecraft:herd"]; ok {
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
			raw = list[0]
		}
		s.herd = [2]int{int(numberField(raw, "min_size", 1)), int(numberField(raw, "max_size", 1))}
	}
	if raw, ok := c["minecraft:density_limit"]; ok {
		s.density = [2]int{int(numberField(raw, "surface", -1)), int(numberField(raw, "underground", -1))}
	}
	if raw, ok := c["minecraft:height_filter"]; ok {
		s.height = &[2]int{int(numberField(raw, "min", -64)), int(numberField(raw, "max", 320))}
	}
	if raw, ok := c["minecraft:permute_type"]; ok {
		var ps []struct {
			Weight int    `json:"weight"`
			Type   string `json:"entity_type"`
		}
		_ = json.Unmarshal(raw, &ps)
		for _, p := range ps {
			s.permute = append(s.permute, permute{weight: p.Weight, id: p.Type})
		}
	}
	return s, s.surface || s.underground || s.underwater
}

// blockNames reads a block filter: a name, a list of names or of {name}.
func blockNames(raw json.RawMessage) []string {
	if raw == nil {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{qualify(one)}
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	var out []string
	for _, e := range list {
		if json.Unmarshal(e, &one) == nil {
			out = append(out, qualify(one))
			continue
		}
		var o struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(e, &o) == nil && o.Name != "" {
			out = append(out, qualify(o.Name))
		}
	}
	return out
}

// Tick spawns and despawns mobs around the players of one region.
func (s *Spawner) Tick(tx *world.Tx, tick int64) {
	diff, _ := world.DifficultyID(tx.World().Difficulty())
	for p := range tx.Players() {
		pos := p.Position()
		r := rand.New(rand.NewPCG(uint64(tick), math.Float64bits(pos[0])^math.Float64bits(pos[2])))
		// The shell 24-44 blocks around a player covers about 17 chunks,
		// each with an 11/2000 chance this tick.
		chunks := math.Pi * (spawnMaxDist*spawnMaxDist - spawnMinDist*spawnMinDist) / 256
		for attempts := chunks * spawnChancePerChunk; attempts > 0; attempts-- {
			if r.Float64() < attempts {
				s.attempt(tx, pos, diff, r)
			}
		}
	}
}

// spawnSpot is a place a herd may spawn at.
type spawnSpot struct {
	pos         cube.Pos
	underground bool
	underwater  bool
	biome       world.Biome
	below       string
}

func (s *Spawner) attempt(tx *world.Tx, player mgl64.Vec3, diff int, r *rand.Rand) {
	a := r.Float64() * 2 * math.Pi
	d := spawnMinDist + r.Float64()*(spawnMaxDist-spawnMinDist)
	x, z := int(math.Floor(player[0]+math.Cos(a)*d)), int(math.Floor(player[2]+math.Sin(a)*d))
	if _, loaded := tx.BlockLoaded(cube.Pos{x, int(player[1]), z}); !loaded {
		return
	}
	spot, ok := pickSpot(tx, x, z, r)
	if !ok {
		return
	}
	type option struct {
		rule *spawnRule
		cond *spawnCond
	}
	var opts []option
	total := 0
	for _, rule := range s.rules {
		if rule.t.def.ID == "minecraft:slime" && !slimeMaySpawn(spot.pos) {
			continue
		}
		for i := range rule.conds {
			c := &rule.conds[i]
			if c.fits(tx, spot, diff) {
				opts = append(opts, option{rule, c})
				total += c.weight
			}
		}
	}
	if total <= 0 {
		return
	}
	pick := r.IntN(total)
	for _, o := range opts {
		if pick -= o.cond.weight; pick < 0 {
			s.spawnHerd(tx, o.rule, o.cond, spot, r)
			return
		}
	}
}

// pickSpot finds where in the column at x, z something may spawn: on the
// surface, in a cave below it, or in water.
func pickSpot(tx *world.Tx, x, z int, r *rand.Rand) (spawnSpot, bool) {
	top := tx.HighestBlock(x, z)
	rng := tx.Range()
	y := top + 1
	underground := false
	if r.IntN(2) == 0 && top-rng[0] > 8 {
		// A random height below the surface: caves.
		y = rng[0] + 1 + r.IntN(top-rng[0])
		underground = true
	}
	pos := cube.Pos{x, y, z}
	if l, ok := tx.Liquid(pos); ok {
		if _, water := l.(block.Water); water {
			return spawnSpot{pos: pos, underwater: true, underground: underground, biome: tx.Biome(pos)}, true
		}
		return spawnSpot{}, false
	}
	if path.Solid(tx, pos) || path.Solid(tx, pos.Side(cube.FaceUp)) || !path.Solid(tx, pos.Side(cube.FaceDown)) {
		return spawnSpot{}, false
	}
	name, _ := tx.Block(pos.Side(cube.FaceDown)).EncodeBlock()
	return spawnSpot{pos: pos, underground: underground, biome: tx.Biome(pos), below: name}, true
}

func (c *spawnCond) fits(tx *world.Tx, s spawnSpot, diff int) bool {
	switch {
	case s.underwater && !c.underwater, !s.underwater && c.underwater && !c.surface && !c.underground:
		return false
	case !s.underwater && s.underground && !c.underground, !s.underwater && !s.underground && !c.surface:
		return false
	case diff < c.difficulty[0] || diff > c.difficulty[1]:
		return false
	case c.height != nil && (s.pos[1] < c.height[0] || s.pos[1] > c.height[1]):
		return false
	case len(c.onBlocks) > 0 && !slices.Contains(c.onBlocks, s.below):
		return false
	case slices.Contains(c.notOnBlocks, s.below):
		return false
	}
	if c.light != nil {
		l := brightness(tx, s.pos)
		if l < c.light[0] || l > c.light[1] {
			return false
		}
	}
	return evalFilter(c.biome, filterEnv{tx: tx, biome: s.biome})
}

func (s *Spawner) spawnHerd(tx *world.Tx, rule *spawnRule, c *spawnCond, spot spawnSpot, r *rand.Rand) {
	if !s.underCaps(tx, rule, c, spot) {
		return
	}
	n := c.herd[0]
	if c.herd[1] > c.herd[0] {
		n += r.IntN(c.herd[1] - c.herd[0] + 1)
	}
	for range n {
		t := rule.t
		if len(c.permute) > 0 {
			t = s.permuted(tx, rule.t, c.permute, r)
		}
		pos := spot.pos.Add(cube.Pos{r.IntN(5) - 2, 0, r.IntN(5) - 2})
		if !spot.underwater && (path.Solid(tx, pos) || path.Solid(tx, pos.Side(cube.FaceUp)) || !path.Solid(tx, pos.Side(cube.FaceDown))) {
			pos = spot.pos
		}
		at := mgl64.Vec3{float64(pos[0]) + 0.5, float64(pos[1]), float64(pos[2]) + 0.5}
		tx.AddEntity(t.spawn(at, r.Float64()*360-180, natural{category: rule.category, underground: spot.underground}))
	}
}

func (s *Spawner) permuted(tx *world.Tx, t *Type, ps []permute, r *rand.Rand) *Type {
	total := 0
	for _, p := range ps {
		total += p.weight
	}
	if total <= 0 {
		return t
	}
	pick := r.IntN(total)
	for _, p := range ps {
		if pick -= p.weight; pick < 0 {
			if p.id == "" {
				return t
			}
			if o, ok := tx.World().EntityRegistry().Lookup(p.id); ok {
				if mt, ok := o.(*Type); ok {
					return mt
				}
			}
			return t
		}
	}
	return t
}

// underCaps checks the population cap of the rule's category and its own
// density limit around spot, and the world's cap.
func (s *Spawner) underCaps(tx *world.Tx, rule *spawnRule, c *spawnCond, spot spawnSpot) bool {
	caps, ok := populationCaps[rule.category]
	if !ok {
		return false
	}
	limit := caps[0]
	if spot.underground {
		limit = caps[1]
	}
	density := c.density[0]
	if spot.underground {
		density = c.density[1]
	}
	cx, cz := spot.pos[0]>>4, spot.pos[2]>>4
	box := cube.Box(float64((cx-capRadiusChunks)<<4), float64(tx.Range()[0]), float64((cz-capRadiusChunks)<<4),
		float64((cx+capRadiusChunks+1)<<4), float64(tx.Range()[1]), float64((cz+capRadiusChunks+1)<<4))
	category, kind, all := 0, 0, 0
	for e := range tx.Entities() {
		m, ok := e.(*Mob)
		if !ok || !m.s.natural {
			continue
		}
		all++
		if !box.Vec3Within(m.data.Pos) || m.s.underground != spot.underground {
			continue
		}
		if m.s.category == rule.category {
			category++
		}
		if m.s.t == rule.t {
			kind++
		}
	}
	return all < globalCap && category < limit && (density < 0 || kind < density)
}
