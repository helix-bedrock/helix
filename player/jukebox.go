package player

import (
	"fmt"
	"maps"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// BDS's names for a jukebox.
const (
	jukeboxBlock    = "minecraft:jukebox"
	jukeboxEntityID = "Jukebox"
	recordItemTag   = "RecordItem"
	finishedTag     = "FinishedRecording"
	ticksPlayingTag = "TicksPlaying"
	nowPlaying      = "record.nowPlaying"
	discSoundPrefix = "record."
)

// BDS's numbers for a jukebox.
const (
	jukeboxPower         = 15
	jukeboxNoteTicks     = 20
	jukeboxSecondsInTick = 0.05
	jukeboxNoteHeight    = 1.2
	jukeboxNoteParticle  = "minecraft:note_particle"
)

// loadDiscs reads the music discs.
func loadDiscs() (map[string]data.MusicDisc, error) {
	discs, err := data.MusicDiscData()
	if err != nil {
		return nil, fmt.Errorf("player: music discs: %w", err)
	}
	return discs, nil
}

// jukeboxEntity is a jukebox's block entity. The client plays the song itself when this
// reaches it with a disc not finished, and stops when the disc is gone.
type jukeboxEntity struct {
	s      *Host
	saved  map[string]any
	record ItemStack
	// ticks is how long the disc has been in, and sinceNote how long since the last
	// look at whether the song is over.
	ticks     int64
	sinceNote int32
	finished  bool
}

// loadJukebox brings a jukebox back as BDS does: with its disc, not playing.
func (s *Host) loadJukebox(saved map[string]any) *jukeboxEntity {
	e := &jukeboxEntity{s: s, saved: maps.Clone(saved), finished: true}
	if item, ok := saved[recordItemTag].(map[string]any); ok {
		e.record = withID(itemFromNBT(item), s.nextStackID)
	}
	delete(e.saved, recordItemTag)
	return e
}

// SaveData is what BDS saves of a jukebox.
func (e *jukeboxEntity) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(e.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = jukeboxEntityID
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	if !e.record.Empty() {
		m[recordItemTag] = e.record.nbt(0, false)
	}
	return m
}

// NetworkData is what is saved, and whether the song plays and how far along.
func (e *jukeboxEntity) NetworkData(pos cube.Pos) map[string]any {
	m := e.SaveData(pos)
	m[finishedTag] = uint8(0)
	if e.finished {
		m[finishedTag] = uint8(1)
	}
	m[ticksPlayingTag] = e.ticks
	return m
}

// ComparatorSignal is the disc's signal.
func (e *jukeboxEntity) ComparatorSignal(*world.Region, cube.Pos) int32 {
	return e.s.discs[e.record.Name].Signal
}

// setPower sets the jukebox's redstone power.
func (e *jukeboxEntity) setPower(r *world.Region, pos cube.Pos, power int32) {
	if set := e.s.circuits.JukeboxPower; set != nil {
		set(r, pos, power)
	}
}

// insert puts in a disc and starts the song: BDS's setRecord with power.
func (e *jukeboxEntity) insert(r *world.Region, pos cube.Pos, disc ItemStack) {
	disc.Count = 1
	e.record, e.ticks, e.sinceNote, e.finished = disc, 0, 0, false
	e.setPower(r, pos, jukeboxPower)
	r.SendBlockEntity(pos)
}

// eject throws the disc out at the first free block around, as BDS does. Clients stop
// the song when they get the jukebox without it.
func (e *jukeboxEntity) eject(r *world.Region, pos cube.Pos) {
	if e.record.Empty() {
		return
	}
	disc := e.record
	e.record, e.ticks, e.sinceNote, e.finished = ItemStack{}, 0, 0, false
	e.setPower(r, pos, 0)
	e.s.throwDrops(r, []BlockDrop{NewBlockDrop(e.freeSpot(r, pos), disc)})
	r.SendBlockEntity(pos)
}

// freeSpot is the first block around pos with nothing to collide with, or pos. BDS tests
// a block property not identified; the order of the two z neighbours is also assumed.
func (e *jukeboxEntity) freeSpot(r *world.Region, pos cube.Pos) cube.Pos {
	for _, off := range [...]cube.Pos{{0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {1, 0, 0}, {-1, 0, 0}, {0, -1, 0}} {
		at := pos.Add(off)
		if b, ok := r.Block(at, 0); ok && len(e.s.reg.Blocks.Properties(b).Collision) == 0 {
			return at
		}
	}
	return pos
}

// BlockEntityRemoved throws out the disc of a jukebox that is gone.
func (e *jukeboxEntity) BlockEntityRemoved(r *world.Region, pos cube.Pos) {
	if e.record.Empty() {
		return
	}
	disc := e.record
	e.record = ItemStack{}
	e.s.throwDrops(r, []BlockDrop{NewBlockDrop(e.freeSpot(r, pos), disc)})
}

// TickBlockEntity is BDS's jukebox tick: every second, a note while the song lasts, and
// when it is over the power goes and clients are told.
func (e *jukeboxEntity) TickBlockEntity(r *world.Region, pos cube.Pos) {
	if e.record.Empty() {
		return
	}
	e.ticks++
	e.sinceNote++
	if e.sinceNote < jukeboxNoteTicks {
		return
	}
	e.sinceNote = 0
	disc, ok := e.s.discs[e.record.Name]
	if !ok || e.finished {
		return
	}
	if float64(disc.Seconds) >= float64(e.ticks)*jukeboxSecondsInTick {
		r.Broadcast(pos, &packet.SpawnParticleEffect{
			EntityUniqueID: -1,
			Position:       blockVec(pos).Add(mgl32.Vec3{0.5, jukeboxNoteHeight, 0.5}),
			ParticleName:   jukeboxNoteParticle,
		}, nil)
		return
	}
	e.finished = true
	e.setPower(r, pos, 0)
	r.SendBlockEntity(pos)
}

// useJukebox is a click on a jukebox: a disc inside comes out, and a disc in hand goes
// in. Any other item does not take the click.
func (p *Player) useJukebox(r *world.Region, u *BlockUse) bool {
	be, ok := r.BlockEntity(u.Pos)
	if !ok {
		return false
	}
	e, ok := be.(*jukeboxEntity)
	if !ok {
		return false
	}
	if !e.record.Empty() {
		e.eject(r, u.Pos)
		return true
	}
	disc, ok := p.s.discs[u.Item.Name]
	if !ok {
		return false
	}
	e.insert(r, u.Pos, u.Item)
	if !p.creative() {
		p.useHeld()
	}
	p.send(&packet.Text{
		TextType: packet.TextTypeJukeboxPopup, NeedsTranslation: true, Message: nowPlaying,
		Parameters: []string{"%item.record_" + strings.TrimPrefix(disc.Sound, discSoundPrefix) + ".desc"},
	})
	p.flush()
	return true
}
