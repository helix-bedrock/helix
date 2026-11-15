package commands

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/helix-bedrock/helix/player"
)

// /give's components argument as BDS 1.26.60 reads it (GiveCommand::execute 0x9FC1C30, the
// item's component update 0xC8F57A0): a JSON object whose keys are component names,
// minecraft's when they have no namespace. can_place_on and can_destroy take {"blocks":
// [names]}, a list that is replaced whole; a block that does not exist leaves the list empty
// and fails. item_lock and keep_on_death are BDS components Helix does not build yet.
const (
	componentCanPlaceOn  = "minecraft:can_place_on"
	componentCanDestroy  = "minecraft:can_destroy"
	componentItemLock    = "minecraft:item_lock"
	componentKeepOnDeath = "minecraft:keep_on_death"
	componentBlocksKey   = "blocks"
)

// applyComponents sets the components of text on item. The error is the message BDS gives,
// or "" for BDS's commands.generic.componentError (text is not JSON or is null).
func applyComponents(h *player.Host, item player.ItemStack, text string) (player.ItemStack, string, bool) {
	var raw any
	if err := json.Unmarshal([]byte(text), &raw); err != nil || raw == nil {
		return item, "", false
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	obj, ok := orderedObject(dec)
	if !ok || len(obj) == 0 {
		return item, "No data was provided.", false
	}
	for _, kv := range obj {
		name := kv.key
		if !strings.Contains(name, ":") {
			name = "minecraft:" + name
		}
		var err string
		item, err = applyComponent(h, item, name, kv.value)
		if err != "" {
			return item, err, false
		}
	}
	return item, "", true
}

// applyComponent sets one component, or returns BDS's message for why it could not.
func applyComponent(h *player.Host, item player.ItemStack, name string, value json.RawMessage) (player.ItemStack, string) {
	var fields map[string]json.RawMessage
	switch name {
	case componentCanPlaceOn, componentCanDestroy:
	case componentItemLock, componentKeepOnDeath:
		return item, "Component '" + name + "' is not built in Helix yet."
	default:
		return item, "Component '" + name + "' is not a supported component."
	}
	if err := json.Unmarshal(value, &fields); err != nil || fields == nil {
		return item, "Component '" + name + "' was not an object."
	}
	blocks, ok := componentBlocks(h, fields[componentBlocksKey])
	if !ok {
		blocks = nil
	}
	if name == componentCanPlaceOn {
		item = item.WithCanPlaceOn(blocks)
	} else {
		item = item.WithCanDestroy(blocks)
	}
	if !ok {
		return item, "Component '" + name + "' could not be updated."
	}
	return item, ""
}

// componentBlocks reads a list of block names, minecraft's when they have no namespace. It
// fails on anything but a non-empty array of known block names.
func componentBlocks(h *player.Host, raw json.RawMessage) ([]string, bool) {
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil || len(names) == 0 {
		return nil, false
	}
	for i, n := range names {
		if !strings.Contains(n, ":") {
			n = "minecraft:" + n
		}
		if !h.BlockExists(n) {
			return nil, false
		}
		names[i] = n
	}
	return names, true
}

// keyValue is one field of a JSON object.
type keyValue struct {
	key   string
	value json.RawMessage
}

// orderedObject reads a JSON object's fields in the order written, as BDS walks them.
func orderedObject(dec *json.Decoder) ([]keyValue, bool) {
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	var out []keyValue
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, ok := t.(string)
		if !ok {
			return nil, false
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, false
		}
		out = append(out, keyValue{key, v})
	}
	return out, true
}
