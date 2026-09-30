package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// sty_992cffc6 AC6 — the lock list is decided by key PRESENCE in the raw
// committed file, so an absent key (default lock) and `= []` (documented
// opt-out) are separate outcomes. Each case asserts the exact pair.
func TestParseLockSubstratePathsSplitsAbsentFromExplicitEmpty(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
		optOut  bool
	}{
		{"no gate section", "[review]\ngate_create = false\n", []string{".satelle/"}, false},
		{"gate section without the key", "[gate]\nedit_exempt_paths = [\".satelle/\"]\n", []string{".satelle/"}, false},
		{"empty file", "", []string{".satelle/"}, false},
		{"key with entries", "[gate]\nlock_substrate_paths = [\".satelle/\", \"docs/process/\"]\n", []string{".satelle/", "docs/process/"}, false},
		{"entries are trimmed", "[gate]\nlock_substrate_paths = [\"  .satelle/ \"]\n", []string{".satelle/"}, false},
		{"multi-line array", "[gate]\nlock_substrate_paths = [\n  \".satelle/\",\n  \"policy/\",\n]\n", []string{".satelle/", "policy/"}, false},
		{"key = []", "[gate]\nlock_substrate_paths = []\n", nil, true},
		{"all-blank entries", "[gate]\nlock_substrate_paths = [\"\", \"  \"]\n", nil, true},
		{"empty multi-line array", "[gate]\nlock_substrate_paths = [\n]\n", nil, true},
		{"key in another table is not the gate key", "[other]\nlock_substrate_paths = []\n", []string{".satelle/"}, false},
		// A damaged file or a value that is not a list of strings is never read as
		// the operator's opt-out.
		{"syntax error fails closed", "[gate\nlock_substrate_paths = []\n", []string{".satelle/"}, false},
		{"not a list fails closed", "[gate]\nlock_substrate_paths = \"\"\n", []string{".satelle/"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, optOut := ParseLockSubstratePaths(tc.content)
			if !reflect.DeepEqual(got, tc.want) || optOut != tc.optOut {
				t.Fatalf("ParseLockSubstratePaths = (%#v, %v), want (%#v, %v)", got, optOut, tc.want, tc.optOut)
			}
		})
	}
}

func TestResolveLockSubstratePathsReadsFileAndFailsClosed(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	cfg := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}

	// (f) an unreadable (here: missing) file fails closed to the default lock.
	roots, optOut := ResolveLockSubstratePaths(cfg, repo)
	if want := []string{filepath.Join(repo, ".satelle/")}; !reflect.DeepEqual(roots, want) || optOut {
		t.Fatalf("missing file = (%v, %v), want (%v, false)", roots, optOut, want)
	}

	// Entries resolve against the repo root; an absolute entry passes through.
	if err := os.WriteFile(cfg, []byte("[gate]\nlock_substrate_paths = [\"policy/\", \"/etc/satelle/\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	roots, optOut = ResolveLockSubstratePaths(cfg, repo)
	if want := []string{filepath.Join(repo, "policy/"), "/etc/satelle/"}; !reflect.DeepEqual(roots, want) || optOut {
		t.Fatalf("entries = (%v, %v), want (%v, false)", roots, optOut, want)
	}

	if err := os.WriteFile(cfg, []byte("[gate]\nlock_substrate_paths = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if roots, optOut = ResolveLockSubstratePaths(cfg, repo); len(roots) != 0 || !optOut {
		t.Fatalf("explicit empty = (%v, %v), want (none, true)", roots, optOut)
	}
}

// The decoded Config carries the key for settings display only; it cannot tell
// absent from `= []`, which is why the lock never reads it.
func TestGateConfigDecodesLockSubstratePathsForDisplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "satelle.toml")
	if err := os.WriteFile(path, []byte("[gate]\nlock_substrate_paths = [\"a/\", \"b/\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Gate.LockSubstratePaths, []string{"a/", "b/"}) {
		t.Fatalf("Gate.LockSubstratePaths = %v", cfg.Gate.LockSubstratePaths)
	}
}
