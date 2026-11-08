package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// BDS 1.26.60's elytra.
const (
	nameElytra = "minecraft:elytra"
	// armorChest is the chest piece's armour slot.
	armorChest = 1

	// elytraWearTicks: every so many gliding ticks the elytra takes elytraWear, on the
	// tick whose count is elytraWearTick past a whole number of them (0xBFE3F20).
	elytraWearTicks = 20
	elytraWearTick  = 19
	elytraWear      = 1
	// glideStartRise is how far the player may have risen in its last movement and still
	// start gliding (0xBCFB8B0).
	glideStartRise = 0.15

	// A crash into a wall while gliding hurts (horizontal speed lost) * crashScale -
	// crashFree; above crashBigDamage it makes the big fall sound (0x78715D0, 0x78731F0).
	crashScale     = 10
	crashFree      = 3
	crashBigDamage = 4
)

// The damage causes of gliding and fireworks, as BDS's ActorDamageCause numbers them.
const (
	DamageFlyIntoWall DamageCause = 21
	DamageFireworks   DamageCause = 23
)

// glideState is the player's gliding on its region.
type glideState struct {
	// ticks is BDS's FallFlyTicksComponent: ticks spent gliding, 0 when not.
	ticks int32
}

// StartGlideFunc decides whether the player may start gliding when its client asks.
type StartGlideFunc func(p *Player) bool

// GlideTickFunc runs once a tick for the player: its gliding time, the elytra's wear and
// the reasons gliding stops.
type GlideTickFunc func(p *Player)

// GlideCrashFunc is a gliding player running into a wall: before and after are its
// horizontal speed before and after the collision.
type GlideCrashFunc func(p *Player, before, after float32)

// GlideEvent is a player starting (Start) or stopping gliding. Cancel it to keep the
// player as it was. Runs: the player's region.
type GlideEvent struct {
	Cancellable
	Player *Player
	Start  bool
}

// OnGlide listens to players starting and stopping gliding.
func OnGlide(s *Setup, fn func(e *GlideEvent), p Priority) error {
	return addListener(s, &s.h.game.glideEv, fn, p)
}

// Gliding reports whether the player is gliding. Runs: the player's region.
func (p *Player) Gliding() bool { return p.Moving(MoveGliding) }

// GlideTicks returns how many ticks the player has been gliding. Runs: the player's region.
func (p *Player) GlideTicks() int32 { return p.glide.ticks }

// SetGliding starts or stops the player's gliding, as the server decides it, and tells the
// player and those who see it. Runs: the player's region.
func (p *Player) SetGliding(on bool) {
	if p.Gliding() == on {
		return
	}
	if p.s.game.glideEv.Has() {
		e := GlideEvent{Player: p, Start: on}
		p.s.game.glideEv.fire(p.s.log, &e)
		if e.Cancelled() {
			p.sendOwnFlags()
			return
		}
	}
	p.SetMoving(MoveGliding, on)
	p.sendOwnFlags()
}

// sendOwnFlags tells the player its own actor flags, after the server decided otherwise
// than its client did.
func (p *Player) sendOwnFlags() {
	if p.entity == nil {
		return
	}
	p.send(actorDataAt(p.runtimeID, protocol.EntityMetadata{protocol.EntityDataKeyFlags: p.actorFlags(), protocol.EntityDataKeyFlagsTwo: p.actorFlagsTwo()}, p.entity.Tick()))
}

// elytraFlies is BDS's isFlyEnabled (0xC922F90): an elytra flies until its damage
// reaches one below its max.
func (p *Player) elytraFlies(s ItemStack) bool {
	if s.Empty() || s.Name != nameElytra {
		return false
	}
	d, ok := p.s.game.durability[s.Name]
	if !ok || d.MaxDamage <= 0 {
		return true
	}
	return itemDamage(s) < int32(d.MaxDamage)-1
}

// chestElytra is the elytra the player wears, if it flies.
func (p *Player) chestElytra() bool { return p.elytraFlies(p.inventory.armor[armorChest]) }

// vanillaStartGlide is BDS's tryStartGliding (0xBCFB8B0) with the elytra its start intent
// asks for (ArmorFlyEnabled): not on the ground, not riding, not in water, not rising
// glideStartRise or more in the last movement, and an elytra that flies in the chest slot.
func vanillaStartGlide(p *Player) bool {
	if p.move.onGround || p.vehicle != nil || p.move.inWater || p.move.velocity[1] >= glideStartRise || p.Gliding() {
		return false
	}
	return p.chestElytra()
}

// vanillaGlideTick is BDS's gliding time (FallFlyTicks, 0x785DE00), the elytra's wear
// (0xBFE3F20: one damage every 20 gliding ticks, through hurtAndBreak so Unbreaking
// counts) and its stop intent for what Helix knows: no elytra that flies, riding, in
// water or on the ground ends gliding. The client stops on its own for the rest (jumping
// again, landing in powder snow).
func vanillaGlideTick(p *Player) {
	if !p.Gliding() {
		p.glide.ticks = 0
		return
	}
	if !p.chestElytra() || p.vehicle != nil || p.move.inWater || p.move.onGround {
		p.glide.ticks = 0
		p.SetGliding(false)
		return
	}
	if p.glide.ticks%elytraWearTicks == elytraWearTick && !p.creative() {
		if p.hurtStack(&p.inventory.armor[armorChest], elytraWear) {
			p.sendArmour()
			p.showArmor()
		}
	}
	p.glide.ticks++
}

// vanillaGlideCrash is BDS's gliding collision damage (0x78715D0, 0x78731F0): the
// horizontal speed lost, times 10, less 3, as fly-into-wall damage, with the big fall
// sound above 4 and the small one below. BDS takes the speed its glide simulation asked
// for against what the move allowed; Helix takes the client's movement, so before is the
// speed of the last movement and after the speed of this one.
func vanillaGlideCrash(p *Player, before, after float32) {
	damage := (before-after)*crashScale - crashFree
	if damage <= 0 {
		return
	}
	if r := p.entity.Region(); r != nil {
		sound := packet.SoundEventFallSmall
		if damage > crashBigDamage {
			sound = packet.SoundEventFallBig
		}
		playSound(r, p.blockPos(), soundEvent{name: sound, pos: p.entity.Position(), extra: -1, entity: "minecraft:player"}, nil)
	}
	p.s.game.sys.Hurt(p, damage, DamageSource{Cause: DamageFlyIntoWall})
}

// flatSpeed is a movement's horizontal length.
func flatSpeed(v mgl32.Vec3) float32 {
	return float32(math.Sqrt(float64(v[0]*v[0] + v[2]*v[2])))
}
