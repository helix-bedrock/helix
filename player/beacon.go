package player

import (
	"maps"
	"slices"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Beacons as BDS 1.26.60 runs them: BeaconBlockActor (vtable 0xFBFC5F0: tick 0xD433070,
// load 0xD4368F0, constructor 0xD4324E0 with the effect tiers) and the payment of its screen
// (ScreenHandlerBeacon in 1.21.3's names). The research note is private.
//
// Every 80 ticks the beacon counts the full layers of beacon base blocks under it (up to 4),
// checks its column is loaded and that no opaque block other than bedrock stops the beam, and
// gives the players in range its effects. Effects are chosen in its screen: the client pays
// one payment item in UI slot 27 with a BeaconPayment action and consumes it.
const (
	nameBeacon       = "minecraft:beacon"
	beaconEntityID   = "Beacon"
	beaconPrimaryTag = "primary"
	beaconSecondTag  = "secondary"

	// beaconInterval is the ticks between two updates.
	beaconInterval = 80
	// beaconLayers is the most layers a pyramid counts.
	beaconLayers = 4
	// beaconPaymentSlot is the payment's slot in the player's UI slots.
	beaconPaymentSlot = 27
	// beaconMaxEffect is the highest effect ID a payment may name.
	beaconMaxEffect = 36
	// effectRegeneration is the only secondary effect besides the primary's second level.
	effectRegeneration = 10
	// beaconDurationBase and beaconDurationPerLevel are the effects' ticks: 180 + 40 a level.
	beaconDurationBase     = 180
	beaconDurationPerLevel = 40
	// opaqueDampening is the light dampening of a block that stops the beam.
	opaqueDampening = 15
)

// beaconBases are the blocks a pyramid is built of.
var beaconBases = []string{
	"minecraft:iron_block", "minecraft:gold_block", "minecraft:emerald_block",
	"minecraft:diamond_block", "minecraft:netherite_block",
}

// beaconPayments are the items the screen takes.
var beaconPayments = []string{
	"minecraft:iron_ingot", "minecraft:gold_ingot", "minecraft:emerald",
	"minecraft:diamond", "minecraft:netherite_ingot",
}

// beaconTiers are the effects each layer count opens, BDS's lists built in the constructor:
// speed and haste, resistance and jump boost, strength, regeneration.
var beaconTiers = [beaconLayers][]int32{{1, 3}, {11, 8}, {5}, {effectRegeneration}}

// beaconRanges are the reach of the effects by layers (BDS's 20, 30, 40, 50 plus one).
var beaconRanges = [beaconLayers]float32{21, 31, 41, 51}

// Beacon is a beacon's block entity.
type Beacon struct {
	s     *Host
	saved map[string]any
	// primary and secondary are the chosen effects, 0 for none.
	primary, secondary int32
	// levels is the full layers found at the last update; active whether the beam is up.
	levels int32
	active bool
	timer  int32
	chunk  world.ChunkHandle
	// found is the update's buffer of entities near it.
	found []*world.Entity
}

// loadBeacon brings back a saved beacon.
func (s *Host) loadBeacon(saved map[string]any) *Beacon {
	b := &Beacon{s: s, saved: maps.Clone(saved)}
	b.primary, _ = saved[beaconPrimaryTag].(int32)
	b.secondary, _ = saved[beaconSecondTag].(int32)
	delete(b.saved, beaconPrimaryTag)
	delete(b.saved, beaconSecondTag)
	return b
}

// BindBlockEntity keeps the chunk the beacon is in.
func (b *Beacon) BindBlockEntity(h world.ChunkHandle) { b.chunk = h }

// SaveData is what BDS saves of a beacon: its two effects.
func (b *Beacon) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(b.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = beaconEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	m[beaconPrimaryTag] = b.primary
	m[beaconSecondTag] = b.secondary
	return m
}

// NetworkData is what is saved: the screen shows the effects from it.
func (b *Beacon) NetworkData(pos cube.Pos) map[string]any { return b.SaveData(pos) }

// Levels returns the full layers found at the last update.
func (b *Beacon) Levels() int32 { return b.levels }

// Effects returns the chosen effects, 0 for none.
func (b *Beacon) Effects() (primary, secondary int32) { return b.primary, b.secondary }

// TickBlockEntity is BDS's tick: an update every 80 ticks.
func (b *Beacon) TickBlockEntity(r *world.Region, pos cube.Pos) {
	b.timer--
	if b.timer > 0 {
		return
	}
	b.timer = beaconInterval
	b.update(r, pos)
}

// update counts the layers, checks the beam, plays the sound of a beacon turning on or off
// and gives the effects.
func (b *Beacon) update(r *world.Region, pos cube.Pos) {
	was := b.levels
	levels, loaded := b.s.beaconLayers(r, pos)
	b.levels = levels
	b.active = loaded && b.s.beamClear(r, pos)
	if (was > 0) != (levels > 0) {
		name := packet.SoundEventBeaconActivate
		if levels <= 0 {
			name = packet.SoundEventBeaconDeactivate
		}
		playSound(r, pos, soundEvent{name: name, pos: blockVec(pos), extra: -1}, nil)
	}
	if b.active && b.levels > 0 {
		b.applyEffects(r, pos)
	}
}

// beaconLayers counts the full layers of base blocks under pos, stopping at the first that is
// not. loaded is false when the block it stopped at is not loaded, which BDS takes as off.
func (s *Host) beaconLayers(r *world.Region, pos cube.Pos) (levels int32, loaded bool) {
	for layer := 1; layer <= beaconLayers; layer++ {
		for dx := -layer; dx <= layer; dx++ {
			for dz := -layer; dz <= layer; dz++ {
				id, ok := r.Block(pos.Add(cube.Pos{dx, -layer, dz}), 0)
				if !ok {
					return levels, false
				}
				if !slices.Contains(beaconBases, s.reg.Blocks.Name(id)) {
					return levels, true
				}
			}
		}
		levels++
	}
	return levels, true
}

// beamClear is whether the column above is loaded to the top and no opaque block but
// bedrock stops the beam.
func (s *Host) beamClear(r *world.Region, pos cube.Pos) bool {
	top := r.Dimension().Range().Max()
	for y := pos[1] + 1; y <= top; y++ {
		id, ok := r.Block(cube.Pos{pos[0], y, pos[2]}, 0)
		if !ok {
			return false
		}
		if s.reg.Blocks.Properties(id).LightDampening >= opaqueDampening && s.reg.Blocks.Name(id) != nameBedrock {
			return false
		}
	}
	return true
}

// available is BDS's isEffectAvailable: the effect is in a tier the layers open.
func (b *Beacon) available(effect int32) bool {
	for t := 0; t < int(b.levels) && t < beaconLayers; t++ {
		if slices.Contains(beaconTiers[t], effect) {
			return true
		}
	}
	return false
}

// applyEffects is BDS's _applyEffects: the primary, and with four layers a secondary that is
// regeneration or the primary again, which then gives the primary at level II instead.
func (b *Beacon) applyEffects(r *world.Region, pos cube.Pos) {
	var effects []int32
	if b.available(b.primary) {
		effects = append(effects, b.primary)
	}
	if b.levels == beaconLayers && b.available(b.secondary) && (b.secondary == effectRegeneration || b.secondary == b.primary) {
		effects = append(effects, b.secondary)
	}
	if len(effects) == 0 {
		return
	}
	amplifier := int32(0)
	if len(effects) == 2 && effects[0] == effects[1] {
		amplifier, effects = 1, effects[:1]
	}
	reach := beaconRanges[min(b.levels, beaconLayers)-1]
	at := blockVec(pos)
	duration := beaconDurationBase + beaconDurationPerLevel*b.levels
	b.found = r.AppendEntitiesIn(b.found[:0], at[0]-reach, at[2]-reach, at[0]+reach, at[2]+reach)
	defer clear(b.found)
	for _, e := range b.found {
		p, ok := e.Owner().(*Player)
		if !ok || p.dead() || !inBeaconReach(e.Position(), at, reach) {
			continue
		}
		for i, id := range effects {
			amp := int32(0)
			if i == 0 {
				amp = amplifier
			}
			p.GiveEffect(ActiveEffect{ID: id, Amplifier: amp, Duration: duration, Ambient: true})
		}
		p.flush()
	}
}

// inBeaconReach is BDS's test: within reach across, and above the beacon's floor less the
// reach, with no limit upwards.
func inBeaconReach(p, at mgl32.Vec3, reach float32) bool {
	return p[0] > at[0]-reach && p[0] < at[0]+reach && p[1] > at[1]-reach &&
		p[2] > at[2]-reach && p[2] < at[2]+reach
}

// useBeacon opens the beacon's screen.
func (p *Player) useBeacon(r *world.Region, u *BlockUse) bool {
	p.openStation(r, u, ContainerBeacon, byte(protocol.ContainerTypeBeacon))
	return true
}

// beaconPayment is a payment a running request made, set on the beacon once it passes.
type beaconPayment struct {
	pending            bool
	primary, secondary int32
}

// payBeacon is BDS's _handleBeaconPaymentAction: an open beacon, a primary from 1 to 36 its
// layers open, a secondary only with four layers, and an item in the payment slot. The item
// must be consumed by the request's own actions.
func (p *Player) payBeacon(a *protocol.BeaconPaymentStackRequestAction) (uint8, []slotRef) {
	r := p.entity.Region()
	if p.window.kind != ContainerBeacon || r == nil {
		return protocol.ItemStackResponseStatusError, nil
	}
	be, ok := r.BlockEntity(p.window.pos)
	b, isBeacon := be.(*Beacon)
	if !ok || !isBeacon {
		return protocol.ItemStackResponseStatusError, nil
	}
	if a.PrimaryEffect <= 0 || a.PrimaryEffect > beaconMaxEffect || !b.available(a.PrimaryEffect) {
		return protocol.ItemStackResponseStatusError, nil
	}
	if a.SecondaryEffect != 0 {
		if b.levels < beaconLayers || a.SecondaryEffect < 0 || a.SecondaryEffect > beaconMaxEffect || !b.available(a.SecondaryEffect) {
			return protocol.ItemStackResponseStatusError, nil
		}
	}
	pay := p.requests.work.ui[beaconPaymentSlot]
	if pay.Empty() || !slices.Contains(beaconPayments, pay.Name) {
		return protocol.ItemStackResponseStatusError, nil
	}
	p.requests.beacon = beaconPayment{pending: true, primary: a.PrimaryEffect, secondary: a.SecondaryEffect}
	return protocol.ItemStackResponseStatusOK, nil
}

// consumeBeaconPayment is the consume of the payment that follows a payment action.
func (p *Player) consumeBeaconPayment(a *protocol.ConsumeStackRequestAction) (uint8, []slotRef, bool) {
	if !p.requests.beacon.pending || a.Source.Container.ContainerID != protocol.ContainerBeaconPayment {
		return 0, nil, false
	}
	src, ok := p.validate(a.Source, true)
	if !ok || a.Count == 0 || a.Count > src.stack.Count {
		return protocol.ItemStackResponseStatusInvalidRemovedAmount, nil, true
	}
	src.stack.Count -= a.Count
	if src.stack.Count == 0 {
		*src.stack = ItemStack{}
	}
	return protocol.ItemStackResponseStatusOK, []slotRef{src}, true
}

// beaconPaid is BDS's endRequest check: a payment leaves the slot empty.
func (p *Player) beaconPaid() bool {
	return !p.requests.beacon.pending || p.requests.work.ui[beaconPaymentSlot].Empty()
}

// commitBeacon sets a passed payment's effects on the beacon (BDS's postRequest).
func (p *Player) commitBeacon() {
	pay := p.requests.beacon
	p.requests.beacon = beaconPayment{}
	r := p.entity.Region()
	if !pay.pending || r == nil {
		return
	}
	be, ok := r.BlockEntity(p.window.pos)
	b, isBeacon := be.(*Beacon)
	if !ok || !isBeacon {
		return
	}
	if pay.primary > 0 && b.available(pay.primary) {
		b.primary = pay.primary
	}
	if pay.secondary > 0 && b.available(pay.secondary) {
		b.secondary = pay.secondary
	}
	b.chunk.Changed()
	r.SendBlockEntity(p.window.pos)
}
