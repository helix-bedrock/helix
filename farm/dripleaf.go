package farm

import (
	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Big and small dripleaf, from BDS 1.26.60 (the research note is private):
//
//   - BigDripleafBlock (vt 0xFBB88D8): onPlace 0xCCDADD0 (the dripleaf under a new one becomes
//     a stem), its tick 0xCCDB5F0 (broken when it cannot stay; a head with power goes back up,
//     else its tilt goes on: unstable to partial to full, full back up), the tilt 0xCCDE3C0
//     (unstable and partial tick again in 10, full in 100; partial and full play tilt_down)
//     and its reset 0xCCDE030 (tilt_up unless it was unstable, the waiting tick dropped), an
//     entity standing on a level head above 11/16 of the block 0xCCDE9D0 (it becomes unstable),
//     power reaching it 0xCCDBA50 (back up), survival 0xCCDCC90 (the top of its column is a
//     head, and what is under it is a dripleaf or its soil, 0xCCDCE70), neighbour change
//     0xCCDE8E0 (from above or below), bone meal 0xCCDD1F0 and 0xCCDCF00 (the head grows one
//     up into air or water).
//   - SmallDripleafBlock (vt 0xFC149F0): its two halves (onPlace 0xD4F8620 puts the upper one),
//     survival 0xD4F8D20 (each half on the other, the lower on clay or moss, or on dirt in
//     water, 0xD4F8C20), bone meal 0xD4F9480 and 0xCCDD810 (a big dripleaf 2 to 5 high where
//     there is air, water or the small dripleaf).
//
// A projectile hitting a head (0xCCDEBD0) tilts it fully at once. Players and mobs tip it by
// standing on it, checked every tick. Not ported: the block_change game event a reset posts,
// and items lying on a leaf.

// Blocks and states of the dripleaves.
const (
	bigDripleaf      = "minecraft:big_dripleaf"
	smallDripleaf    = "minecraft:small_dripleaf_block"
	stateLeafHead    = "big_dripleaf_head"
	stateLeafTilt    = "big_dripleaf_tilt"
	stateUpperHalf   = "upper_block_bit"
	nameClay         = "minecraft:clay"
	nameMossBlock    = "minecraft:moss_block"
	leafStem, leafHd = 0, 1
)

// The tilts of a big dripleaf, in BDS's order (stateInfo.age).
const (
	tiltNone int8 = iota
	tiltUnstable
	tiltPartial
	tiltFull
	tiltCount
)

// tiltNames are the big_dripleaf_tilt values by tilt.
var tiltNames = [tiltCount]string{"none", "unstable", "partial_tilt", "full_tilt"}

// Numbers of the dripleaves in BDS.
const (
	// tiltStepTicks is how long unstable and partial last; tiltFullTicks how long full does.
	tiltStepTicks, tiltFullTicks = 10, 100
	// leafStandHeight is how far up the block an entity's feet must be to tip a level head.
	leafStandHeight = 0.6875
	// smallToBigMin and smallToBigSpread: bone meal grows a big dripleaf of 2 + up to 3.
	smallToBigMin, smallToBigSpread = 2, 4
	directions                      = 4
)

// leafKit is the states of the dripleaves, made once by NewTicker.
type leafKit struct {
	// big is each big dripleaf state by head, tilt and direction.
	big [2][tiltCount][directions]uint32
	// small is each small dripleaf half by upper bit and direction.
	small      [2][directions]uint32
	clay, moss uint32
}

// newLeafKit fills the dripleaf states into t.info and returns their kit.
func (t *Ticker) newLeafKit() *leafKit {
	b := t.blocks
	k := &leafKit{clay: blockOrNone(b, nameClay), moss: blockOrNone(b, nameMossBlock)}
	names := direction.Names()
	for head := range int8(2) {
		for tilt := range tiltCount {
			for d := range int8(directions) {
				id, ok := b.StateID(bigDripleaf, map[string]any{stateLeafHead: uint8(head), stateLeafTilt: tiltNames[tilt], direction.CardinalState: names[d]}) //nolint:gosec // 0 or 1
				if !ok {
					continue
				}
				k.big[head][tilt][d] = id
				t.info[id] = stateInfo{kind: kindBigDripleaf, age: tilt, facing: head, crop: uint8(d)} //nolint:gosec // below 4
			}
		}
	}
	for upper := range int8(2) {
		for d := range int8(directions) {
			id, ok := b.StateID(smallDripleaf, map[string]any{stateUpperHalf: uint8(upper), direction.CardinalState: names[d]}) //nolint:gosec // 0 or 1
			if !ok {
				continue
			}
			k.small[upper][d] = id
			t.info[id] = stateInfo{kind: kindSmallDripleaf, facing: upper, crop: uint8(d)} //nolint:gosec // below 4
		}
	}
	return k
}

// PowerFunc reports whether redstone powers the block at pos. Runs: owner of the region.
type PowerFunc func(r *world.Region, pos cube.Pos) bool

// SetPower sets what tells a big dripleaf it is powered. Without it none ever is. Runs: any
// goroutine.
func (t *Ticker) SetPower(f PowerFunc) {
	if f == nil {
		t.power.Store(nil)
		return
	}
	t.power.Store(&f)
}

// powered reports whether redstone powers pos.
func (t *Ticker) powered(r *world.Region, pos cube.Pos) bool {
	f := t.power.Load()
	return f != nil && (*f)(r, pos)
}

// bigLeafPlaced is BigDripleafBlock's onPlace: the dripleaf under it becomes a stem.
func (t *Ticker) bigLeafPlaced(r *world.Region, pos cube.Pos) {
	below := pos.Side(cube.FaceDown)
	b, ok := r.Block(below, 0)
	if !ok {
		return
	}
	if s := t.state(b); s.kind == kindBigDripleaf && s.facing == leafHd {
		r.SetBlock(below, 0, t.leafKit.big[leafStem][tiltNone][s.crop])
	}
}

// bigLeafSoil is BigDripleafBlock's mayPlaceOn: a big dripleaf, the plant soils or clay.
func (t *Ticker) bigLeafSoil(below uint32) bool {
	return t.state(below).kind == kindBigDripleaf || t.grassKit.soils[below] || below == t.leafKit.clay
}

// bigLeafSurvives is its canSurvive: the top of its column is a head, and it stands on a
// dripleaf or its soil. own is the state at pos, which is not in the world yet when it is
// being placed.
func (t *Ticker) bigLeafSurvives(r *world.Region, pos cube.Pos, own stateInfo) bool {
	top := pos
	for {
		b, ok := r.Block(top.Side(cube.FaceUp), 0)
		if !ok {
			return true
		}
		if t.state(b).kind != kindBigDripleaf {
			break
		}
		top = top.Side(cube.FaceUp)
	}
	if top == pos {
		if own.facing != leafHd {
			return false
		}
	} else if b, ok := r.Block(top, 0); ok && t.state(b).facing != leafHd {
		return false
	}
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	return !ok || t.bigLeafSoil(below)
}

// bigLeafTick is its tick.
func (t *Ticker) bigLeafTick(r *world.Region, pos cube.Pos, block uint32, s stateInfo) {
	if !t.bigLeafSurvives(r, pos, s) {
		if t.fall != nil {
			t.fall(r, pos, block)
		}
		return
	}
	if s.facing != leafHd {
		return
	}
	if t.powered(r, pos) {
		t.resetTilt(r, pos, s)
		return
	}
	switch s.age {
	case tiltUnstable:
		t.setTilt(r, pos, s, tiltPartial)
	case tiltPartial:
		t.setTilt(r, pos, s, tiltFull)
	case tiltFull:
		t.resetTilt(r, pos, s)
	}
}

// setTilt is BDS's 0xCCDE3C0: the head at tilt, its next tick, and tilt_down past unstable.
func (t *Ticker) setTilt(r *world.Region, pos cube.Pos, s stateInfo, tilt int8) {
	block := t.leafKit.big[leafHd][tilt][s.crop]
	r.SetBlock(pos, 0, block)
	r.UpdateNeighbours(pos)
	delay := int32(tiltStepTicks)
	if tilt == tiltFull {
		delay = tiltFullTicks
	}
	r.ScheduleTick(pos, block, delay, 0)
	if tilt != tiltUnstable {
		playSound(r, pos, packet.SoundEventBigDripleafTiltDown)
	}
}

// resetTilt is BDS's 0xCCDE030: level again, tilt_up unless it was unstable, and no tick left
// waiting.
func (t *Ticker) resetTilt(r *world.Region, pos cube.Pos, s stateInfo) {
	if s.age != tiltUnstable {
		playSound(r, pos, packet.SoundEventBigDripleafTiltUp)
	}
	block := t.leafKit.big[leafHd][tiltNone][s.crop]
	r.CancelTick(pos, block)
	if s.age == tiltNone {
		return
	}
	r.SetBlock(pos, 0, block)
	r.UpdateNeighbours(pos)
}

// DripleafPowered is power reaching the big dripleaf at pos: a tilted head goes back up.
// Runs: owner of the region.
func (t *Ticker) DripleafPowered(r *world.Region, pos cube.Pos) {
	b, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	if s := t.state(b); s.kind == kindBigDripleaf && s.facing == leafHd && s.age != tiltNone {
		t.resetTilt(r, pos, s)
	}
}

// StandOnDripleaf is an entity whose feet are at feetY standing on the block at pos: a level,
// unpowered big dripleaf head under feet above 11/16 of it starts to tip. Runs: owner of the
// region.
func (t *Ticker) StandOnDripleaf(r *world.Region, pos cube.Pos, feetY float32) {
	b, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	s := t.state(b)
	if s.kind != kindBigDripleaf || s.facing != leafHd || s.age != tiltNone || t.powered(r, pos) {
		return
	}
	if feetY > float32(pos[1])+leafStandHeight {
		t.setTilt(r, pos, s, tiltUnstable)
	}
}

// ProjectileHitDripleaf is a projectile hitting the block at pos (BDS's 0xCCDEBD0): a big
// dripleaf head not fully tilted tilts fully at once. Runs: owner of the region.
func (t *Ticker) ProjectileHitDripleaf(r *world.Region, pos cube.Pos) {
	b, ok := r.Block(pos, 0)
	if !ok {
		return
	}
	if s := t.state(b); s.kind == kindBigDripleaf && s.facing == leafHd && s.age != tiltFull {
		t.setTilt(r, pos, s, tiltFull)
	}
}

// bigLeafRoom reports whether a big dripleaf may grow into pos: air, water or a small
// dripleaf, under the top of the dimension.
func (t *Ticker) bigLeafRoom(r *world.Region, pos cube.Pos) bool {
	if pos[1] > r.Dimension().Range()[1] {
		return false
	}
	b, ok := r.Block(pos, 0)
	if !ok {
		return false
	}
	return b == t.blocks.Air() || t.blocks.Properties(b).Material == materialWater || t.state(b).kind == kindSmallDripleaf
}

// leafColumnTop is the top dripleaf of the big dripleaf column through pos.
func (t *Ticker) leafColumnTop(r *world.Region, pos cube.Pos) cube.Pos {
	for {
		b, ok := r.Block(pos.Side(cube.FaceUp), 0)
		if !ok || t.state(b).kind != kindBigDripleaf {
			return pos
		}
		pos = pos.Side(cube.FaceUp)
	}
}

// fertilizeBigLeaf is bone meal on a big dripleaf: the head grows one block up.
func (t *Ticker) fertilizeBigLeaf(r *world.Region, pos cube.Pos) bool {
	top := t.leafColumnTop(r, pos)
	above := top.Side(cube.FaceUp)
	if !t.bigLeafRoom(r, above) {
		return false
	}
	b, _ := r.Block(top, 0)
	s := t.state(b)
	r.SetBlock(top, 0, t.leafKit.big[leafStem][tiltNone][s.crop])
	t.set(r, above, t.leafKit.big[leafHd][tiltNone][s.crop])
	return true
}

// smallLeafPlaced is SmallDripleafBlock's onPlace: a lower half puts its upper half above.
func (t *Ticker) smallLeafPlaced(r *world.Region, pos cube.Pos, s stateInfo) {
	if s.facing != 0 {
		return
	}
	above := pos.Side(cube.FaceUp)
	if r.SetBlock(above, 0, t.leafKit.small[1][s.crop]) {
		r.UpdateNeighbours(above)
	}
}

// smallLeafSoil is SmallDripleafBlock's mayPlaceOn for the plant at pos: clay or moss, or a
// dirt block with water in the plant's block.
func (t *Ticker) smallLeafSoil(r *world.Region, pos cube.Pos, below uint32) bool {
	k := t.leafKit
	if below == k.clay || below == k.moss {
		return true
	}
	return t.water(r, pos) && t.hasTag(below, tagDirt)
}

// smallLeafSurvives is its canSurvive: each half on the other, the lower on its soil.
func (t *Ticker) smallLeafSurvives(r *world.Region, pos cube.Pos, s stateInfo) bool {
	lower := pos
	other := pos.Side(cube.FaceUp)
	if s.facing != 0 {
		lower, other = pos.Side(cube.FaceDown), pos.Side(cube.FaceDown)
	}
	if b, ok := r.Block(other, 0); ok && t.state(b).kind != kindSmallDripleaf {
		return false
	}
	below, ok := r.Block(lower.Side(cube.FaceDown), 0)
	return !ok || t.smallLeafSoil(r, lower, below)
}

// smallLeafMayPlace is SmallDripleafBlock's mayPlace for its lower half: room for the upper
// half and its soil under it.
func (t *Ticker) smallLeafMayPlace(r *world.Region, pos cube.Pos) bool {
	if pos[1]+1 > r.Dimension().Range()[1] {
		return false
	}
	below, ok := r.Block(pos.Side(cube.FaceDown), 0)
	if ok && !t.smallLeafSoil(r, pos, below) {
		return false
	}
	above, ok := r.Block(pos.Side(cube.FaceUp), 0)
	return !ok || above == t.blocks.Air() || t.blocks.Properties(above).Material == materialWater
}

// fertilizeSmallLeaf is bone meal on a small dripleaf: a big dripleaf 2 to 5 high from its
// lower half, as far as there is room.
func (t *Ticker) fertilizeSmallLeaf(r *world.Region, pos cube.Pos, s stateInfo) bool {
	lower := pos
	if s.facing != 0 {
		lower = pos.Side(cube.FaceDown)
	}
	k := t.leafKit
	r.SetBlock(lower.Side(cube.FaceUp), 0, t.blocks.Air())
	want := smallToBigMin + t.rng(r).IntN(smallToBigSpread)
	height := 0
	for height < want && t.bigLeafRoom(r, lower.Side(cube.FaceUp).Add(cube.Pos{0, height - 1, 0})) {
		height++
	}
	height = max(height, 1)
	q := t.quiet(r)
	q.on = true
	for i := range height {
		head := uint8(leafStem)
		if i == height-1 {
			head = leafHd
		}
		r.SetBlock(lower.Add(cube.Pos{0, i, 0}), 0, k.big[head][tiltNone][s.crop])
	}
	q.on = false
	for i := range height {
		r.UpdateNeighbours(lower.Add(cube.Pos{0, i, 0}))
	}
	return true
}
