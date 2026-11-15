package farm

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Chorus plants and flowers, from BDS 1.26.60 (the research note is private):
//
//   - ChorusPlantBlock: connections 0xCB5B7A0 (each side joins a chorus plant or flower, the
//     bottom also end stone; the same rule TheEndGenerator::decorationPostProcessChunk
//     runs), survival 0xCB5AA90, neighbour change 0xCB5A9C0 (a tick 1 later when it
//     cannot stay), its tick 0xCB5A870 (breaks with its drops when it still cannot stay).
//   - ChorusFlowerBlock: survival 0xCB59C40 (end stone or a chorus plant under it, or air
//     under it and exactly one chorus plant beside it with air on the other sides), neighbour
//     change 0xCB59B90 and tick 0xCB58A40 as the plant's but breaking with no drops, the
//     growth on random ticks 0xCB58C40 (grown flowers placed by 0xCB59860 with LevelSound
//     125, a dead one plays 126; the sides test is 0xCB59740), and a projectile breaking it
//     with its drops under projectilescanbreakblocks (0xCB59F20).
const (
	chorusPlant  = "minecraft:chorus_plant"
	chorusFlower = "minecraft:chorus_flower"
	endStone     = "minecraft:end_stone"
)

// Numbers of chorus in BDS.
const (
	// chorusDeadAge is a flower that no longer grows; chorusMaxGrowAge the oldest that does.
	chorusDeadAge, chorusMaxGrowAge = 5, 4
	// chorusBranchOdds is the most side branches a flower tries, one more on end stone.
	chorusBranchOdds = 4
	// chorusStemTries is how far down a flower counts its stem; chorusTallStem is the stem
	// from which it may stop growing up.
	chorusStemTries, chorusTallStem = 4, 2
	// chorusUpOdds is the odds a tall stem grows up against, one more on end stone.
	chorusUpOdds = 4
)

// chorusSides are the sides a chorus plant's connections name, in the order of the bits of
// its mask, with their state names.
func chorusSides() [6]struct {
	face cube.Face
	name string
} {
	return [6]struct {
		face cube.Face
		name string
	}{
		{cube.FaceDown, "down"}, {cube.FaceUp, "up"}, {cube.FaceNorth, "north"},
		{cube.FaceSouth, "south"}, {cube.FaceWest, "west"}, {cube.FaceEast, "east"},
	}
}

// chorusBranchFaces are the sides a flower's branch draw names: BDS's 0 is z-1, 1 x+1, 2
// z+1, 3 x-1.
func chorusBranchFaces() [4]cube.Face {
	return [4]cube.Face{cube.FaceNorth, cube.FaceEast, cube.FaceSouth, cube.FaceWest}
}

// chorusKit is the chorus states, made once by NewTicker.
type chorusKit struct {
	// plants are the plant by its connections, bit k for chorusSides()[k].
	plants [1 << 6]uint32
	// flowers are the flower by age.
	flowers  [chorusDeadAge + 1]uint32
	endStone uint32
}

// newChorusKit fills the chorus states into t.info and returns their kit. The plant's age is
// its connection mask.
func (t *Ticker) newChorusKit() *chorusKit {
	b := t.blocks
	k := &chorusKit{endStone: blockOrNone(b, endStone)}
	for mask := range len(k.plants) {
		props := map[string]any{}
		for i, side := range chorusSides() {
			props["minecraft:connection_"+side.name] = mask>>i&1 == 1
		}
		id, ok := b.StateID(chorusPlant, props)
		if !ok {
			k.plants[mask] = math.MaxUint32
			continue
		}
		k.plants[mask] = id
		t.info[id] = stateInfo{kind: kindChorusPlant, age: int8(mask)} //nolint:gosec // 6 bits
	}
	for age := range int32(chorusDeadAge + 1) {
		id, ok := b.StateID(chorusFlower, map[string]any{stateAge: age})
		if !ok {
			k.flowers[age] = math.MaxUint32
			continue
		}
		k.flowers[age] = id
		t.info[id] = stateInfo{kind: kindChorusFlower, age: int8(age)}
	}
	return k
}

