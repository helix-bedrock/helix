package commands

import (
	"fmt"
	"math"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/command"
	"github.com/helix-bedrock/helix/player"
	"github.com/helix-bedrock/helix/world"
)

// BDS 1.26.60's /teleport, alias /tp (TeleportCommand: setup 0xBBE57B0, execute
// 0xBBE9C50), with its ten overloads in BDS's order. A position destination is read from
// the command's source, ~ rotations from each victim's own. Not built yet: the
// checkForBlocks check (commands.tp.safeTeleportFail) and commands.tp.far. Helix always
// puts a player at the given position after a dimension change, which is what
// forceProvidedPositionOnDimensionChange asks for.

var teleport = command.Spec{
	Name: "teleport", Aliases: []string{"tp"}, Description: "commands.tp.description", Permission: command.GameDirectors,
	Overloads: []command.Runnable{
		tpToPosition{}, tpToPositionRotated{}, tpToPositionFacingPosition{}, tpToPositionFacingEntity{},
		tpVictimToPositionRotated{}, tpVictimToPosition{}, tpVictimToPositionFacingPosition{},
		tpVictimToPositionFacingEntity{}, tpToEntity{}, tpVictimToEntity{},
	},
}

// teleportFacing is BDS's TeleportFacing enum, the one word "facing".
type teleportFacing string

// Type is the enum's name in BDS.
func (teleportFacing) Type() string { return "TeleportFacing" }

// Options is the enum's one word.
func (teleportFacing) Options(command.Source) []string { return []string{"facing"} }

// tpToPosition is /tp <destination: x y z> [checkForBlocks] [force...].
type tpToPosition struct {
	Destination                            command.Position
	CheckForBlocks                         command.Optional[bool]
	ForceProvidedPositionOnDimensionChange command.Optional[bool]
}

// Run moves the runner.
func (c tpToPosition) Run(src command.Source, out *command.Output) {
	tpPosition(src, out, nil, c.Destination, keepRotation)
}

// tpToPositionRotated is /tp <destination> [yRot] [xRot] ....
type tpToPositionRotated struct {
	Destination                            command.Position
	YRot                                   command.Optional[command.RelativeFloat]
	XRot                                   command.Optional[command.RelativeFloat]
	CheckForBlocks                         command.Optional[bool]
	ForceProvidedPositionOnDimensionChange command.Optional[bool]
}

// Run moves and turns the runner.
func (c tpToPositionRotated) Run(src command.Source, out *command.Output) {
	tpPosition(src, out, nil, c.Destination, rotated(c.YRot, c.XRot))
}

// tpToPositionFacingPosition is /tp <destination> facing <lookAtPosition> ....
type tpToPositionFacingPosition struct {
	Destination                            command.Position
	Facing                                 teleportFacing
	LookAtPosition                         command.Position
	CheckForBlocks                         command.Optional[bool]
	ForceProvidedPositionOnDimensionChange command.Optional[bool]
}

// Run moves the runner, looking at a point.
func (c tpToPositionFacingPosition) Run(src command.Source, out *command.Output) {
	tpPosition(src, out, nil, c.Destination, facingPoint(c.LookAtPosition.Resolve(src)))
}

// tpToPositionFacingEntity is /tp <destination> facing <lookAtEntity> ....
type tpToPositionFacingEntity struct {
	Destination                            command.Position
	Facing                                 teleportFacing
	LookAtEntity                           command.Targets
	CheckForBlocks                         command.Optional[bool]
	ForceProvidedPositionOnDimensionChange command.Optional[bool]
}

// Run moves the runner, looking at an entity.
func (c tpToPositionFacingEntity) Run(src command.Source, out *command.Output) {
	if at, ok := lookTarget(out, c.LookAtEntity); ok {
		tpPosition(src, out, nil, c.Destination, facingPoint(at))
	}
}

