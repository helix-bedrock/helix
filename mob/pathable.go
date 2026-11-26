package mob

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/entity/sensor"
)

// pathRuleKind is which of BDS 1.26.60's per-block path-through checks a block type uses
// (helix-data's block_path_rules.json, read from each block type's vtable at runtime).
type pathRuleKind uint8

const (
	// ruleDefault: pathable when the material does not block motion.
	ruleDefault pathRuleKind = iota
	ruleAlways
	ruleNever
	ruleLiquid
	ruleDoor
	ruleTrapdoor
	ruleSlab
	ruleTopSnow
	ruleLantern
	ruleBed
	ruleCampfire
	ruleFire
	ruleScaffolding
	ruleBerryBush
	ruleBigDripleaf
	ruleHangingSign
	rulePitcherCrop
)

var pathRuleNames = map[string]pathRuleKind{
	"always": ruleAlways, "never": ruleNever, "liquid": ruleLiquid, "door": ruleDoor,
	"trapdoor": ruleTrapdoor, "slab": ruleSlab, "top_snow": ruleTopSnow, "lantern": ruleLantern,
	"bed": ruleBed, "campfire": ruleCampfire, "fire": ruleFire, "scaffolding": ruleScaffolding,
	"sweet_berry_bush": ruleBerryBush, "big_dripleaf": ruleBigDripleaf,
	"hanging_sign": ruleHangingSign, "pitcher_crop": rulePitcherCrop,
}

// pathRule is one block state's path-through check with the state values it reads.
type pathRule struct {
	kind pathRuleKind
	// val is the trapdoor's direction, the top snow's height, the berry bush's growth or
	// the big dripleaf's tilt.
	val int32
	// flag is the trapdoor's open bit, the slab's "uses the default" (top or double), the
	// lantern's and hanging sign's hanging, the campfire's extinguished, the big
	// dripleaf's head and the pitcher crop's upper half.
	flag bool
}

// bigDripleafFullTilt is the value index of big_dripleaf_tilt's "full_tilt".
const bigDripleafFullTilt = 3

// readPathRule fills the rule of a state of the block type name, whose rule name is kind.
func readPathRule(kind, name string, s map[string]any) pathRule {
	r := pathRule{kind: pathRuleNames[kind]}
	switch r.kind {
	case ruleTrapdoor:
		r.val, _ = s["direction"].(int32)
		r.flag = stateTrue(s, "open_bit")
	case ruleSlab:
		half, _ := s["minecraft:vertical_half"].(string)
		r.flag = half == "top" || strings.Contains(name, "double")
	case ruleTopSnow:
		r.val, _ = s["height"].(int32)
	case ruleLantern, ruleHangingSign:
		r.flag = stateTrue(s, "hanging")
	case ruleCampfire:
		r.flag = stateTrue(s, "extinguished")
	case ruleBerryBush:
		r.val, _ = s["growth"].(int32)
	case ruleBigDripleaf:
		r.flag = stateTrue(s, "big_dripleaf_head")
		r.val = tiltIndex(s["big_dripleaf_tilt"])
	case rulePitcherCrop:
		r.flag = stateTrue(s, "upper_block_bit")
	}
	return r
}

func tiltIndex(v any) int32 {
	switch v {
	case "unstable":
		return 1
	case "partial_tilt":
		return 2
	case "full_tilt":
		return bigDripleafFullTilt
	}
	return 0
}

// maxPathableDepth bounds the block-above checks of slabs, lanterns and beds; in BDS they
// stop after one step because every block that asks again has a collision box.
const maxPathableDepth = 4

