// migrate.go — surgical format-migration for operator-owned agents.toml
// (epic:substrate-convergence order:7). harness= → command=; expand bare
// claude|grok presets to full templates; add missing role=.
// Comment-preserving line edits only — never a TOML marshal round-trip.
package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/bobmcallan/satelle/internal/agentcli"
)

// MigrateAgents rewrites format drift in an agents.toml body:
//   - harness = X  →  command = X  (skipped when command= already present)
//   - bare command = "claude"|"grok" → full multi-token template
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
	harnessN, expandN, roleN := 0, 0, 0

	for _, s := range sections {
		// harness → command
		if !hasKeyInSection(lines, s.header, "command") {
			if renameKeyInSection(lines, s.header, "harness", "command") {
				changed = true
				harnessN++
			}
		}
		// expand bare claude|grok command values to full templates
		if expandBareCommandInSection(lines, s.header) {
			changed = true
			expandN++
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
		// inject_principles → principles (when principles= absent)
		if !hasKeyInSection(lines, s.header, "principles") {
			if inj, ok := injectPrinciplesValue(lines, s.header); ok {
				joined := strings.Join(lines, "\n")
				val := `"session"`
				if !inj {
					val = `"none"`
				}
				joined = UpsertKey(joined, s.header, "principles", val)
				lines = strings.Split(joined, "\n")
				// Drop inject_principles line
				removeKeyInSection(lines, s.header, "inject_principles")
				changed = true
				notes = append(notes, "inject_principles->principles")
			}
		}
	}

	// Flatten [agents.NAME] headers → [NAME] (nested dual-read removed).
	flatN := 0
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "[agents.") && strings.HasSuffix(t, "]") && !strings.HasPrefix(t, "[[") {
			name := strings.TrimSuffix(strings.TrimPrefix(t, "[agents."), "]")
			indent := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
			lines[i] = indent + "[" + name + "]"
			changed = true
			flatN++
		}
	}

	if !changed {
		return content, nil, nil
	}
	if harnessN > 0 {
		notes = append(notes, "harness->command")
	}
	if expandN > 0 {
		notes = append(notes, "expand bare preset")
	}
	if roleN > 0 {
		notes = append(notes, "added role=")
	}
	if flatN > 0 {
		notes = append(notes, "flatten [agents.NAME]")
	}
	return strings.Join(lines, "\n"), notes, nil
}

// injectPrinciplesValue reads inject_principles from a section if present.
func injectPrinciplesValue(lines []string, header string) (bool, bool) {
	start, end := sectionRange(lines, header)
	if start < 0 {
		return false, false
	}
	for i := start; i < end; i++ {
		ln := strings.TrimSpace(lines[i])
		if strings.HasPrefix(ln, "inject_principles") {
			if strings.Contains(ln, "false") {
				return false, true
			}
			if strings.Contains(ln, "true") {
				return true, true
			}
		}
	}
	return false, false
}

// removeKeyInSection blanks the first assignment of key in section (keeps line
// count stable for subsequent ranges; empty lines are fine).
func removeKeyInSection(lines []string, header, key string) {
	start, end := sectionRange(lines, header)
	if start < 0 {
		return
	}
	for i := start; i < end; i++ {
		if isKeyLine(lines[i], key) {
			lines[i] = ""
			return
		}
	}
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

// expandBareCommandInSection rewrites a bare command = "claude"|"grok" value to
// the full multi-token preset template (agentcli.NewRunner). Bare codex is NOT
// expanded — no full template exists; validate rejects it. Preserves indent and
// any trailing comment; rewrites only the quoted RHS. Idempotent: a multi-token
// value is left untouched. Returns true if a line was rewritten.
func expandBareCommandInSection(lines []string, section string) bool {
	start, end := sectionRange(lines, section)
	if start < 0 {
		return false
	}
	for i := start; i < end; i++ {
		if !isKeyLine(lines[i], "command") {
			continue
		}
		ln := lines[i]
		indent := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
		rest := strings.TrimSpace(ln)
		// rest starts with "command"
		afterKey := rest[len("command"):]
		eqIdx := strings.Index(afterKey, "=")
		if eqIdx < 0 {
			return false
		}
		// beforeVal includes the '=' and any spaces after it that we re-emit
		// by reconstructing: key + afterKey[:eqIdx+1] + space + quoted + trailing
		prefix := indent + "command" + afterKey[:eqIdx+1]
		valAndTrail := afterKey[eqIdx+1:]
		// preserve leading spaces after '='
		sp := 0
		for sp < len(valAndTrail) && (valAndTrail[sp] == ' ' || valAndTrail[sp] == '\t') {
			sp++
		}
		spaces := valAndTrail[:sp]
		valPart := valAndTrail[sp:]
		if !strings.HasPrefix(valPart, `"`) {
			return false
		}
		// Find closing unescaped quote.
		endq := 1
		for endq < len(valPart) {
			if valPart[endq] == '"' && valPart[endq-1] != '\\' {
				break
			}
			endq++
		}
		if endq >= len(valPart) {
			return false
		}
		unq, err := strconv.Unquote(valPart[:endq+1])
		if err != nil {
			return false
		}
		fields := strings.Fields(unq)
		if len(fields) != 1 {
			return false // already multi-token (or empty)
		}
		tok := strings.ToLower(fields[0])
		// Expand only claude|grok; bare codex stays for validate to reject.
		if tok != agentcli.CLIClaude && tok != agentcli.CLIGrok {
			return false
		}
		r, err := agentcli.NewRunner(tok)
		if err != nil {
			return false
		}
		full := r.Command()
		trailing := valPart[endq+1:] // space + comment, if any
		lines[i] = prefix + spaces + strconv.Quote(full) + trailing
		return true
	}
	return false
}
