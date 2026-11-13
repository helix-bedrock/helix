package circuit

import (
	"maps"
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/redstone"
	"github.com/helix-bedrock/world/cube"
)

// Piston rules from BDS 1.26.60.
const (
	pistonEntityID = "PistonArm"
	movingEntityID = "MovingBlock"
	// pistonCarryHeight is how far above a moving block an entity still rides on it.
	pistonCarryHeight = 0.05
)

// A piston's states as BDS saves them.
const (
	pistonRetracted uint8 = iota
	pistonExpanding
	pistonExpanded
	pistonRetracting
)

// Saved fields of a piston and of a block on the move.
const (
	tagProgress     = "Progress"
	tagLastProgress = "LastProgress"
	tagState        = "State"
	tagNewState     = "NewState"
	tagSticky       = "Sticky"
	tagAttached     = "AttachedBlocks"
	tagBreaks       = "BreakBlocks"
	tagMovable      = "isMovable"
	tagMovingBlock  = "movingBlock"
	tagMovingExtra  = "movingBlockExtra"
	tagMovingEntity = "movingEntity"
	tagExpanding    = "expanding"
	tagPistonX      = "pistonPosX"
	tagPistonY      = "pistonPosY"
	tagPistonZ      = "pistonPosZ"
)

// pistonFaces is the way each facing_direction state of a piston points: BDS's
// table has the sides the other way round from other blocks.
var pistonFaces = [6]cube.Face{cube.FaceDown, cube.FaceUp, cube.FaceSouth, cube.FaceNorth, cube.FaceEast, cube.FaceWest}

// pistonFacing is one of those states, which is how BDS walks the blocks.
type pistonFacing uint8

// opposite is the state pointing the other way.
func (f pistonFacing) opposite() pistonFacing { return f ^ 1 }

// neighbourOrder is the order BDS looks at a sticky block's neighbours: all but the
// one given.
var neighbourOrder = [6][5]pistonFacing{
	{1, 2, 3, 4, 5},
	{0, 2, 3, 4, 5},
	{1, 0, 3, 4, 5},
	{1, 0, 2, 4, 5},
	{1, 0, 2, 3, 5},
	{1, 0, 2, 3, 4},
}

// loadPistonRules reads BDS's rules.
func loadPistonRules() (pistonRules, error) {
	d, err := data.PistonMoveData()
	if err != nil {
		return pistonRules{}, err
	}
	return pistonRules{immovable: nameSet(d.Immovable), breaks: nameSet(d.Breaks), noPull: nameSet(d.NoPull), sticky: nameSet(d.Sticky)}, nil
}

func nameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// facingOf is the way a piston or arm block points.
func (t *Ticker) facingOf(block uint32) pistonFacing {
	f := intState(t.blocks.States(block), stateFacing)
	if f < 0 || f >= int32(len(pistonFaces)) {
		return 0
	}
	return pistonFacing(f) //nolint:gosec // checked above
}

// pistonBlockedFace is the way signal travels when it comes in at a piston's front,
// which is what the piston does not take.
func pistonBlockedFace(block uint32, t *Ticker) redstone.Face {
	return redstone.Face(pistonFaces[t.facingOf(block).opposite()]) //nolint:gosec // faces fit
}

// pistonRules is what pistons do with each block name.
type pistonRules struct {
	immovable, breaks, noPull, sticky map[string]bool
}

// pistonEntity is a piston's block entity, which moves the blocks and is what the
// client draws the arm from. Runs: owner of its chunk.
type pistonEntity struct {
	t                *Ticker
	saved            map[string]any
	sticky           bool
	progress, last   float32
	state, newState  uint8
	lastState        uint8
	attached, breaks []cube.Pos
}

// SaveData is the piston as BDS saves it.
func (e *pistonEntity) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(e.saved)
	if m == nil {
		m = map[string]any{}
	}
	m["id"] = pistonEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	m[tagMovable] = flag(e.state == pistonRetracted)
	m[tagProgress], m[tagLastProgress] = e.progress, e.last
	m[tagState], m[tagNewState] = e.state, e.newState
	m[tagSticky] = flag(e.sticky)
	m[tagAttached], m[tagBreaks] = posList(e.attached), posList(e.breaks)
	return m
}

// NetworkData is what the client needs to draw the arm and what it carries: all of it.
func (e *pistonEntity) NetworkData(pos cube.Pos) map[string]any { return e.SaveData(pos) }