// pathable matches BDS 1.26.60: whether a mob may move through block id at to coming from
// from, by the block type's own check (doors and trapdoors by their side and the move's
// direction, short blocks by the block above, fire by the mob's fire immunity).
func (v *view) pathable(id uint32, from, to path.Pos, depth int) bool {
	def := !v.blocks.Goal(id).BlocksMotion
	r := v.blocks.pathRule(id)
	switch r.kind {
	case ruleAlways:
		return true
	case ruleNever:
		return false
	case ruleLiquid:
		return v.blocks.Path(id).Material != materialLava
	case ruleDoor:
		if from == to {
			return true
		}
		facing, open, hinge := v.door(to)
		return doorPassable(from, to, facing, open, hinge)
	case ruleTrapdoor:
		if from == to || !r.flag {
			return true
		}
		return trapdoorPassable(from, to, r.val)
	case ruleSlab:
		if r.flag {
			return def
		}
		return v.clearAbove(to, def, depth)
	case ruleLantern:
		if r.flag {
			return def
		}
		return v.clearAbove(to, def, depth)
	case ruleBed:
		return v.clearAbove(to, def, depth)
	case ruleTopSnow:
		return v.topSnowPassable(r.val, from, to)
	case ruleCampfire:
		return r.flag || v.fireImmune()
	case ruleFire:
		return v.fireImmune() || v.chasesThroughFire()
	case ruleScaffolding:
		return from.Y <= to.Y
	case ruleBerryBush:
		return r.val <= 0 || v.sensorStopsContact(to)
	case ruleBigDripleaf:
		return !r.flag || r.val == bigDripleafFullTilt
	case ruleHangingSign, rulePitcherCrop:
		return r.flag
	}
	return def
}

// clearAbove is BDS's check of a bottom slab, a standing lantern and a bed: blocked when the
// block above has a collision box, pathable when the block above is, else def.
func (v *view) clearAbove(to path.Pos, def bool, depth int) bool {
	above := path.Pos{X: to.X, Y: to.Y + 1, Z: to.Z}
	b := v.block(above, 0)
	if (pathWorld{v}).CollisionBox(path.Block(b), above).HasVolume() {
		return false
	}
	if depth < maxPathableDepth && v.pathable(b, to, above, depth+1) {
		return true
	}
	return def
}

// topSnowPassable is BDS's top snow check: a full block of snow blocks, going level or down
// passes, and going up passes by at most one block, from a block with a box low enough
// when the snow is three layers or more.
func (v *view) topSnowPassable(height int32, from, to path.Pos) bool {
	const fullHeight = 7
	if height == fullHeight {
		return false
	}
	if to.Y <= from.Y {
		return true
	}
	if to.Y-from.Y > 1 {
		return false
	}
	if height < 3 {
		return true
	}
	box := (pathWorld{v}).CollisionBox(path.Block(v.block(from, 0)), from)
	if !box.HasVolume() {
		return false
	}
	top := float32(height+1)*0.125 + float32(to.Y)
	return top-box.Max.Y <= 1
}

func (v *view) fireImmune() bool {
	return v.m != nil && v.m.flags.Has(behavior.FlagFireImmune)
}

// fireChasers are the actor types BDS lets path through fire while they have a target:
// those of its zombie class (which carries the zombie monster categories) and the iron golem.
var fireChasers = map[string]bool{
	"minecraft:zombie": true, "minecraft:husk": true, "minecraft:drowned": true,
	"minecraft:zombie_villager": true, "minecraft:zombie_villager_v2": true,
	"minecraft:zombie_pigman": true, "minecraft:iron_golem": true,
}

// chasesThroughFire matches BDS: a zombie or an iron golem paths through fire while its
// target is still in the world.
func (v *view) chasesThroughFire() bool {
	m := v.m
	return m != nil && fireChasers[m.typ] && m.target != behavior.NoID && m.actor(m.target) != nil
}

// causeContact is BDS's contact damage cause.
const causeContact = 1

// sensorStopsContact matches BDS's check of a grown sweet berry bush: passable when the mob's
// damage sensor stops contact damage with the bush at p as the block subject (the fox's
// does); a mob without a damage sensor never passes. Only the trigger is matched: whether
// BDS also runs its event here was not read.
func (v *view) sensorStopsContact(p path.Pos) bool {
	m := v.m
	if m == nil || m.sensors == nil || m.sensors.Damage == nil {
		return false
	}
	t := m.sensors.Damage.Match(causeContact, resolver{m}.Tester(m, nil, &p))
	return t != nil && t.DealsDamage != sensor.DealsYes
}

