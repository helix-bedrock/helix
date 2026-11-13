package mob

import "github.com/helix-bedrock/entity/state"

// DataValue is one entry of the mob's synched actor data that its components wrote
// (variant, mark variant, colour, fuse time, skin ID, ambient sound timing): Value is an
// int32, float64 or string, as the client reads that key.
type DataValue struct {
	Key   int
	Value any
}

// ActorData is the actor data the mob's components wrote, in the order first written. The
// engine sends it with the mob's flags. Runs: the mob's region, outside its think.
func (m *Mob) ActorData() []DataValue { return m.data }

// setData writes a key and marks the actor data changed when the value is new.
func (m *Mob) setData(key int, v any) {
	for i := range m.data {
		if m.data[i].Key == key {
			if m.data[i].Value == v {
				return
			}
			m.data[i].Value = v
			m.flagsChanged()
			return
		}
	}
	m.data = append(m.data, DataValue{Key: key, Value: v})
	m.flagsChanged()
}

// SetIntData, SetFloatData and SetStringData are state.DataMob: the components' actor data.
func (s stateMob) SetIntData(key int, v int32)     { s.setData(key, v) }
func (s stateMob) SetFloatData(key int, v float64) { s.setData(key, float32(v)) } // actor data floats are 32-bit
func (s stateMob) SetStringData(key int, v string) { s.setData(key, v) }

var _ state.DataMob = stateMob{}
