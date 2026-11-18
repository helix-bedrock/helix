package mob

import "github.com/helix-bedrock/entity/behavior"

// The flying and shooting goals' hooks: circle_around_anchor (behavior.Circler) and
// swoop_attack (behavior.Swooper) of the phantom, fire_at_target (behavior.Firer) of the
// breeze. BDS 1.26.60's BDS's code reads
// BDS's code, which BDS's code sets from the hurt-this-tick
// byte and clears that byte.

// ProjectileHooks is what an engine that spawns a mob's aimed projectiles implements on
// its Hooks.
type ProjectileHooks interface {
	// ShootAlong spawns the projectile definition at from, flying along dir at the
	// projectile's own power, with the mob as its owner. It runs in the mob's Apply.
	ShootAlong(m *Mob, def string, from, dir behavior.Vec3)
}

// MoveControl implements behavior.MoveController: the move control's max turn.
func (m *Mob) MoveControl() (maxTurn float32, ok bool) { return m.ctl.MaxTurn, m.hasMove }

// SetBodyYaw implements behavior.MoveController.
func (m *Mob) SetBodyYaw(yaw float32) { m.bodyYaw = yaw }

// WantedPos implements behavior.Circler: the move control's wanted position.
func (m *Mob) WantedPos() behavior.Vec3 { return m.ctl.Wanted }

// HorizontalCollision implements behavior.Swooper: HorizontalCollisionFlagComponent, a
// collision on x or z in the last move.
func (m *Mob) HorizontalCollision() bool {
	return m.body.HorizontalCollision[0] || m.body.HorizontalCollision[1]
}

// WasHurt implements behavior.Swooper: hurt between the last two base ticks.
func (m *Mob) WasHurt() bool { return m.wasHurt }

// SpawnProjectile implements behavior.Firer: the projectile spawns at Apply.
func (m *Mob) SpawnProjectile(def string, from, dir behavior.Vec3) {
	m.do(op{kind: opSpawnProjectile, name: def, at: from, dir: dir})
}
