// Package direction is every direction the engine works with beyond the block
// faces and positions in the world library's cube package: the four cardinal
// directions, the numberings block states use for them, the orders BDS walks
// the sides of a block in, and turning a player's yaw into a direction.
//
// BDS numbers one direction several ways depending on the block, and the order
// it visits neighbours in changes what happens in the world. Both live here, so
// a system names what it means instead of keeping its own table of numbers. A
// system whose order is its own alone lists the faces itself.
//
// All values are plain and safe to use from any goroutine.
package direction

import (
	"math"

	"github.com/helix-bedrock/world/cube"
)

// CardinalState is the block state most blocks keep a Cardinal in.
const CardinalState = "minecraft:cardinal_direction"

// Cardinal is one of the four horizontal directions, numbered as BDS's
// minecraft:cardinal_direction values and its yaw index are.
type Cardinal uint8

// The cardinal directions, in BDS's order.
const (
	South Cardinal = iota
	West
	North
	East
)

// cardinalMask keeps a Cardinal in range when it is used as an index.
const cardinalMask = 3

var (
	cardinalNames = [4]string{"south", "west", "north", "east"}
	cardinalFaces = [4]cube.Face{cube.FaceSouth, cube.FaceWest, cube.FaceNorth, cube.FaceEast}
	eastFirst     = [4]Cardinal{East, West, South, North}
	eastFirstOf   = [4]int{2, 1, 3, 0}
	northFirstOf  = [4]int{2, 3, 0, 1}
)

// Names returns the state value of each Cardinal, in order.
func Names() [4]string { return cardinalNames }

// Parse returns the Cardinal a state value names; false if it names none.
func Parse(name string) (Cardinal, bool) {
	for i, n := range cardinalNames {
		if n == name {
			return Cardinal(i), true //nolint:gosec // i is below 4
		}
	}
	return South, false
}

// Of returns the Cardinal of a side face; false for the top, the bottom and
// anything that is not a face.
func Of(face cube.Face) (Cardinal, bool) {
	switch face {
	case cube.FaceSouth:
		return South, true
	case cube.FaceWest:
		return West, true
	case cube.FaceNorth:
		return North, true
	case cube.FaceEast:
		return East, true
	}
	return South, false
}

// FromEastFirst returns the Cardinal of a value in the east, west, south, north
// numbering; false if it is out of range.
func FromEastFirst(v int) (Cardinal, bool) {
	if v < 0 || v >= len(eastFirst) {
		return South, false
	}
	return eastFirst[v], true
}

// String returns the Cardinal's state value.
func (c Cardinal) String() string { return cardinalNames[c&cardinalMask] }

// Opposite returns the direction the other way.
func (c Cardinal) Opposite() Cardinal { return (c ^ 2) & cardinalMask }

// Face returns the side face looking this way.
func (c Cardinal) Face() cube.Face { return cardinalFaces[c&cardinalMask] }

// FacesZ reports whether the direction lies along the Z axis.
func (c Cardinal) FacesZ() bool { return c&1 == 0 }

// Beside returns the two faces to either side of something looking this way:
// east then west, or north then south.
func (c Cardinal) Beside() [2]cube.Face {
	if c.FacesZ() {
		return [2]cube.Face{cube.FaceEast, cube.FaceWest}
	}
	return [2]cube.Face{cube.FaceNorth, cube.FaceSouth}
}

// EastFirst is the direction in the east, west, south, north numbering of
// stairs' weirdo_direction and trapdoors' direction states.
func (c Cardinal) EastFirst() int { return eastFirstOf[c&cardinalMask] }

// NorthFirst is the direction in the clockwise north, east, south, west
// numbering of cocoa's direction state and the bits of vines'.
func (c Cardinal) NorthFirst() int { return northFirstOf[c&cardinalMask] }

// FromSteps is BDS's floor(f) & 3, for a yaw already divided into quarter turns.
func FromSteps(f float32) Cardinal {
	return Cardinal(int(math.Floor(float64(f))) & cardinalMask) //nolint:gosec // masked
}

// FromYaw is the direction a yaw in degrees looks in, by BDS's floor(yaw * 4 /
// 360 + 0.5) & 3.
func FromYaw(yaw float32) Cardinal { return FromSteps(yaw*4/360 + 0.5) }

// Clockwise returns the side faces clockwise from north.
func Clockwise() [4]cube.Face {
	return [4]cube.Face{cube.FaceNorth, cube.FaceEast, cube.FaceSouth, cube.FaceWest}
}

// Sides returns the side faces in Bedrock's own face order.
func Sides() [4]cube.Face {
	return [4]cube.Face{cube.FaceNorth, cube.FaceSouth, cube.FaceWest, cube.FaceEast}
}

// Spread returns the side faces in the order liquids spread to them. Each face's
// opposite is the one beside it: index i ^ 1.
func Spread() [4]cube.Face {
	return [4]cube.Face{cube.FaceWest, cube.FaceEast, cube.FaceNorth, cube.FaceSouth}
}

// Updates returns all six faces in the order BDS tells neighbours of a change.
func Updates() [6]cube.Face {
	return [6]cube.Face{cube.FaceWest, cube.FaceEast, cube.FaceSouth, cube.FaceNorth, cube.FaceUp, cube.FaceDown}
}

// Vertical reports whether a face is the top or bottom.
func Vertical(face cube.Face) bool { return face == cube.FaceDown || face == cube.FaceUp }

// Axis is one of the three axes, numbered as pillar_axis values are.
type Axis uint8

// The axes, in BDS's order.
const (
	AxisY Axis = iota
	AxisX
	AxisZ
)

// AxisOf returns the axis a face lies along. Anything that is not a side face is
// on the Y axis.
func AxisOf(face cube.Face) Axis {
	switch face {
	case cube.FaceNorth, cube.FaceSouth:
		return AxisZ
	case cube.FaceWest, cube.FaceEast:
		return AxisX
	}
	return AxisY
}

// BDS's sine table has 65536 entries a full turn; angles are given to it in
// those units.
const (
	// UnitsPerRadian turns radians into sine table units.
	UnitsPerRadian = 10430.378
	sineQuarter    = 16384
	degreeRadians  = 0.017453292
	sineTableSize  = 65536
)

// Units turns degrees into sine table units, as BDS does.
func Units(degrees float32) float32 { return degrees * degreeRadians * UnitsPerRadian }

// Sin is BDS's table sine of an angle in sine table units.
func Sin(units float32) float32 {
	return float32(math.Sin(float64(uint16(int32(units))) * 2 * math.Pi / sineTableSize)) //nolint:gosec // wraps as the table index does
}

// Cos is BDS's table cosine of an angle in sine table units.
func Cos(units float32) float32 { return Sin(units + sineQuarter) }
