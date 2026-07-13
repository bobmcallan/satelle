package verb

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bobmcallan/satelle/internal/buildinfo"
)

// embeddedChangelog is the binary-shipped CHANGELOG — the only consumer channel
// for `satelle changelog` and the require-init drift gate (sty_b5fa838a).
// Downstream repos never have satelle's source tree; the installed binary is the
// sole satelle artifact. Repo-root CHANGELOG.md is build input (copied into
// this embed at release), not a runtime source for consumers.
//
//go:embed embedded/CHANGELOG.md
var embeddedChangelog string

// changelogPath is the well-known file at the repo root (overrideable in tests).
// Used only as a DEV build-input fallback when the embed is empty (broken build
// tree); product paths always prefer embeddedChangelog.
var changelogPath = func() string {
	// Prefer walking up from CWD for a CHANGELOG.md (repo root).
	wd, err := os.Getwd()
	if err != nil {
		return "CHANGELOG.md"
	}
	dir := wd
	for {
		p := filepath.Join(dir, "CHANGELOG.md")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Join(wd, "CHANGELOG.md")
		}
		dir = parent
	}
}

// readChangelogBody returns the shipped changelog. Prefer the embedded bytes
// always (consumer channel). Fall back to on-disk only when embed is empty so
// a local `go run` against an incomplete tree can still develop; absence of
// both is an error (never silent empty success — sty_b5fa838a AC3).
func readChangelogBody() ([]byte, error) {
	if strings.TrimSpace(embeddedChangelog) != "" {
		return []byte(embeddedChangelog), nil
	}
	path := changelogPath()
	b, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(b)) != "" {
		return b, nil
	}
	if err != nil {
		return nil, fmt.Errorf("changelog unavailable: embedded empty and %s: %w", path, err)
	}
	return nil, fmt.Errorf("changelog unavailable: embedded and %s are both empty", path)
}

func init() {
	Register(&Verb{
		Name:        "changelog",
		Description: "Retrieve changelog entries between two versions (no git history)",
		Invoke:      changelogInvoke,
	})
}

// ChangelogEntry is one release section from CHANGELOG.md.
type ChangelogEntry struct {
	Version  string              `json:"version"`
	Date     string              `json:"date,omitempty"`
	Breaking bool                `json:"breaking"`
	Sections map[string][]string `json:"sections,omitempty"`
	Raw      string              `json:"raw,omitempty"`
}

// ChangelogResult is the changelog verb response.
type ChangelogResult struct {
	From     string           `json:"from,omitempty"`
	To       string           `json:"to,omitempty"`
	Breaking bool             `json:"breaking"`
	Entries  []ChangelogEntry `json:"entries"`
}

func changelogInvoke(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &req)
	}
	to := strings.TrimSpace(req.To)
	if to == "" {
		to = buildinfo.Resolve().Version
	}
	from := strings.TrimSpace(req.From)

	body, err := readChangelogBody()
	if err != nil {
		// AC3: absence is an error, never empty success (sty_b5fa838a).
		return nil, err
	}
	all := parseChangelog(string(body))
	var out []ChangelogEntry
	for _, e := range all {
		if !inRange(e.Version, from, to) {
			continue
		}
		out = append(out, e)
	}
	res := ChangelogResult{From: from, To: to, Entries: out}
	for _, e := range out {
		if e.Breaking {
			res.Breaking = true
			break
		}
	}
	return json.Marshal(res)
}

// parseChangelog parses Keep-a-Changelog style content into entries.
func parseChangelog(body string) []ChangelogEntry {
	var entries []ChangelogEntry
	var cur *ChangelogEntry
	var section string
	flush := func() {
		if cur != nil {
			entries = append(entries, *cur)
			cur = nil
			section = ""
		}
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## [") {
			flush()
			// ## [X.Y.Z] - DATE  or ## [X.Y.Z]
			rest := strings.TrimPrefix(line, "## [")
			end := strings.Index(rest, "]")
			if end < 0 {
				continue
			}
			ver := rest[:end]
			date := ""
			if i := strings.Index(rest[end:], " - "); i >= 0 {
				date = strings.TrimSpace(rest[end+i+3:])
			}
			cur = &ChangelogEntry{
				Version:  ver,
				Date:     date,
				Sections: map[string][]string{},
				Raw:      line,
			}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(line, "### ") {
			section = strings.TrimSpace(strings.TrimPrefix(line, "### "))
			if strings.EqualFold(section, "Breaking") {
				cur.Breaking = true
			}
			cur.Raw += "\n" + line
			continue
		}
		if strings.HasPrefix(line, "- ") && section != "" {
			cur.Sections[section] = append(cur.Sections[section], strings.TrimSpace(strings.TrimPrefix(line, "- ")))
			cur.Raw += "\n" + line
			continue
		}
		if strings.TrimSpace(line) != "" {
			cur.Raw += "\n" + line
		}
	}
	flush()
	return entries
}

// inRange reports whether version is in (from, to] — from exclusive, to inclusive.
// Empty from means all versions <= to. Empty to is treated as unbounded high.
func inRange(version, from, to string) bool {
	if to != "" && cmpSemver(version, to) > 0 {
		return false
	}
	if from != "" && cmpSemver(version, from) <= 0 {
		return false
	}
	return true
}

// CmpSemverExported is the package-external semver compare for drift gates.
func CmpSemverExported(a, b string) int { return cmpSemver(a, b) }

// ChangelogRange returns entries with from < version <= to from CHANGELOG.md
// (on-disk preferred; embedded fallback). Used by the CLI drift gate.
func ChangelogRange(from, to string) ([]ChangelogEntry, error) {
	body, err := readChangelogBody()
	if err != nil {
		return nil, err
	}
	var out []ChangelogEntry
	for _, e := range parseChangelog(string(body)) {
		if inRange(e.Version, from, to) {
			out = append(out, e)
		}
	}
	return out, nil
}

// cmpSemver compares X.Y.Z numeric triples. Returns -1, 0, 1.
// Non-parseable parts compare as 0.
func cmpSemver(a, b string) int {
	ap := parseSemver(a)
	bp := parseSemver(b)
	for i := 0; i < 3; i++ {
		if ap[i] < bp[i] {
			return -1
		}
		if ap[i] > bp[i] {
			return 1
		}
	}
	return 0
}

func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}
