package verb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	dir := t.TempDir()
	path := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(path, []byte(fixtureChangelog), 0o644); err != nil {
		t.Fatal(err)
	}
	old := changelogPath
	changelogPath = func() string { return path }
	t.Cleanup(func() { changelogPath = old })

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
	// Without git — pure file parse.
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

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