// isChorusPlant reports whether b is a chorus plant, in any of its connection states.
func (t *Ticker) isChorusPlant(b uint32) bool { return t.state(b).kind == kindChorusPlant }

// isChorus reports whether b is a chorus plant or flower.
func (t *Ticker) isChorus(b uint32) bool {
	k := t.state(b).kind
	return k == kindChorusPlant || k == kindChorusFlower
}

// chorusJoins returns the connections of a chorus plant at pos from its neighbours.
func (t *Ticker) chorusJoins(r *world.Region, pos cube.Pos) int {
	mask := 0
	for i, side := range chorusSides() {
		b, ok := r.Block(pos.Side(side.face), 0)
		if ok && (t.isChorus(b) || side.face == cube.FaceDown && b == t.chorusKit.endStone) {
			mask |= 1 << i
		}
	}
	return mask
}

// joinChorus sets the connections of the chorus plant at pos, without telling its
// neighbours: only how it looks changes.
func (t *Ticker) joinChorus(r *world.Region, pos cube.Pos) {
	b, ok := r.Block(pos, 0)
	if !ok || !t.isChorusPlant(b) {
		return
	}
	if want := t.chorusKit.plants[t.chorusJoins(r, pos)]; want != b && want != math.MaxUint32 {
		t.quietSet(r, pos, want)
	}
}

// chorusPlantSurvives is the plant's canSurvive: never in liquid; with blocks both above
// and below, no chorus beside it; it stands on end stone or a chorus plant, or, with air
// above or below, holds on to a chorus beside it that has a chorus plant or flower under it.
func (t *Ticker) chorusPlantSurvives(r *world.Region, pos cube.Pos) bool {
	if t.liquidAt(r, pos) {
		return false
	}
	open := t.isAir(r, pos.Side(cube.FaceUp)) || t.isAir(r, pos.Side(cube.FaceDown))
	for _, f := range chorusBranchFaces() {
		side := pos.Side(f)
		b, ok := r.Block(side, 0)
		if !ok || !t.isChorus(b) {
			continue
		}
		if !open {
			return false
		}
		if under, ok := r.Block(side.Side(cube.FaceDown), 0); ok && t.isChorus(under) {
			return true
		}
	}
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	return !ok || below == t.chorusKit.endStone || t.isChorusPlant(below)
}

// chorusFlowerSurvives is the flower's canSurvive: never in liquid; on end stone or a chorus
// plant, or over air with exactly one chorus plant beside it and air on its other sides.
func (t *Ticker) chorusFlowerSurvives(r *world.Region, pos cube.Pos) bool {
	if t.liquidAt(r, pos) {
		return false
	}
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if !ok || below == t.chorusKit.endStone || t.isChorusPlant(below) {
		return true
	}
	if below != t.blocks.Air() {
		return false
	}
	plants := 0
	for _, f := range chorusBranchFaces() {
		b, ok := r.Block(pos.Side(f), 0)
		switch {
		case !ok:
		case t.isChorusPlant(b):
			plants++
		case b != t.blocks.Air():
			return false
		}
	}
	return plants == 1
}

// chorusFlowerTick is the flower's random tick: under the top of the dimension, with air
// above and not too old, it grows up, or out to the sides, or dies.
func (t *Ticker) chorusFlowerTick(r *world.Region, pos cube.Pos, s stateInfo) {
	above := pos.Side(cube.FaceUp)
	if above[1] > r.Dimension().Range()[1] || !t.isAir(r, above) || s.age > chorusMaxGrowAge {
		return
	}
	rng := t.rng(r)
	up, onEndStone := t.chorusGrowsUp(r, pos)
	if up && t.chorusSidesAir(r, above, cube.FaceUp) {
		t.set(r, pos, t.chorusKit.plants[0])
		t.growChorusFlower(r, above, s.age)
		return
	}
	branches := rng.IntN(chorusBranchOdds)
	if onEndStone {
		branches++
	}
	grew := false
	for range branches {
		f := chorusBranchFaces()[rng.IntN(len(chorusBranchFaces()))]
		side := pos.Side(f)
		if t.isAir(r, side) && t.isAir(r, side.Side(cube.FaceDown)) && t.chorusSidesAir(r, side, f.Opposite()) {
			t.growChorusFlower(r, side, s.age+1)
			grew = true
		}
	}
	if grew {
		t.set(r, pos, t.chorusKit.plants[0])
		return
	}
	t.set(r, pos, t.chorusKit.flowers[chorusDeadAge])
	chorusSound(r, pos, packet.SoundEventChorusDeath)
}

