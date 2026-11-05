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
