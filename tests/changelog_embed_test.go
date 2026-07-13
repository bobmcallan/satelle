//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChangelogEmbedDownstream proves the installed binary answers changelog
// truthfully with no repo CHANGELOG.md, no git, no network (sty_b5fa838a / vire).
// Prefer-embed: a planted on-disk lie must not win.
func TestChangelogEmbedDownstream(t *testing.T) {
	repo := t.TempDir()
	// No init, no CHANGELOG.md, no .git — pure empty consumer tree.
	out, err := run(t, testBin, repo, "changelog", "--from", "0.0.212", "--to", "0.0.219")
	if err != nil {
		t.Fatalf("changelog in empty repo: %v\n%s", err, out)
	}
	var res struct {
		Breaking bool `json:"breaking"`
		Entries  []struct {
			Version  string `json:"version"`
			Breaking bool   `json:"breaking"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if !res.Breaking {
		t.Fatalf("want breaking:true for 0.0.212→0.0.219; got %s", out)
	}
	if len(res.Entries) == 0 {
		t.Fatal("want embedded entries; got none")
	}

	// Plant a disk CHANGELOG that would hide Breaking if preferred.
	if err := os.WriteFile(filepath.Join(repo, "CHANGELOG.md"), []byte("## [0.0.1]\n### Fixed\n- no\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out2, err := run(t, testBin, repo, "changelog", "--from", "0.0.212", "--to", "0.0.219")
	if err != nil {
		t.Fatalf("with disk lie: %v\n%s", err, out2)
	}
	if !strings.Contains(out2, `"breaking":true`) && !strings.Contains(out2, `"breaking": true`) {
		t.Fatalf("disk changelog leaked into consumer path:\n%s", out2)
	}
}