// tpVictimToPositionRotated is /tp <victim> <destination> [yRot] [xRot] ....
type tpVictimToPositionRotated struct {
	Victim                                 command.Targets
	Destination                            command.Position
	YRot                                   command.Optional[command.RelativeFloat]
	XRot                                   command.Optional[command.RelativeFloat]
	CheckForBlocks                         command.Optional[bool]
	ForceProvidedPositionOnDimensionChange command.Optional[bool]
}

// Run moves and turns the victims.
func (c tpVictimToPositionRotated) Run(src command.Source, out *command.Output) {
	tpPosition(src, out, c.Victim.List, c.Destination, rotated(c.YRot, c.XRot))
}

// tpVictimToPosition is /tp <victim> <destination> ....
type tpVictimToPosition struct {
	Victim                                 command.Targets
	Destination                            command.Position
	CheckForBlocks                         command.Optional[bool]
	ForceProvidedPositionOnDimensionChange command.Optional[bool]
}

// Run moves the victims.
func (c tpVictimToPosition) Run(src command.Source, out *command.Output) {
	tpPosition(src, out, c.Victim.List, c.Destination, keepRotation)
}

// tpVictimToPositionFacingPosition is /tp <victim> <destination> facing <lookAtPosition> ....
type tpVictimToPositionFacingPosition struct {
	Victim                                 command.Targets
	Destination                            command.Position
	Facing                                 teleportFacing
	LookAtPosition                         command.Position
	CheckForBlocks                         command.Optional[bool]
	ForceProvidedPositionOnDimensionChange command.Optional[bool]
}

// Run moves the victims, looking at a point.
func (c tpVictimToPositionFacingPosition) Run(src command.Source, out *command.Output) {
	tpPosition(src, out, c.Victim.List, c.Destination, facingPoint(c.LookAtPosition.Resolve(src)))
}

// tpVictimToPositionFacingEntity is /tp <victim> <destination> facing <lookAtEntity> ....
type tpVictimToPositionFacingEntity struct {
	Victim                                 command.Targets
	Destination                            command.Position
	Facing                                 teleportFacing
	LookAtEntity                           command.Targets
	CheckForBlocks                         command.Optional[bool]
	ForceProvidedPositionOnDimensionChange command.Optional[bool]
}

// Run moves the victims, looking at an entity.
func (c tpVictimToPositionFacingEntity) Run(src command.Source, out *command.Output) {
	if at, ok := lookTarget(out, c.LookAtEntity); ok {
		tpPosition(src, out, c.Victim.List, c.Destination, facingPoint(at))
	}
}

// tpToEntity is /tp <destination: target>, with no other arguments in BDS.
type tpToEntity struct {
	Destination command.Targets
}

// Run moves the runner to the entity.
func (c tpToEntity) Run(src command.Source, out *command.Output) {
	tpEntity(src, out, nil, c.Destination)
}

// tpVictimToEntity is /tp <victim> <destination: target> ....
type tpVictimToEntity struct {
	Victim                                 command.Targets
	Destination                            command.Targets
	CheckForBlocks                         command.Optional[bool]
	ForceProvidedPositionOnDimensionChange command.Optional[bool]
}

// Run moves the victims to the entity.
func (c tpVictimToEntity) Run(src command.Source, out *command.Output) {
	tpEntity(src, out, c.Victim.List, c.Destination)
}

// facingRule is the rotation a victim gets from its own and where it lands (feet).
type facingRule func(own world.Rotation, feet mgl32.Vec3) world.Rotation

// keepRotation leaves the victim facing as it was.
func keepRotation(own world.Rotation, _ mgl32.Vec3) world.Rotation { return own }

// rotated is yRot and xRot, each kept when left out and ~ from the victim's own.
func rotated(yRot, xRot command.Optional[command.RelativeFloat]) facingRule {
	return func(own world.Rotation, _ mgl32.Vec3) world.Rotation {
		r := own
		if yRot.Set {
			r.Yaw = yRot.Value.Resolve(own.Yaw)
			r.HeadYaw = r.Yaw
		}
		if xRot.Set {
			r.Pitch = xRot.Value.Resolve(own.Pitch)
		}
		return r
	}
}

