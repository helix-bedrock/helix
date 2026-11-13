package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl64"
)

// AttachPoint is a named point on an entity's body, BDS's ActorLocation. Items, sounds and
// pulls start from or aim at one: a fishing hook is cast from the drop point and reels in
// toward the head, a thrown item's sound plays at the drop point, a bow's at the weapon point.
type AttachPoint uint8

// The attach points, in BDS's order (sub_7560A00 switches on these numbers).
const (
	AttachFeet AttachPoint = iota
	AttachBody
	AttachWeapon
	AttachHead
	AttachDrop
	AttachExplosion
	AttachEyes
	AttachBreathing
	AttachMouth
)

// The player's attach point offsets, from BDS 1.26.60. A player's position is its eyes
// (OffsetsComponent height offset 1.62001, set by the Player constructor 0xBCC9950), and the
// player sets none of the head, eyes, breathing or mouth offsets, so those four points are
// the position itself. The drop offset is the one every actor starts with (0xB38ABA0):
// 0.2 down and 0.15 ahead, turned with the yaw.
const (
	attachDropDown  = 0.2
	attachDropAhead = 0.15
)

// AttachPos returns where point a is on the player now, as BDS's attach position
// (sub_7560A00 through sub_B39CED0) works it out for a player:
//   - feet and explosion: the eyes less the eye height;
//   - body and weapon: the middle of the bounding box;
//   - head, eyes, breathing, mouth: the eyes;
//   - drop: the eyes, 0.2 down and 0.15 ahead along the yaw.
//
// The zero vector if the player is not in a world. Runs: the player's region.
func (p *Player) AttachPos(a AttachPoint) mgl64.Vec3 {
	if p.entity == nil {
		return mgl64.Vec3{}
	}
	eyes := p.entity.Position()
	switch a {
	case AttachFeet, AttachExplosion:
		return eyes.Sub(mgl64.Vec3{0, eyeHeight, 0})
	case AttachBody, AttachWeapon:
		lo, hi, _ := p.BlastBox()
		return lo.Add(hi).Mul(0.5)
	case AttachDrop:
		yaw := float64(p.entity.Rotation().Yaw) * math.Pi / 180
		return eyes.Add(mgl64.Vec3{
			float64(-float32(math.Sin(yaw)) * attachDropAhead),
			-attachDropDown,
			float64(math.Cos(yaw)) * attachDropAhead,
		})
	default:
		return eyes
	}
}
