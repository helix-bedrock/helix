package mob

import (
	"slices"

	"github.com/google/uuid"
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/path"
	"github.com/helix-bedrock/helix/village"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// A mob with minecraft:dweller belongs to a village (package village): the hooks the
// village goals need (Dweller, Sleeper, Worker, Indoorer, Hider, DoorUser) and the
// village.Dweller side of claims.

// Villages is the engine's village system as mobs reach it.
type Villages interface {
	// Manager is the village manager of r's dimension, nil for none. Safe while mobs
	// think.
	Manager(r *world.Region) *village.Manager
}

// BDS's numbers for dwellers.
const (
	// poiFailEvent is the level event a failed trip to a POI shows, poiFailShows times on
	// the POI's top while the fail count is poiFailMax or less (0xBD0D270).
	poiFailEvent = 2009
	poiFailShows = 3
	poiFailMax   = 2
	// dwellerInhabitant is the role that takes job sites.
	dwellerInhabitant = "inhabitant"
	// markerOpenDoor is the component the door goals need.
	markerOpenDoor = "minecraft:annotation.open_door"
	// componentHide is minecraft:hide.
	componentHide = "minecraft:hide"
	// nightDarken is the sky darkening from which a dweller wants shelter at night.
	nightDarken = 4
)

// dweller is the mob's DwellerComponent state.
type dweller struct {
	village *village.Village
	// villageID is the village's UUID, kept from the save until the village is found.
	villageID uuid.UUID
	pois      [3]behavior.POI
	has       [3]bool
	fails     [3]int32
	// profession is the preferred profession a claim set, "" to use the component's.
	profession string
	// timer counts down to the next dweller update.
	timer int32
	// sleepW and sleepH are the size before lying down.
	sleepW, sleepH float32
	sleeping       bool
	// hiding and triggered are HideComponent's flags.
	hiding, triggered bool
	// trading is the player in the trade window, NoID for none.
	trading behavior.ID
}

// dwellerTick counts down to the dweller's update and queues it (update_interval_base
// plus a random part of update_interval_variant). Runs in think.
func (m *Mob) dwellerTick() {
	if m.states == nil || m.states.Dweller == nil {
		return
	}
	if m.dw.timer > 0 {
		m.dw.timer--
		return
	}
	c := m.states.Dweller
	m.dw.timer = max(c.UpdateIntervalBase, 1) + m.rnd.NextInt(max(c.UpdateIntervalVariant, 0)+1)
	m.do(op{kind: opDwellerTick})
}

// runDwellerTick is the queued update: the village manager does the rest.
func (m *Mob) runDwellerTick(r *world.Region) {
	if m.rt.Villages == nil || m.states == nil || m.states.Dweller == nil {
		return
	}
	if mgr := m.rt.Villages.Manager(r); mgr != nil {
		c := m.states.Dweller
		mgr.DwellerTick(r, m, c.CanFindPOI, c.CanMigrate)
	}
}

// SetRegion points the mob at the region it is in, for engine work on it outside its tick
// (a village claim running its event). Runs: that region's goroutine.
func (m *Mob) SetRegion(r *world.Region) { m.v.r = r }

// --- behavior.Dweller ---

// IsDweller reports minecraft:dweller.
func (m *Mob) IsDweller() bool { return m.states != nil && m.states.Dweller != nil }

// POI is the mob's own POI of the kind.
func (m *Mob) POI(kind behavior.POIKind) (behavior.POI, bool) {
	if kind < 0 || int(kind) >= len(m.dw.pois) || !m.dw.has[kind] {
		return behavior.POI{}, false
	}
	return m.dw.pois[kind], true
}

// FreePOI is the village's nearest POI of the kind to p that has room.
func (m *Mob) FreePOI(kind behavior.POIKind, p behavior.Pos) (behavior.POI, bool) {
	if m.dw.village == nil {
		return behavior.POI{}, false
	}
	return m.dw.village.FreePOI(kind, p)
}

// POIResult counts failed trips to a POI and shows the fail particles.
func (m *Mob) POIResult(kind behavior.POIKind, ok bool) {
	if kind < 0 || int(kind) >= len(m.dw.fails) {
		return
	}
	if ok {
		m.dw.fails[kind] = 0
		return
	}
	m.dw.fails[kind]++
	if m.dw.fails[kind] > poiFailMax || !m.dw.has[kind] {
		return
	}
	p := m.dw.pois[kind].Pos
	at := behavior.Vec3{X: float32(p.X) + 0.5, Y: float32(p.Y) + 1, Z: float32(p.Z) + 0.5}
	for range poiFailShows {
		m.do(op{kind: opLevelEvent, n: poiFailEvent, at: at})
	}
}

// Village is the bounds of the mob's village, or of the nearest within 64 blocks.
func (m *Mob) Village(search int32) (behavior.Box, bool) {
	if v := m.dw.village; v != nil {
		return v.Bounds(), true
	}
	if m.rt.Villages == nil || m.v.r == nil {
		return behavior.Box{}, false
	}
	mgr := m.rt.Villages.Manager(m.v.r)
	if mgr == nil {
		return behavior.Box{}, false
	}
	limit := float32(64)
	if search >= 0 {
		limit = min(limit, float32(search))
	}
	v, ok := mgr.NearestView(m.body.Pos, limit)
	if !ok {
		return behavior.Box{}, false
	}
	return v.Bounds(), true
}

// --- village.Dweller ---

// HeadY is the height of the mob's eyes.
func (m *Mob) HeadY() float32 { return m.eyeHeight() }

// Baby reports a baby.
func (m *Mob) Baby() bool { return m.flags.Has(behavior.FlagBaby) }

// Role is dweller_role.
func (m *Mob) Role() string {
	if m.states == nil || m.states.Dweller == nil {
		return ""
	}
	return m.states.Dweller.DwellerRole
}

// Nitwit reports the nitwit family, which never takes a job site.
func (m *Mob) Nitwit() bool { return slices.Contains(m.families, "nitwit") }

// Profession is the preferred profession: the one a claim set, else the dweller
// component's preferred_profession.
func (m *Mob) Profession() string {
	if m.dw.profession != "" {
		return m.dw.profession
	}
	if m.states != nil && m.states.Dweller != nil {
		return m.states.Dweller.PreferredProfession
	}
	return ""
}

// SetProfession sets the preferred profession; "" falls back to the component's.
func (m *Mob) SetProfession(name string) { m.dw.profession = name }

// Locked reports the trade lock (1.26.60 0x7240180): trade experience or tier above 0.
func (m *Mob) Locked() bool { return m.TradeExperience() != 0 || m.TradeTier() != 0 }

// Fire runs a definition event on the mob. Runs: the mob's region, outside its think.
func (m *Mob) Fire(event string) { m.inst.Execute(event, m.tester(nil)) }

// SetPOI gives or takes the mob's POI of the kind.
func (m *Mob) SetPOI(kind village.Kind, p behavior.POI, ok bool) {
	if kind < 0 || int(kind) >= len(m.dw.pois) {
		return
	}
	m.dw.pois[kind], m.dw.has[kind] = p, ok
	if !ok {
		m.dw.fails[kind] = 0
	}
}

// SetVillage sets the mob's village.
func (m *Mob) SetVillage(v *village.Village) {
	m.dw.village = v
	if v != nil {
		m.dw.villageID = v.ID
	} else {
		m.dw.villageID = uuid.Nil
	}
}

// VillageID is the UUID of the mob's village (DwellingUniqueID), uuid.Nil for none.
func (m *Mob) VillageID() uuid.UUID { return m.dw.villageID }

// SetVillageID sets the village a loaded mob belonged to; its next dweller update joins it.
func (m *Mob) SetVillageID(id uuid.UUID) { m.dw.villageID = id }

var _ village.Dweller = (*Mob)(nil)

// --- behavior.Sleeper ---

// BedDirection is the direction state of the bed at p.
func (m *Mob) BedDirection(p behavior.Pos) (int32, bool) {
	return goalWorld{&m.v}.BlockState(p, "direction")
}

// Sleeping reports the sleeping flag.
func (m *Mob) Sleeping() bool { return m.flags.Has(behavior.FlagSleeping) }

// SetSleeping lies the mob down or wakes it, putting its size back.
func (m *Mob) SetSleeping(on bool) {
	if on && !m.dw.sleeping {
		m.dw.sleepW, m.dw.sleepH = m.body.Width, m.body.Height
	}
	if !on && m.dw.sleeping && m.dw.sleepW > 0 {
		m.setSize(m.dw.sleepW, m.dw.sleepH)
	}
	m.dw.sleeping = on
	m.SetFlag(behavior.FlagSleeping, on)
}

// SetSleepCollider sets the box while lying down.
func (m *Mob) SetSleepCollider(w, h float32) {
	m.body.Vel = behavior.Vec3{}
	m.setSize(w, h)
}

// SetSleepYaw turns the body, head and look while lying down.
func (m *Mob) SetSleepYaw(yaw float32) { m.yaw, m.headYaw, m.bodyYaw = yaw, yaw, yaw }

// HealOnWake heals what the mob is missing.
func (m *Mob) HealOnWake() { m.health = m.maxHealth }

// StandUpSpot is where the mob gets out of the bed at bed: the free spot next to either
// half nearest to where it lay down from, with room for its feet and head on something
// solid.
func (m *Mob) StandUpSpot(bed behavior.Pos, from behavior.Vec3) (behavior.Pos, bool) {
	dir, ok := m.BedDirection(bed)
	if !ok {
		return behavior.Pos{}, false
	}
	foot := bed
	switch dir & 3 {
	case 0:
		foot.Z--
	case 1:
		foot.X++
	case 2:
		foot.Z++
	case 3:
		foot.X--
	}
	w := goalWorld{&m.v}
	best, bestD, found := behavior.Pos{}, float32(0), false
	for _, half := range [2]behavior.Pos{bed, foot} {
		for dx := int32(-1); dx <= 1; dx++ {
			for dz := int32(-1); dz <= 1; dz++ {
				p := behavior.Pos{X: half.X + dx, Y: half.Y, Z: half.Z + dz}
				if p == bed || p == foot || !m.standable(w, p) {
					continue
				}
				fx, fz := float32(p.X)+0.5-from.X, float32(p.Z)+0.5-from.Z
				if d := fx*fx + fz*fz; !found || d < bestD {
					best, bestD, found = p, d, true
				}
			}
		}
	}
	return best, found
}

// standable reports room for feet and head at p over a solid block.
func (m *Mob) standable(w goalWorld, p behavior.Pos) bool {
	below := behavior.Pos{X: p.X, Y: p.Y - 1, Z: p.Z}
	head := behavior.Pos{X: p.X, Y: p.Y + 1, Z: p.Z}
	return w.Block(below).Solid && !w.Block(p).BlocksMotion && !w.Block(head).BlocksMotion
}

// --- behavior.Worker and Indoorer ---

// RainingAt reports rain falling on p.
func (m *Mob) RainingAt(p behavior.Pos) bool {
	if m.v.r == nil {
		return false
	}
	return m.rt.Env.RainingAt(m.v.r, cube.Pos{int(p.X), int(p.Y), int(p.Z)})
}

// PlayWorkSound plays a job site's sound at the mob.
func (m *Mob) PlayWorkSound(sound string) {
	if n := (resolver{m}).Sound(sound); n != behavior.NoSound {
		m.do(op{kind: opSound, n: int32(n), at: m.Eyes()}) //nolint:gosec // sound numbers are small
	}
}

// StampWork tells the village the mob worked. Helix keeps no work stamps yet: they feed
// the iron golem spawn, which is not built.
func (m *Mob) StampWork() {}

// WantsShelter reports a reason to be indoors: night or rain on the mob.
func (m *Mob) WantsShelter() bool {
	if m.v.r == nil {
		return false
	}
	return m.rt.Env.SkyDarken(m.v.r) >= nightDarken || m.RainingAt(blockAt(m.body.Pos))
}

func blockAt(p behavior.Vec3) behavior.Pos {
	return behavior.Pos{X: floor32(p.X), Y: floor32(p.Y), Z: floor32(p.Z)}
}

// --- behavior.Hider ---

// Hide is the HideComponent's flags; ok is false without minecraft:hide.
func (m *Mob) Hide() (hiding, triggered, ok bool) {
	comps := m.inst.Components()
	if comps == nil || comps[componentHide] == nil {
		return false, false, false
	}
	return m.dw.hiding, m.dw.triggered, true
}

// SetHide replaces the HideComponent's flags.
func (m *Mob) SetHide(hiding, triggered bool) { m.dw.hiding, m.dw.triggered = hiding, triggered }

// Raid reports a raid on the mob's village. Raids are not built.
func (m *Mob) Raid() (exists, running bool) { return false, false }

// CanReach reports a path to p.
func (m *Mob) CanReach(p behavior.Vec3) bool {
	if m.nv == nil {
		return false
	}
	return m.navigate.CanPathTo(p)
}

// --- behavior.PlayerTrader ---

// TradingPlayer is the player in the mob's trade window, NoID for none.
func (m *Mob) TradingPlayer() behavior.ID { return m.dw.trading }

// TradeInterrupted reports the entity that cut the trade short still being there; nothing
// does yet.
func (m *Mob) TradeInterrupted() bool { return false }

// --- behavior.DoorUser ---

// UsesDoors reports minecraft:annotation.open_door.
func (m *Mob) UsesDoors() bool {
	return m.states != nil && m.states.Markers[markerOpenDoor]
}

// PathNodes is the navigation's path and the node it walks to.
func (m *Mob) PathNodes() ([]behavior.Pos, int, bool) {
	if m.nv == nil {
		return nil, 0, false
	}
	p, i := m.nv.Path()
	if p == nil {
		return nil, 0, false
	}
	m.pathNodes = m.pathNodes[:0]
	for _, pt := range p.Points {
		m.pathNodes = append(m.pathNodes, behavior.Pos(pt.Pos))
	}
	return m.pathNodes, i, true
}

// Door is the door at p.
func (m *Mob) Door(p behavior.Pos) (behavior.DoorState, bool) {
	info := m.rt.Blocks.Path(m.v.block(path.Pos(p), 0))
	if info == nil || info.Props&path.PropDoor == 0 || info.Kind == path.KindIronDoor {
		return behavior.DoorState{}, false
	}
	w := goalWorld{&m.v}
	dir, _ := w.BlockState(p, "direction")
	open, _ := w.BlockState(p, "open_bit")
	hinge, _ := w.BlockState(p, "door_hinge_bit")
	return behavior.DoorState{Direction: dir, Open: open != 0, Hinge: hinge != 0}, true
}

// SetDoorOpen opens or closes the door at p.
func (m *Mob) SetDoorOpen(p behavior.Pos, open bool) {
	n := int32(0)
	if open {
		n = 1
	}
	m.do(op{kind: opDoor, pos: p, n: n})
}

// Width is the mob's box width.
func (m *Mob) Width() float32 { return m.body.Width }

// Difficulty is the level's difficulty.
func (m *Mob) Difficulty() int { return int(m.rt.Env.Difficulty()) }

// DoorEvent shows a level event at p with data.
func (m *Mob) DoorEvent(id int32, p behavior.Vec3, data int32) {
	m.do(op{kind: opDoorEvent, n: id, at: p, amount: float32(data)})
}

var (
	_ behavior.Sleeper      = (*Mob)(nil)
	_ behavior.Worker       = (*Mob)(nil)
	_ behavior.Indoorer     = (*Mob)(nil)
	_ behavior.Hider        = (*Mob)(nil)
	_ behavior.PlayerTrader = (*Mob)(nil)
	_ behavior.DoorUser     = (*Mob)(nil)
)