// facingPoint turns the victim's eyes at its landing place towards a point.
func facingPoint(at mgl32.Vec3) facingRule {
	return func(_ world.Rotation, feet mgl32.Vec3) world.Rotation {
		d := at.Sub(feet.Add(mgl32.Vec3{0, player.EyeHeight, 0}))
		yaw := float32(math.Atan2(float64(-d[0]), float64(d[2])) * 180 / math.Pi)
		pitch := float32(-math.Atan2(float64(d[1]), math.Hypot(float64(d[0]), float64(d[2]))) * 180 / math.Pi)
		return world.Rotation{Yaw: yaw, HeadYaw: yaw, Pitch: pitch}
	}
}

// lookTarget is the eyes of the one entity a facing overload looks at.
func lookTarget(out *command.Output, t command.Targets) (mgl32.Vec3, bool) {
	if len(t.List) != 1 {
		out.Fail("commands.generic.tooManyTargets")
		return mgl32.Vec3{}, false
	}
	p, ok := t.List[0].(*player.Player)
	if !ok {
		out.Fail("commands.generic.noTargetMatch")
		return mgl32.Vec3{}, false
	}
	return p.State().Position.Add(mgl32.Vec3{0, player.EyeHeight, 0}), true
}

// victimsOf are the players to move: those named, or the one running the command.
func victimsOf(src command.Source, out *command.Output, list []command.Target) []*player.Player {
	if list == nil {
		if p, ok := player.SourcePlayer(src); ok {
			return []*player.Player{p}
		}
		out.Fail("commands.generic.noTargetMatch")
		return nil
	}
	var victims []*player.Player
	for _, t := range list {
		if p, ok := t.(*player.Player); ok {
			victims = append(victims, p)
		}
	}
	if len(victims) == 0 {
		out.Fail("commands.generic.noTargetMatch")
	}
	return victims
}

// tpPosition moves the victims to a position, in their own dimensions.
func tpPosition(src command.Source, out *command.Output, list []command.Target, dest command.Position, face facingRule) {
	victims := victimsOf(src, out, list)
	if len(victims) == 0 {
		return
	}
	feet := dest.Resolve(src)
	names := make([]string, 0, len(victims))
	for _, v := range victims {
		names = append(names, v.Name())
		v.Run(func(p *player.Player) { moveVictim(p, nil, feet, face) })
	}
	out.Success("commands.tp.success.coordinates", strings.Join(names, ", "),
		fmt.Sprintf("%.2f", feet[0]), fmt.Sprintf("%.2f", feet[1]), fmt.Sprintf("%.2f", feet[2]))
}

// tpEntity moves the victims to one entity, into its dimension if it is in another.
func tpEntity(src command.Source, out *command.Output, list []command.Target, dest command.Targets) {
	if len(dest.List) != 1 {
		out.Fail("commands.generic.tooManyTargets")
		return
	}
	to, ok := dest.List[0].(*player.Player)
	if !ok {
		out.Fail("commands.generic.noTargetMatch")
		return
	}
	victims := victimsOf(src, out, list)
	if len(victims) == 0 {
		return
	}
	st := to.State()
	var dim *world.Dimension
	if st.Region != nil {
		dim = st.Region.Dimension()
	}
	names := make([]string, 0, len(victims))
	for _, v := range victims {
		names = append(names, v.Name())
		v.Run(func(p *player.Player) { moveVictim(p, dim, st.Position, keepRotation) })
	}
	out.Success("commands.tp.success", strings.Join(names, ", "), to.Name())
}

// moveVictim teleports one player. Runs: the player's region.
func moveVictim(p *player.Player, dim *world.Dimension, feet mgl32.Vec3, face facingRule) {
	own := world.Rotation{}
	if e := p.Entity(); e != nil {
		own = e.Rotation()
	}
	p.Teleport(dim, feet, face(own, feet))
}
