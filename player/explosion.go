package player

import (
	"fmt"
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/loot"
	"github.com/helix-bedrock/helix/registry"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/nbt"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// BDS 1.26.60's numbers for an explosion.
const (
	// explosionGrid is the side of the cube of rays; only its faces send one.
	explosionGrid = 16
	// A ray starts at radius*(rand*explosionRandom+explosionBase).
	explosionRandom = 0.6
	explosionBase   = 0.7
	// A ray moves explosionStep at a time, and fades by explosionFade each step.
	explosionStep = 0.3
	explosionFade = 0.22500001
	// A block takes (resistance+resistanceAdd)*resistanceShare from a ray.
	resistanceAdd   = 0.3
	resistanceShare = 0.3
	// sourceCap is what a source's maximum resistance is divided by.
	sourceCap = 5
	// pullBackSteps is how far back along its speed a source inside a block looks for a
	// way out; minSpeed is the length under which a direction counts as none.
	pullBackSteps = 5
	minSpeed      = 0.0001
	// damageMultiplier is in BDS's damage: ((impact^2+impact)/2*7*2r+1)*scale.
	damageMultiplier = 7
	// noDamageScale is the damage scale under which an explosion hurts nothing.
	noDamageScale = 0.00001
	// blastReach makes the box searched for entities one block wider than twice the radius.
	blastReach = 1
	// clipLimit is how many blocks a ray of exposure walks before it gives up.
	clipLimit = 200
	// smokeOdds is one in how many broken blocks the client gets smoke at.
	smokeOdds = 8
	// fireOdds is one in how many air blocks in the blast catch fire.
	fireOdds = 3
	// dropStackStep and dropStackEvery let drops of one explosion pile up further the
	// more kinds there are.
	dropStackStep  = 16
	dropStackEvery = 6
	// noResistance is the default maximum resistance.
	noResistance = math.MaxFloat32

	nameFire = "minecraft:fire"
	// netherStar is the only item BDS's item table does not let explosions destroy.
	netherStar = "minecraft:nether_star"

	blastTagRadius = "radius"
	blastTagX      = "originX"
	blastTagY      = "originY"
	blastTagZ      = "originZ"
	blastTagSize   = "size"
	blastTagPos    = "pos%d%c"
	// nbtWrapper is the compound's two bytes in front of a generic event's tags.
	nbtWrapper = 2

	// The damage causes of explosions.
	DamageBlockExplosion  DamageCause = causeBlockBlast
	DamageEntityExplosion DamageCause = causeEntityBlast
)

// Explosion is an explosion to run, BDS's Explosion. Make one with [NewExplosion], change
// what you like and run it with [Host.Explode]. Vanilla's primed TNT is only one user.
type Explosion struct {
	// Pos is where it starts; Radius how strong it is.
	Pos    mgl32.Vec3
	Radius float32
	// Source is what explodes, if anything. It is not hurt, and distances to entities are
	// measured from it. Attacker is the player behind it.
	Source   *world.Entity
	Attacker *Player
	// Fire sets one in three air blocks in the blast on fire. BreaksBlocks breaks blocks.
	Fire, BreaksBlocks bool
	// AllowUnderwater lets it break blocks and hurt from under water, and lets rays pass
	// through liquids. IgnoreResistance has blocks take nothing from a ray.
	AllowUnderwater, IgnoreResistance bool
	// Underwater says whether it counts as under water; nil works it out from Source or
	// the liquid at Pos.
	Underwater *bool
	// MaxResistance caps what a block takes from a ray when there is a Source, at a fifth.
	MaxResistance float32
	// DamageScale and KnockbackScale scale what entities get.
	DamageScale, KnockbackScale float32
	// FixedDamage, when HasFixedDamage is set, replaces the worked out damage.
	FixedDamage    int32
	HasFixedDamage bool
	// Cause is the damage cause without an Attacker.
	Cause DamageCause
	// Particle is the level event played and Sound the sound.
	Particle int32
	Sound    string
	// DropRadius is what drops decay with, one in this many survive; 0 is Radius.
	DropRadius float32
}

// NewExplosion returns BDS's defaults for an explosion that breaks blocks.
func NewExplosion(pos mgl32.Vec3, radius float32) *Explosion {
	return &Explosion{
		Pos: pos, Radius: radius, BreaksBlocks: true, MaxResistance: noResistance, DamageScale: 1, KnockbackScale: 1,
		Cause: DamageBlockExplosion, Particle: packet.LevelEventParticlesExplosion, Sound: packet.SoundEventExplode,
	}
}

// ExplodeFunc runs an explosion, BDS's Explosion::explode. False if nothing happened.
type ExplodeFunc func(s *Host, r *world.Region, e *Explosion) bool

// ExplosionEvent fires once an explosion knows which blocks it reaches, before anything
// is hurt or broken. Blocks may be changed; Cancel stops the explosion. Runs: owner of
// the explosion's region.
type ExplosionEvent struct {
	Cancellable
	Region    *world.Region
	Explosion *Explosion
	Blocks    []cube.Pos
}

// OnExplosion listens to explosions.
func OnExplosion(s *Setup, fn func(e *ExplosionEvent), p Priority) error {
	return addListener(s, &s.h.game.explosionEv, fn, p)
}

// Exploded is a block type's answer to an explosion taking it (BDS's onExploded), called
// after its drops are worked out and before it is removed. Vanilla's TNT lights here.
type Exploded interface {
	Exploded(s *Host, r *world.Region, pos cube.Pos, block uint32, e *Explosion)
}

// Blastable is an entity an explosion reaches: the [world.Entity]'s Owner.
type Blastable interface {
	// BlastBox is the entity's box, false if explosions pass it by.
	BlastBox() (lo, hi mgl32.Vec3, ok bool)
	// BlastHit is the damage it takes. Called even for 0 damage.
	BlastHit(damage float32, src DamageSource)
	// BlastPush adds to its speed.
	BlastPush(push mgl32.Vec3)
}

// Explode runs an explosion. Runs: owner of the region.
func (s *Host) Explode(r *world.Region, e *Explosion) bool {
	return s.game.sys.Explode(s, r, e)
}

// vanillaExplode is BDS 1.26.60's Explosion::explode.
func vanillaExplode(s *Host, r *world.Region, e *Explosion) bool {
	if e.Radius == 0 {
		return false
	}
	underwater := s.explosionUnderwater(r, e)
	if e.Source != nil && s.solidAt(r, floorPos(e.Pos)) {
		s.pullBack(r, e, &underwater)
	}
	var blocks []cube.Pos
	if e.BreaksBlocks && (!underwater || e.AllowUnderwater) {
		blocks = s.blastRays(r, e)
	}
	if s.game.explosionEv.Has() {
		ev := ExplosionEvent{Region: r, Explosion: e, Blocks: blocks}
		s.game.explosionEv.fire(s.log, &ev)
		if ev.Cancelled() {
			return false
		}
		blocks = ev.Blocks
	}
	s.blastEntities(r, e, underwater)
	at := floorPos(e.Pos)
	if e.Radius > 0 {
		r.Broadcast(at, &packet.LevelSoundEvent{SoundType: e.Sound, Position: e.Pos, ExtraData: -1}, nil)
		levelEvent(r, at, e.Pos, e.Particle, int32(e.Radius))
	}
	smoke := s.takeBlocks(r, e, blocks)
	if pk := blockExplosionPacket(e, smoke); pk != nil {
		r.Broadcast(at, pk, nil)
	}
	if e.Fire {
		s.lightBlast(r, blocks)
	}
	return true
}

// explosionUnderwater is whether the explosion counts as under water.
func (s *Host) explosionUnderwater(r *world.Region, e *Explosion) bool {
	if e.Underwater != nil {
		return *e.Underwater
	}
	at := e.Pos
	if e.Source != nil {
		at = e.Source.Position()
	}
	return s.waterAt(r, floorPos(at))
}

// waterAt reports water in either layer of pos.
func (s *Host) waterAt(r *world.Region, pos cube.Pos) bool {
	m, _ := liquidAt(s.reg, r, pos)
	return m == registry.MaterialWater
}

// solidAt reports a block of solid material at pos.
func (s *Host) solidAt(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && s.reg.Blocks.Properties(b).SolidMaterial
}

// pullBack moves an explosion whose source is inside a block back along the source's
// speed, up to pullBackSteps blocks, or leaves it where it was if none of them is free.
func (s *Host) pullBack(r *world.Region, e *Explosion, underwater *bool) {
	tnt, ok := e.Source.Owner().(*PrimedTNT)
	if !ok {
		return
	}
	length := tnt.vel.Len()
	steps := min(int(math.Ceil(float64(length))), pullBackSteps)
	if steps <= 0 || length < minSpeed {
		return
	}
	dir := tnt.vel.Mul(1 / length)
	at := e.Pos
	for range steps {
		at = at.Sub(dir)
		if !s.solidAt(r, floorPos(at)) {
			e.Pos = at
			*underwater = s.waterAt(r, floorPos(at))
			return
		}
	}
}

// blastRays sends BDS's rays out from the cube's faces and returns the blocks they
// reach, in the order they found them.
func (s *Host) blastRays(r *world.Region, e *Explosion) []cube.Pos {
	seen := map[cube.Pos]struct{}{}
	var out []cube.Pos
	for x := range explosionGrid {
		for y := range explosionGrid {
			for z := range explosionGrid {
				if !onGridFace(x) && !onGridFace(y) && !onGridFace(z) {
					continue
				}
				dir := mgl32.Vec3{gridUnit(x), gridUnit(y), gridUnit(z)}
				out = s.blastRay(r, e, dir.Mul(1/dir.Len()), seen, out)
			}
		}
	}
	return out
}

func onGridFace(n int) bool { return n == 0 || n == explosionGrid-1 }

// gridUnit is a place in the grid, as -1 to 1.
func gridUnit(n int) float32 {
	f := float32(n) / (explosionGrid - 1)
	return f + f - 1
}

// blastRay walks one ray until it fades.
func (s *Host) blastRay(r *world.Region, e *Explosion, dir mgl32.Vec3, seen map[cube.Pos]struct{}, out []cube.Pos) []cube.Pos {
	blocks := s.reg.Blocks
	left := (rand.Float32()*explosionRandom + explosionBase) * e.Radius //nolint:gosec // an explosion, not a secret
	at := e.Pos
	for left > 0 {
		pos := floorPos(at)
		b, ok := r.Block(pos, 0)
		if !ok {
			break
		}
		if !e.AllowUnderwater {
			if extra, has := r.Block(pos, 1); has && blocks.Properties(extra).Material == registry.MaterialWater {
				left = 0
			}
		}
		props := blocks.Properties(b)
		add := e.Fire
		liquid := props.Material == registry.MaterialWater || props.Material == registry.MaterialLava
		if b != blocks.Air() && !(e.AllowUnderwater && liquid) {
			var resistance float32
			if !e.IgnoreResistance {
				resistance = props.ExplosionResistance
			}
			if e.Source != nil {
				resistance = min(resistance, e.MaxResistance/sourceCap)
			}
			left -= (resistance + resistanceAdd) * resistanceShare
			add = left > 0
		}
		if _, dup := seen[pos]; add && !dup {
			seen[pos] = struct{}{}
			out = append(out, pos)
		}
		at = at.Add(dir.Mul(explosionStep))
		left -= explosionFade
	}
	return out
}

// blastEntities hurts and pushes what is near, the source excepted.
func (s *Host) blastEntities(r *world.Region, e *Explosion, underwater bool) {
	reach := 2*e.Radius + blastReach
	x0, x1 := blastEdge(e.Pos[0]-reach), blastEdge(e.Pos[0]+reach)
	y0, y1 := blastEdge(e.Pos[1]-reach), blastEdge(e.Pos[1]+reach)
	z0, z1 := blastEdge(e.Pos[2]-reach), blastEdge(e.Pos[2]+reach)
	center := e.Pos
	if e.Source != nil {
		center = e.Source.Position()
	}
	for _, o := range r.AppendEntitiesIn(nil, x0, z0, x1, z1) {
		target, ok := o.Owner().(Blastable)
		if !ok || o == e.Source {
			continue
		}
		lo, hi, ok := target.BlastBox()
		if !ok || hi[0] < x0 || lo[0] > x1 || hi[1] < y0 || lo[1] > y1 || hi[2] < z0 || lo[2] > z1 {
			continue
		}
		s.blastEntity(r, e, o, target, center, lo, hi, underwater)
	}
}

func blastEdge(f float32) float32 { return float32(math.Floor(float64(f))) }

// blastEntity is one entity's damage and knockback. Players are measured from their
// position, anything else from the near side of its box.
func (s *Host) blastEntity(r *world.Region, e *Explosion, o *world.Entity, target Blastable, center, lo, hi mgl32.Vec3, underwater bool) {
	pos := o.Position()
	dist := mgl32.Vec3{pos[0] - center[0], lo[1] - center[1], pos[2] - center[2]}.Len()
	if _, player := o.Owner().(*Player); !player {
		dist = max(0, dist-(hi[0]-lo[0])/2)
	}
	reach := 2 * e.Radius
	if dist/reach > 1 {
		return
	}
	impact := (1 - dist/reach) * s.exposure(r, e.Pos, lo, hi) * e.KnockbackScale
	if float32(math.Abs(float64(e.DamageScale))) > noDamageScale {
		damage := float32(e.FixedDamage)
		if !e.HasFixedDamage {
			damage = ((impact*impact+impact)/2*damageMultiplier*reach + 1) * e.DamageScale
		}
		if e.Source != nil && underwater && !e.AllowUnderwater {
			damage = 0
		}
		src := DamageSource{Cause: e.Cause}
		if e.Attacker != nil {
			src = DamageSource{Cause: DamageEntityExplosion, Attacker: e.Attacker}
		}
		target.BlastHit(damage, src)
	}
	dir := pos.Sub(e.Pos)
	if l := dir.Len(); l >= minSpeed && impact != 0 {
		target.BlastPush(dir.Mul(impact / l))
	}
}

// exposure is the share of rays from a grid in the box that reach the explosion.
func (s *Host) exposure(r *world.Region, at, lo, hi mgl32.Vec3) float32 {
	if lo[0] <= at[0] && at[0] <= hi[0] && lo[1] <= at[1] && at[1] <= hi[1] && lo[2] <= at[2] && at[2] <= hi[2] {
		return 1
	}
	size := hi.Sub(lo)
	stepX, stepY, stepZ := exposureStep(size[0]), exposureStep(size[1]), exposureStep(size[2])
	offX, offZ := exposureOffset(stepX), exposureOffset(stepZ)
	var clear, total int
	for fx := float32(0); fx <= 1; fx += stepX {
		for fy := float32(0); fy <= 1; fy += stepY {
			for fz := float32(0); fz <= 1; fz += stepZ {
				from := mgl32.Vec3{size[0]*fx + lo[0] + offX, size[1]*fy + lo[1], size[2]*fz + lo[2] + offZ}
				if !s.rayBlocked(r, from, at) {
					clear++
				}
				total++
			}
		}
	}
	return float32(clear) / float32(total)
}

func exposureStep(size float32) float32 { return 1 / (size + size + 1) }

func exposureOffset(step float32) float32 {
	return (1 - float32(int32(1/step))*step) / 2
}

// rayBlocked walks the blocks from one point to another and reports whether a collision
// box is in the way. Blocks not loaded do not block.
func (s *Host) rayBlocked(r *world.Region, from, to mgl32.Vec3) bool {
	var pos, end, step [3]int
	var next, delta, a, d [3]float64
	for n := range 3 {
		a[n], d[n] = float64(from[n]), float64(to[n]-from[n])
		pos[n], end[n] = itemFloor(from[n]), itemFloor(to[n])
		switch {
		case d[n] > 0:
			step[n], delta[n], next[n] = 1, 1/d[n], (float64(pos[n]+1)-a[n])/d[n]
		case d[n] < 0:
			step[n], delta[n], next[n] = -1, -1/d[n], (float64(pos[n])-a[n])/d[n]
		default:
			next[n] = math.Inf(1)
		}
	}
	for range clipLimit {
		b, ok := r.Block(cube.Pos{pos[0], pos[1], pos[2]}, 0)
		if !ok {
			return false
		}
		for _, box := range s.boxesAt(r, cube.Pos{pos[0], pos[1], pos[2]}, b) {
			if segmentHits(a, d, pos, box) {
				return true
			}
		}
		if pos == end {
			return false
		}
		n := 0
		if next[1] < next[n] {
			n = 1
		}
		if next[2] < next[n] {
			n = 2
		}
		if next[n] > 1 {
			return false
		}
		pos[n] += step[n]
		next[n] += delta[n]
	}
	return false
}

// segmentHits reports whether the segment a+t*d, t in 0..1, passes through a block's box.
func segmentHits(a, d [3]float64, pos [3]int, box registry.Box) bool {
	t0, t1 := 0.0, 1.0
	for n := range 3 {
		lo, hi := float64(pos[n])+float64(box[n]), float64(pos[n])+float64(box[n+3])
		if d[n] == 0 {
			if a[n] < lo || a[n] > hi {
				return false
			}
			continue
		}
		enter, leave := (lo-a[n])/d[n], (hi-a[n])/d[n]
		if enter > leave {
			enter, leave = leave, enter
		}
		t0, t1 = max(t0, enter), min(t1, leave)
		if t0 > t1 {
			return false
		}
	}
	return true
}

// takeBlocks breaks the blocks of the blast and drops what they leave. It returns the
// places the client gets smoke at.
func (s *Host) takeBlocks(r *world.Region, e *Explosion, blocks []cube.Pos) []cube.Pos {
	if !e.BreaksBlocks {
		return nil
	}
	reg := s.reg.Blocks
	air := reg.Air()
	radius := e.Radius
	if e.DropRadius != 0 {
		radius = e.DropRadius
	}
	var smoke []cube.Pos
	var drops []blastDrop
	for _, pos := range blocks {
		if rand.IntN(smokeOdds) == 0 { //nolint:gosec // smoke, not a secret
			smoke = append(smoke, pos)
		}
		b, ok := r.Block(pos, 0)
		if !ok || b == air {
			continue
		}
		drops = s.blastDrops(b, radius, pos, drops)
		if extra, has := r.Block(pos, 1); has && extra != air {
			drops = s.blastDrops(extra, radius, pos, drops)
			r.SetBlock(pos, 1, air)
		}
		if hit, isHit := s.game.blocks[reg.Name(b)].(Exploded); isHit {
			hit.Exploded(s, r, pos, b, e)
		}
		r.SetBlock(pos, 0, air)
		r.UpdateNeighbours(pos)
	}
	if s.level.DoTileDrops {
		d := r.Dimension()
		for _, drop := range drops {
			thrown := NewBlockDrop(drop.pos, drop.stack)
			s.spawnItem(d, thrown.Stack, thrown.Pos, thrown.Velocity, world.Rotation{}, thrown.PickupDelay, 0)
		}
	}
	return smoke
}

// blastDrop is a stack an explosion is about to drop, and the block it came from.
type blastDrop struct {
	stack ItemStack
	pos   cube.Pos
}

// blastDrops adds what a block leaves to the explosion's drops: equal items pile up, as
// high as more kinds of drops allow.
func (s *Host) blastDrops(block uint32, radius float32, pos cube.Pos, drops []blastDrop) []blastDrop {
	items, _ := s.game.sys.Drops(s, block, &loot.Context{Explosion: true, Radius: radius})
	for _, it := range items {
		limit := min(int(it.maxStack(s.reg)), dropStackStep*(len(drops)/dropStackEvery)+dropStackStep)
		for i := range drops {
			if it.Count == 0 {
				break
			}
			if !drops[i].stack.stacksWith(it) {
				continue
			}
			moved := min(max(limit-int(drops[i].stack.Count), 0), int(it.Count))
			drops[i].stack.Count += uint8(moved) //nolint:gosec // at most a stack
			it.Count -= uint8(moved)             //nolint:gosec // at most a stack
		}
		if it.Count > 0 {
			drops = append(drops, blastDrop{stack: it, pos: pos})
		}
	}
	return drops
}

// lightBlast sets one in fireOdds of the air blocks in the blast on fire.
func (s *Host) lightBlast(r *world.Region, blocks []cube.Pos) {
	reg := s.reg.Blocks
	fire, ok := reg.Block(nameFire)
	if !ok {
		return
	}
	for _, pos := range blocks {
		if b, has := r.Block(pos, 0); has && b == reg.Air() && rand.IntN(fireOdds) == 0 { //nolint:gosec // fire, not a secret
			r.SetBlock(pos, 0, fire)
			r.UpdateNeighbours(pos)
		}
	}
}

// blockExplosionPacket is what shows smoke at the broken blocks: tags with no compound
// around them.
func blockExplosionPacket(e *Explosion, smoke []cube.Pos) packet.Packet {
	tags := map[string]any{
		blastTagRadius: e.Radius, blastTagX: e.Pos[0], blastTagY: e.Pos[1], blastTagZ: e.Pos[2], blastTagSize: int32(len(smoke)), //nolint:gosec // a few blocks
	}
	for i, pos := range smoke {
		tags[fmt.Sprintf(blastTagPos, i, 'x')] = float32(pos[0])
		tags[fmt.Sprintf(blastTagPos, i, 'y')] = float32(pos[1])
		tags[fmt.Sprintf(blastTagPos, i, 'z')] = float32(pos[2])
	}
	data, err := nbt.MarshalEncoding(tags, nbt.NetworkLittleEndian)
	if err != nil || len(data) <= nbtWrapper {
		return nil
	}
	return &packet.LevelEventGeneric{EventID: packet.LevelEventParticlesBlockExplosion, SerialisedEventData: data[nbtWrapper : len(data)-1]}
}

// BlastBox is the player's body, unless spectating. Runs: the player's region.
func (p *Player) BlastBox() (lo, hi mgl32.Vec3, ok bool) {
	mode := p.gameMode
	if mode == GameModeDefault {
		mode = p.s.level.GameMode
	}
	if p.entity == nil || p.dead() || mode == GameModeSpectator {
		return lo, hi, false
	}
	at := p.entity.Position()
	lo = mgl32.Vec3{at[0] - playerHalfWidth, at[1] - eyeHeight, at[2] - playerHalfWidth}
	return lo, mgl32.Vec3{at[0] + playerHalfWidth, lo[1] + playerHeight, at[2] + playerHalfWidth}, true
}

// BlastHit hurts the player.
func (p *Player) BlastHit(damage float32, src DamageSource) { p.s.game.sys.Hurt(p, damage, src) }

// BlastPush adds to the player's speed: nothing at full knockback resistance.
func (p *Player) BlastPush(push mgl32.Vec3) {
	if p.attributes.get(attrKnockbackResistance) >= 1 {
		return
	}
	p.move.velocity = p.move.velocity.Add(push)
	p.send(&packet.SetActorMotion{EntityRuntimeID: p.runtimeID, Velocity: p.move.velocity, Tick: p.entity.Tick()})
	p.flush()
}

// BlastBox is the item's box.
func (i *ItemActor) BlastBox() (lo, hi mgl32.Vec3, ok bool) {
	if i.taken || i.entity == nil {
		return lo, hi, false
	}
	at := i.entity.Position()
	half := mgl32.Vec3{itemHalfSize, itemHalfSize, itemHalfSize}
	return at.Sub(half), at.Add(half), true
}

// BlastHit takes from the item's health, and it is gone at none left. An item that is
// not explodable takes nothing.
func (i *ItemActor) BlastHit(damage float32, _ DamageSource) {
	if i.stack.Name == netherStar {
		return
	}
	i.health -= int32(damage)
	if i.health <= 0 {
		i.taken = true
		i.entity.Remove()
	}
}

// BlastPush adds to the item's speed.
func (i *ItemActor) BlastPush(push mgl32.Vec3) {
	if i.taken {
		return
	}
	i.vel = i.vel.Add(push)
	pushed(i.entity, i.vel)
}

// BlastBox is the orb's box.
func (o *XPOrb) BlastBox() (lo, hi mgl32.Vec3, ok bool) {
	if o.taken || o.body.entity == nil {
		return lo, hi, false
	}
	at := o.body.entity.Position()
	half := mgl32.Vec3{itemHalfSize, itemHalfSize, itemHalfSize}
	return at.Sub(half), at.Add(half), true
}

// BlastHit takes from the orb's health, for what BDS's orbs take damage from: of
// explosions, only those an entity caused.
func (o *XPOrb) BlastHit(damage float32, src DamageSource) {
	if src.Cause != DamageEntityExplosion {
		return
	}
	o.health -= int32(damage)
	if o.health <= 0 {
		o.taken = true
		o.body.entity.Remove()
	}
}

// BlastPush adds to the orb's speed.
func (o *XPOrb) BlastPush(push mgl32.Vec3) {
	if o.taken {
		return
	}
	o.body.vel = o.body.vel.Add(push)
	pushed(o.body.entity, o.body.vel)
}

// pushed tells those who see an entity its new speed.
func pushed(e *world.Entity, vel mgl32.Vec3) {
	e.Send(&packet.SetActorMotion{EntityRuntimeID: e.RuntimeID(), Velocity: vel, Tick: e.Tick()})
}
