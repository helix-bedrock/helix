package player

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// Every kind reads its numbers from its definition.
func TestProjectileSpecs(t *testing.T) {
	p := testPlayer(t)
	snow, ok := p.s.projectileSpec(nameSnowball, "")
	if !ok || snow.Power != 1.5 || snow.Gravity != 0.03 || snow.Inertia != projectileInertia || !snow.RemoveOnHit || snow.DamageOnly != "blaze" || !snow.Knockback {
		t.Errorf("snowball %+v", snow)
	}
	arrow, ok := p.s.projectileSpec(nameArrow, arrowGroupPlayer)
	if !ok || arrow.Power != 3 || arrow.Gravity != 0.05 || !arrow.Stick || arrow.ShakeTicks != 7 || !arrow.DestroyOnHit || !arrow.Bounce || arrow.PowerMultiplier != 2 || !arrow.CeilDamage {
		t.Errorf("player arrow %+v", arrow)
	}
	pearl, ok := p.s.projectileSpec(nameEnderPearl, "")
	if !ok || !pearl.TeleportOwner || pearl.Inertia != 1 || pearl.LiquidInertia != 1 || pearl.Gravity != 0.025 {
		t.Errorf("pearl %+v", pearl)
	}
	if _, ok := p.s.projectileSpec(nameEgg, ""); !ok {
		t.Error("no egg")
	}
}

// A bow's draw follows BDS's sum: too short does nothing, a second is 0.33, a full draw 1.
func TestBowPull(t *testing.T) {
	for _, c := range []struct {
		ticks int32
		want  float32
	}{{0, 0}, {20, 1}, {40, 1}, {10, 0.41666666}} {
		if got := float32(bowPull(c.ticks)); got != c.want {
			t.Errorf("%d ticks: %v, want %v", c.ticks, got, c.want)
		}
	}
	if bowPull(1) >= bowMinPull {
		t.Error("one tick shoots")
	}
}

// An arrow's damage is its speed times two, with Power, rounded up.
func TestProjectileDamage(t *testing.T) {
	p := testPlayer(t)
	arrow, _ := p.s.projectileSpec(nameArrow, arrowGroupPlayer)
	if got := projectileDamage(arrow, 3, 0, false); got != 6 {
		t.Errorf("full speed arrow %v, want 6", got)
	}
	if got := projectileDamage(arrow, 3, 1, false); got != 9 {
		t.Errorf("with Power I %v, want 9", got)
	}
	for range 50 {
		if got := projectileDamage(arrow, 3, 0, true); got < 6 || got > 11 {
			t.Fatalf("critical %v, want 6 to 11", got)
		}
	}
}

// The ray stops at a block's box and says which block.
func TestProjectileHitsTargetBlock(t *testing.T) {
	p, d := machineWorld(t)
	var y int
	at := cube.Pos{3, 0, 8}
	onRegion(d, func(r *world.Region) {
		y = surface(r, p.s.reg.Blocks.Air(), 3, 8)
		at[1] = y
		setNamed(t, p, r, at, "minecraft:target", nil)
	})
	hits := make(chan struct{}, 1)
	p.s.SetCircuitHooks(CircuitHooks{HitTarget: func(_ *world.Region, pos cube.Pos, hit mgl64.Vec3, arrow bool) bool {
		if pos == at && !arrow && hit[2] > 7.9 && hit[2] < 8.1 {
			select {
			case hits <- struct{}{}:
			default:
			}
		}
		return true
	}})
	spec, _ := p.s.projectileSpec(nameSnowball, "")
	var thrown *Projectile
	onRegion(d, func(r *world.Region) {
		thrown = p.s.SpawnProjectile(r.Dimension(), spec, mgl64.Vec3{3.5, float64(y) + 0.5, 4.5}, mgl64.Vec3{0, 0, 1}, spec.Power, nil)
	})
	waitRegion(t, d, func(*world.Region) bool { return thrown.gone })
	select {
	case <-hits:
	default:
		t.Error("the target block was not hit")
	}
}

// An arrow stays in a block, then falls when the block goes.
func TestArrowSticksThenFalls(t *testing.T) {
	p, d := machineWorld(t)
	var y int
	at := cube.Pos{3, 0, 8}
	onRegion(d, func(r *world.Region) {
		y = surface(r, p.s.reg.Blocks.Air(), 3, 8)
		at[1] = y
		setNamed(t, p, r, at, "minecraft:stone", nil)
	})
	spec, _ := p.s.projectileSpec(nameArrow, arrowGroupPlayer)
	var arrow *Projectile
	onRegion(d, func(r *world.Region) {
		arrow = p.s.SpawnProjectile(r.Dimension(), spec, mgl64.Vec3{3.5, float64(y) + 0.5, 4.5}, mgl64.Vec3{0, 0, 1}, spec.Power, nil)
	})
	waitRegion(t, d, func(*world.Region) bool { return arrow.stuck })
	if arrow.stuckAt != at {
		t.Errorf("stuck in %v, want %v", arrow.stuckAt, at)
	}
	onRegion(d, func(r *world.Region) { setNamed(t, p, r, at, "minecraft:air", nil) })
	waitRegion(t, d, func(*world.Region) bool { return !arrow.stuck })
}

