package config

// Hosted-server binding writer (sty_2fc93374). `satelle login` records the
// hosted server URL + project slug in the COMMITTED satelle.toml. We write with
// a surgical [hosted] section upsert — never a full TOML re-encode — so the
// scaffold's comments and any keys this binary does not model survive untouched.
// The OAuth tokens are secrets and never pass through here; they go to the
// per-user credential store outside the repo (internal/hosted).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SaveHostedServer upserts the [hosted] section (server + optional project) into
// the committed config at configPath, preserving all surrounding content. An
// empty configPath falls back to ./<DefaultDataDir>/<ConfigName>. project may be
// empty (the key is then omitted). The file is created if absent.
func SaveHostedServer(configPath, server, project string) error {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" {
		return fmt.Errorf("config: hosted server URL is empty")
	}
	if strings.TrimSpace(configPath) == "" {
		configPath = filepath.Join(DefaultDataDir, ConfigName)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("config: create %s: %w", filepath.Dir(configPath), err)
	}

	var existing string
	if b, err := os.ReadFile(configPath); err == nil {
		existing = string(b)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("config: read %s: %w", configPath, err)
	}

	block := hostedBlock(server, project)
	updated := upsertSection(existing, "hosted", block)
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", configPath, err)
	}
	return nil
}

// hostedBlock renders the [hosted] table body (header + keys), no trailing newline.
func hostedBlock(server, project string) string {
	var b strings.Builder
	b.WriteString("[hosted]\n")
	fmt.Fprintf(&b, "server = %q\n", server)
	if strings.TrimSpace(project) != "" {
		fmt.Fprintf(&b, "project = %q", project)
	} else {
		// trim the trailing newline from the server line so the block has none.
		return strings.TrimRight(b.String(), "\n")
	}
	return b.String()
}

// upsertSection replaces an existing top-level [name] table (from its header
// line up to the next table header or EOF) with block, or appends block when the
// table is absent. Comments and unmodeled tables/keys outside the section are
// preserved verbatim.
func upsertSection(content, name, block string) string {
	header := "[" + name + "]"
	lines := strings.Split(content, "\n")
	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == header {
			start = i
			break
		}
	}
	if start == -1 {
		// Append. Separate from prior content by a single blank line.
		trimmed := strings.TrimRight(content, "\n")
		if trimmed == "" {
			return block + "\n"
		}
		return trimmed + "\n\n" + block + "\n"
	}
	// Find the end: the next line that begins a new table ([...] / [[...]]).
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "[") {
			end = i
			break
		}
	}
	rebuilt := append([]string{}, lines[:start]...)
	rebuilt = append(rebuilt, strings.Split(block, "\n")...)
	// Preserve a blank separator before the following table if one existed.
	rebuilt = append(rebuilt, lines[end:]...)
	return strings.Join(rebuilt, "\n")
}
