package player

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// soundEvent is a sound played in the world, BDS's LevelSoundEvent. It is the one place
// that packet is written, so a protocol change to it is made here.
type soundEvent struct {
	// name is the sound, a packet.SoundEvent constant.
	name string
	pos  mgl32.Vec3
	// extra is the sound's data: a block's network hash for block sounds, -1 for none.
	extra int32
	// entity is the type of the entity making it, "" for none.
	entity string
}

// packet is the sound as the client gets it.
func (s soundEvent) packet() *packet.LevelSoundEvent {
	return &packet.LevelSoundEvent{SoundType: s.name, Position: s.pos, ExtraData: s.extra, EntityType: s.entity}
}

// handleLevelSoundEvent is a sound the client made itself, such as a sweet berry bush
// rustling as the player moves through it. BDS 1.26.60's handler (0x8CDBF50, through
// 0xCDD91F0 with no player left out) plays it as sent to every player near its position,
// the sender too: the client does not play these itself but waits for the server's copy.
// A global one goes to every player. Runs: the player's region.
func handleLevelSoundEvent(p *Player, pk *packet.LevelSoundEvent) {
	if p.entity == nil {
		return
	}
	out := &packet.LevelSoundEvent{
		SoundType: pk.SoundType, Position: pk.Position, ExtraData: pk.ExtraData, EntityType: pk.EntityType,
		BabyMob: pk.BabyMob, DisableRelativeVolume: pk.DisableRelativeVolume,
	}
	if pk.DisableRelativeVolume {
		p.s.Broadcast(out)
		return
	}
	if r := p.entity.Region(); r != nil {
		r.Broadcast(floorPos(pk.Position), out, nil)
	}
}

// playSound plays s to every player near at, except the one given. Runs: owner of the
// region.
func playSound(r *world.Region, at cube.Pos, s soundEvent, except *world.Viewer) {
	r.Broadcast(at, s.packet(), except)
}

// blockSound is a sound a block makes, carrying the block as BDS sends it.
func (s *Host) blockSound(name string, pos mgl32.Vec3, block uint32) soundEvent {
	return soundEvent{name: name, pos: pos, extra: int32(s.reg.Blocks.Hash(block))} //nolint:gosec // hashes are sent as int32
}

// blockCenter is the middle of the block at pos, where most block sounds play.
func blockCenter(pos cube.Pos) mgl32.Vec3 { return blockVec(pos).Add(mgl32.Vec3{0.5, 0.5, 0.5}) }

// by is the sound made by an entity of the given type.
func (s soundEvent) by(entity string) soundEvent {
	s.entity = entity
	return s
}
