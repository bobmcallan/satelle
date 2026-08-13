package config

// Per-key config writer for the web settings editor (sty_ffe53865). Like
// SaveHostedServer (hosted.go), it edits the COMMITTED satelle.toml with a
// surgical in-place upsert — never a full TOML re-encode — so comments and any
// keys this binary does not model survive verbatim. SaveHostedServer replaces a
// whole [hosted] table (it removes an omitted project); this writer upserts
// INDIVIDUAL keys, which is what an à-la-carte settings table needs.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeyEdit is one key to write. Section is the TOML table ("" = a root key, above
// the first table header). Value is the already-rendered TOML right-hand side
// (e.g. `"info"`, `8787`, `true`, `["a", "b"]`) — the caller owns typing/quoting.
type KeyEdit struct {
	Section string
	Key     string
	Value   string
}

// SaveConfigValues applies edits to the committed config at configPath, creating
// the file if absent. Each key is upserted in place; unrelated content is
// preserved. An empty configPath falls back to ./<DefaultDataDir>/<ConfigName>.
func SaveConfigValues(configPath string, edits []KeyEdit) error {
	if strings.TrimSpace(configPath) == "" {
		configPath = filepath.Join(DefaultDataDir, ConfigName)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("config: create %s: %w", filepath.Dir(configPath), err)
	}
	var content string
	if b, err := os.ReadFile(configPath); err == nil {
		content = string(b)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("config: read %s: %w", configPath, err)
	}
	for _, e := range edits {
		content = UpsertKey(content, e.Section, e.Key, e.Value)
	}
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", configPath, err)
	}
	return nil
}

// UpsertKey sets `key = value` within section (empty section = the root block
// before the first table header), replacing an existing assignment in place,
// appending the key to an existing section, or appending a new [section].
// Comments and unmodeled keys/tables are preserved.
func UpsertKey(content, section, key, value string) string {
	line := key + " = " + value
	lines := strings.Split(content, "\n")

	start, end := sectionRange(lines, section)
	if start == -1 {
		// Section absent → append a fresh table.
		trimmed := strings.TrimRight(content, "\n")
		block := "[" + section + "]\n" + line
		if trimmed == "" {
			return block + "\n"
		}
		return trimmed + "\n\n" + block + "\n"
	}
	// Replace an existing assignment of key within [start,end).
	for i := start; i < end; i++ {
		if isKeyLine(lines[i], key) {
			lines[i] = line
			return strings.Join(lines, "\n")
		}
	}
	// Not present → insert at the end of the section body (after the last
	// non-blank line within range, so it sits with the section's other keys).
	insertAt := start
	for i := start; i < end; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			insertAt = i + 1
		}
	}
	out := append([]string{}, lines[:insertAt]...)
	out = append(out, line)
	out = append(out, lines[insertAt:]...)
	return strings.Join(out, "\n")
}

// RemoveKey drops a single `key = …` assignment inside section, leaving every
// other line (including comments) verbatim. A missing section or key is a no-op.
// Used by redactForTransmit to strip [hosted] project from a settings push
// (sty_ea18294f) without a full TOML re-encode.
func RemoveKey(content, section, key string) string {
	lines := strings.Split(content, "\n")
	start, end := sectionRange(lines, section)
	if start == -1 {
		return content
	}
	for i := start; i < end; i++ {
		if isKeyLine(lines[i], key) {
			out := append([]string{}, lines[:i]...)
			out = append(out, lines[i+1:]...)
			return strings.Join(out, "\n")
		}
	}
	return content
}

// HasSection reports whether content contains an exact trimmed `[section]` header.
func HasSection(content, section string) bool {
	if strings.TrimSpace(section) == "" {
		return false
	}
	start, _ := sectionRange(strings.Split(content, "\n"), section)
	return start != -1
}

