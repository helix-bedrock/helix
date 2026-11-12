package fire

import (
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Candles and candle cakes lighting and going out, from BDS 1.26.60's AbstractCandleBlock
// (CandleBlock vtable 0xFB9D380, CandleCakeBlock 0xFB9D7F8). canLight (0xCCF3250): not lit
// and no water in the block's liquid layer. tryLight (0xCB45EC0, the cake's 0xCB481E0) sets
// lit with the ignite sound at the block's corner; extinguish (0xCCF3350 then 0xCCF3580) clears
// it with extinguish.candle there. Water reaching a lit one puts it out (neighborChanged
// 0xCCF3010). Clicks, placing and eating are the player package's.
//
// Not done: the smoke particles extinguish shows per candle (0xCCF7140) and the cake's
// particle when lit (LevelEvent through vfunc 2032), and a burning projectile lighting a candle
// (slot 133, 0xCCF37D0); Helix's projectiles do not ask blocks yet.

// candleLit is the lit state of a candle or candle cake.
const candleLit = "lit"

// IsCandle reports whether block is a candle or a candle cake, of any colour.
func (t *Ticker) IsCandle(block uint32) bool { return t.is(block, isCandle) }

// CandleLit reports whether block is a lit candle or candle cake.
func (t *Ticker) CandleLit(block uint32) bool {
	if !t.is(block, isCandle) {
		return false
	}
	v, _ := t.blocks.States(block)[candleLit].(uint8)
	return v != 0
}

// LightCandle is BDS's tryLight: an unlit candle or candle cake with no water in its block
// lights. It reports whether it lit. Runs: owner of the region.
func (t *Ticker) LightCandle(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	if !ok || !t.is(b, isCandle) || t.CandleLit(b) || t.WaterIn(r, pos) {
		return false
	}
	return t.setCandle(r, pos, b, 1, packet.SoundEventIgnite)
}

// ExtinguishCandle is BDS's extinguish: a lit candle or candle cake goes out. It reports
// whether it went out. Runs: owner of the region.
func (t *Ticker) ExtinguishCandle(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	if !ok || !t.CandleLit(b) {
		return false
	}
	return t.setCandle(r, pos, b, 0, packet.SoundEventExtinguishCandle)
}

// candleChanged is a candle's onPlace and neighborChanged: water in its block puts it out.
func (t *Ticker) candleChanged(r *world.Region, pos cube.Pos) {
	if t.WaterIn(r, pos) {
		t.ExtinguishCandle(r, pos)
	}
}

// candleAt reports whether the block at pos is a candle or candle cake.
func (t *Ticker) candleAt(r *world.Region, pos cube.Pos) bool {
	b, ok := r.Block(pos, 0)
	return ok && t.is(b, isCandle)
}

// setCandle sets the lit state, playing sound at the block's corner as BDS does.
func (t *Ticker) setCandle(r *world.Region, pos cube.Pos, b uint32, lit uint8, sound string) bool {
	states := t.blocks.States(b)
	next := make(map[string]any, len(states))
	for k, v := range states {
		next[k] = v
	}
	next[candleLit] = lit
	id, ok := t.blocks.StateID(t.blocks.Name(b), next)
	if !ok {
		return false
	}
	r.Broadcast(pos, &packet.LevelSoundEvent{
		SoundType: sound, Position: mgl32.Vec3{float32(pos[0]), float32(pos[1]), float32(pos[2])}, ExtraData: -1,
	}, nil)
	t.set(r, pos, id)
	return true
}

// candleName reports whether name is a candle or candle cake block, of any colour.
func candleName(name string) bool {
	return strings.HasSuffix(name, "candle") || strings.HasSuffix(name, "candle_cake")
}
