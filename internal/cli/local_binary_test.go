package cli

import (
	"os"
	"path/filepath"
	"testing"
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

func TestBinaryScopeLabelOf(t *testing.T) {
	// A pin path → repo-local label naming the pin path.
	if got := binaryScopeLabelOf("/home/me/proj/.satelle/satelle"); got != "repo-local pin: /home/me/proj/.satelle/satelle" {
		t.Errorf("pin label = %q", got)
	}
	// Any non-pin path → global.
	for _, p := range []string{"/usr/local/bin/satelle", "/home/me/proj/satelle"} {
		if got := binaryScopeLabelOf(p); got != "global" {
			t.Errorf("%q label = %q, want global", p, got)
		}
	}
}
