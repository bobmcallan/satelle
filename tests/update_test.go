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
	// The serve channel is stubbed EMPTY so this CLI-path test stays hermetic:
	// without it, serve discovery reaches the real GitHub API (sty_0dcedb0d).
	// An empty list plus no installed serve binary is the documented no-op.
	mux.HandleFunc("/api/releases", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[]`) })
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
		"SATELLE_RELEASE_LIST_API="+srv.URL+"/api/releases",
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
	if !strings.Contains(string(out), "no serve release published") {
		t.Errorf("expected the no-serve-release no-op to be named:\n%s", out)
	}

	got, err := os.ReadFile(filepath.Join(installDir, "satelle"))
	if err != nil {
		t.Fatalf("installed binary not present: %v", err)
	}
	if string(got) != string(bin) {
		t.Errorf("installed binary not replaced with the release asset:\n%q", got)
	}
}

// TestUpdateLocalInstallsIntoRepo drives `satelle update --local` against a
// fixture release server: it must install the release into THIS repo's
// .satelle/satelle (the repo-local pin), not the global install dir
// (sty_fe3ee313). The repo is init'd so .satelle/ exists for the pin to land in.
func TestUpdateLocalInstallsIntoRepo(t *testing.T) {
	const tag = "v9.9.9"
	name := fmt.Sprintf("satelle-%s-%s-%s", tag, runtime.GOOS, runtime.GOARCH)
	bin := []byte("repo-local satelle pin\n")
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

	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	cmd := exec.Command(testBin, "update", "--local")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"SATELLE_RELEASE_API="+srv.URL+"/api/releases/latest",
		"SATELLE_RELEASE_BASE="+srv.URL+"/dl",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update --local: %v\n%s", err, out)
	}

	pin := filepath.Join(repo, ".satelle", "satelle")
	got, err := os.ReadFile(pin)
	if err != nil {
		t.Fatalf("repo-local pin not installed at %s: %v", pin, err)
	}
	if string(got) != string(bin) {
		t.Errorf("pin is not the release asset:\n%q", got)
	}
}

// TestUpdateInstallsServeFromLaterPage drives the real `satelle update` against
// a fixture whose serve release sits on the SECOND page of the release list —
// the production shape of sty_0dcedb0d, where a run of CLI releases had carried
// the serve release off page one. Before the fix, update printed a skip and
// exited 0 with no serve binary installed; now the serve asset must land.
func TestUpdateInstallsServeFromLaterPage(t *testing.T) {
	const cliTag = "v9.9.9"
	const serveTag = "serve-v9.9.8"
	cliName := fmt.Sprintf("satelle-%s-%s-%s", cliTag, runtime.GOOS, runtime.GOARCH)
	serveName := fmt.Sprintf("satelle-serve-v9.9.8-%s-%s", runtime.GOOS, runtime.GOARCH)
	cliBin := []byte("new satelle binary\n")
	serveBin := []byte("new satelle-serve binary\n")
	cliSum, serveSum := sha256.Sum256(cliBin), sha256.Sum256(serveBin)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q}`, cliTag)
	})
	mux.HandleFunc("/api/releases", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			// A full page of CLI-only releases, so the walk must continue.
			var entries []string
			for i := 0; i < 100; i++ {
				entries = append(entries, fmt.Sprintf(`{"tag_name":"v0.0.%d","draft":false}`, 900-i))
			}
			fmt.Fprintf(w, "[%s]", strings.Join(entries, ","))
		case "2":
			fmt.Fprintf(w, `[{"tag_name":%q,"draft":false}]`, serveTag)
		default:
			fmt.Fprint(w, `[]`)
		}
	})
	mux.HandleFunc("/dl/"+cliTag+"/"+cliName, func(w http.ResponseWriter, r *http.Request) { w.Write(cliBin) })
	mux.HandleFunc("/dl/"+cliTag+"/"+cliName+".sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(cliSum[:]), cliName)
	})
	mux.HandleFunc("/dl/"+serveTag+"/"+serveName, func(w http.ResponseWriter, r *http.Request) { w.Write(serveBin) })
	mux.HandleFunc("/dl/"+serveTag+"/"+serveName+".sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(serveSum[:]), serveName)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	installDir := t.TempDir()
	env := append(os.Environ(),
		"SATELLE_RELEASE_API="+srv.URL+"/api/releases/latest",
		"SATELLE_RELEASE_LIST_API="+srv.URL+"/api/releases",
		"SATELLE_RELEASE_BASE="+srv.URL+"/dl",
		"SATELLE_INSTALL_DIR="+installDir,
	)
	cmd := exec.Command(testBin, "update", "--no-restart")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	serveTarget := filepath.Join(installDir, "satelle-serve")
	if runtime.GOOS == "windows" {
		serveTarget += ".exe"
	}
	got, err := os.ReadFile(serveTarget)
	if err != nil {
		t.Fatalf("serve binary not installed from page two: %v\n%s", err, out)
	}
	if string(got) != string(serveBin) {
		t.Errorf("installed serve binary is not the release asset:\n%q", got)
	}
	if !strings.Contains(string(out), serveTag) {
		t.Errorf("update output did not name %s:\n%s", serveTag, out)
	}
}

// TestUpdateFailsWhenServeReleaseUnresolvable proves AC2 with the real binary:
// a serve release that cannot be resolved while a serve binary IS installed
// fails the verb. Exiting 0 here is what let a release read green while the live
// service kept running an older serve binary (sty_0dcedb0d).
func TestUpdateFailsWhenServeReleaseUnresolvable(t *testing.T) {
	const cliTag = "v9.9.9"
	cliName := fmt.Sprintf("satelle-%s-%s-%s", cliTag, runtime.GOOS, runtime.GOARCH)
	cliBin := []byte("new satelle binary\n")
	cliSum := sha256.Sum256(cliBin)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q}`, cliTag)
	})
	mux.HandleFunc("/api/releases", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	})
	mux.HandleFunc("/dl/"+cliTag+"/"+cliName, func(w http.ResponseWriter, r *http.Request) { w.Write(cliBin) })
	mux.HandleFunc("/dl/"+cliTag+"/"+cliName+".sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(cliSum[:]), cliName)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	installDir := t.TempDir()
	serveTarget := filepath.Join(installDir, "satelle-serve")
	if runtime.GOOS == "windows" {
		serveTarget += ".exe"
	}
	if err := os.WriteFile(serveTarget, []byte("an older serve binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(testBin, "update", "--no-restart")
	cmd.Env = append(os.Environ(),
		"SATELLE_RELEASE_API="+srv.URL+"/api/releases/latest",
		"SATELLE_RELEASE_LIST_API="+srv.URL+"/api/releases",
		"SATELLE_RELEASE_BASE="+srv.URL+"/dl",
		"SATELLE_INSTALL_DIR="+installDir,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("update must FAIL when the serve release cannot be resolved:\n%s", out)
	}
	if !strings.Contains(string(out), "satelle-serve update failed") {
		t.Errorf("failure must name the serve half:\n%s", out)
	}
}
