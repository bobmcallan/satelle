package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// mkLocalPin creates <root>/.satelle/satelle as a regular executable file.
func mkLocalPin(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, ".satelle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pin := filepath.Join(dir, "satelle")
	if err := os.WriteFile(pin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return pin
}

func TestFindDotSatelleRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := findDotSatelleRoot(nested); !ok || got != root {
		t.Errorf("findDotSatelleRoot(nested) = %q,%v; want %q,true", got, ok, root)
	}
	// No .satelle/ anywhere up the (real, temp) tree.
	bare := t.TempDir()
	if got, ok := findDotSatelleRoot(bare); ok {
		t.Errorf("findDotSatelleRoot(bare) = %q,%v; want \"\",false", got, ok)
	}
}

func TestRepoLocalTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	want := filepath.Join(root, ".satelle", "satelle")
	if got := repoLocalTarget(); got != want {
		t.Errorf("repoLocalTarget() = %q, want %q (nearest .satelle/ up-tree)", got, want)
	}
}

func TestLocalReexecTarget(t *testing.T) {
	root := t.TempDir()
	pin := mkLocalPin(t, root)
	nested := filepath.Join(root, "deep", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pin present and different from self → re-exec it.
	if got, ok := localReexecTarget(nested, "/usr/local/bin/satelle", false); !ok || got != pin {
		t.Errorf("present+different = %q,%v; want %q,true", got, ok, pin)
	}
	// We ARE the pin (self == local) → no re-exec.
	if got, ok := localReexecTarget(nested, pin, false); ok {
		t.Errorf("self==pin = %q,%v; want \"\",false", got, ok)
	}
	// Loop marker set → no re-exec even with a pin present.
	if _, ok := localReexecTarget(nested, "/usr/local/bin/satelle", true); ok {
		t.Error("marker set should suppress re-exec")
	}
	// No pin present → no re-exec.
	bare := t.TempDir()
	if _, ok := localReexecTarget(bare, "/usr/local/bin/satelle", false); ok {
		t.Error("absent pin should not re-exec")
	}
}

func TestPinRepoRootOf(t *testing.T) {
	// A <root>/.satelle/satelle path is the repo-local pin → local mode.
	if got, ok := pinRepoRootOf("/home/me/proj/.satelle/satelle"); !ok || got != "/home/me/proj" {
		t.Errorf("pin path = %q,%v; want /home/me/proj,true", got, ok)
	}
	// The global binary is not a pin.
	for _, p := range []string{"/usr/local/bin/satelle", "/home/me/proj/satelle", "/home/me/proj/.satelle/satelle-old"} {
		if _, ok := pinRepoRootOf(p); ok {
			t.Errorf("%q should NOT be detected as a repo-local pin", p)
		}
	}
}

func TestLocalDeterministicPort(t *testing.T) {
	a := localDeterministicPort("/home/me/repo-a")
	b := localDeterministicPort("/home/me/repo-b")
	// In range, never the global default, and stable.
	for _, p := range []int{a, b} {
		if p < localWebPortBase || p >= localWebPortBase+localWebPortSpan {
			t.Errorf("port %d out of local range [%d,%d)", p, localWebPortBase, localWebPortBase+localWebPortSpan)
		}
		if p == config.DefaultWebPort {
			t.Errorf("local port must differ from the global default %d", config.DefaultWebPort)
		}
	}
	if a != localDeterministicPort("/home/me/repo-a") {
		t.Error("deterministic port must be stable for the same root")
	}
	if a == b {
		t.Error("distinct repos should (here) get distinct ports")
	}
}

func TestResolveServePort(t *testing.T) {
	const cfg = 9001
	// --port flag wins over everything.
	if got := resolveServePort(1234, cfg, "/r", true); got != 1234 {
		t.Errorf("flag should win: got %d", got)
	}
	// Explicit [web_port] wins over local-deterministic.
	if got := resolveServePort(0, cfg, "/r", true); got != cfg {
		t.Errorf("config web_port should win over local default: got %d", got)
	}
	// Local mode with no flag/config → deterministic per-repo port.
	if got := resolveServePort(0, 0, "/r", true); got != localDeterministicPort("/r") {
		t.Errorf("local mode should use deterministic port: got %d", got)
	}
	// Global mode with no flag/config → the default port.
	if got := resolveServePort(0, 0, "", false); got != config.DefaultWebPort {
		t.Errorf("global mode should use default port: got %d", got)
	}
}
