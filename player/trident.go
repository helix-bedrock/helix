package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Tridents, from BDS 1.26.60: TridentItem's release (sub_C663550) throws a thrown_trident, or
// with Riptide launches the player; ThrownTrident's tick (sub_BC998C0) and its Loyalty return
// (sub_BC99330); its void (sub_BC99300). Its flight, damage, sticking and hit sounds come
// from the thrown_trident definition through the projectile code.
//
// Not done: the riptide spin's own damage and wear (BDS's movement simulation runs the launch,
// which Helix leaves to the client), Channeling on lightning rods, and the hit_ground sound.
// Channeling's thunder test reads the thunder level above 0 and the target's column to the
// sky; BDS's exact test was not read. stop_on_hurt is taken as the trident stopping dead.

// The trident's names and numbers.
const (
	nameTrident       = "minecraft:trident"
	nameThrownTrident = "minecraft:thrown_trident"
	// tridentMinUse is how long it must be held back before it throws or launches.
	tridentMinUse  = 10
	enchantLoyalty = int16(31)
	// tridentReturnAfter is how long it stays stuck before Loyalty brings it back.
	tridentReturnAfter = 5
	// Each tick of the return: the height closes by dy * tridentClimb * level; the speed
	// keeps tridentKeep and gains tridentPull * level toward the owner.
	tridentClimb = 0.015
	tridentKeep  = 0.95
	tridentPull  = 0.05
	// tridentKey is where BDS saves the thrown trident's item.
	tridentKey = "Trident"
)

// riptideSounds are the launch sounds by Riptide level: 1, 2, 3 and up.
var riptideSounds = [...]string{packet.SoundEventTridentRiptide1, packet.SoundEventTridentRiptide2, packet.SoundEventTridentRiptide3}

// tridentState is what a thrown trident carries beyond a projectile.
type tridentState struct {
	// loyalty, impaling and channeling are the trident's enchantment levels.
	loyalty, impaling, channeling int32
	// dealt is set once it hurt something; returning once it flies back.
	dealt, returning bool
}

// startTrident starts holding a trident back.
func (p *Player) startTrident(held ItemStack) {
	u := &p.using
	slot := p.inventory.held
	if u.active && u.slot == slot && u.item.Name == held.Name {
		return
	}
	*u = usingState{active: true, slot: slot, item: held, left: bowUseTicks, bow: true}
	p.sendActorFlags()
}

// releaseTrident is TridentItem's release after used ticks.
func (p *Player) releaseTrident(held ItemStack, used int32) {
	r := p.entity.Region()
	if r == nil || used < tridentMinUse {
		return
	}
	if level := heldEnchant(held, EnchantRiptide); level > 0 {
		if p.Wet() && p.vehicle == nil {
			sound := riptideSounds[min(int(level), len(riptideSounds))-1]
			playSound(r, p.blockPos(), soundEvent{name: sound, pos: p.AttachPos(AttachWeapon), extra: -1, entity: "minecraft:player"}, nil)
		}
		return
	}
	playSound(r, p.blockPos(), soundEvent{name: packet.SoundEventTridentThrow, pos: p.AttachPos(AttachWeapon), extra: -1, entity: "minecraft:player"}, nil)
	spec, ok := p.s.projectileSpec(nameThrownTrident, "")
	if !ok {
		return
	}
	p.hurtHeld(1)
	thrown := p.inventory.Held()
	if thrown.Name != nameTrident {
		// It broke.
		return
	}
	eyes, rot := p.entity.Position(), p.entity.Rotation()
	t := p.s.SpawnProjectile(r.Dimension(), spec, projectileEyes(spec, eyes, rot), lookDirection(rot), spec.Power, p)
	t.item = thrown
	t.item.Count = 1
	t.trident = &tridentState{
		loyalty: heldEnchant(thrown, enchantLoyalty), impaling: heldEnchant(thrown, EnchantImpaling), channeling: heldEnchant(thrown, EnchantChanneling),
	}
	if p.creative() {
		t.pickup = arrowPickupCreative
		return
	}
	inv := &p.inventory
	s := &inv.main[inv.held]
	if s.Count--; s.Count <= 0 {
		*s = ItemStack{}
	}
	p.sendHeld()
	p.flush()
}

