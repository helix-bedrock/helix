package effect

import "slices"

// Keys of an effect in a saved entity's ActiveEffects, as BDS 1.26.60 writes them
// (MobEffectInstance save 0xCAB8B90, load 0xCAB9E10).
const (
	Tag          = "ActiveEffects"
	tagID        = "Id"
	tagAmplifier = "Amplifier"
	tagDuration  = "Duration"
	tagEasy      = "DurationEasy"
	tagNormal    = "DurationNormal"
	tagHard      = "DurationHard"
	tagAmbient   = "Ambient"
	tagParticles = "ShowParticles"
	tagAnimation = "DisplayOnScreenTextureAnimation"
	tagStored    = "StoredEffects"
)

// Save writes the effects as BDS saves them; no effects, no key.
func (l List) Save(data map[string]any) {
	if len(l) == 0 {
		delete(data, Tag)
		return
	}
	list := make([]any, 0, len(l))
	for i := range l {
		list = append(list, l[i].NBT())
	}
	data[Tag] = list
}

// NBT is one effect as BDS saves it. BDS keeps a duration per difficulty; Helix has one,
// written to all three.
func (e *Instance) NBT() map[string]any {
	m := map[string]any{
		tagID: uint8(e.ID), tagAmplifier: uint8(min(max(e.Amplifier, 0), 255)), //nolint:gosec // clamped, BDS saves bytes
		tagDuration: e.Duration, tagEasy: e.Duration, tagNormal: e.Duration, tagHard: e.Duration,
		tagAmbient: boolByte(e.Ambient), tagParticles: boolByte(!e.Hidden), tagAnimation: uint8(0),
	}
	if len(e.stored) > 0 {
		list := make([]any, 0, len(e.stored))
		for i := range e.stored {
			list = append(list, e.stored[i].NBT())
		}
		m[tagStored] = list
	}
	return m
}

func boolByte(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// Load reads the effects BDS saved, in ID order.
func Load(data map[string]any) List {
	list, ok := data[Tag].([]any)
	if !ok {
		return nil
	}
	var l List
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if e, ok := FromNBT(m); ok {
			l = append(l, e)
		}
	}
	slices.SortStableFunc(l, func(a, b Instance) int { return int(a.ID - b.ID) })
	return l
}

// FromNBT reads one saved effect; false without an ID or duration.
func FromNBT(m map[string]any) (Instance, bool) {
	id, ok := m[tagID].(uint8)
	dur, okd := m[tagDuration].(int32)
	if !ok || !okd || id == 0 {
		return Instance{}, false
	}
	amp, _ := m[tagAmplifier].(uint8)
	ambient, _ := m[tagAmbient].(uint8)
	particles, okp := m[tagParticles].(uint8)
	e := Instance{ID: int32(id), Amplifier: int32(amp), Duration: dur, Ambient: ambient != 0, Hidden: okp && particles == 0}
	if stored, ok := m[tagStored].([]any); ok {
		for _, v := range stored {
			if sm, ok := v.(map[string]any); ok {
				if s, ok := FromNBT(sm); ok {
					s.stored = nil
					e.stored = append(e.stored, s)
				}
			}
		}
	}
	return e, true
}