// posList is positions as BDS saves them: three numbers each, in one list.
func posList(l []cube.Pos) []any {
	out := make([]any, 0, 3*len(l))
	for _, p := range l {
		out = append(out, int32(p[0]), int32(p[1]), int32(p[2])) //nolint:gosec // block coordinates fit int32
	}
	return out
}

// listPos is posList turned around.
func listPos(v any) []cube.Pos {
	l, _ := v.([]any)
	out := make([]cube.Pos, 0, len(l)/3)
	for i := 0; i+2 < len(l); i += 3 {
		x, _ := l[i].(int32)
		y, _ := l[i+1].(int32)
		z, _ := l[i+2].(int32)
		out = append(out, cube.Pos{int(x), int(y), int(z)})
	}
	return out
}

// loadPiston brings back a saved piston.
func (t *Ticker) loadPiston(saved map[string]any) *pistonEntity {
	e := &pistonEntity{t: t, saved: maps.Clone(saved)}
	e.progress, _ = saved[tagProgress].(float32)
	e.last, _ = saved[tagLastProgress].(float32)
	e.state, _ = saved[tagState].(uint8)
	e.newState, _ = saved[tagNewState].(uint8)
	e.lastState = e.state
	sticky, _ := saved[tagSticky].(uint8)
	e.sticky = sticky != 0
	e.attached, e.breaks = listPos(saved[tagAttached]), listPos(saved[tagBreaks])
	return e
}

// TickBlockEntity is BDS's piston tick: the power read, the start or end of a move,
// then the arm's step.
func (e *pistonEntity) TickBlockEntity(r *world.Region, pos cube.Pos) {
	t := e.t
	block, ok := r.Block(pos, 0)
	if !ok || t.kind(block) != kindPiston {
		return
	}
	dir := t.facingOf(block)
	e.last = e.progress
	strength := t.circuitOf(r).sys.Strength(toPos(pos))
	if e.newState == pistonExpanding && e.state != pistonExpanding && strength == 0 {
		e.newState = e.state
	}
	if e.lastState != e.state {
		e.moveSound(r, pos)
		e.lastState = e.state
	}
	moving := e.state == pistonExpanding || e.state == pistonRetracting
	switch {
	case strength > 0 && e.state == pistonRetracted && e.newState == pistonRetracted:
		e.newState = pistonExpanding
	case strength <= 0 && e.state == pistonExpanded && e.newState == pistonExpanded:
		e.newState = pistonRetracting
	}
	if e.newState != e.state {
		if !e.change(r, pos, block, dir) {
			return
		}
		moving = true
	}
	switch e.state {
	case pistonExpanding:
		e.progress = min(e.progress+e.t.settings.Get().Piston.Step, 1)
		if e.progress >= 1 {
			e.newState = pistonExpanded
		}
	case pistonRetracting:
		e.progress = max(e.progress-e.t.settings.Get().Piston.Step, 0)
		if e.progress <= 0 {
			e.newState = pistonRetracted
		}
	}
	if moving {
		r.SendBlockEntity(pos)
		e.pushEntities(r, pos, dir)
	}
}

// pushEntities moves what a moving arm or block runs into out in front of it, and what
// rides on a block moving sideways. Runs: owner of its chunk.
func (e *pistonEntity) pushEntities(r *world.Region, pos cube.Pos, dir pistonFacing) {
	expanding := e.state == pistonExpanding
	if e.t.entityBox == nil || (!expanding && e.state != pistonRetracting) {
		return
	}
	front := pos.Side(pistonFaces[dir])
	step := mgl32.Vec3{float32(front[0] - pos[0]), float32(front[1] - pos[1]), float32(front[2] - pos[2])}
	travel, gone := step, e.progress
	if !expanding {
		travel, gone = step.Mul(-1), 1-e.progress
	}
	c := e.t.circuitOf(r)
	if expanding {
		e.pushOutOf(c, pos, travel, gone, false)
	}
	for _, at := range e.attached {
		e.pushOutOf(c, at, travel, gone, true)
	}
}

