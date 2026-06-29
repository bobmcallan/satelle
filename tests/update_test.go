//go:build integration

package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestUpdateReplacesBinary drives the real `satelle update` against a fixture
// release server (SATELLE_RELEASE_API/BASE overrides) and a throwaway install
// dir: it must download, sha256-verify, and replace the installed binary.
// --no-restart keeps it from touching any real service.
func TestUpdateReplacesBinary(t *testing.T) {
	const tag = "v9.9.9"
	name := fmt.Sprintf("satelle-%s-%s-%s", tag, runtime.GOOS, runtime.GOARCH)
	bin := []byte("brand new satelle binary\n")
	sum := sha256.Sum256(bin)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q}`, tag)
	})
	mux.HandleFunc("/dl/"+tag+"/"+name, func(w http.ResponseWriter, r *http.Request) { w.Write(bin) })
	mux.HandleFunc("/dl/"+tag+"/"+name+".sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	installDir := t.TempDir()
	cmd := exec.Command(testBin, "update", "--no-restart")
	cmd.Env = append(os.Environ(),
		"SATELLE_RELEASE_API="+srv.URL+"/api/releases/latest",
		"SATELLE_RELEASE_BASE="+srv.URL+"/dl",
		"SATELLE_INSTALL_DIR="+installDir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), tag) {
		t.Errorf("update output did not mention %s:\n%s", tag, out)
	}

	got, err := os.ReadFile(filepath.Join(installDir, "satelle"))
	if err != nil {
		t.Fatalf("installed binary not present: %v", err)
	}
	if string(got) != string(bin) {
		t.Errorf("installed binary not replaced with the release asset:\n%q", got)
	}
}
