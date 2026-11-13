package circuit

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/redstone"
	"github.com/helix-bedrock/world/cube"
)

// Rails, from BDS 1.26.60's BaseRailBlock, PoweredRailBlock, ActivatorRailBlock and
// DetectorRailBlock.
const (
	nameRail          = "minecraft:rail"
	nameGoldenRail    = "minecraft:golden_rail"
	nameDetectorRail  = "minecraft:detector_rail"
	nameActivatorRail = "minecraft:activator_rail"

	stateRailDirection = "rail_direction"
	stateRailData      = "rail_data_bit"

	// railNone is no direction worked out yet.
	railNone int32 = -1
	// railFirstCurve is the first direction that is a curve: only plain rails have them.
	railFirstCurve = 6
	// railDetectorInset is how far in from the sides of the block it looks, and from the top.
	railDetectorInset = 0.2
	// railGroupGolden and railGroupActivator keep the two kinds of powered rail apart.
	railGroupActivator int32 = 0
	railGroupGolden    int32 = 1
)

// IsRail reports whether the block called name is a rail: BDS's rail tag.
func IsRail(name string) bool {
	return name == nameRail || name == nameGoldenRail || name == nameDetectorRail || name == nameActivatorRail
}

// CartBoxFunc says whether an entity is a minecart, and what a comparator reads of it
// (-1 if it holds no items).
type CartBoxFunc func(e *world.Entity) (signal int32, ok bool)

// SetCartBox sets how detector rails see carts. Call it before any player joins.
func (t *Ticker) SetCartBox(fn CartBoxFunc) { t.cartBox = fn }

// railAt is BDS's BaseRailBlock::Rail: a rail and the places it wants to connect to.
type railAt struct {
	t        *Ticker
	c        *regionCircuit
	pos      cube.Pos
	block    uint32
	straight bool
	links    []cube.Pos
}

// isRailAt reports whether the block at pos is a rail.
func (t *Ticker) isRailAt(c *regionCircuit, pos cube.Pos) bool {
	b, ok := c.r.Block(pos, 0)
	return ok && IsRail(t.blocks.Name(b))
}

func (t *Ticker) newRail(c *regionCircuit, pos cube.Pos) *railAt {
	b, ok := c.r.Block(pos, 0)
	if !ok {
		return nil
	}
	name := t.blocks.Name(b)
	if !IsRail(name) {
		return nil
	}
	a := &railAt{t: t, c: c, pos: pos, block: b, straight: name != nameRail}
	a.link(intState(t.blocks.States(b), stateRailDirection))
	return a
}

// railNear is BDS's getRail: a rail at pos, or above it, or below it.
func (a *railAt) railNear(pos cube.Pos) *railAt {
	if a.t.isRailAt(a.c, pos) {
		return a.t.newRail(a.c, pos)
	}
	if up := pos.Side(cube.FaceUp); a.t.isRailAt(a.c, up) {
		return a.t.newRail(a.c, up)
	}
	if down := pos.Side(cube.FaceDown); a.t.isRailAt(a.c, down) {
		return a.t.newRail(a.c, down)
	}
	return nil
}

// link is BDS's updateConnections. Directions 2 and 3 really do both connect the same
// way: the code shares the case.
func (a *railAt) link(dir int32) {
	x, y, z := a.pos[0], a.pos[1], a.pos[2]
	north, south := cube.Pos{x, y, z - 1}, cube.Pos{x, y, z + 1}
	west, east := cube.Pos{x - 1, y, z}, cube.Pos{x + 1, y, z}
	switch dir {
	case 0:
		a.links = []cube.Pos{north, south}
	case 1:
		a.links = []cube.Pos{west, east}
	case 2, 3:
		a.links = []cube.Pos{{x - 1, y + 1, z}, east}
	case 4:
		a.links = []cube.Pos{{x, y + 1, z - 1}, south}
	case 5:
		a.links = []cube.Pos{north, {x, y + 1, z + 1}}
	case 6:
		a.links = []cube.Pos{east, south}
	case 7:
		a.links = []cube.Pos{west, south}
	case 8:
		a.links = []cube.Pos{west, north}
	case 9:
		a.links = []cube.Pos{east, north}
	default:
		a.links = nil
	}
}

