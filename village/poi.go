// Package village is BDS 1.26.60's villages: the points of interest (beds, bells and job
// sites) villagers claim, the per-dimension manager that finds them around villagers and
// groups them into villages, and the claims, job loss and the trade lock. It knows no block
// or mob by name: the engine (the player package's Host) answers which block is which POI
// (Env) and does what a claim does to a mob (Dweller).
package village

import (
	"maps"
	"strings"

	"github.com/helix-bedrock/entity/behavior"
)

// Kind is a POI's type: BDS's POIType, in the order the goals number them.
type Kind = behavior.POIKind

// The kinds.
const (
	Bed     = behavior.POIBed
	Meeting = behavior.POIMeetingArea
	Job     = behavior.POIJobSite
	kinds   = 3
)

// Def is one kind of POI block: BDS's POI definition (1.26.60 ctor 0xBF27D50; beds and bells
// 0xBEF7940).
type Def struct {
	// Name is the POI's name: "villager" for beds and bells, the profession for a job site.
	// A villager with a preferred profession only takes job sites of that name.
	Name string
	Kind Kind
	// Capacity is how many dwellers may own one (+128).
	Capacity int32
	// Weight is the job site's weight in the random pick of an unemployed villager (+136).
	Weight int32
	// Radius is how near counts as there (+124); UseBox makes the goals aim at the block's
	// box instead (+184, beds).
	Radius float32
	UseBox bool
	// Sound is the work sound (+144), "" for none.
	Sound string
	// InitEvent runs on the dweller that claims it (+48); EndEvent on one that loses it
	// while not locked into its profession (+88).
	InitEvent, EndEvent string
}

// Table maps block names to their POI. Read-only once the server runs; change it at boot.
type Table struct {
	byBlock map[string]*Def
}

// unskilled is every job site's end event.
const unskilled = "minecraft:become_unskilled"

// jobSite is a job site row of BDS's registry (0xBEF31F0): capacity 1, radius 2.
func jobSite(name string, weight int32, sound, event string) *Def {
	return &Def{Name: name, Kind: Job, Capacity: 1, Weight: weight, Radius: 2, Sound: sound, InitEvent: event, EndEvent: unskilled}
}

// Vanilla is BDS 1.26.60's POI table (0xBEF31F0).
func Vanilla() *Table {
	bed := &Def{Name: "villager", Kind: Bed, Capacity: 1, Weight: 1, Radius: 0.75, UseBox: true}
	bell := &Def{Name: "villager", Kind: Meeting, Capacity: 20, Weight: 1, Radius: 7}
	armorer := jobSite("armorer", 5, "block.blastfurnace.fire_crackle", "minecraft:become_armorer")
	butcher := jobSite("butcher", 5, "block.smoker.smoke", "minecraft:become_butcher")
	t := &Table{byBlock: map[string]*Def{
		"minecraft:bed":               bed,
		"minecraft:bell":              bell,
		"minecraft:composter":         jobSite("farmer", 1, "block.composter.fill", "minecraft:become_farmer"),
		"minecraft:lectern":           jobSite("librarian", 8, "item.book.put", "minecraft:become_librarian"),
		"minecraft:grindstone":        jobSite("weaponsmith", 5, "block.grindstone.use", "minecraft:become_weaponsmith"),
		"minecraft:blast_furnace":     armorer,
		"minecraft:lit_blast_furnace": armorer,
		"minecraft:smoker":            butcher,
		"minecraft:lit_smoker":        butcher,
		"minecraft:fletching_table":   jobSite("fletcher", 5, "block.fletching_table.use", "minecraft:become_fletcher"),
		"minecraft:cartography_table": jobSite("cartographer", 6, "block.cartography_table.use", "minecraft:become_cartographer"),
		"minecraft:brewing_stand":     jobSite("cleric", 8, "potion.brewed", "minecraft:become_cleric"),
		"minecraft:smithing_table":    jobSite("toolsmith", 5, "block.smithing_table.use", "minecraft:become_toolsmith"),
		"minecraft:cauldron":          jobSite("leatherworker", 5, "bucket.fill.water", "minecraft:become_leatherworker"),
		"minecraft:barrel":            jobSite("fisherman", 3, "block.barrel.open", "minecraft:become_fisherman"),
		"minecraft:loom":              jobSite("shepherd", 3, "block.loom.use", "minecraft:become_sheperd"),
		"minecraft:stonecutter_block": jobSite("mason", 3, "block.stonecutter.use", "minecraft:become_mason"),
	}}
	return t
}

// Clone returns a copy whose rows can be changed without touching t.
func (t *Table) Clone() *Table { return &Table{byBlock: maps.Clone(t.byBlock)} }

// Set makes a block a POI; nil takes the block out of the table.
func (t *Table) Set(block string, d *Def) {
	block = full(block)
	if d == nil {
		delete(t.byBlock, block)
		return
	}
	t.byBlock[block] = d
}

// Lookup is the POI of a block name.
func (t *Table) Lookup(block string) (*Def, bool) {
	d, ok := t.byBlock[full(block)]
	return d, ok
}

// find is a POI of the table with the name and kind, nil for none.
func (t *Table) find(name string, kind Kind) *Def {
	for _, d := range t.byBlock {
		if d.Name == name && d.Kind == kind {
			return d
		}
	}
	return nil
}

func full(name string) string {
	if !strings.Contains(name, ":") {
		return "minecraft:" + name
	}
	return name
}
