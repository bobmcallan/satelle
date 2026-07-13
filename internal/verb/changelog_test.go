package verb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureChangelog = `# Changelog

## [0.0.3] - 2026-07-13

### Breaking
- CLI surface simplified; run satelle init after upgrade

### Added
- something new (sty_aaa)

## [0.0.2] - 2026-07-12

### Fixed
- a bug (sty_bbb)

## [0.0.1] - 2026-07-11

### Added
- first release
`

func TestParseChangelogAndRange(t *testing.T) {
	all := parseChangelog(fixtureChangelog)
	if len(all) != 3 {
		t.Fatalf("entries = %d, want 3", len(all))
	}
	if all[0].Version != "0.0.3" || !all[0].Breaking {
		t.Fatalf("top entry: %+v", all[0])
	}
	if all[1].Breaking {
		t.Error("0.0.2 should not be breaking")
	}

	// Range (0.0.1, 0.0.3] → 0.0.2 and 0.0.3
	var got []string
	for _, e := range all {
		if inRange(e.Version, "0.0.1", "0.0.3") {
			got = append(got, e.Version)
		}
	}
	if len(got) != 2 || got[0] != "0.0.3" || got[1] != "0.0.2" {
		t.Fatalf("range = %v", got)
	}
}

func TestChangelogVerbFixture(t *testing.T) {
	// Consumer channel is the embed; inject a fixture by overriding embed.
	oldEmbed := embeddedChangelog
	oldPath := changelogPath
	embeddedChangelog = fixtureChangelog
	// Disk path must not matter when embed is set.
	changelogPath = func() string { return filepath.Join(t.TempDir(), "missing.md") }
	t.Cleanup(func() {
		embeddedChangelog = oldEmbed
		changelogPath = oldPath
	})

	raw, err := changelogInvoke(context.Background(), mustJSON(map[string]any{
		"from": "0.0.1", "to": "0.0.3",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var res ChangelogResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Breaking {
		t.Error("range including 0.0.3 must set top-level breaking")
	}
	if len(res.Entries) != 2 {
		t.Fatalf("entries = %d %+v", len(res.Entries), res.Entries)
	}
	// Without git — pure parse of the embedded body.
	if res.Entries[0].Version != "0.0.3" || !res.Entries[0].Breaking {
		t.Fatalf("first entry: %+v", res.Entries[0])
	}
}

func TestCmpSemver(t *testing.T) {
	if cmpSemver("0.0.10", "0.0.9") <= 0 {
		t.Error("0.0.10 > 0.0.9")
	}
	if cmpSemver("v1.2.3", "1.2.3") != 0 {
		t.Error("v prefix ignored")
	}
}

// TestChangelogPreferEmbed proves the consumer channel is the embed even when
// a disk CHANGELOG.md exists (and differs) — AC5: disk is build input only.
func TestChangelogPreferEmbed(t *testing.T) {
	if strings.TrimSpace(embeddedChangelog) == "" {
		t.Fatal("embedded changelog empty — go:embed failed")
	}
	dir := t.TempDir()
	// Plant a disk file that would LIE if preferred: no Breaking, wrong versions.
	path := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("# empty disk\n## [9.9.9] - 2099-01-01\n### Fixed\n- lie\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := changelogPath
	changelogPath = func() string { return path }
	t.Cleanup(func() { changelogPath = old })

	raw, err := changelogInvoke(context.Background(), mustJSON(map[string]any{
		"from": "0.0.212", "to": "0.0.219",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var res ChangelogResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Breaking {
		t.Fatalf("embed must report breaking for 0.0.212→0.0.219; got %+v", res)
	}
	// Disk's 9.9.9 must not appear.
	for _, e := range res.Entries {
		if e.Version == "9.9.9" {
			t.Fatal("disk changelog leaked into consumer path")
		}
	}
}

// TestChangelogAbsenceErrors: empty embed + missing disk → error, not empty success.
func TestChangelogAbsenceErrors(t *testing.T) {
	oldEmbed := embeddedChangelog
	oldPath := changelogPath
	embeddedChangelog = ""
	changelogPath = func() string { return filepath.Join(t.TempDir(), "no-such-CHANGELOG.md") }
	t.Cleanup(func() {
		embeddedChangelog = oldEmbed
		changelogPath = oldPath
	})
	_, err := changelogInvoke(context.Background(), mustJSON(map[string]any{
		"from": "0.0.1", "to": "0.0.2",
	}))
	if err == nil {
		t.Fatal("want error when changelog is totally absent")
	}
	if _, err := ChangelogRange("0.0.1", "0.0.2"); err == nil {
		t.Fatal("ChangelogRange must error when absent")
	}
}

// TestEmbeddedMatchesRepoRoot guards the release copy step: when the satelle
// module's root CHANGELOG.md is present, it must equal the embed bytes.
func TestEmbeddedMatchesRepoRoot(t *testing.T) {
	// Walk up from this package to the module root.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var root string
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			root = d
			break
		}
		if filepath.Dir(d) == d {
			t.Skip("no go.mod above package")
		}
	}
	rootCL := filepath.Join(root, "CHANGELOG.md")
	b, err := os.ReadFile(rootCL)
	if err != nil {
		t.Skip("no root CHANGELOG.md")
	}
	if string(b) != embeddedChangelog {
		t.Fatalf("internal/verb/embedded/CHANGELOG.md drifted from repo-root CHANGELOG.md — copy before release")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