// pushOutOf moves entities out of a block that started at base and has gone this far
// along travel. A block, unlike the arm, also takes along what rides on it.
func (e *pistonEntity) pushOutOf(c *regionCircuit, base cube.Pos, travel mgl32.Vec3, gone float32, carries bool) {
	lo := mgl32.Vec3{float32(base[0]), float32(base[1]), float32(base[2])}.Add(travel.Mul(gone))
	hi := lo.Add(mgl32.Vec3{1, 1, 1})
	c.found = c.r.AppendEntitiesIn(c.found[:0], lo[0]-plateReach, lo[2]-plateReach, hi[0]+plateReach, hi[2]+plateReach)
	for _, o := range c.found {
		eLo, eHi, _, ok := e.t.entityBox(o)
		if !ok {
			continue
		}
		if carries && travel[1] == 0 && eLo[1] >= hi[1] && eLo[1] < hi[1]+pistonCarryHeight {
			x, z := (eLo[0]+eHi[0])/2, (eLo[2]+eHi[2])/2
			if x >= lo[0] && x <= hi[0] && z >= lo[2] && z <= hi[2] {
				e.moveEntity(c.r, o, travel.Mul(e.t.settings.Get().Piston.Step))
			}
			continue
		}
		if eHi[0] <= lo[0] || eLo[0] >= hi[0] || eHi[1] <= lo[1] || eLo[1] >= hi[1] || eHi[2] <= lo[2] || eLo[2] >= hi[2] {
			continue
		}
		var by mgl32.Vec3
		for i := range 3 {
			switch {
			case travel[i] > 0:
				by[i] = hi[i] - eLo[i]
			case travel[i] < 0:
				by[i] = lo[i] - eHi[i]
			}
		}
		e.moveEntity(c.r, o, by)
	}
}

// moveEntity moves an entity through the engine's hook, which knows how each kind moves.
func (e *pistonEntity) moveEntity(r *world.Region, o *world.Entity, by mgl32.Vec3) {
	if push := e.t.entityPush; push != nil {
		push(r, o, by)
		return
	}
	o.Apply(world.Movement{Pos: o.Position().Add(by), Rot: o.Rotation(), OnGround: o.OnGround(), Tick: o.Tick()})
}

// change starts or ends a move; false if the piston cannot move now.
func (e *pistonEntity) change(r *world.Region, pos cube.Pos, block uint32, dir pistonFacing) bool {
	t := e.t
	front := pos.Side(pistonFaces[dir])
	switch e.newState {
	case pistonExpanding:
		e.state = pistonExpanding
		if !e.collect(r, pos, front, dir) {
			e.state = pistonRetracted
			e.attached, e.breaks = e.attached[:0], e.breaks[:0]
			return false
		}
		e.spawnMoving(r, pos, dir, true)
		arm := t.armOf(block)
		if r.SetBlock(front, 0, arm) {
			r.UpdateNeighbours(front)
		}
	case pistonRetracting:
		e.state = pistonRetracting
		if b, ok := r.Block(front, 0); ok && t.isArm(b) && r.SetBlock(front, 0, t.blocks.Air()) {
			r.UpdateNeighbours(front)
		}
		if e.sticky {
			back := dir.opposite()
			if e.collect(r, pos, front.Side(pistonFaces[dir]), back) {
				e.spawnMoving(r, pos, back, false)
			} else {
				e.attached, e.breaks = e.attached[:0], e.breaks[:0]
			}
		}
	case pistonExpanded, pistonRetracted:
		move := dir
		if e.newState == pistonRetracted {
			move = dir.opposite()
		}
		e.finish(r, move)
		e.state = e.newState
	}
	return true
}

// collect is BDS's look at what moves, from at in direction move: false if the
// piston is stopped or there are too many.
func (e *pistonEntity) collect(r *world.Region, pos, at cube.Pos, move pistonFacing) bool {
	e.attached, e.breaks = e.attached[:0], e.breaks[:0]
	if !e.walk(r, pos, at, move, move) {
		return false
	}
	return len(e.attached) <= e.t.settings.Get().Piston.PushLimit
}

