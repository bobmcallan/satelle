package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureGrokFolderTrustedFirstWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := t.TempDir()

	changed, abs, err := EnsureGrokFolderTrusted(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first write should report changed")
	}
	wantAbs, _ := filepath.Abs(repo)
	if abs != filepath.Clean(wantAbs) {
		t.Fatalf("abs = %q, want %q", abs, wantAbs)
	}
	body, err := os.ReadFile(filepath.Join(home, ".grok", GrokTrustedFoldersName))
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, `[folders."`+abs+`"]`) {
		t.Fatalf("missing section for %s:\n%s", abs, s)
	}
	if !strings.Contains(s, "trusted = true") {
		t.Fatalf("missing trusted=true:\n%s", s)
	}
	if !strings.Contains(s, "decided_at = ") {
		t.Fatalf("missing decided_at:\n%s", s)
	}
	if !GrokFolderTrusted(s, abs) {
		t.Fatal("GrokFolderTrusted false after write")
	}
}

func TestEnsureGrokFolderTrustedIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := t.TempDir()

	if _, _, err := EnsureGrokFolderTrusted(repo); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".grok", GrokTrustedFoldersName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed, _, err := EnsureGrokFolderTrusted(repo)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second call should not rewrite when already trusted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("bytes changed on idempotent call:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestEnsureGrokFolderTrustedOnlyThisRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoA := t.TempDir()
	repoB := t.TempDir()

	// Seed an unrelated trusted folder (as if /hooks-trust already ran).
	other := filepath.Join(home, "other-project")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `[folders."` + other + `"]
trusted = true
decided_at = 1
`
	if err := os.MkdirAll(filepath.Join(home, ".grok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".grok", GrokTrustedFoldersName), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, absA, err := EnsureGrokFolderTrusted(repoA); err != nil {
		t.Fatal(err)
	} else if absA == other {
		t.Fatal("trusted wrong path")
	}
	body, _ := os.ReadFile(filepath.Join(home, ".grok", GrokTrustedFoldersName))
	s := string(body)
	if !strings.Contains(s, other) || !GrokFolderTrusted(s, other) {
		t.Fatalf("unrelated folder must stay trusted:\n%s", s)
	}
	absA, _ := filepath.Abs(repoA)
	if !GrokFolderTrusted(s, filepath.Clean(absA)) {
		t.Fatalf("repo A not trusted:\n%s", s)
	}
	absB, _ := filepath.Abs(repoB)
	if GrokFolderTrusted(s, filepath.Clean(absB)) {
		t.Fatalf("repo B must not be trusted yet:\n%s", s)
	}
}

func TestEnsureGrokFolderTrustedPromotesFalse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := t.TempDir()
	abs, _ := filepath.Abs(repo)
	abs = filepath.Clean(abs)
	seed := `[folders."` + abs + `"]
trusted = false
decided_at = 9
`
	if err := os.MkdirAll(filepath.Join(home, ".grok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".grok", GrokTrustedFoldersName), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, _, err := EnsureGrokFolderTrusted(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("promoting trusted=false should change")
	}
	body, _ := os.ReadFile(filepath.Join(home, ".grok", GrokTrustedFoldersName))
	if !GrokFolderTrusted(string(body), abs) {
		t.Fatalf("still untrusted:\n%s", body)
	}
	// Single section — no duplicate headers.
	if strings.Count(string(body), `[folders."`+abs+`"]`) != 1 {
		t.Fatalf("duplicate section:\n%s", body)
	}
}
