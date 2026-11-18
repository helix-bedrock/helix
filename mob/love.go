package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/state"
)

// The villager breeding food (BDS 1.26.60 finds the slot, eats it): a
// slot of bread holding breadBreed or more, or of one of the crops holding cropBreed or
// more. The three crop items are read as BDS item globals that are not named in the db;
// they are taken as carrot, potato and beetroot.
const (
	itemBread  = "minecraft:bread"
	breadBreed = 3
	cropBreed  = 12
)

// breedCrops are the items of which cropBreed make a villager ready to breed.
var breedCrops = [...]string{"minecraft:carrot", "minecraft:potato", "minecraft:beetroot"}

// love is the Villager's love state: its partner (set with the in-love flag by),
// whether it started the pairing and its willing flag.
type love struct {
	partner behavior.ID
	leader  bool
	willing bool
}

// LovePartner implements behavior.Lover.
func (m *Mob) LovePartner(id behavior.ID) behavior.ID {
	if id == m.id {
		return m.love.partner
	}
	if s := m.snapshotOf(id); s != nil {
		return s.lovePartner
	}
	return behavior.NoID
}

// SetLovePartner implements behavior.Lover: another mob's partner changes at Apply.
func (m *Mob) SetLovePartner(id, partner behavior.ID) {
	if id == m.id {
		m.setLovePartner(partner)
		return
	}
	m.do(op{kind: opLovePartner, id: id, other: partner})
}

// setLovePartner is: the partner and the in-love flag together.
func (m *Mob) setLovePartner(partner behavior.ID) {
	m.love.partner = partner
	m.SetFlag(behavior.FlagInLove, partner != behavior.NoID)
}

// LoveLeader implements behavior.Lover.
func (m *Mob) LoveLeader(id behavior.ID) bool {
	if id == m.id {
		return m.love.leader
	}
	s := m.snapshotOf(id)
	return s != nil && s.loveLeader
}

// SetLoveLeader implements behavior.Lover.
func (m *Mob) SetLoveLeader(id behavior.ID, on bool) {
	if id == m.id {
		m.love.leader = on
		return
	}
	m.do(op{kind: opLoveLeader, id: id, n: boolInt(on)})
}

// Willing implements behavior.Lover. Nothing Helix runs makes a villager willing yet.
func (m *Mob) Willing(id behavior.ID) bool {
	if id == m.id {
		return m.love.willing
	}
	s := m.snapshotOf(id)
	return s != nil && s.willing
}

// SetWilling implements behavior.Lover.
func (m *Mob) SetWilling(id behavior.ID, on bool) {
	if id == m.id {
		m.love.willing = on
		return
	}
	m.do(op{kind: opWilling, id: id, n: boolInt(on)})
}

// HasBreedFood implements behavior.Lover.
func (m *Mob) HasBreedFood(id behavior.ID) bool {
	if id == m.id {
		return m.breedFoodSlot() >= 0
	}
	s := m.snapshotOf(id)
	return s != nil && s.breedFood
}

// UseBreedFood implements behavior.Lover: another mob eats at Apply.
func (m *Mob) UseBreedFood(id behavior.ID) {
	if id == m.id {
		m.useBreedFood()
		return
	}
	m.do(op{kind: opUseBreedFood, id: id})
}

// breedFoodSlot is the first inventory slot with enough food to breed, -1 for none.
func (m *Mob) breedFoodSlot() int {
	for i, it := range m.Inventory() {
		if it.Empty() {
			continue
		}
		if it.Name == itemBread && it.Count >= breadBreed || isBreedCrop(it.Name) && it.Count >= cropBreed {
			return i
		}
	}
	return -1
}

// useBreedFood takes breadBreed bread or cropBreed of a crop from the food slot.
func (m *Mob) useBreedFood() {
	i := m.breedFoodSlot()
	if i < 0 {
		return
	}
	it := &m.inv.slots[i]
	switch {
	case it.Name == itemBread:
		it.Count -= breadBreed
	case isBreedCrop(it.Name):
		it.Count -= cropBreed
	default:
		return
	}
	if it.Count <= 0 {
		*it = behavior.Item{}
	}
}

func isBreedCrop(name string) bool {
	for _, c := range breedCrops {
		if c == name {
			return true
		}
	}
	return false
}

// WakeBreedSensors implements behavior.Lover: BDS resets the partner's sensing cache
// (component 0xADEC332E). Helix's mobs keep no sensing cache, so there is nothing to reset.
func (m *Mob) WakeBreedSensors(behavior.ID) {}

// VillageWantsBabies implements behavior.Lover: dweller is false without minecraft:dweller;
// a dweller with no village wants none.
func (m *Mob) VillageWantsBabies() (wants, dweller bool) {
	if m.states == nil || m.states.Dweller == nil {
		return false, false
	}
	v := m.dw.village
	return v != nil && v.WantsBabies(), true
}

// SpawnBaby implements behavior.Lover: the baby of the mob's own type is made at Apply
// through Hooks.Born, so false only when there is no engine to make it. A baby the engine
// then fails to make still costs the food.
func (m *Mob) SpawnBaby(partner behavior.ID) bool {
	if m.hooks == nil {
		return false
	}
	m.do(op{kind: opSpawnBaby, id: partner})
	return true
}

// runLoveOp runs a love op at Apply on the mob it names.
func (m *Mob) runLoveOp(o *op) {
	if o.kind == opSpawnBaby {
		if m.hooks != nil {
			m.hooks.Born(m, state.Birth{Baby: m.typ, Partner: o.id, Owner: behavior.NoID, LoveCause: behavior.NoID})
		}
		return
	}
	pm := m.rt.Env.Mob(m.v.r, o.id)
	if pm == nil || pm.Dead() {
		return
	}
	switch o.kind {
	case opLovePartner:
		pm.setLovePartner(o.other)
	case opLoveLeader:
		pm.love.leader = o.n != 0
	case opWilling:
		pm.love.willing = o.n != 0
	case opUseBreedFood:
		pm.useBreedFood()
	}
	pm.snapshot()
}

// snapshotOf is another mob as it was at the end of the last tick, nil for none.
func (m *Mob) snapshotOf(id behavior.ID) *Snapshot {
	if m.v.r == nil {
		return nil
	}
	s, _ := m.rt.Env.Actor(m.v.r, id).(*Snapshot)
	return s
}

func boolInt(on bool) int32 {
	if on {
		return 1
	}
	return 0
}
