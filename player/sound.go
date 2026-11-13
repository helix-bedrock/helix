package player

import (
	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// soundEvent is a sound played in the world, BDS's LevelSoundEvent. It is the one place
// that packet is written, so a protocol change to it is made here.
type soundEvent struct {
	// name is the sound, a packet.SoundEvent constant.
	name string
	pos  mgl64.Vec3
	// extra is the sound's data: a block's network hash for block sounds, -1 for none.
	extra int32
	// entity is the type of the entity making it, "" for none.
	entity string
}

// packet is the sound as the client gets it.
func (s soundEvent) packet() *packet.LevelSoundEvent {
	return &packet.LevelSoundEvent{SoundType: s.name, Position: world.Vec32(s.pos), ExtraData: s.extra, EntityType: s.entity}
}

// handleLevelSoundEvent is a sound the client says it made, such as a sweet berry bush
// rustling as the player moves through it. BDS 1.26.60's handler (0x8CDBF50, through
// 0xCDD91F0 with no player left out) plays whatever is sent, where it says, to every player
// near it, the sender too; the client waits for that copy instead of playing these itself.
//
// Helix goes past BDS here (lead's decision, 2026-11-10): a client could fake any sound,
// anywhere, as often as it likes, so the packet is only a request. Only sounds the server's
// own state confirms are played, at the player's position, never the client's, with the
// server's own data, at most once a tick each; anything else is dropped. Runs: the player's
// region.
func handleLevelSoundEvent(p *Player, pk *packet.LevelSoundEvent) {
	if p.entity == nil || p.dead() {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	var s soundEvent
	switch pk.SoundType {
	case packet.SoundEventSweetBerryBushHurt:
		// The bush scratching: only while the server finds the player in a grown bush.
		if !p.move.inBerry || p.move.soundTick == p.entity.Tick() {
			return
		}
		s = soundEvent{name: pk.SoundType, pos: p.entity.Position(), extra: -1}
		p.move.soundTick = p.entity.Tick()
	default:
		p.s.log.Debug("client sound dropped", "player", p.name, "sound", pk.SoundType)
		return
	}
	playSound(r, p.blockPos(), s, nil)
}

// playSound plays s to every player near at, except the one given. Runs: owner of the
// region.
func playSound(r *world.Region, at cube.Pos, s soundEvent, except *world.Viewer) {
	r.Broadcast(at, s.packet(), except)
}

// blockSound is a sound a block makes, carrying the block as BDS sends it.
func (s *Host) blockSound(name string, pos mgl64.Vec3, block uint32) soundEvent {
	return soundEvent{name: name, pos: pos, extra: int32(s.reg.Blocks.Hash(block))} //nolint:gosec // hashes are sent as int32
}

// blockCenter is the middle of the block at pos, where most block sounds play.
func blockCenter(pos cube.Pos) mgl64.Vec3 { return blockVec(pos).Add(mgl64.Vec3{0.5, 0.5, 0.5}) }

// by is the sound made by an entity of the given type.
func (s soundEvent) by(entity string) soundEvent {
	s.entity = entity
	return s
}
