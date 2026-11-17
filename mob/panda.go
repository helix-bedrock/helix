package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/nav"
)

// The panda's roll (behavior.Roller) and sneeze (behavior.Sneezer). BDS 1.26.60's Panda
// constructor (0xBEB6EC0) is the only one that adds RollCounterComponent; the panda's tick
// counts it up while it rolls (actor flag 67) and resets it otherwise.

// typePanda is the type that has a roll counter.
const typePanda = "minecraft:panda"

// rollTick keeps the roll counter.
func (m *Mob) rollTick() {
	if m.typ != typePanda {
		return
	}
	if m.flags.Has(behavior.FlagRolling) {
		m.rollCount++
	} else {
		m.rollCount = 0
	}
}

// RollCounter implements behavior.Roller.
func (m *Mob) RollCounter() (int32, bool) { return m.rollCount, m.typ == typePanda }

// WalkDirectly implements behavior.Roller: the walker's straight line test with the loose
// block checks.
func (m *Mob) WalkDirectly(from, to behavior.Vec3, sx, sy, sz int32) bool {
	nm := m.navMob()
	return nav.WalkDirectly(pathWorld{&m.v}, &nm, from, to, sx, sy, sz, true)
}

// MakeJump implements behavior.Sneezer: the other mob's jump control jumps, at Apply.
func (m *Mob) MakeJump(a behavior.Actor) { m.do(op{kind: opMakeJump, id: a.ID()}) }

// LevelEventData implements behavior.Sneezer: the level event goes out at Apply.
func (m *Mob) LevelEventData(id int32, p behavior.Vec3, data int32) {
	m.do(op{kind: opLevelEventData, n: id, at: p, other: behavior.ID(data)})
}

// DropOwnLoot implements behavior.Sneezer: the table is rolled and dropped at the mob, at
// Apply.
func (m *Mob) DropOwnLoot(table string) {
	m.do(op{kind: opDropLoot, name: table, at: m.Pos(), id: behavior.NoID, n: -1})
}
