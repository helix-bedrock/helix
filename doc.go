// Package helix is the plugin API of Helix (RFC 0001).
//
// A plugin is a struct that embeds helix.Plugin and has an Info and a Setup
// method. Setup registers listeners, any number per event, with options:
//
//	type Skyblock struct {
//		helix.Plugin
//		islands map[string]*Island
//	}
//
//	func (s *Skyblock) Info() helix.Info {
//		return helix.Info{Name: "Skyblock", Description: "Islands and generators."}
//	}
//
//	func (s *Skyblock) Setup(r *helix.Setup) {
//		r.BlockBreak(s.protect, helix.Priority(event.High))
//		r.BlockBreak(s.ores, helix.OnlyBlock("minecraft:diamond_ore"))
//		r.PlayerJoin(s.kit)
//	}
//
// Plugins are passed to New, which starts the server with them. They can be
// enabled and disabled while the server runs through Server.Plugins. A panic
// in plugin code is recovered, logged with the plugin's name, and never stops
// the server.
package helix
