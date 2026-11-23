package mob

import (
	"github.com/helix-bedrock/entity/physics"
	"github.com/helix-bedrock/helix/world"
)

// PhysicsView lets code outside the mob runtime, such as the player's movement simulation,
// see the world as mob physics does: the same blocks, collision boxes and liquids. Keep one
// per user and reuse it; it is not safe for use by many goroutines at once.
type PhysicsView struct{ v view }

// PhysicsView returns a new view of the runtime's blocks. Runs: anywhere.
func (rt *Runtime) PhysicsView() *PhysicsView {
	return &PhysicsView{v: view{blocks: rt.Blocks, env: rt.Env}}
}

// World returns the physics world of r, valid until the next call. Runs: r's tick.
func (p *PhysicsView) World(r *world.Region) physics.World {
	p.v.r = r
	return physWorld{&p.v}
}