// connectsTo reports whether a wants to connect to o. Only x and z count.
func (a *railAt) connectsTo(o *railAt) bool {
	for _, l := range a.links {
		if l[0] == o.pos[0] && l[2] == o.pos[2] {
			return true
		}
	}
	return false
}

// dropSoftLinks is BDS's removeSoftConnections: links to rails that do not want to
// connect back go, and the rest are moved to where the rail really is.
func (a *railAt) dropSoftLinks() {
	kept := a.links[:0]
	for _, l := range a.links {
		if o := a.railNear(l); o != nil && o.connectsTo(a) {
			kept = append(kept, o.pos)
		}
	}
	a.links = kept
}

// wantsNeighbour is BDS's hasNeighborRail.
func (a *railAt) wantsNeighbour(pos cube.Pos) bool {
	o := a.railNear(pos)
	if o == nil {
		return false
	}
	o.dropSoftLinks()
	return o.connectsTo(a) || len(o.links) != 2
}

// curve is the curve that four connections make when they make exactly one.
func curve(n, s, w, e bool) int32 {
	switch {
	case s && e && !n && !w:
		return 6
	case s && w && !n && !e:
		return 7
	case n && w && !s && !e:
		return 8
	case n && e && !s && !w:
		return 9
	}
	return railNone
}

// slope turns a flat direction into a slope if a rail is one up at either end of it.
func (a *railAt) slope(dir int32) int32 {
	x, y, z := a.pos[0], a.pos[1]+1, a.pos[2]
	switch dir {
	case 0:
		if a.t.isRailAt(a.c, cube.Pos{x, y, z - 1}) {
			dir = 4
		}
		if a.t.isRailAt(a.c, cube.Pos{x, y, z + 1}) {
			dir = 5
		}
	case 1:
		if a.t.isRailAt(a.c, cube.Pos{x + 1, y, z}) {
			dir = 2
		}
		if a.t.isRailAt(a.c, cube.Pos{x - 1, y, z}) {
			dir = 3
		}
	}
	return dir
}

// place is BDS's Rail::place: work out the direction from the rails around, set it, and
// have the rails around connect back.
func (a *railAt) place(strength int32, force bool) {
	x, y, z := a.pos[0], a.pos[1], a.pos[2]
	n := a.wantsNeighbour(cube.Pos{x, y, z - 1})
	s := a.wantsNeighbour(cube.Pos{x, y, z + 1})
	w := a.wantsNeighbour(cube.Pos{x - 1, y, z})
	e := a.wantsNeighbour(cube.Pos{x + 1, y, z})
	dir := railNone
	switch {
	case (n || s) && !w && !e:
		dir = 0
	case (w || e) && !n && !s:
		dir = 1
	}
	if !a.straight {
		if c := curve(n, s, w, e); c != railNone {
			dir = c
		}
	}
	if dir == railNone {
		if n || s {
			dir = 0
		}
		if w || e {
			dir = 1
		}
		if !a.straight {
			dir = powerCurve(dir, strength, n, s, w, e)
		}
	}
	dir = a.slope(dir)
	if dir == railNone {
		dir = intState(a.t.blocks.States(a.block), stateRailDirection)
	}
	a.link(dir)
	block := a.t.with(a.block, stateRailDirection, dir)
	powered := a.straight && strength >= 1
	if powered {
		block = a.t.with(block, stateRailData, flag(true))
	}
	if !force && !powered {
		return
	}
	a.set(block)
	links := append([]cube.Pos(nil), a.links...)
	for _, l := range links {
		o := a.railNear(l)
		if o == nil {
			continue
		}
		o.dropSoftLinks()
		if o.connectsTo(a) || len(o.links) != 2 {
			o.connectTo(a)
		}
	}
}

// powerCurve is the choice between curves when more than one fits: power at a junction
// decides which way it goes.
func powerCurve(dir, strength int32, n, s, w, e bool) int32 {
	if strength >= 1 {
		if s && e {
			dir = 6
		}
		if w && s {
			dir = 7
		}
		if e && n {
			dir = 9
		}
		if n && w {
			dir = 8
		}
		return dir
	}
	if n && w {
		dir = 8
	}
	if e && n {
		dir = 9
	}
	if w && s {
		dir = 7
	}
	if s && e {
		dir = 6
	}
	return dir
}

