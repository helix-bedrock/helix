package farm

import (
	"maps"
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// The composter, from BDS 1.26.60's ComposterBlock:
//
//   - Adding items (0xCB29E20): an item composts with its chance (the item's percent, BDS's
//     table read at runtime, helix-data's bds/compost.json); a composter at 7 or 8 takes
//     nothing. Each item of the count is used: on an empty composter it always raises the
//     level, otherwise when a draw below 100 is at most its chance. Reaching 7 stops there
//     and queues the composter's tick in 20 ticks. If the level rose, the block is set and
//     block.composter.fill_success plays, else block.composter.fill.
//   - The tick (0xCB275E0) and onPlace (0xCB26A30): at 7 a tick 20 later makes it 8, with
//     block.composter.ready.
//   - Emptying (0xCB27BB0): level 0 and block.composter.empty; the caller gives the bone meal.

// Composter is the composter block.
const Composter = "minecraft:composter"

// The composter's numbers.
const (
	stateComposterLevel = "composter_fill_level"
	composterReady      = 7
	composterFull       = 8
	composterDelay      = 20
	compostDraw         = 100
)

// composterKit holds the composter's states and the compost chances.
type composterKit struct {
	levels  [composterFull + 1]uint32
	chances atomic.Pointer[map[string]uint8]
}

func (t *Ticker) newComposterKit() *composterKit {
	k := &composterKit{}
	for lvl := range int32(composterFull + 1) {
		if id, ok := t.blocks.StateID(Composter, map[string]any{stateComposterLevel: lvl}); ok {
			k.levels[lvl] = id
			t.info[id] = stateInfo{kind: kindComposter, age: int8(lvl)}
		}
	}
	chances, err := data.CompostChances()
	if err != nil {
		chances = map[string]uint8{}
	}
	k.chances.Store(&chances)
	return k
}

// CompostChance is the percent chance one item raises a composter's level, 0 for an item
// that does not compost. Runs: any goroutine.
func (t *Ticker) CompostChance(item string) uint8 {
	return (*t.composterKit.chances.Load())[item]
}

// SetCompostChance changes an item's compost chance (percent, 100 always raises the level);
// 0 stops it composting. Runs: any goroutine.
func (t *Ticker) SetCompostChance(item string, chance uint8) {
	for {
		old := t.composterKit.chances.Load()
		next := maps.Clone(*old)
		if chance == 0 {
			delete(next, item)
		} else {
			next[item] = chance
		}
		if t.composterKit.chances.CompareAndSwap(old, &next) {
			return
		}
	}
}

// ComposterLevel is the fill level of a composter block, false for another block.
func (t *Ticker) ComposterLevel(block uint32) (int, bool) {
	s := t.state(block)
	return int(s.age), s.kind == kindComposter
}

// IsComposter reports whether block is a composter.
func (t *Ticker) IsComposter(block uint32) bool { return t.state(block).kind == kindComposter }

// Compost adds count of item to the composter at pos and returns how many were used (0
// if it took none: not a composter, an item that does not compost, or a level of 7 or 8).
// Used items are gone, whether or not they raised the level. Runs: owner of the chunk.
func (t *Ticker) Compost(r *world.Region, pos cube.Pos, item string, count int) int {
	b, ok := r.Block(pos, 0)
	s := t.state(b)
	if !ok || s.kind != kindComposter || count <= 0 {
		return 0
	}
	chance := int(t.CompostChance(item))
	if chance <= 0 || s.age >= composterReady {
		return 0
	}
	rng := t.rng(r)
	lvl, used := int(s.age), 0
	for used < count {
		used++
		if lvl != 0 && rng.IntN(compostDraw) > chance {
			continue
		}
		lvl++
		if lvl == composterReady {
			r.ScheduleTick(pos, t.composterKit.levels[lvl], composterDelay, 0)
			break
		}
	}
	if lvl == int(s.age) {
		playSound(r, pos, packet.SoundEventComposterFill)
		return used
	}
	t.set(r, pos, t.composterKit.levels[lvl])
	playSound(r, pos, packet.SoundEventComposterFillLayer)
	return used
}

// EmptyComposter empties a full composter at pos, reporting whether it was full. The caller
// gives out the bone meal. Runs: owner of the chunk.
func (t *Ticker) EmptyComposter(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	if s := t.state(b); !ok || s.kind != kindComposter || s.age != composterFull {
		return false
	}
	t.set(r, pos, t.composterKit.levels[0])
	playSound(r, pos, packet.SoundEventComposterEmpty)
	return true
}

// composterTick is the composter's queued tick: at 7 it becomes ready.
func (t *Ticker) composterTick(r *world.Region, pos cube.Pos) {
	if b, ok := r.Block(pos, 0); ok && t.state(b).kind == kindComposter && t.state(b).age == composterReady {
		t.set(r, pos, t.composterKit.levels[composterFull])
		playSound(r, pos, packet.SoundEventComposterReady)
	}
}

// playSound plays a block sound with no data at the middle of pos.
func playSound(r *world.Region, pos cube.Pos, name string) {
	at := mgl64.Vec3{float64(pos[0]) + 0.5, float64(pos[1]) + 0.5, float64(pos[2]) + 0.5}
	r.Broadcast(pos, &packet.LevelSoundEvent{SoundType: name, Position: world.Vec32(at), ExtraData: -1}, nil)
}