// As BDS's tick does, it waits at the edge of the loaded chunks with its speed.
func TestArrowStaysInLoadedChunks(t *testing.T) {
	p, d := machineWorld(t)
	var y int
	onRegion(d, func(r *world.Region) { y = surface(r, p.s.reg.Blocks.Air(), 3, 8) })
	spec, _ := p.s.projectileSpec(nameArrow, arrowGroupPlayer)
	var arrow *Projectile
	onRegion(d, func(r *world.Region) {
		arrow = p.s.SpawnProjectile(r.Dimension(), spec, mgl64.Vec3{3.5, float64(y) + 100, 8.5}, mgl64.Vec3{1, 0, 0}, spec.Power, nil)
	})
	var last mgl64.Vec3
	still := 0
	waitRegion(t, d, func(*world.Region) bool {
		if pos := arrow.Entity().Position(); pos != last {
			last, still = pos, 0
			return false
		}
		still++
		return still > 3 && !arrow.waiting
	})
	if arrow.Velocity()[0] < 1 || arrow.stuck || arrow.gone {
		t.Errorf("waiting at %v: velocity %v, stuck %v, gone %v", last, arrow.Velocity(), arrow.stuck, arrow.gone)
	}
}

// One falling into the void is gone.
func TestArrowGoneBelowWorld(t *testing.T) {
	p, d := machineWorld(t)
	spec, _ := p.s.projectileSpec(nameArrow, arrowGroupPlayer)
	var arrow *Projectile
	onRegion(d, func(r *world.Region) {
		low := float32(r.Dimension().Range()[0]) - voidDepth - 1
		arrow = p.s.SpawnProjectile(r.Dimension(), spec, mgl64.Vec3{3.5, float64(low), 8.5}, mgl64.Vec3{0, -1, 0}, spec.Power, nil)
	})
	waitRegion(t, d, func(*world.Region) bool { return arrow.gone })
}

// Only what is thrown counts as thrown; an arrow needs a bow or a dispenser.
func TestThrowableKinds(t *testing.T) {
	if throwable(nameSnowball) == "" || throwable(nameEgg) == "" || throwable(nameEnderPearl) == "" {
		t.Error("something throwable is not")
	}
	if throwable(nameArrow) != "" || throwable("minecraft:stone") != "" {
		t.Error("something not throwable is")
	}
}

// A new one stays where it starts for its first tick, so players first see it there.
func TestProjectileWaitsItsFirstTick(t *testing.T) {
	p, d := machineWorld(t)
	spec, _ := p.s.projectileSpec(nameArrow, arrowGroupPlayer)
	at := mgl64.Vec3{3.5, 200, 4.5}
	arrow := &Projectile{s: p.s, spec: spec, vel: mgl64.Vec3{0, 0, 3}, waiting: true, separated: true}
	arrow.entity = d.AddEntity(arrow.config(990001, 990001, at, world.Rotation{}))
	onRegion(d, func(*world.Region) {
		if arrow.waiting && arrow.entity.Position() != at {
			t.Errorf("moved to %v while waiting", arrow.entity.Position())
		}
	})
	waitRegion(t, d, func(*world.Region) bool { return arrow.entity.Position() != at })
}

// The definition's offset starts an arrow a little below the eyes.
func TestProjectileStartsBelowEyes(t *testing.T) {
	p := testPlayer(t)
	spec, _ := p.s.projectileSpec(nameArrow, arrowGroupPlayer)
	got := projectileEyes(spec, mgl64.Vec3{0, 10, 0}, world.Rotation{Yaw: 90})
	if math.Abs(got[1]-9.9) > 1e-6 || got[0] != 0 || got[2] != 0 {
		t.Errorf("starts at %v", got)
	}
}

// Near by BDS gives Ticks 0; at its far distance, three quarters of the dropped ticks.
func TestProjectileMoveTicks(t *testing.T) {
	s := ProjectileSpec{MoveDroppedTicks: 7, MoveFarDistance: 80}
	for _, c := range []struct {
		dist float32
		want int64
	}{{0, 0}, {10, 0}, {16, 1}, {40, 2}, {80, 5}, {500, 5}} {
		if got := s.moveTicks(float64(c.dist)); got != c.want {
			t.Errorf("at %v: %d, want %d", c.dist, got, c.want)
		}
	}
}

// A projectile that hit a box must count as in it, or it falls out the next tick. Hits land on
// a face, and rounding can put them just off it.
func TestProjectileHitStaysInBox(t *testing.T) {
	lo, hi := mgl64.Vec3{10, 64, 30}, mgl64.Vec3{11, 65, 31}
	for i := range 20000 {
		f := float32(i) / 20000
		from := mgl64.Vec3{float64(10 + f), float64(65.03 + 3*f), float64(30.7 - f/2)}
		to := from.Add(mgl64.Vec3{float64(0.05 - f/10), float64(-0.1 - 3*f), float64(f/10 - 0.05)})
		at, hits := segmentEntersBox(from, to, lo, hi)
		if !hits {
			continue
		}
		pos := clampToBox(from.Add(to.Sub(from).Mul(float64(at))), lo, hi)
		if !insideBox(pos, lo, hi) {
			t.Fatalf("hit %v counts as outside its box", pos)
		}
	}
	if insideBox(mgl64.Vec3{10.5, 65.01, 30.5}, lo, hi) {
		t.Error("a point above the block counts as inside")
	}
}
