package player

import (
	"math"
	"math/rand/v2"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
	"github.com/helix-bedrock/world/mcdb"
)

// BDS 1.26.60's lightning bolt (LightningBolt: init 0xBD525A0, normalTick 0xBD52630,
// Actor::thunderHit 0xB3AAC20).
const (
	nameLightningBolt = "minecraft:lightning_bolt"
	// boltLife is the ticks a strike lasts; a bolt flashes again 1 to boltFlashes times.
	boltLife    = 2
	boltFlashes = 3
	// boltFlashWait and boltFlashSpread: a flash comes boltFlashWait to boltFlashWait +
	// boltFlashSpread - 1 ticks after the last one ends.
	boltFlashWait   = 3
	boltFlashSpread = 12
	// boltSpreadFires is how many more fires a bolt tries around where it struck.
	boltSpreadFires = 4
	// boltDamage, boltBurnSeconds: what a bolt does to what it hits.
	boltDamage      = 5
	boltBurnSeconds = 8
	// boltReach is how far around it the bolt hits, boltReachUp how much further up.
	boltReach   = 3
	boltReachUp = 6
	// boltFireDifficulty is the difficulty from which a bolt sets fires (normal).
	boltFireDifficulty = 2
	// DamageLightning is a lightning strike.
	DamageLightning DamageCause = 24
)

// LightningBolt is a lightning strike: BDS's LightningBolt. Runs: owner of its entity.
type LightningBolt struct {
	s      *Host
	entity *world.Entity
	life   int32
	// flashes are the flashes still to come.
	flashes int32
	hit     bool
	// VisualOnly bolts set no fire and hurt nothing.
	VisualOnly bool
}

// Entity returns the bolt's entity.
func (b *LightningBolt) Entity() *world.Entity { return b.entity }

// Strike makes a lightning bolt at pos. Runs: owner of the region.
func (s *Host) Strike(r *world.Region, pos mgl32.Vec3) *LightningBolt {
	b := &LightningBolt{s: s, life: boltLife, flashes: int32(rand.IntN(boltFlashes)) + 1} //nolint:gosec // weather, not a secret
	b.entity = r.Dimension().AddEntity(world.EntityConfig{
		UniqueID: itemUniqueIDBase | rand.Int64N(itemUniqueIDBase), RuntimeID: s.runtimeIDs.Add(1), Pos: pos, //nolint:gosec // an entity ID, not a secret
		Owner: b, Spawn: b.spawnPackets, Move: b.movePacket, Input: b.tick,
	})
	return b
}

// StrikePlayer makes a lightning bolt at p's feet. Runs: anywhere; the bolt is made on p's
// region.
func (s *Host) StrikePlayer(p *Player) bool {
	return p.Run(func(p *Player) {
		if r := p.entity.Region(); r != nil {
			at := p.entity.Position()
			s.Strike(r, mgl32.Vec3{at[0], at[1] - eyeHeight, at[2]})
		}
	})
}

// MobsIn appends the feet of the players in the chunk at pos to dst, what lightning may
// strike. Runs: owner of the region.
func (s *Host) MobsIn(r *world.Region, pos mcdb.ChunkPos, dst []mgl32.Vec3) []mgl32.Vec3 {
	x0, z0 := float32(pos[0])*16, float32(pos[1])*16
	for _, e := range r.AppendEntitiesIn(nil, x0, z0, x0+16, z0+16) {
		p, ok := e.Owner().(*Player)
		if !ok || p.dead() {
			continue
		}
		at := e.Position()
		if at[0] < x0 || at[0] >= x0+16 || at[2] < z0 || at[2] >= z0+16 {
			continue
		}
		dst = append(dst, mgl32.Vec3{at[0], at[1] - eyeHeight, at[2]})
	}
	return dst
}

