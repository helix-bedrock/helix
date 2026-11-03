package player

import (
	"math"
	"slices"

	"github.com/helix-bedrock/helix/direction"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// Numbers of BDS 1.26.60's shield.
const (
	itemShield = "minecraft:shield"
	// shieldCategory is the name of the shield's item cooldown.
	shieldCategory = "shield"
	// tagAxe marks the items whose hit puts a shield on cooldown.
	tagAxe = "minecraft:is_axe"
	// shieldRaiseTicks is how long a shield must be up before it blocks.
	shieldRaiseTicks = 5
	// shieldCooldownTicks is how long an axe's hit keeps a shield down.
	shieldCooldownTicks = 100
	// shieldPush and shieldLift are how hard a blocked hit pushes the attacker.
	shieldPush = 0.6
	shieldLift = 0.6
	// causesBlocked is the bit set of the damage causes a shield stops: BDS also lets
	// it stop contact damage from one kind of actor, which nothing deals yet.
	causesBlocked     = 0x840C0C
	causeBlockedLimit = 24
	// swingTicks is how long an arm swing lasts. A swing keeps a shield down.
	swingTicks = 6
)

// shieldState is the player's shield on its region, as BDS keeps it in the player's
// actor flags, the shield's stack and the player's tick.
type shieldState struct {
	// clock counts the player's ticks; raisedAt is when the shield in raisedID went
	// up, 0 while it is down, and seenAt what the last tick found there.
	clock, raisedAt, seenAt int64
	raisedID                int32
	// cooldown is the ticks left before the shield may go up again.
	cooldown int32
	// swing is the ticks of the arm swing so far; swinging while it runs.
	swing    int32
	swinging bool
	// The actor flags: up, going up or down, blocked a hit, and broke blocking it.
	blocking, transition, blocked, broke bool
}

// PlayerBlockEvent fires when a player's shield is about to stop a hit. Cancel lets
// the hit through.
type PlayerBlockEvent struct {
	Cancellable
	Player *Player
	Damage float32
	Source DamageSource
}

// OnPlayerBlock listens to players blocking with a shield.
func OnPlayerBlock(s *Setup, fn func(e *PlayerBlockEvent), p Priority) error {
	return addListener(s, &s.h.game.blockEv, fn, p)
}

// ShieldUpFunc reports whether the player is holding its shield up.
type ShieldUpFunc func(p *Player) bool

// ShieldBlockFunc stops a hit with the player's shield; false lets it through.
type ShieldBlockFunc func(p *Player, damage float32, src DamageSource) bool

// Blocking reports whether the player's shield would stop a hit from in front: it is
// up and has been for long enough. Runs: the player's region.
func (p *Player) Blocking() bool {
	sh := &p.shield
	return sh.blocking && p.heldShield() != nil && sh.clock-sh.raisedAt >= int64(p.Tuning(TuneShieldRaise))
}

// ShieldCooldown returns the ticks before the player's shield may go up again. Runs:
// the player's region.
func (p *Player) ShieldCooldown() int32 { return p.shield.cooldown }

// SetShieldCooldown keeps the player's shield down for ticks and tells its client.
// Runs: the player's region.
func (p *Player) SetShieldCooldown(ticks int32) {
	p.shield.cooldown = ticks
	p.send(&packet.ClientStartItemCooldown{Category: shieldCategory, Duration: ticks})
}

// heldShield is the shield the player would block with: the offhand first, then the
// held item, as BDS looks for it.
func (p *Player) heldShield() *ItemStack {
	inv := &p.inventory
	if inv.offhand.Name == itemShield {
		return &inv.offhand
	}
	if s := &inv.main[inv.held]; s.Name == itemShield {
		return s
	}
	return nil
}

// vanillaShieldUp is BDS's rule for raising a shield: sneaking, not swimming, not
// using an item, not swinging, and off cooldown. BDS's sneaking does not count in
// scaffolding, and a rider raises it without sneaking; Helix has neither yet. As in
// BDS the rule holds without a shield too, and the actor flag goes up.
func vanillaShieldUp(p *Player) bool {
	if p.heldShield() != nil && p.shield.cooldown > 0 {
		return false
	}
	return p.Moving(MoveSneaking) && !p.Moving(MoveSwimming) && !p.using.active && !p.shield.swinging
}

// tickShield moves the shield on, once a tick, and shows those who see the player
// what changed. Runs: the player's region.
func (p *Player) tickShield() {
	if p.dead() {
		return
	}
	sh := &p.shield
	before := p.shieldFlags()
	sh.clock++
	if sh.cooldown > 0 {
		sh.cooldown--
	}
	p.tickSwing()
	sh.blocked, sh.broke = false, false
	held := p.heldShield()
	if held != nil {
		sh.transition = sh.raisedAt != sh.seenAt
		sh.seenAt = sh.raisedAt
	}
	up := p.s.game.sys.ShieldUp(p)
	if up && !sh.blocking {
		sh.transition = true
	}
	sh.blocking = up
	switch {
	case !up || held == nil:
		sh.raisedAt, sh.raisedID = 0, 0
	case sh.raisedID != held.netID:
		sh.raisedAt, sh.raisedID = sh.clock, held.netID
	}
	if p.shieldFlags() != before {
		p.sendActorFlags()
	}
}

// startSwing is BDS's swing: a swing past its first half starts over.
func (p *Player) startSwing() {
	sh := &p.shield
	if sh.swinging && sh.swing >= 0 && sh.swing < p.swingLength()/2 {
		return
	}
	sh.swing, sh.swinging = -1, true
}

// tickSwing counts the arm swing on.
func (p *Player) tickSwing() {
	sh := &p.shield
	if !sh.swinging {
		return
	}
	sh.swing++
	if sh.swing >= p.swingLength() {
		sh.swing, sh.swinging = 0, false
	}
}

// swingLength is BDS's length of a swing: Haste and Conduit Power shorten it by their
// level, Mining Fatigue lengthens it by twice its level.
func (p *Player) swingLength() int32 {
	var haste int32
	if a, ok := p.Effect(EffectHaste); ok {
		haste = a + 1
	}
	if a, ok := p.Effect(EffectConduitPower); ok {
		haste = max(haste, a+1)
	}
	if haste > 0 {
		return swingTicks - haste
	}
	if a, ok := p.Effect(EffectMiningFatigue); ok {
		return swingTicks + 2*a + 2
	}
	return swingTicks
}

// shieldFlags are the actor flags of the player's shield. They are past the first 64
// flags, in the second set.
func (p *Player) shieldFlags() int64 {
	sh := &p.shield
	var f int64
	if sh.blocking {
		f |= 1 << (protocol.EntityDataFlagBlocking - flagsPerSet)
	}
	if sh.transition {
		f |= 1 << (protocol.EntityDataFlagTransitionBlocking - flagsPerSet)
	}
	if sh.blocked {
		f |= 1 << (protocol.EntityDataFlagBlockedUsingShield - flagsPerSet)
	}
	if sh.broke {
		f |= 1 << (protocol.EntityDataFlagBlockedUsingDamagedShield - flagsPerSet)
	}
	return f
}

// lowerShield takes the shield down at once: a player that died holds none up.
func (p *Player) lowerShield() {
	before := p.shieldFlags()
	sh := &p.shield
	sh.blocking, sh.transition, sh.blocked, sh.broke = false, false, false, false
	sh.raisedAt, sh.seenAt, sh.raisedID, sh.swing, sh.swinging = 0, 0, 0, 0, false
	if before != 0 {
		p.sendActorFlags()
	}
}

// blocks is BDS's test of a hit against a shield: the shield is up, the cause is one
// it stops, and what caused the hit is in front of the player, by where it looks on
// the flat. A cause with no actor behind it is always stopped.
func (p *Player) blocks(src DamageSource) bool {
	if !p.Blocking() || src.Cause < 0 || src.Cause >= causeBlockedLimit || causesBlocked>>src.Cause&1 == 0 {
		return false
	}
	if src.Attacker == nil || src.Attacker.entity == nil || p.entity == nil {
		return true
	}
	rot := p.entity.Rotation()
	yaw, flat := direction.Units(rot.Yaw), direction.Cos(direction.Units(rot.Pitch))
	to := src.Attacker.entity.Position().Sub(p.entity.Position())
	return to[0]*-direction.Sin(yaw)*flat+to[2]*direction.Cos(yaw)*flat > 0
}

// vanillaShieldBlock is BDS's block: the event, the sound, the attacker's push, an
// axe's cooldown, then the shield's wear of the whole damage plus one (none in
// creative) and the flags that make it shake.
func vanillaShieldBlock(p *Player, damage float32, src DamageSource) bool {
	if !p.blocks(src) {
		return false
	}
	g := p.s.game
	if g.blockEv.Has() {
		e := PlayerBlockEvent{Player: p, Damage: damage, Source: src}
		g.blockEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
	}
	r := p.entity.Region()
	if r == nil {
		return false
	}
	pos, at := p.entity.Position(), p.blockPos()
	r.Broadcast(at, &packet.LevelSoundEvent{SoundType: packet.SoundEventShieldBlock, Position: pos, ExtraData: -1}, nil)
	if a := src.Attacker; a != nil && src.Cause == DamageEntityAttack {
		a.pushOffShield(p)
		if a.holdsAxe() {
			r.Broadcast(at, &packet.LevelSoundEvent{SoundType: packet.SoundEventBreak, Position: pos, ExtraData: -1}, nil)
			p.SetShieldCooldown(int32(p.Tuning(TuneShieldCooldown)))
		}
	}
	p.shield.blocked = true
	if s := p.heldShield(); s != nil && !p.creative() {
		offhand := s == &p.inventory.offhand
		changed, broke := p.wearStack(s, int32(math.Floor(float64(damage)))+1)
		p.shield.broke = broke
		if broke && !offhand {
			r.Broadcast(at, &packet.LevelSoundEvent{SoundType: packet.SoundEventBreak, Position: pos, ExtraData: -1}, nil)
		}
		if changed {
			if offhand {
				p.send(&packet.InventorySlot{WindowID: protocol.WindowIDOffHand, NewItem: p.inventory.offhand.network(p.s.reg)})
			} else {
				p.sendHeld()
			}
			p.sendEquipment()
		}
	}
	// The client's own shield shakes from these flags too, unlike its other flags,
	// which it sets itself.
	r.Broadcast(at, &packet.SetActorData{
		EntityRuntimeID: p.runtimeID,
		EntityMetadata:  protocol.EntityMetadata{protocol.EntityDataKeyFlags: p.actorFlags(), protocol.EntityDataKeyFlagsTwo: p.actorFlagsTwo()},
		Tick:            p.entity.Tick(),
	}, nil)
	return true
}

// holdsAxe reports whether the player's held item is an axe.
func (p *Player) holdsAxe() bool {
	held := p.inventory.Held()
	if held.Empty() {
		return false
	}
	t, ok := p.s.reg.Items.ByName(held.Name)
	return ok && slices.Contains(t.Tags, tagAxe)
}

// pushOffShield is BDS's push on an attacker whose hit was blocked: away from the
// blocker, as a knockback of its own. The client moves itself, so it is told.
func (p *Player) pushOffShield(blocker *Player) {
	if p.entity == nil || blocker.entity == nil {
		return
	}
	resist := max(1-p.attributes.get(attrKnockbackResistance), 0) * p.Tuning(TuneKnockbackTakenScale)
	away := p.entity.Position().Sub(blocker.entity.Position())
	vel := p.knockbackStep(p.move.velocity, away[0], away[2], p.Tuning(TuneShieldPush)*resist, p.Tuning(TuneShieldLift)*resist)
	p.move.velocity = vel
	p.send(&packet.SetActorMotion{EntityRuntimeID: p.runtimeID, Velocity: vel, Tick: p.entity.Tick()})
	p.flush()
}