// moveSide is BDS's side of a move between two blocks: 0 to 3 by the larger of the x and z
// steps, ties going to z; a door passes it the step from the door to the mob, a trapdoor
// the step from the mob to the trapdoor.
func moveSide(dx, dz int32) int32 {
	if abs32i(dx) > abs32i(dz) {
		if dx <= 0 {
			return 3
		}
		return 1
	}
	if dz > 0 {
		return 2
	}
	return 0
}

func abs32i(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// doorPassable is BDS's door check: the side the door's panel is on (by facing, then by
// the hinge when open) must be on the other axis than the move.
func doorPassable(from, to path.Pos, facing int32, open, hinge bool) bool {
	side := moveSide(from.X-to.X, from.Z-to.Z)
	var h int32
	if hinge {
		h = 1
	}
	var panel int32
	switch facing {
	case 0:
		panel = 3
		if open {
			panel = 2 * h
		}
	case 1:
		panel = 0
		if open {
			panel = 2*h + 1
		}
	case 2:
		panel = 1
		if open {
			panel = 2 * (1 - h)
		}
	case 3:
		panel = 2
		if open {
			panel = 2*(1-h) + 1
		}
	default:
		return true
	}
	return panel&1 != side&1
}

// trapdoorPassable is BDS's check of an open trapdoor: its panel, standing on the side its
// direction names (directions 0 and 1 on the x axis), must be on the other axis than the
// move.
func trapdoorPassable(from, to path.Pos, direction int32) bool {
	side := moveSide(to.X-from.X, to.Z-from.Z)
	xPanel := direction < 2
	return xPanel != (side&1 == 1)
}

// pathRule returns state id's path-through check.
func (b *Blocks) pathRule(id uint32) pathRule {
	if int(id) < len(b.rules) {
		return b.rules[id]
	}
	return pathRule{}
}

// nameMatcher turns a list of block names (with or without minecraft:) into a test of a
// block state, by its block name.
func (b *Blocks) nameMatcher(names []string) func(path.Block) bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[ns(n)] = true
	}
	return func(blk path.Block) bool { return set[b.reg.Name(uint32(blk))] }
}

// descriptorMatcher turns block descriptors (a name, or an object with a name or a
// q.any_tag / q.all_tags call on the block's tags) into a test of a block state by its
// block, nil when there are none. The block names are worked out here, once.
func (b *Blocks) descriptorMatcher(descs []string) func(path.Block) bool {
	if len(descs) == 0 {
		return nil
	}
	names := map[string]bool{}
	var tagExprs []string
	for _, d := range descs {
		d = strings.TrimSpace(d)
		if !strings.HasPrefix(d, "{") {
			names[ns(d)] = true
			continue
		}
		var o struct{ Name, Tags string }
		if json.Unmarshal([]byte(d), &o) != nil {
			continue
		}
		if o.Name != "" {
			names[ns(o.Name)] = true
		}
		if o.Tags != "" {
			tagExprs = append(tagExprs, o.Tags)
		}
	}
	if len(tagExprs) > 0 {
		for id := range b.reg.Count() {
			name := b.reg.Name(uint32(id)) //nolint:gosec // a state count
			if names[name] {
				continue
			}
			tags := b.reg.Tags(uint32(id)) //nolint:gosec // a state count
			for _, e := range tagExprs {
				if blockTagsMatch(e, tags) {
					names[name] = true
					break
				}
			}
		}
	}
	return func(blk path.Block) bool { return names[b.reg.Name(uint32(blk))] }
}

// blockTagsMatch runs a q.any_tag('a', ...) or q.all_tags(...) call on a block's tags.
func blockTagsMatch(expr string, tags []string) bool {
	open := strings.IndexByte(expr, '(')
	end := strings.LastIndexByte(expr, ')')
	if open < 0 || end < open {
		return false
	}
	fn := strings.ToLower(strings.TrimSpace(expr[:open]))
	all := strings.HasSuffix(fn, "all_tags")
	if !all && !strings.HasSuffix(fn, "any_tag") {
		return false
	}
	anyHas := false
	for _, a := range strings.Split(expr[open+1:end], ",") {
		has := slices.Contains(tags, strings.Trim(strings.TrimSpace(a), `'"`))
		if all && !has {
			return false
		}
		anyHas = anyHas || has
	}
	return anyHas
}
