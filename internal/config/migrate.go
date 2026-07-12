// migrate.go — surgical format-migration for operator-owned agents.toml
// (epic:substrate-convergence order:7). harness= → command=; add missing role=.
// Comment-preserving line edits only — never a TOML marshal round-trip.
package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// MigrateAgents rewrites format drift in an agents.toml body:
//   - harness = X  →  command = X  (skipped when command= already present)
//   - missing role= → role = "reviewer"|"agent" via ResolvedRole
//
// Returns the migrated content and a list of change notes. When nothing is
// stale, out equals content byte-for-byte and changes is empty (idempotent).
// A parse error returns err; callers must leave the on-disk file intact.
func MigrateAgents(content string) (out string, changes []string, err error) {
	ac, err := decodeAgentsContent(content)
	if err != nil {
		return "", nil, err
	}

	lines := strings.Split(content, "\n")
	// Enumerate table headers from the TEXT so sectionRange matches real headers
	// (including legacy agents.NAME nested form).
	type sec struct {
		header  string // raw table name as in [header]
		logical string // name for ResolvedRole
		binding AgentBinding
	}
	var sections []sec
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, "[") || !strings.HasSuffix(t, "]") {
			continue
		}
		// skip array tables [[…]]
		if strings.HasPrefix(t, "[[") {
			continue
		}
		header := strings.TrimSpace(t[1 : len(t)-1])
		logical, binding := bindingForHeader(ac, header)
		sections = append(sections, sec{header: header, logical: logical, binding: binding})
		_ = i
	}

	changed := false
	var notes []string
	harnessN, roleN := 0, 0

	for _, s := range sections {
		// harness → command
		if !hasKeyInSection(lines, s.header, "command") {
			if renameKeyInSection(lines, s.header, "harness", "command") {
				changed = true
				harnessN++
			}
		}
		// add role=
		if !hasKeyInSection(lines, s.header, "role") {
			role := ResolvedRole(s.logical, s.binding)
			// UpsertKey works on a joined string; rejoin and re-split after each add.
			joined := strings.Join(lines, "\n")
			joined = UpsertKey(joined, s.header, "role", strconv.Quote(role))
			lines = strings.Split(joined, "\n")
			changed = true
			roleN++
		}
	}

	if !changed {
		return content, nil, nil
	}
	if harnessN > 0 {
		notes = append(notes, "harness->command")
	}
	if roleN > 0 {
		notes = append(notes, "added role=")
	}
	return strings.Join(lines, "\n"), notes, nil
}

// decodeAgentsContent parses agents.toml body the same way LoadAgents classifies
// sections. Returns an error when the file is unparseable.
func decodeAgentsContent(content string) (AgentsConfig, error) {
	var raw map[string]toml.Primitive
	md, err := toml.Decode(content, &raw)
	if err != nil {
		return AgentsConfig{}, fmt.Errorf("parse agents.toml: %w", err)
	}
	ac := AgentsConfig{Agents: map[string]AgentBinding{}}
	for key, prim := range raw {
		switch key {
		case "executor":
			var b AgentBinding
			if err := md.PrimitiveDecode(prim, &b); err != nil {
				return AgentsConfig{}, fmt.Errorf("parse [executor]: %w", err)
			}
			ac.Executor = b
		case "reviewer":
			var b AgentBinding
			if err := md.PrimitiveDecode(prim, &b); err != nil {
				return AgentsConfig{}, fmt.Errorf("parse [reviewer]: %w", err)
			}
			ac.Reviewer = b
		case "agents":
			// legacy nested container
			var nested map[string]AgentBinding
			if err := md.PrimitiveDecode(prim, &nested); err != nil {
				return AgentsConfig{}, fmt.Errorf("parse [agents]: %w", err)
			}
			for n, b := range nested {
				ac.Agents[n] = b
			}
		default:
			var b AgentBinding
			if err := md.PrimitiveDecode(prim, &b); err != nil {
				return AgentsConfig{}, fmt.Errorf("parse [%s]: %w", key, err)
			}
			ac.Agents[key] = b
		}
	}
	return ac, nil
}

// bindingForHeader maps a table header to the logical agent name + binding.
func bindingForHeader(ac AgentsConfig, header string) (logical string, b AgentBinding) {
	switch {
	case header == "executor":
		return "executor", ac.Executor
	case header == "reviewer":
		return "reviewer", ac.Reviewer
	case strings.HasPrefix(header, "agents."):
		name := strings.TrimPrefix(header, "agents.")
		return name, ac.Agents[name]
	default:
		return header, ac.Agents[header]
	}
}

// hasKeyInSection reports whether section has an uncommented key= assignment.
func hasKeyInSection(lines []string, section, key string) bool {
	start, end := sectionRange(lines, section)
	if start < 0 {
		return false
	}
	for i := start; i < end; i++ {
		if isKeyLine(lines[i], key) {
			return true
		}
	}
	return false
}

// renameKeyInSection rewrites the key token of oldKey → newKey in section,
// preserving indent, equals, RHS, and any trailing comment. Returns true if a
// line was rewritten.
func renameKeyInSection(lines []string, section, oldKey, newKey string) bool {
	start, end := sectionRange(lines, section)
	if start < 0 {
		return false
	}
	for i := start; i < end; i++ {
		if !isKeyLine(lines[i], oldKey) {
			continue
		}
		// Preserve leading whitespace; replace only the key token.
		ln := lines[i]
		indent := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
		rest := strings.TrimSpace(ln)
		// rest starts with oldKey
		after := rest[len(oldKey):]
		lines[i] = indent + newKey + after
		return true
	}
	return false
}
