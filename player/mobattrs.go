package player

import (
	"math"

	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// attrJumpStrengthName is a horse's jump strength attribute.
const attrJumpStrengthName = "minecraft:horse.jump_strength"

// mobAttrs are the mob attributes clients act on: the speed and jump strength a rider's
// client steers a horse by, and the max health. A mob's components can be read after it
// was first shown (a mob loaded with its chunk is sent before its first tick), so a change
// is sent on, as BDS sends an actor's dirty attributes with UpdateAttributes.
type mobAttrs struct {
	speed, maxHealth, jump, jumpMax float32
	hasJump                         bool
}

// currentAttrs reads the mob's attributes now.
func (m *Mob) currentAttrs() mobAttrs {
	a := mobAttrs{speed: m.m.MovementSpeed(), maxHealth: m.m.MaxHealth()}
	a.jump, a.jumpMax, a.hasJump = m.m.JumpStrength()
	return a
}

// syncAttrs sends viewers the attributes that changed since they were last sent. Runs: the
// mob's apply, each tick; it allocates only when something changed.
func (m *Mob) syncAttrs() {
	a := m.currentAttrs()
	was := m.sentAttrs
	if a == was {
		return
	}
	m.sentAttrs = a
	var attrs []protocol.Attribute
	if a.speed != was.speed {
		attrs = append(attrs, mobAttribute(attrMovementName, a.speed, math.MaxFloat32))
	}
	if a.maxHealth != was.maxHealth {
		attrs = append(attrs, mobAttribute(attrHealthName, m.m.HealthValue(), a.maxHealth))
	}
	if a.hasJump && (a.jump != was.jump || a.jumpMax != was.jumpMax || !was.hasJump) {
		attrs = append(attrs, mobAttribute(attrJumpStrengthName, a.jump, max(a.jumpMax, a.jump)))
	}
	if len(attrs) == 0 {
		return
	}
	m.entity.Send(&packet.UpdateAttributes{EntityRuntimeID: m.entity.RuntimeID(), Attributes: attrs})
}

// mobAttribute is one attribute of an UpdateAttributes, its default its value.
func mobAttribute(name string, v, mx float32) protocol.Attribute {
	return protocol.Attribute{
		AttributeValue: protocol.AttributeValue{Name: name, Value: v, Max: mx},
		DefaultMax:     mx, Default: v,
	}
}