// connectTo is BDS's Rail::connectTo: this rail gains a link and takes the shape that gives.
func (a *railAt) connectTo(o *railAt) {
	a.links = append(a.links, o.pos)
	var n, s, w, e bool
	for _, l := range a.links {
		switch {
		case l[0] == a.pos[0] && l[2] == a.pos[2]-1:
			n = true
		case l[0] == a.pos[0] && l[2] == a.pos[2]+1:
			s = true
		case l[0] == a.pos[0]-1 && l[2] == a.pos[2]:
			w = true
		case l[0] == a.pos[0]+1 && l[2] == a.pos[2]:
			e = true
		}
	}
	dir := railNone
	if n || s {
		dir = 0
	}
	if w || e {
		dir = 1
	}
	if !a.straight {
		if c := curve(n, s, w, e); c != railNone {
			dir = c
		}
	}
	dir = a.slope(dir)
	if dir == railNone {
		return
	}
	a.link(dir)
	a.set(a.t.with(a.block, stateRailDirection, dir))
}

// set writes the rail's state and makes its component, as BDS's _createCircuitComponent:
// a powered or activator rail's component runs along it, so a new direction needs a new one.
func (a *railAt) set(block uint32) {
	c := a.c
	locked := c.sys.Locked()
	c.sys.Lock(true)
	changed := a.block != block && c.r.SetBlock(a.pos, 0, block)
	c.sys.Lock(locked)
	a.block = block
	if changed {
		c.r.UpdateNeighbours(a.pos)
	}
	if !locked {
		a.t.railComponent(c, a.pos, block, false)
	}
}

// railFace is the way along a rail its component looks.
func railFace(dir int32) redstone.Face {
	if dir >= 1 && dir <= 3 {
		return redstone.West
	}
	return redstone.South
}

// railComponent makes the component a rail has for its state: a powered or activator
// rail's runs along it, a curved plain rail takes power to choose, and any other plain
// rail has none.
func (t *Ticker) railComponent(c *regionCircuit, pos cube.Pos, block uint32, load bool) {
	p := toPos(pos)
	states := t.blocks.States(block)
	dir := intState(states, stateRailDirection)
	switch t.blocks.Name(block) {
	case nameGoldenRail, nameActivatorRail:
		old := c.sys.Component(p, redstone.KindRail)
		if old != nil && old.Direction() == railFace(dir) {
			c.sys.Invalidate(p)
			return
		}
		if old != nil {
			c.sys.Remove(p)
		}
		comp := c.sys.Create(p, redstone.KindRail, railFace(dir))
		if comp == nil {
			return
		}
		group := railGroupActivator
		if t.blocks.Name(block) == nameGoldenRail {
			group = railGroupGolden
		}
		comp.SetRailGroup(group)
		if bitState(states, stateRailData) {
			comp.SetStrength(fullPower)
		}
		c.sys.Invalidate(p)
	case nameRail:
		if dir < railFirstCurve {
			if c.sys.At(p) != nil {
				c.sys.Remove(p)
			}
			return
		}
		if comp := c.sys.Create(p, redstone.KindConsumer, redstone.Down); comp != nil && !load {
			c.sys.Invalidate(p)
		}
	case nameDetectorRail:
		if comp := c.sys.Create(p, redstone.KindProducer, redstone.NoFace); comp != nil {
			if bitState(states, stateRailData) {
				comp.SetStrength(fullPower)
			}
			comp.SetAttach(true)
		}
	}
}

// railSurvives is BDS's rail canSurvive: the block below holds it, and a slope also needs
// the block it climbs to hold it.
func (t *Ticker) railSurvives(r *world.Region, pos cube.Pos, block uint32) bool {
	holds := func(at cube.Pos) bool {
		b, ok := r.Block(at, 0)
		return ok && t.attach.Supports(b, cube.FaceUp)
	}
	if !holds(pos.Side(cube.FaceDown)) {
		return false
	}
	switch intState(t.blocks.States(block), stateRailDirection) {
	case 2:
		return holds(pos.Side(cube.FaceEast))
	case 3:
		return holds(pos.Side(cube.FaceWest))
	case 4:
		return holds(pos.Side(cube.FaceNorth))
	case 5:
		return holds(pos.Side(cube.FaceSouth))
	}
	return true
}

// railPlaced runs for a rail put down in the world.
func (t *Ticker) railPlaced(c *regionCircuit, pos cube.Pos) {
	if a := t.newRail(c, pos); a != nil {
		a.place(int32(c.sys.Strength(toPos(pos))), true)
	}
}

