package player

import (
	"slices"

	"github.com/helix-bedrock/protocol/packet"
)

// BDS 1.26.60's /fog stack on a player (its fog component: push 0x743EA10, pop 0x743EC80,
// remove 0x743EF50, load 0x743FF40): fog settings by ID, each tagged with a user ID, the
// newest drawn on top. Saved as fogCommandStack, the fog then user ID of each in turn.
const (
	// MaxFogs is how many fog settings a player's stack holds.
	MaxFogs = 16
	// FogUntagged is the user ID of a fog pushed without one.
	FogUntagged = "untagged"
	fogStackTag = "fogCommandStack"
)

// fogEntry is one fog setting on a player's stack.
type fogEntry struct{ fog, user string }

// PushFog puts a fog setting on top of the player's stack, tagged user ("" for
// FogUntagged); false when the stack is full. Runs: the player's region.
func (p *Player) PushFog(fog, user string) bool {
	if len(p.fogs) >= MaxFogs {
		return false
	}
	if user == "" {
		user = FogUntagged
	}
	p.fogs = append(p.fogs, fogEntry{fog, user})
	p.sendFogs()
	return true
}

// PopFog takes off the newest fog setting tagged user, or the newest of all for ""; false if
// there was none. Runs: the player's region.
func (p *Player) PopFog(user string) bool {
	for i := len(p.fogs) - 1; i >= 0; i-- {
		if user == "" || p.fogs[i].user == user {
			p.fogs = slices.Delete(p.fogs, i, i+1)
			p.sendFogs()
			return true
		}
	}
	return false
}

// RemoveFogs takes off every fog setting tagged user, or all of them for ""; false if there
// was none. Runs: the player's region.
func (p *Player) RemoveFogs(user string) bool {
	n := len(p.fogs)
	p.fogs = slices.DeleteFunc(p.fogs, func(f fogEntry) bool { return user == "" || f.user == user })
	if len(p.fogs) == n {
		return false
	}
	p.sendFogs()
	return true
}

// Fogs returns the fog settings on the player's stack, oldest first. Runs: the player's region.
func (p *Player) Fogs() []string {
	out := make([]string, len(p.fogs))
	for i, f := range p.fogs {
		out[i] = f.fog
	}
	return out
}

// sendFogs tells the player its fog stack.
func (p *Player) sendFogs() { p.send(&packet.PlayerFog{Stack: p.Fogs()}) }

// loadFogs reads the saved stack: pairs of non-empty strings, at most MaxFogs.
func (p *Player) loadFogs(data map[string]any) {
	var list []string
	switch l := data[fogStackTag].(type) {
	case []string:
		list = l
	case []any:
		for _, v := range l {
			s, _ := v.(string)
			list = append(list, s)
		}
	}
	for i := 0; i+1 < len(list) && len(p.fogs) < MaxFogs; i += 2 {
		if list[i] != "" && list[i+1] != "" {
			p.fogs = append(p.fogs, fogEntry{list[i], list[i+1]})
		}
	}
}

// saveFogs writes the stack as BDS keeps it.
func (p *Player) saveFogs(data map[string]any) {
	list := make([]string, 0, 2*len(p.fogs))
	for _, f := range p.fogs {
		list = append(list, f.fog, f.user)
	}
	data[fogStackTag] = list
}
