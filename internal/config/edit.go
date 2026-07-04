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
