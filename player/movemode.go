package player

// The ways a player moves besides walking, falling and jumping, each with its own limits for the
// speed check (speed.go) and the vertical check (vertical.go): Helix's own rules, beyond BDS,
// from the client's physics with room to spare. None of them is left unchecked but spectating,
// being dead and riding (vehicles have their own check, vehicle.go).
//
//   - In water or lava, once the surface is well over the feet: the liquid's drag and a swimming
//     push, Depth Strider moving it toward walking on land; up or down, the drag and a push of
//     0.1, more over a bubble column.
//   - Gliding: the elytra's drag; a dive adds a tenth of the falling speed to the horizontal speed,
//     pulling up turns horizontal speed into rising; a rocket the player holds allows its speed
//     for as long as it burns.
//   - Creative flying: the air's drag and the fly speed and vertical fly speed abilities.
//   - Riptide: the launch adds its strength, and a launch from the ground its lift, for the spin.

const (
	// modeLiquidDrag is what water or lava leaves of a swimmer's speed each tick, and
	// modeLiquidPush the most a swimming stroke adds, a sprint swim's with room to spare.
	modeLiquidDrag, modeLiquidPush = 0.9, 0.035
	// modeDepthStriderMax is the Depth Strider level that moves like walking on land.
	modeDepthStriderMax = 3
	// modeGlideDrag is what the elytra leaves of the horizontal speed each tick; modeGlideDive
	// how much of the falling speed a dive turns into horizontal speed, and modeGlideTurn the
	// most a turn adds.
	modeGlideDrag, modeGlideDive, modeGlideTurn = 0.99, 0.1, 0.01
	// modeRocketSpeed is the most a rocket held while gliding drives the player, in any direction.
	modeRocketSpeed = 2.2
	// modeFlyDrag is what the air leaves of a flyer's speed, modeFlyPush how many times the fly
	// speed ability one tick adds (sprinting doubles it, with room to spare).
	modeFlyDrag, modeFlyPush = 0.91, 3
	// modeRiptideStrength is a Riptide launch's speed per level plus one, over 4 (3 at level 3);
	// modeRiptideLift the rise of a launch from the ground.
	modeRiptideStrength, modeRiptideLift = 3, 1.2

	nameSoulSand     = "minecraft:soul_sand"
	nameSoulSoil     = "minecraft:soul_soil"
	nameBubbleColumn = "minecraft:bubble_column"
)

// moveMode is the way a player moves this input.
type moveMode uint8

const (
	// modeWalk is walking, falling and jumping: on land, in the air and in shallow liquid.
	modeWalk moveMode = iota
	modeLiquid
	modeGlide
	modeFly
	// modeFree is not held: spectating, dead or riding.
	modeFree
)

// moveBoost is what the player holds that drives it: a rocket while gliding. Region tick.
type moveBoost struct {
	// rocketTicks is how many more ticks a rocket the player holds burns.
	rocketTicks int32
}

// moveMode is the way the player moves this input.
func (p *Player) moveMode() moveMode {
	switch {
	case p.spectating() || p.dead() || p.vehicle != nil:
		return modeFree
	case p.Gliding():
		return modeGlide
	case p.abilities.Flag(AbilityFlying) && p.abilities.Flag(AbilityMayFly):
		return modeFly
	case p.deepInLiquid():
		return modeLiquid
	}
	return modeWalk
}

// noteRocket lets the player be driven by a rocket burning for ticks more ticks. Region tick.
func (p *Player) noteRocket(ticks int32) {
	p.move.boost.rocketTicks = max(p.move.boost.rocketTicks, ticks)
}

// tickBoost burns one tick of a held rocket. Region tick.
func (p *Player) tickBoost() {
	if b := &p.move.boost; b.rocketTicks > 0 {
		b.rocketTicks--
	}
}

// riptideLaunch is the speed a Riptide launch of level gives.
func riptideLaunch(level int32) float32 {
	return modeRiptideStrength * float32(level+1) / 4
}

// boostSpeed is the horizontal speed a rocket or a Riptide launch allows the player, which
// carried after the last move.
func (p *Player) boostSpeed(carried float32) float32 {
	var v float32
	if p.move.boost.rocketTicks > 0 {
		v = modeRocketSpeed
	}
	if p.spin.active {
		v = max(v, carried+p.spin.launch)
	}
	return v
}

// liquidPhysics is what water or lava leaves of a swimmer's horizontal speed and the most a tick
// adds: Depth Strider moves both toward walking on land.
func (p *Player) liquidPhysics() (friction, push float32) {
	ds := min(float32(heldEnchant(p.inventory.armor[armorFeet], enchantDepthStrider)), modeDepthStriderMax) / modeDepthStriderMax
	friction = modeLiquidDrag + (speedWalkFriction-modeLiquidDrag)*ds
	push = modeLiquidPush + max(p.walkPush()-modeLiquidPush, 0)*ds
	return friction, push
}

// glidePhysics is what the elytra leaves of the horizontal speed and the most a tick adds: a dive
// turns a tenth of the falling speed into horizontal speed, a turn only turns it (0.01 for
// rounding), and pulling up takes back what it gave as rising.
func (p *Player) glidePhysics() (friction, push float32) {
	// Rising by pulling up on the last move cost horizontal speed: the elytra makes no speed of its
	// own, so a glide cannot hover and keep its speed.
	v := &p.move.vert
	return modeGlideDrag, modeGlideDive*max(-v.vy, 0) + modeGlideTurn - v.glidePull/vertGlideTrade
}

// flyPhysics is what the air leaves of a flyer's horizontal speed and the most a tick adds.
func (p *Player) flyPhysics() (friction, push float32) {
	return modeFlyDrag, max(p.abilities.Speed(AbilityFlySpeed), 0) * modeFlyPush
}
