package portal

import (
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/world/cube"
)

// endRingReach is how far the end portal frame ring is from the middle of the 3 by 3
// portal: the frames stand on the 5 by 5 square's sides, corners left out.
const endRingReach = 2

// ringOffsets are the twelve frame places around a portal's middle.
var ringOffsets = func() []cube.Pos {
	var out []cube.Pos
	for i := -1; i <= 1; i++ {
		out = append(out,
			cube.Pos{i, 0, -endRingReach}, cube.Pos{i, 0, endRingReach},
			cube.Pos{-endRingReach, 0, i}, cube.Pos{endRingReach, 0, i})
	}
	return out
}()

// IsFrame reports whether block is an end portal frame, and whether it has its eye.
func (p *Portals) IsFrame(block uint32) (frame, eye bool) {
	if p.typeOf(block) != p.frameTy {
		return false, false
	}
	switch v := p.reg.States(block)[stateEye].(type) {
	case uint8:
		return true, v != 0
	case bool:
		return true, v
	}
	return true, false
}

// WithEye is the frame block with its eye in.
func (p *Portals) WithEye(block uint32) (uint32, bool) {
	states := p.reg.States(block)
	next := make(map[string]any, len(states))
	for k, v := range states {
		next[k] = v
	}
	next[stateEye] = uint8(1)
	return p.reg.StateID(nameFrame, next)
}

// OpenEndPortal is the end portal frame's pattern check (BDS sub_CAF30D0 on a 5 by 5
// BlockPattern): when the frame at pos got its eye and it completes a ring of twelve
// frames with eyes around a 3 by 3 square, the square becomes end portal. It reports
// whether a portal opened, with the middle of the square.
//
// BDS also checks that the frames face inward; Helix takes any facing so far.
func (p *Portals) OpenEndPortal(r *world.Region, pos cube.Pos) (cube.Pos, bool) {
	for _, off := range ringOffsets {
		mid := pos.Sub(off)
		if !p.ringDone(r, mid) {
			continue
		}
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				at := mid.Add(cube.Pos{dx, 0, dz})
				if r.SetBlock(at, 0, p.endPortal) {
					r.UpdateNeighbours(at)
				}
			}
		}
		return mid, true
	}
	return cube.Pos{}, false
}

// ringDone reports whether all twelve frames around mid have their eye.
func (p *Portals) ringDone(r *world.Region, mid cube.Pos) bool {
	for _, off := range ringOffsets {
		b, ok := r.Block(mid.Add(off), 0)
		if !ok {
			return false
		}
		if frame, eye := p.IsFrame(b); !frame || !eye {
			return false
		}
	}
	return true
}