// chorusGrowsUp decides whether a flower may grow straight up, and whether its stem stands
// on end stone: on end stone or air it may; on a stem of chorusTallStem or more (counted
// down chorusStemTries blocks) only by chance against its height; on anything else not.
func (t *Ticker) chorusGrowsUp(r *world.Region, pos cube.Pos) (up, onEndStone bool) {
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	switch {
	case !ok:
		return false, false
	case below == t.chorusKit.endStone, below == t.blocks.Air():
		return true, false
	case !t.isChorusPlant(below):
		return false, false
	}
	stem := 1
	for range chorusStemTries {
		b, ok := r.Block(cube.Pos{pos[0], pos[1] - stem - 1, pos[2]}, 0)
		if ok && t.isChorusPlant(b) {
			stem++
			continue
		}
		onEndStone = ok && b == t.chorusKit.endStone
		break
	}
	if stem < chorusTallStem {
		return true, onEndStone
	}
	odds := chorusUpOdds
	if onEndStone {
		odds++
	}
	return stem <= t.rng(r).IntN(odds), onEndStone
}

// chorusSidesAir reports whether the four sides of pos other than except are air.
func (t *Ticker) chorusSidesAir(r *world.Region, pos cube.Pos, except cube.Face) bool {
	for _, f := range chorusBranchFaces() {
		if f != except && !t.isAir(r, pos.Side(f)) {
			return false
		}
	}
	return true
}

// growChorusFlower places a flower of age at pos, with the grow sound.
func (t *Ticker) growChorusFlower(r *world.Region, pos cube.Pos, age int8) {
	if age < 0 || int(age) >= len(t.chorusKit.flowers) || t.chorusKit.flowers[age] == math.MaxUint32 {
		return
	}
	t.set(r, pos, t.chorusKit.flowers[age])
	chorusSound(r, pos, packet.SoundEventChorusGrow)
}

// chorusSound plays a chorus sound at the corner of pos, where BDS plays it.
func chorusSound(r *world.Region, pos cube.Pos, name string) {
	at := mgl32.Vec3{float32(pos[0]), float32(pos[1]), float32(pos[2])}
	r.Broadcast(pos, &packet.LevelSoundEvent{SoundType: name, Position: at, ExtraData: -1}, nil)
}

// chorusFall breaks a chorus block that can no longer stay: a plant with its drops, a flower
// with none (BDS's destroyBlock without resources), but its break particles.
func (t *Ticker) chorusFall(r *world.Region, pos cube.Pos, block uint32) {
	if t.state(block).kind == kindChorusPlant {
		if t.fall != nil {
			t.fall(r, pos, block)
		}
		return
	}
	t.set(r, pos, t.blocks.Air())
	at := mgl32.Vec3{float32(pos[0]) + 0.5, float32(pos[1]) + 0.5, float32(pos[2]) + 0.5}
	r.Broadcast(pos, &packet.LevelEvent{
		EventType: packet.LevelEventParticlesDestroyBlock, Position: at, EventData: int32(t.blocks.Hash(block)), //nolint:gosec // hashes are sent as int32
	}, nil)
}

// ProjectileHitChorus is a projectile hitting the block at pos (BDS's 0xCB59F20): with
// canBreak (the projectilescanbreakblocks game rule) a chorus flower breaks with its drops.
// Runs: owner of the region.
func (t *Ticker) ProjectileHitChorus(r *world.Region, pos cube.Pos, canBreak bool) {
	if !canBreak || t.fall == nil {
		return
	}
	if b, ok := r.Block(pos, 0); ok && t.state(b).kind == kindChorusFlower {
		t.fall(r, pos, b)
	}
}
