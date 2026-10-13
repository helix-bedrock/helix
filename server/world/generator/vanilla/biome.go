package vanilla

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/helix-bedrock/helix-data/data"
	"github.com/helix-bedrock/helix/server/world"
	// Registers the biomes looked up by ID.
	_ "github.com/helix-bedrock/helix/server/world/biome"
)

// biomeNode is a node of Java's biome search tree: a leaf with a biome, or
// a branch, each with the parameter ranges it covers.
type biomeNode struct {
	params   [7][2]int64
	children []*biomeNode
	biome    world.Biome
}

// distance is the squared distance from p to the node's ranges.
func (n *biomeNode) distance(p *climate) int64 {
	var d int64
	for i, r := range n.params {
		v := p[i]
		var k int64
		switch {
		case v < r[0]:
			k = r[0] - v
		case v > r[1]:
			k = v - r[1]
		}
		d += k * k
	}
	return d
}

// biomeSearch finds the biome for a climate, as Java's search tree does:
// starting from the last result, and keeping it unless a leaf is strictly
// closer.
func (n *biomeNode) search(p *climate, last **biomeNode) world.Biome {
	best, bestDist := n, int64(1<<62)
	if *last != nil {
		best, bestDist = *last, (*last).distance(p)
	}
	n.searchInto(p, &best, &bestDist)
	*last = best
	return best.biome
}

func (n *biomeNode) searchInto(p *climate, best **biomeNode, bestDist *int64) {
	d := n.distance(p)
	if d >= *bestDist {
		return
	}
	if n.children == nil {
		*best, *bestDist = n, d
		return
	}
	for _, c := range n.children {
		c.searchInto(p, best, bestDist)
	}
}

// loadBiomeTree reads a dimension's biome tree from helix-data.
func loadBiomeTree(dimension string) (*biomeNode, error) {
	var trees map[string]json.RawMessage
	if err := json.Unmarshal(data.BiomeTree(), &trees); err != nil {
		return nil, fmt.Errorf("vanilla: biome tree: %w", err)
	}
	raw, ok := trees[dimension]
	if !ok {
		return nil, fmt.Errorf("vanilla: no biome tree for %q", dimension)
	}
	return parseBiomeNode(raw)
}

func parseBiomeNode(raw json.RawMessage) (*biomeNode, error) {
	var j struct {
		Params []struct {
			Min int64 `json:"min"`
			Max int64 `json:"max"`
		} `json:"parameters"`
		Type     string            `json:"_type"`
		Biome    string            `json:"biome"`
		Children []json.RawMessage `json:"subTree"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("vanilla: biome tree node: %w", err)
	}
	if len(j.Params) != 7 {
		return nil, fmt.Errorf("vanilla: biome tree node with %d parameters", len(j.Params))
	}
	n := &biomeNode{}
	for i, p := range j.Params {
		n.params[i] = [2]int64{p.Min, p.Max}
	}
	if j.Type == "leaf" {
		id, ok := bedrockBiomeIDs[strings.TrimPrefix(j.Biome, "minecraft:")]
		if !ok {
			return nil, fmt.Errorf("vanilla: no Bedrock ID for biome %q", j.Biome)
		}
		b, ok := world.BiomeByID(id)
		if !ok {
			return nil, fmt.Errorf("vanilla: Helix has no biome %q (Bedrock ID %d)", j.Biome, id)
		}
		n.biome = b
		return n, nil
	}
	for _, c := range j.Children {
		child, err := parseBiomeNode(c)
		if err != nil {
			return nil, err
		}
		n.children = append(n.children, child)
	}
	if len(n.children) == 0 {
		return nil, fmt.Errorf("vanilla: biome tree branch with no children")
	}
	return n, nil
}