// walk adds the block at at and what it takes along. reached is the way it was
// reached. A block already listed is done.
func (e *pistonEntity) walk(r *world.Region, pos, at cube.Pos, reached, move pistonFacing) bool {
	t := e.t
	if slices.Contains(e.attached, at) {
		return true
	}
	if y := r.Dimension().Range(); at[1] < y[0] || at[1] > y[1] {
		return false
	}
	block, ok := r.Block(at, 0)
	if !ok {
		return false
	}
	if block == t.blocks.Air() {
		return true
	}
	name := t.blocks.Name(block)
	rules := &t.pistons
	pulling := e.state == pistonRetracting
	switch {
	case rules.breaks[name]:
		if reached == move && !slices.Contains(e.breaks, at) {
			e.breaks = append(e.breaks, at)
		}
		return true
	case rules.immovable[name], pulling && rules.noPull[name]:
		return false
	case t.kind(block) == kindPiston && at != pos:
		// A piston that is out does not move, and one to the side is simply left.
		if be, ok := r.BlockEntity(at); ok {
			if o, ok := be.(*pistonEntity); ok && o.state != pistonRetracted {
				return reached != move
			}
		}
	}
	e.attached = append(e.attached, at)
	if !rules.sticky[name] {
		return e.walk(r, pos, at.Side(pistonFaces[move]), move, move)
	}
	for _, f := range neighbourOrder[reached.opposite()] {
		n := at.Side(pistonFaces[f])
		if nb, ok := r.Block(n, 0); ok && f != move && rules.sticky[t.blocks.Name(nb)] && t.blocks.Name(nb) != name {
			// Slime does not hold on to honey.
			continue
		}
		if !e.walk(r, pos, n, f, move) {
			return false
		}
	}
	return true
}

// spawnMoving breaks what is in the way and turns what moves into moving blocks, one
// on from where they stood. Their block entities go with them.
func (e *pistonEntity) spawnMoving(r *world.Region, pos cube.Pos, move pistonFacing, expanding bool) {
	t := e.t
	for _, at := range e.breaks {
		if b, ok := r.Block(at, 0); ok {
			t.fall(r, at, b)
		}
	}
	blocks := make([]uint32, len(e.attached))
	saved := make([]map[string]any, len(e.attached))
	for i, at := range e.attached {
		blocks[i], _ = r.Block(at, 0)
		if be, ok := r.TakeBlockEntity(at); ok {
			saved[i] = be.SaveData(at)
		}
	}
	for _, at := range e.attached {
		r.SetBlock(at, 0, t.blocks.Air())
	}
	for i, at := range e.attached {
		to := at.Side(pistonFaces[move])
		if !r.SetBlock(to, 0, t.moving) {
			continue
		}
		be, ok := r.BlockEntity(to)
		if !ok {
			continue
		}
		if m, ok := be.(*movingEntity); ok {
			m.block, m.entity, m.piston, m.expanding = blocks[i], saved[i], pos, expanding
			r.SendBlockEntity(to)
		}
	}
	for _, at := range e.attached {
		r.UpdateNeighbours(at)
	}
}

// finish sets the moved blocks down where they went, one on from the positions kept,
// and forgets them.
func (e *pistonEntity) finish(r *world.Region, move pistonFacing) {
	for _, at := range e.attached {
		to := at.Side(pistonFaces[move])
		if be, ok := r.BlockEntity(to); ok {
			if m, ok := be.(*movingEntity); ok {
				m.place(r, to)
			}
		}
	}
	e.attached, e.breaks = e.attached[:0], e.breaks[:0]
}

// moveSound is the piston's sound as a move starts.
func (e *pistonEntity) moveSound(r *world.Region, pos cube.Pos) {
	var sound string
	switch e.state {
	case pistonExpanding:
		sound = packet.SoundEventPistonOut
	case pistonRetracting:
		sound = packet.SoundEventPistonIn
	default:
		return
	}
	r.Broadcast(pos, &packet.LevelSoundEvent{
		SoundType: sound, Position: mgl32.Vec3{float32(pos[0]) + 0.5, float32(pos[1]) + 0.5, float32(pos[2]) + 0.5},
		ExtraData: -1,
	}, nil)
}

// movingEntity is a block on its way, standing where it is going. It is drawn by its
// piston and set down when that piston is done, or as soon as the piston is gone.
type movingEntity struct {
	t         *Ticker
	block     uint32
	entity    map[string]any
	piston    cube.Pos
	expanding bool
}

