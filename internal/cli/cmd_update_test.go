package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseLatestTag(t *testing.T) {
	tag, err := parseLatestTag([]byte(`{"tag_name":"v0.0.9","name":"v0.0.9"}`))
	if err != nil || tag != "v0.0.9" {
		t.Fatalf("parseLatestTag = %q, %v", tag, err)
	}
	if _, err := parseLatestTag([]byte(`{"name":"x"}`)); err == nil {
		t.Error("expected error when tag_name is absent")
	}
}

func TestAssetName(t *testing.T) {
	got := assetName("v0.0.9")
	want := fmt.Sprintf("satelle-v0.0.9-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got != want {
		t.Errorf("assetName = %q, want %q", got, want)
	}
}

func TestUpdateAvailable(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.0.6", "v0.0.9", true},
		{"0.0.9", "v0.0.9", false}, // leading v normalised
		{"v0.0.9", "v0.0.9", false},
		{"0.0.0-dev+abc-dirty", "v0.0.9", true},
	}
	for _, c := range cases {
		if got := updateAvailable(c.current, c.latest); got != c.want {
			t.Errorf("updateAvailable(%q,%q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

// TestFromSourceProceeds locks the behaviour `satelle update` shares with
// `claude update` (sty_d2936170): a from-source/dev build is NOT blocked — it
// proceeds to install whenever the latest release differs, and is a graceful
// no-op ("already up to date") only when it already matches. The old
// from-source refusal guard was removed; the dev escape hatch is `--local`
// (sty_fe3ee313), not a refusal.
func TestFromSourceProceeds(t *testing.T) {
	// A from-source dev build with a differing latest release proceeds to install.
	if !updateAvailable("0.0.0-dev+abc-dirty", "v0.0.9") {
		t.Error("a from-source build must self-update when the latest release differs (no refusal)")
	}
	// Equal versions are a graceful no-op, not an error.
	if updateAvailable("v0.0.9", "v0.0.9") {
		t.Error("an up-to-date install must report no update available")
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("a fake binary")
	sum := sha256.Sum256(data)
	good := hex.EncodeToString(sum[:]) + "  satelle-v0.0.9-linux-amd64"
	if err := verifyChecksum(data, good); err != nil {
		t.Errorf("matching checksum rejected: %v", err)
	}
	if err := verifyChecksum(data, "deadbeef  satelle"); err == nil {
		t.Error("mismatched checksum accepted")
	}
}

func TestReplaceExecutable(t *testing.T) {
	target := filepath.Join(t.TempDir(), "bin", "satelle") // dir does not exist yet
	if err := replaceExecutable(target, []byte("v2 binary")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "v2 binary" {
		t.Fatalf("target content = %q, %v", got, err)
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("target not executable: %v", info.Mode())
	}
	// No leftover temp files in the dir.
	entries, _ := os.ReadDir(filepath.Dir(target))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".satelle-update-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// TestDownloadAndReplaceFrom drives the full download→verify→replace path
// against a local fixture server — no network, no real binary.
func TestDownloadAndReplaceFrom(t *testing.T) {
	bin := []byte("the new satelle binary bytes")
	sum := sha256.Sum256(bin)
	name := "satelle-v9.9.9-linux-amd64"

	mux := http.NewServeMux()
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) { w.Write(bin) })
	mux.HandleFunc("/"+name+".sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "satelle")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := downloadAndReplaceFrom(context.Background(), srv.URL, name, target); err != nil {
		t.Fatalf("downloadAndReplaceFrom: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(bin) {
		t.Errorf("target not replaced with new bytes: %q", got)
	}

	// A corrupted checksum aborts and leaves the existing binary intact.
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) { w.Write(bin) })
	mux2.HandleFunc("/"+name+".sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "deadbeef  %s\n", name)
	})
	srv2 := httptest.NewServer(mux2)
	defer srv2.Close()
	keep := filepath.Join(t.TempDir(), "satelle")
	_ = os.WriteFile(keep, []byte("keep me"), 0o755)
	if err := downloadAndReplaceFrom(context.Background(), srv2.URL, name, keep); err == nil {
		t.Error("expected sha mismatch error")
	}
	if got, _ := os.ReadFile(keep); string(got) != "keep me" {
		t.Errorf("binary replaced despite checksum failure: %q", got)
	}
}
