// Package workflow parses the authored DOT graph that defines the lifecycle.
// State semantics are DERIVED from this graph, never hardcoded by name.
package workflow

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Node is one workflow step and the agent it allocates.
type Node struct {
	Name  string
	Agent string
	Shape string
}

// Graph is the parsed lifecycle.
type Graph struct {
	Nodes map[string]Node
	Edges [][2]string
}

var (
	nodeRE = regexp.MustCompile(`(?m)^\s*([a-z_]+)\s*\[([^\]]*)\]`)
	attrRE = regexp.MustCompile(`(\w+)\s*=\s*"?([^",\]]+)"?`)
	edgeRE = regexp.MustCompile(`([a-z_]+)\s*->\s*([a-z_]+)`)
)

// ParseFile reads and parses a DOT workflow.
func ParseFile(path string) (Graph, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Graph{}, err
	}
	return Parse(string(raw))
}

// Parse builds the Graph from DOT source.
func Parse(src string) (Graph, error) {
	g := Graph{Nodes: map[string]Node{}}
	for _, m := range nodeRE.FindAllStringSubmatch(src, -1) {
		node := Node{Name: m[1]}
		for _, a := range attrRE.FindAllStringSubmatch(m[2], -1) {
			switch a[1] {
			case "agent":
				node.Agent = strings.TrimSpace(a[2])
			case "shape":
				node.Shape = strings.TrimSpace(a[2])
			}
		}
		g.Nodes[node.Name] = node
	}
	for _, m := range edgeRE.FindAllStringSubmatch(src, -1) {
		g.Edges = append(g.Edges, [2]string{m[1], m[2]})
	}
	if len(g.Nodes) == 0 {
		return Graph{}, fmt.Errorf("workflow declares no nodes")
	}
	return g, nil
}

// PerformingStates are the states whose node allocates a performing agent —
// derived from the graph's agent= attributes, so renaming a state cannot
// silently change the guard.
func (g Graph) PerformingStates() map[string]bool {
	states := map[string]bool{}
	for name, node := range g.Nodes {
		if node.Agent != "" && node.Shape == "" {
			states[name] = true
		}
	}
	return states
}

// Terminal reports whether a state has no outgoing edge.
func (g Graph) Terminal(state string) bool {
	for _, e := range g.Edges {
		if e[0] == state {
			return false
		}
	}
	return true
}