// SaveData is the moving block as BDS saves it.
func (m *movingEntity) SaveData(pos cube.Pos) map[string]any {
	blocks := m.t.blocks
	out := map[string]any{
		"id": movingEntityID, tagMovable: uint8(0), tagExpanding: flag(m.expanding),
		"x": int32(pos[0]), "y": int32(pos[1]), "z": int32(pos[2]), //nolint:gosec // block coordinates fit int32
		tagMovingBlock: blockCompound(blocks, m.block),
		tagMovingExtra: blockCompound(blocks, blocks.Air()),
		tagPistonX:     int32(m.piston[0]), tagPistonY: int32(m.piston[1]), tagPistonZ: int32(m.piston[2]), //nolint:gosec // block coordinates fit int32
	}
	if m.entity != nil {
		out[tagMovingEntity] = m.entity
	}
	return out
}

// NetworkData is all of it: the client draws the block from it.
func (m *movingEntity) NetworkData(pos cube.Pos) map[string]any {
	out := m.SaveData(pos)
	delete(out, tagMovingEntity)
	return out
}

// TickBlockEntity sets the block down if its piston is gone.
func (m *movingEntity) TickBlockEntity(r *world.Region, pos cube.Pos) {
	b, ok := r.Block(m.piston, 0)
	if !ok {
		return
	}
	if m.t.kind(b) != kindPiston {
		m.place(r, pos)
	}
}

// place sets the block down with its block entity, and tells its neighbours.
func (m *movingEntity) place(r *world.Region, pos cube.Pos) {
	if !r.SetBlock(pos, 0, m.block) {
		return
	}
	if m.entity != nil {
		restore := maps.Clone(m.entity)
		restore["x"], restore["y"], restore["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
		if maker := r.Dimension().BlockEntityMaker(); maker != nil {
			if be, ok := maker.Load(pos, restore); ok {
				r.SetBlockEntity(pos, be)
				r.SendBlockEntity(pos)
			}
		}
	}
	r.UpdateNeighbours(pos)
	// An observer that was moved fires.
	if m.t.kind(m.block) == kindObserver {
		m.t.turnObserver(m.t.circuitOf(r), pos, m.t.with(m.block, statePowered, flag(false)), true)
	}
}

// blockCompound is a block as BDS saves one inside another.
func blockCompound(blocks *registry.Blocks, block uint32) map[string]any {
	return map[string]any{"name": blocks.Name(block), "states": blocks.States(block), "version": blocks.Version(block)}
}

// loadMoving brings back a saved moving block.
func (t *Ticker) loadMoving(saved map[string]any) *movingEntity {
	m := &movingEntity{t: t, block: t.blocks.Air()}
	if c, ok := saved[tagMovingBlock].(map[string]any); ok {
		name, _ := c["name"].(string)
		states, _ := c["states"].(map[string]any)
		if id, ok := t.blocks.StateID(name, states); ok {
			m.block = id
		}
	}
	m.entity, _ = saved[tagMovingEntity].(map[string]any)
	x, _ := saved[tagPistonX].(int32)
	y, _ := saved[tagPistonY].(int32)
	z, _ := saved[tagPistonZ].(int32)
	m.piston = cube.Pos{int(x), int(y), int(z)}
	expanding, _ := saved[tagExpanding].(uint8)
	m.expanding = expanding != 0
	return m
}

// armOf is the arm of a piston block, facing as it does.
func (t *Ticker) armOf(piston uint32) uint32 {
	name := nameArm
	if t.blocks.Name(piston) == nameStickyPiston {
		name = nameStickyArm
	}
	return t.named(name, piston)
}

// isArm reports whether block is a piston's arm.
func (t *Ticker) isArm(block uint32) bool {
	n := t.blocks.Name(block)
	return n == nameArm || n == nameStickyArm
}

// armNeighbour removes an arm whose piston is gone and breaks a piston whose arm is
// gone while it is out, as one breaks with the other in BDS.
func (t *Ticker) armNeighbour(r *world.Region, pos cube.Pos, block uint32) {
	dir := pistonFaces[t.facingOf(block)]
	if t.isArm(block) {
		behind := pos.Side(dir.Opposite())
		if b, ok := r.Block(behind, 0); ok && t.kind(b) != kindPiston && r.SetBlock(pos, 0, t.blocks.Air()) {
			r.UpdateNeighbours(pos)
		}
		return
	}
	be, ok := r.BlockEntity(pos)
	if !ok {
		return
	}
	e, ok := be.(*pistonEntity)
	if !ok || e.state != pistonExpanded {
		return
	}
	if b, ok := r.Block(pos.Side(dir), 0); ok && !t.isArm(b) {
		t.fall(r, pos, block)
	}
}