// railPower is a rail's component changing.
func (t *Ticker) railPower(c *regionCircuit, pos cube.Pos, block uint32, strength int16, first bool) {
	states := t.blocks.States(block)
	switch t.blocks.Name(block) {
	case nameGoldenRail:
		t.setByCircuit(c, pos, t.with(block, stateRailData, flag(strength > 0)))
	case nameActivatorRail:
		// BDS flips the bit and sets it if powered, so losing power turns it off.
		t.setByCircuit(c, pos, t.with(block, stateRailData, flag(strength > 0 || !bitState(states, stateRailData))))
	case nameRail:
		if !first {
			if a := t.newRail(c, pos); a != nil {
				locked := c.sys.Locked()
				c.sys.Lock(true)
				a.place(int32(strength), true)
				c.sys.Lock(locked)
			}
		}
	}
}

// CartOnRail is a minecart inside a block: a detector rail that is up looks for carts.
// Runs: owner of the region.
func (t *Ticker) CartOnRail(r *world.Region, pos cube.Pos) {
	b, ok := r.Block(pos, 0)
	if !ok || t.blocks.Name(b) != nameDetectorRail || bitState(t.blocks.States(b), stateRailData) {
		return
	}
	t.detectorCheck(t.circuitOf(r), pos, b)
}

// detectorCarts lists the carts in the detector rail's box.
func (t *Ticker) detectorCarts(c *regionCircuit, pos cube.Pos, fn func(e *world.Entity, signal int32)) {
	if t.cartBox == nil || t.entityBox == nil {
		return
	}
	x, y, z := float32(pos[0]), float32(pos[1]), float32(pos[2])
	lo := mgl32.Vec3{x + railDetectorInset, y, z + railDetectorInset}
	hi := mgl32.Vec3{x + 1 - railDetectorInset, y + 1 - railDetectorInset, z + 1 - railDetectorInset}
	c.found = c.r.AppendEntitiesIn(c.found[:0], x-1, z-1, x+2, z+2)
	for _, e := range c.found {
		signal, isCart := t.cartBox(e)
		if !isCart {
			continue
		}
		bLo, bHi, _, ok := t.entityBox(e)
		if ok && bHi[0] > lo[0] && bLo[0] < hi[0] && bHi[1] > lo[1] && bLo[1] < hi[1] && bHi[2] > lo[2] && bLo[2] < hi[2] {
			fn(e, signal)
		}
	}
}

// detectorCheck is BDS's _checkPressed and _handlePressed.
func (t *Ticker) detectorCheck(c *regionCircuit, pos cube.Pos, block uint32) {
	pressed := false
	t.detectorCarts(c, pos, func(*world.Entity, int32) { pressed = true })
	was := bitState(t.blocks.States(block), stateRailData)
	if pressed {
		now := block
		if !was {
			now = t.with(block, stateRailData, flag(true))
			t.setByCircuit(c, pos, now)
		}
		c.r.ScheduleTick(pos, now, t.settings.Get().Rail.DetectorDelay, 0)
		if was {
			return
		}
	} else {
		if !was {
			return
		}
		t.setByCircuit(c, pos, t.with(block, stateRailData, flag(false)))
	}
	if comp := c.sys.Component(toPos(pos), redstone.KindProducer); comp != nil {
		comp.SetStrength(onOff(pressed))
	}
}

// detectorSignal is BDS's getComparatorSignal for a detector rail: the first cart in its
// box that holds items.
func (t *Ticker) detectorSignal(c *regionCircuit, pos cube.Pos) int32 {
	best := int32(-1)
	t.detectorCarts(c, pos, func(_ *world.Entity, signal int32) {
		if best < 0 && signal >= 0 {
			best = signal
		}
	})
	return max(best, 0)
}

// railKind sorts the rail blocks.
func railKind(name string) (kind, bool) {
	switch name {
	case nameGoldenRail, nameActivatorRail:
		return kindPoweredRail, true
	case nameRail:
		return kindPlainRail, true
	case nameDetectorRail:
		return kindDetectorRail, true
	}
	return kindNone, false
}

// isRailKind reports whether k is a rail.
func isRailKind(k kind) bool {
	return k == kindPoweredRail || k == kindPlainRail || k == kindDetectorRail
}
