package remotenode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/remotedef"
)

func tarOf(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, h := range entries {
		h := h
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write(bytes.Repeat([]byte("x"), int(h.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func TestSubstrateRoundTripReplacesTheDirectories(t *testing.T) {
	def := remotedef.Dir(t.TempDir(), "edge")
	if err := remotedef.Scaffold(def, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	packed, err := PackSubstrate(def)
	if err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	// What a fresh init scaffolded: it must not survive beside the remote's own.
	for _, f := range []string{"workflows/default-done.toml", "skills/init-skill.md", "principles/init-principle.md"} {
		p := filepath.Join(dataDir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("scaffolded"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(dataDir, "satelle.toml")
	if err := os.WriteFile(other, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := UnpackSubstrate(dataDir, packed); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"workflows/done.toml", "workflows/step.toml", "workflows/agents.toml", "skills/remote-work.md", "principles/remote-conduct.md"} {
		want, _ := os.ReadFile(filepath.Join(def, filepath.FromSlash(rel)))
		got, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(rel)))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s not unpacked intact: %v", rel, err)
		}
	}
	for _, rel := range []string{"workflows/default-done.toml", "skills/init-skill.md", "principles/init-principle.md"} {
		if _, err := os.Stat(filepath.Join(dataDir, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s survived: the node's substrate must be exactly the remote's", rel)
		}
	}
	if b, _ := os.ReadFile(other); string(b) != "keep" {
		t.Error("files outside the substrate directories must be left alone")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "remote.toml")); err == nil {
		t.Error("remote.toml (host commands) must not be shipped to the node")
	}
}

func TestUnpackRefusesAnythingOutsideTheSubstrate(t *testing.T) {
	for name, packed := range map[string][]byte{
		"traversal":     tarOf(t, tar.Header{Name: "workflows/../../escape.toml", Size: 1}),
		"absolute":      tarOf(t, tar.Header{Name: "/etc/passwd", Size: 1}),
		"other dir":     tarOf(t, tar.Header{Name: "config/.env", Size: 1}),
		"top-level":     tarOf(t, tar.Header{Name: "workflows", Size: 1}),
		"symlink":       tarOf(t, tar.Header{Name: "skills/link.md", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}),
		"not gzip":      []byte("plain"),
		"empty-garbage": nil,
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			keep := filepath.Join(dataDir, "workflows", "keep.toml")
			_ = os.MkdirAll(filepath.Dir(keep), 0o755)
			_ = os.WriteFile(keep, []byte("x"), 0o644)
			if err := UnpackSubstrate(dataDir, packed); err == nil {
				t.Fatal("must refuse")
			}
			if _, err := os.Stat(keep); err != nil {
				t.Error("a refused substrate must not have touched the node's directories")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(dataDir), "escape.toml")); err == nil {
				t.Error("an entry escaped the data dir")
			}
		})
	}
}

func TestPackRefusesAnOversizedSubstrate(t *testing.T) {
	def := remotedef.Dir(t.TempDir(), "edge")
	if err := remotedef.Scaffold(def, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	// Incompressible bytes, past the cap.
	big := make([]byte, MaxSubstrateBytes+4096)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(def, "skills", "big.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PackSubstrate(def); err == nil || !strings.Contains(err.Error(), "channel carries at most") {
		t.Errorf("err = %v", err)
	}
}
