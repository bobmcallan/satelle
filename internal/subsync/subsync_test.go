package subsync

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRestoreByteExact: a deploy's files round-trip byte-for-byte (0o644),
// nested dirs created as needed (AC2 byte-exact restore).
func TestRestoreByteExact(t *testing.T) {
	dataDir := t.TempDir()
	files := []File{
		{Path: "skills/team-review.md", Content: []byte("---\ntype: skill\n---\nbody\n")},
		{Path: "agents.toml", Content: []byte("[executor]\nharness = \"in-loop\"\n")},
		{Path: "tasks/tsk_abc.md", Content: []byte("nested parent dir")},
	}
	res, err := Restore(dataDir, files)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if res.Written != len(files) || len(res.Skipped) != 0 {
		t.Fatalf("wrote %d skipped %v, want written %d skipped empty", res.Written, res.Skipped, len(files))
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatalf("read back %s: %v", f.Path, err)
		}
		if string(got) != string(f.Content) {
			t.Errorf("%s bytes diverged: got %q want %q", f.Path, got, f.Content)
		}
	}
}

// TestRestoreOverwritesExisting: a deploy overwrites a file already on disk,
// matching the latest version byte-for-byte rather than merging.
func TestRestoreOverwritesExisting(t *testing.T) {
	dataDir := t.TempDir()
	dest := filepath.Join(dataDir, "constitution.md")
	if err := os.WriteFile(dest, []byte("OLD"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(dataDir, []File{{Path: "constitution.md", Content: []byte("NEW")}}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "NEW" {
		t.Errorf("constitution.md = %q, want NEW (overwrite)", got)
	}
}

// TestRestoreRefusesUnsafePath: a hostile manifest segment ("..") must not
// escape the data dir (cleanRel stays a hard error — sty_84f14ace plan).
func TestRestoreRefusesUnsafePath(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := Restore(dataDir, []File{{Path: "../escape.md", Content: []byte("x")}}); err == nil {
		t.Error("Restore(../escape.md) unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dataDir), "escape.md")); err == nil {
		t.Error("an unsafe path escaped the data dir")
	}
}

// TestExcludedLocalMatchesRestore (sty_0fd04503 AC2): the exported early-out
// agrees with Restore's skip set; unsafe paths return false so Restore still
// hard-errors (not silently skipped pre-fetch).
func TestExcludedLocalMatchesRestore(t *testing.T) {
	for _, p := range []string{
		"backups/pre-mutation/x.md",
		"logs/server.log",
		"stories/sty_x.md",
		"satelle.db",
		"satelle.db-wal",
		"satelle.local.toml",
	} {
		if !ExcludedLocal(p) {
			t.Errorf("ExcludedLocal(%q) = false, want true", p)
		}
	}
	for _, p := range []string{
		"documents/ok.md",
		"skills/a.md",
		"agents.toml",
		"constitution.md",
	} {
		if ExcludedLocal(p) {
			t.Errorf("ExcludedLocal(%q) = true, want false", p)
		}
	}
	// Unsafe paths: not a skip — Restore hard-errors.
	for _, p := range []string{"../escape.md", "/abs.md", ""} {
		if ExcludedLocal(p) {
			t.Errorf("ExcludedLocal(%q) = true, want false (defer to Restore hard-error)", p)
		}
	}
}

// TestRestoreSkipsExcludedPaths: excludedLocal paths are skipped (not hard-errored)
// so a mixed batch still completes and legitimate files land byte-exact
// (sty_84f14ace AC1). Excluded paths are never written (AC3) and appear in
// Result.Skipped (AC4).
func TestRestoreSkipsExcludedPaths(t *testing.T) {
	dataDir := t.TempDir()
	// Pre-seed a live database that must not be clobbered.
	dbPath := filepath.Join(dataDir, "satelle.db")
	if err := os.WriteFile(dbPath, []byte("LIVE-DB"), 0o644); err != nil {
		t.Fatal(err)
	}
	walPath := filepath.Join(dataDir, "satelle.db-wal")
	if err := os.WriteFile(walPath, []byte("LIVE-WAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillBody := []byte("---\ntype: skill\n---\nbody\n")
	agentsBody := []byte("[executor]\nharness = \"in-loop\"\n")
	files := []File{
		{Path: "backups/pre-mutation/skills/x.md", Content: []byte("poison")},
		{Path: "skills/a.md", Content: skillBody},
		{Path: "agents.toml", Content: agentsBody},
		{Path: "satelle.db", Content: []byte("HOSTILE")},
		{Path: "satelle.db-wal", Content: []byte("HOSTILE-WAL")},
		{Path: "stories/sty_x.md", Content: []byte("nope")},
		{Path: "logs/server.log", Content: []byte("nope")},
	}
	res, err := Restore(dataDir, files)
	if err != nil {
		t.Fatalf("Restore mixed batch: %v", err)
	}
	if res.Written != 2 {
		t.Errorf("Written = %d, want 2", res.Written)
	}
	if len(res.Skipped) != 5 {
		t.Errorf("Skipped = %v, want 5 paths", res.Skipped)
	}
	// AC4: Skipped names the skipped paths.
	wantSkip := map[string]bool{
		"backups/pre-mutation/skills/x.md": true,
		"satelle.db":                       true,
		"satelle.db-wal":                   true,
		"stories/sty_x.md":                 true,
		"logs/server.log":                  true,
	}
	for _, p := range res.Skipped {
		if !wantSkip[p] {
			t.Errorf("unexpected skipped path %q", p)
		}
		delete(wantSkip, p)
	}
	for p := range wantSkip {
		t.Errorf("missing skipped path %q", p)
	}

	// AC1: legitimate files byte-exact.
	gotSkill, err := os.ReadFile(filepath.Join(dataDir, "skills", "a.md"))
	if err != nil {
		t.Fatalf("skills/a.md not written: %v", err)
	}
	if string(gotSkill) != string(skillBody) {
		t.Errorf("skills/a.md = %q, want %q", gotSkill, skillBody)
	}
	gotAgents, err := os.ReadFile(filepath.Join(dataDir, "agents.toml"))
	if err != nil {
		t.Fatalf("agents.toml not written: %v", err)
	}
	if string(gotAgents) != string(agentsBody) {
		t.Errorf("agents.toml = %q, want %q", gotAgents, agentsBody)
	}

	// AC3: live db/wal untouched.
	db, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(db) != "LIVE-DB" {
		t.Errorf("satelle.db was clobbered: %q", db)
	}
	wal, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(wal) != "LIVE-WAL" {
		t.Errorf("satelle.db-wal was clobbered: %q", wal)
	}
	// Excluded trees must not appear.
	for _, rel := range []string{
		"backups/pre-mutation/skills/x.md",
		"stories/sty_x.md",
		"logs/server.log",
	} {
		if _, err := os.Stat(filepath.Join(dataDir, filepath.FromSlash(rel))); err == nil {
			t.Errorf("excluded path %s was written", rel)
		}
	}
}

// TestCleanRel rejects the shapes a manifest must never carry.
func TestCleanRel(t *testing.T) {
	for _, bad := range []string{"", "/abs", `back\slash`, "a/../b", "a//b", "a/./b", "a\x00b"} {
		if _, err := cleanRel(bad); err == nil {
			t.Errorf("cleanRel(%q) unexpectedly succeeded", bad)
		}
	}
	for _, ok := range []string{"skills/x.md", "agents.toml", "tasks/sub/tsk_1.md"} {
		if _, err := cleanRel(ok); err != nil {
			t.Errorf("cleanRel(%q) unexpectedly failed: %v", ok, err)
		}
	}
}