// RemoveSection drops `[section]` and its body (up to the next table header or
// EOF). Missing section is a no-op. One blank-line seam is collapsed so a
// removed middle table does not leave a double gap.
func RemoveSection(content, section string) string {
	if strings.TrimSpace(section) == "" {
		return content
	}
	lines := strings.Split(content, "\n")
	start, end := sectionRange(lines, section)
	if start == -1 {
		return content
	}
	header := start - 1 // sectionRange start is the line AFTER the header
	if header < 0 {
		return content
	}
	out := append([]string{}, lines[:header]...)
	rest := lines[end:]
	// Drop one leading blank of the remainder when the kept prefix already
	// ends on a blank (or is empty), so the seam is a single blank line.
	if len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
		if len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == "" {
			rest = rest[1:]
		}
	}
	out = append(out, rest...)
	return strings.Join(out, "\n")
}

// HasKey reports whether content already assigns `key` inside `section`
// (empty section = root block before the first table header). Distinguishes
// "key absent from the file" from "key present with empty value" — Load cannot
// (sty_c73f8905 AC6 config reconciliation).
func HasKey(content, section, key string) bool {
	lines := strings.Split(content, "\n")
	start, end := sectionRange(lines, section)
	if start == -1 {
		return false
	}
	for i := start; i < end; i++ {
		if isKeyLine(lines[i], key) {
			return true
		}
	}
	return false
}

// ListValueContains reports whether content's assignment of key inside section
// is a TOML string array that includes val (exact match after unquoting). False
// when the key/section is absent, the value is not a list of strings, or val
// is not among the quoted entries. Used by init analysis / migrate to detect
// a missing managed default entry without re-encoding the whole file.
func ListValueContains(content, section, key, val string) bool {
	for _, item := range ListStringValues(content, section, key) {
		if item == val {
			return true
		}
	}
	return false
}

// ListStringValues returns the quoted string entries of a TOML array assignment
// for key inside section, in order. Returns nil when the key is absent or the
// RHS is not a bracketed list. Only double-quoted string elements are collected
// (the shape init seeds for edit_exempt_paths).
func ListStringValues(content, section, key string) []string {
	lines := strings.Split(content, "\n")
	start, end := sectionRange(lines, section)
	if start == -1 {
		return nil
	}
	for i := start; i < end; i++ {
		if !isKeyLine(lines[i], key) {
			continue
		}
		return parseQuotedListRHS(lines[i])
	}
	return nil
}

// parseQuotedListRHS extracts double-quoted strings from a key = [...] line.
func parseQuotedListRHS(line string) []string {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return nil
	}
	rhs := strings.TrimSpace(line[eq+1:])
	if !strings.HasPrefix(rhs, "[") {
		return nil
	}
	// Collect "..." segments; empty list yields a non-nil empty slice so callers
	// can distinguish "key present as []" from "key absent" (nil).
	out := []string{}
	rest := rhs
	for {
		i := strings.Index(rest, `"`)
		if i < 0 {
			break
		}
		rest = rest[i+1:]
		j := strings.Index(rest, `"`)
		if j < 0 {
			break
		}
		out = append(out, rest[:j])
		rest = rest[j+1:]
	}
	return out
}

// sectionRange returns [start,end) line indices for section's body. For the root
// section ("") start is 0 and end is the first table header (or len). For a named
// section start is the line AFTER its header and end is the next header (or len).
// Returns (-1,-1) when a named section's header is absent.
func sectionRange(lines []string, section string) (int, int) {
	if section == "" {
		end := len(lines)
		for i, ln := range lines {
			if isTableHeader(ln) {
				end = i
				break
			}
		}
		return 0, end
	}
	header := "[" + section + "]"
	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == header {
			start = i + 1
			break
		}
	}
	if start == -1 {
		return -1, -1
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if isTableHeader(lines[i]) {
			end = i
			break
		}
	}
	return start, end
}

func isTableHeader(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "[")
}

// isKeyLine reports whether line is an uncommented assignment of key.
func isKeyLine(line, key string) bool {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") || !strings.HasPrefix(t, key) {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(t[len(key):]), "=")
}