func (b *LightningBolt) spawnPackets(e *world.Entity) []packet.Packet {
	return []packet.Packet{&packet.AddActor{
		EntityUniqueID: e.UniqueID(), EntityRuntimeID: e.RuntimeID(), EntityType: nameLightningBolt,
		Position: e.Position(), EntityMetadata: protocol.EntityMetadata{protocol.EntityDataKeyScale: float32(1)},
	}}
}

func (b *LightningBolt) movePacket(e *world.Entity) packet.Packet {
	return &packet.MoveActorAbsolute{EntityRuntimeID: e.RuntimeID(), Position: e.Position()}
}

// tick is the bolt's tick: on the first, thunder and fire; it hits what is around it once,
// flashes again a few times and goes.
func (b *LightningBolt) tick(e *world.Entity) {
	r := e.Region()
	if r == nil {
		return
	}
	at := e.Position()
	pos := cube.Pos{int(math.Floor(float64(at[0]))), int(math.Floor(float64(at[1]))), int(math.Floor(float64(at[2])))}
	life := b.life
	if life == boltLife {
		for _, name := range [...]string{packet.SoundEventThunder, packet.SoundEventExplode} {
			playSound(r, pos, soundEvent{name: name, pos: at, extra: -1, entity: nameLightningBolt}, nil)
		}
		b.s.boltHitBlocks(r, pos)
		if b.setsFire() {
			b.fire(r, pos)
			for range boltSpreadFires {
				b.fire(r, pos.Add(cube.Pos{rand.IntN(3) - 1, rand.IntN(3) - 1, rand.IntN(3) - 1})) //nolint:gosec // weather, not a secret
			}
		}
	}
	b.life = life - 1
	if life <= 0 {
		if b.flashes == 0 {
			e.Remove()
			return
		}
		if life <= -boltFlashWait-int32(rand.IntN(boltFlashSpread)) { //nolint:gosec // weather, not a secret
			b.flashes--
			b.life = 1
			if b.setsFire() {
				b.fire(r, pos)
			}
		}
	}
	if b.life >= 0 && !b.VisualOnly && !b.hit {
		b.hitAround(r, at)
		b.hit = true
	}
}

// setsFire is BDS's check: not visual only, normal difficulty or harder, and dofiretick.
func (b *LightningBolt) setsFire() bool {
	f := b.s.fire
	return !b.VisualOnly && f != nil && b.s.level.Difficulty >= boltFireDifficulty && f.Rules().DoFireTick
}

// fire puts fire at pos if it is air there and fire may stay.
func (b *LightningBolt) fire(r *world.Region, pos cube.Pos) {
	blocks := b.s.reg.Blocks
	if old, ok := r.Block(pos, 0); !ok || old != blocks.Air() || !b.s.fire.MayPlace(r, pos) {
		return
	}
	if f, ok := blocks.Block(nameFire); ok && r.SetBlock(pos, 0, f) {
		r.UpdateNeighbours(pos)
	}
}

// hitAround is each living entity in reach struck (Actor::thunderHit): 5 damage, and set on fire for
// 8 seconds unless it does not burn.
func (b *LightningBolt) hitAround(r *world.Region, at mgl32.Vec3) {
	lo := mgl32.Vec3{at[0] - boltReach, at[1] - boltReach, at[2] - boltReach}
	hi := mgl32.Vec3{at[0] + boltReach, at[1] + boltReachUp + boltReach, at[2] + boltReach}
	for _, e := range r.AppendEntitiesIn(nil, lo[0], lo[2], hi[0], hi[2]) {
		l, ok := e.Owner().(Living)
		if !ok || l.Dead() {
			continue
		}
		elo, ehi, _, _ := b.s.EntityBox(e)
		if ehi[0] < lo[0] || elo[0] > hi[0] || ehi[1] < lo[1] || elo[1] > hi[1] || ehi[2] < lo[2] || elo[2] > hi[2] {
			continue
		}
		l.TakeDamage(boltDamage, DamageSource{Cause: DamageLightning})
		if !l.FireImmune() {
			l.SetOnFire(boltBurnSeconds)
		}
	}
}