// tridentHit is what a trident adds to hitting a living thing: Impaling's damage on one in
// water or rain, and Channeling's lightning on one under a thundering sky.
func (t *Projectile) tridentHit(r *world.Region, target Living, at mgl32.Vec3) float32 {
	tr := t.trident
	if tr == nil {
		return 0
	}
	var extra float32
	if tr.impaling > 0 && target.Wet() {
		extra = float32(tr.impaling) * smiteLikeDamage
	}
	if tr.channeling > 0 && t.s.Thunder(r) > 0 && t.s.skyOpen(r, at) {
		t.s.Strike(r, at)
		playSound(r, floorPos(at), soundEvent{name: packet.SoundEventTridentThunder, pos: at, extra: -1, entity: nameThrownTrident}, nil)
	}
	return extra
}

// tridentHurt is a trident that hurt something: it stops, to fall, and Loyalty may call it.
func (t *Projectile) tridentHurt() {
	if t.trident == nil {
		return
	}
	t.trident.dealt = true
	t.vel = mgl32.Vec3{}
}

// skyOpen reports whether nothing is above at, by the height map.
func (s *Host) skyOpen(r *world.Region, at mgl32.Vec3) bool {
	h, ok := r.Height(int(math.Floor(float64(at[0]))), int(math.Floor(float64(at[2]))))
	return ok && int(math.Floor(float64(at[1]))) >= h
}

// tridentReturns is ThrownTrident's tick of Loyalty: once stuck for 5 ticks, or once it
// hurt something, it flies back to a living owner, and drops as an item if its owner is
// dead. With no owner around it stays. True if it took this tick's movement.
func (t *Projectile) tridentReturns(r *world.Region, e *world.Entity) bool {
	tr := t.trident
	if tr == nil || tr.loyalty <= 0 {
		return false
	}
	if !tr.returning && !tr.dealt && !(t.stuck && t.stuckTicks >= tridentReturnAfter) {
		return false
	}
	o := t.owner
	if o == nil || o.entity == nil || o.entity.Region() != r {
		return false
	}
	if o.dead() {
		if !t.item.Empty() && t.pickup != arrowPickupCreative {
			d := e.Position()
			t.s.spawnItem(r.Dimension(), t.s.freshStack(t.item), d, mgl32.Vec3{}, world.Rotation{}, blockDropDelay, 0)
		}
		t.remove(e)
		return true
	}
	t.returnStep(r, e, o, float32(tr.loyalty))
	return true
}

// returnStep is one tick of flying back to the owner (sub_BC99330), through blocks; the owner
// picks it up on touching it.
func (t *Projectile) returnStep(r *world.Region, e *world.Entity, o *Player, level float32) {
	tr := t.trident
	pos := e.Position()
	target := o.entity.Position()
	d := target.Sub(pos)
	pos[1] += d[1] * tridentClimb * level
	var dir mgl32.Vec3
	if dist := d.Len(); dist >= minPushDist {
		dir = d.Mul(1 / dist)
	}
	t.vel = t.vel.Mul(tridentKeep).Add(dir.Mul(tridentPull * level))
	if !tr.returning {
		tr.returning, t.stuck = true, false
		playSound(r, o.blockPos(), soundEvent{name: packet.SoundEventTridentReturn, pos: target, extra: -1, entity: nameThrownTrident}, nil)
		e.Send(actorData(e.RuntimeID(), protocol.EntityMetadata{protocol.EntityDataKeyFlags: t.flags()}))
	}
	pos = pos.Add(t.vel)
	t.turn()
	e.Apply(world.Movement{Pos: pos, Rot: t.rot, Tick: e.Tick()})
	if lo, hi, ok := o.BlastBox(); ok && overlaps(pos, pos, lo.Sub(arrowTouchReach), hi.Add(arrowTouchReach)) {
		t.pickedUpBy(r, e, o)
	}
}

// saveTrident and loadTrident keep the thrown trident's item as BDS does.
func (t *Projectile) saveTrident(data map[string]any) {
	if t.trident != nil && !t.item.Empty() {
		data[tridentKey] = t.item.nbt(0, false)
	}
}

func (t *Projectile) loadTrident(data map[string]any) {
	if t.spec.ID != nameThrownTrident {
		return
	}
	if m, ok := data[tridentKey].(map[string]any); ok {
		t.item = withID(itemFromNBT(m), t.s.nextStackID)
	}
	t.trident = &tridentState{
		loyalty: heldEnchant(t.item, enchantLoyalty), impaling: heldEnchant(t.item, EnchantImpaling), channeling: heldEnchant(t.item, EnchantChanneling),
	}
}
