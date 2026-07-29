package agentinstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallIdempotentAndUpdate(t *testing.T) {
	home := t.TempDir()
	rs, err := Install(home, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Action != "created" {
		t.Fatalf("first install: %+v", rs)
	}
	path := rs[0].Path
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), MarkerLine) {
		t.Fatalf("missing marker: %s", b)
	}
	if !strings.Contains(string(b), "@agentclientprotocol/codex-acp") {
		t.Fatalf("codex launcher must spawn adapter: %s", b)
	}
	// Identical re-run → unchanged.
	rs2, err := Install(home, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if rs2[0].Action != "unchanged" {
		t.Fatalf("want unchanged, got %+v", rs2)
	}
	// Mutate a satelle-owned file (keep marker) then reinstall → updated.
	stale := "#!/bin/sh\n" + MarkerLine + " codex — do not edit\necho stale\n"
	if err := os.WriteFile(path, []byte(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	rs3, err := Install(home, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if rs3[0].Action != "updated" {
		t.Fatalf("want updated, got %+v", rs3)
	}
	// Unmarked existing file → skipped, not overwritten.
	foreignPath := LauncherPath(home, "claude")
	_ = os.MkdirAll(filepath.Dir(foreignPath), 0o755)
	if err := os.WriteFile(foreignPath, []byte("#!/bin/sh\necho foreign\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rs4, err := Install(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if rs4[0].Action != "skipped" {
		t.Fatalf("want skipped for unmarked, got %+v", rs4)
	}
	fb, _ := os.ReadFile(foreignPath)
	if !strings.Contains(string(fb), "foreign") {
		t.Fatalf("unmarked content must remain: %s", fb)
	}
}

func TestRemoveMarkedAndUnmarked(t *testing.T) {
	home := t.TempDir()
	if _, err := Install(home, "claude"); err != nil {
		t.Fatal(err)
	}
	path := LauncherPath(home, "claude")
	rs, err := Remove(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Action != "removed" {
		t.Fatalf("want removed: %+v", rs)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file should be gone: %v", err)
	}
	// Absent → ok.
	rs2, err := Remove(home, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if rs2[0].Action != "absent" {
		t.Fatalf("want absent: %+v", rs2)
	}
	// Unmarked → skipped, left in place.
	bin := filepath.Join(home, RelBin)
	_ = os.MkdirAll(bin, 0o755)
	foreign := filepath.Join(bin, "satelle-grok")
	if err := os.WriteFile(foreign, []byte("#!/bin/sh\necho foreign\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rs3, err := Remove(home, "grok")
	if err != nil {
		t.Fatal(err)
	}
	if rs3[0].Action != "skipped" {
		t.Fatalf("want skipped: %+v", rs3)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("unmarked file must remain")
	}
}

func TestInstallAllAndUnknown(t *testing.T) {
	home := t.TempDir()
	rs, err := Install(home, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 {
		t.Fatalf("all should install 3: %+v", rs)
	}
	names := SortedNames(rs)
	if strings.Join(names, ",") != "claude,codex,grok" {
		t.Fatalf("names = %v", names)
	}
	if _, err := Install(home, "nope"); err == nil {
		t.Fatal("unknown name must error")
	}
}

func TestContentClaudeGrok(t *testing.T) {
	c, err := Content("claude")
	if err != nil || !strings.Contains(c, "exec claude") {
		t.Fatalf("claude content: %v %q", err, c)
	}
	g, err := Content("grok")
	if err != nil || !strings.Contains(g, "exec grok") {
		t.Fatalf("grok content: %v %q", err, g)
	}
}
