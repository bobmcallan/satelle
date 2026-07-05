//go:build integration

package tests

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// stubSubstrateServer is a contract-faithful in-memory mirror of satelle-server's
// project substrate backup API (sty_caaca515): PUT replaces the whole snapshot
// (absent paths deleted) and reports added/updated/unchanged/removed; GET ?full=1
// returns every file with base64 bytes. It accepts the seeded bearer "acc".
func stubSubstrateServer(t *testing.T, slug string) *httptest.Server {
	t.Helper()
	files := map[string][]byte{}
	sha := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	type wire struct {
		Path    string `json:"path"`
		Kind    string `json:"kind,omitempty"`
		SHA256  string `json:"sha256,omitempty"`
		Size    int64  `json:"size,omitempty"`
		Content string `json:"content,omitempty"`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/projects/"+slug+"/substrate", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer acc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPut:
			var in struct {
				Files []wire `json:"files"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			incoming := map[string][]byte{}
			for _, f := range in.Files {
				b, err := base64.StdEncoding.DecodeString(f.Content)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				incoming[f.Path] = b
			}
			var added, updated, unchanged, removed int
			for p, b := range incoming {
				if old, ok := files[p]; ok {
					if sha(old) == sha(b) {
						unchanged++
					} else {
						updated++
					}
				} else {
					added++
				}
			}
			for p := range files {
				if _, keep := incoming[p]; !keep {
					removed++
				}
			}
			files = incoming
			_ = json.NewEncoder(w).Encode(map[string]int{"added": added, "updated": updated, "unchanged": unchanged, "removed": removed})
		case http.MethodGet:
			out := []wire{}
			if r.URL.Query().Get("full") == "1" {
				paths := make([]string, 0, len(files))
				for p := range files {
					paths = append(paths, p)
				}
				sort.Strings(paths)
				for _, p := range paths {
					b := files[p]
					out = append(out, wire{Path: p, SHA256: sha(b), Size: int64(len(b)), Content: base64.StdEncoding.EncodeToString(b)})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"files": out})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// TestSubstrateBackupRoundTripEndToEnd is the AC4 proof, end-to-end through the
// real binary: bind a repo to a project, push its git-tracked authored .satelle/
// substrate, then pull it into a CLEAN checkout and assert the reconstructed tree
// is byte-identical to the source authored tree — and that local-only state
// (satelle.db, its WAL/SHM) never left the machine (AC5). A re-push is idempotent
// (AC2).
func TestSubstrateBackupRoundTripEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	bin := testBin
	repo := t.TempDir()
	xdg := t.TempDir()

	// Scaffold the repo (seeds .satelle/ authored substrate + the local db + a
	// managed .gitignore that ignores the db/logs), then make it a git work tree
	// and stage the authored substrate. git add respects the .gitignore, so the
	// local-only db/WAL are naturally left untracked.
	mustRun(t, bin, repo, "init")
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "config", "user.email", "t@t.t")
	gitRun(t, repo, "config", "user.name", "t")
	gitRun(t, repo, "add", ".satelle")

	ts := stubSubstrateServer(t, "acme")
	seedCredential(t, xdg, ts.URL)

	// bind → the slug lands in the committed satelle.toml (AC1).
	bindOut, err := projectCmd(bin, repo, xdg, "project", "bind", "acme").CombinedOutput()
	if err != nil {
		t.Fatalf("project bind: %v\n%s", err, bindOut)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.toml")); !strings.Contains(string(b), `project = "acme"`) {
		t.Fatalf("bind did not record the project in satelle.toml:\n%s", b)
	}

	// push → uploads the authored set (AC2).
	pushOut, err := projectCmd(bin, repo, xdg, "project", "push", "--server", ts.URL).CombinedOutput()
	if err != nil {
		t.Fatalf("project push: %v\n%s", err, pushOut)
	}
	if !strings.Contains(string(pushOut), "Pushed") {
		t.Fatalf("push output unexpected:\n%s", pushOut)
	}

	// pull into a CLEAN dir → reconstruct the tree (AC3).
	dst := t.TempDir()
	pullOut, err := projectCmd(bin, repo, xdg, "project", "pull", "--server", ts.URL, "--dir", dst).CombinedOutput()
	if err != nil {
		t.Fatalf("project pull: %v\n%s", err, pullOut)
	}

	// The authored set = the repo's git-tracked .satelle files. Compare byte-exact.
	authored := gitTracked(t, repo)
	if len(authored) == 0 {
		t.Fatal("no authored substrate tracked — test fixture is wrong")
	}
	for _, rel := range authored {
		src, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			t.Fatalf("read source %s: %v", rel, err)
		}
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Errorf("restored tree missing %s: %v", rel, err)
			continue
		}
		if string(got) != string(src) {
			t.Errorf("restored %s differs from source (not byte-identical)", rel)
		}
	}

	// The restored tree contains ONLY the authored set — no extras, and crucially
	// no local-only state (satelle.db + sidecars) ever crossed the wire (AC5).
	restored := walkRel(t, filepath.Join(dst, ".satelle"))
	authoredSet := map[string]bool{}
	for _, rel := range authored {
		authoredSet[filepath.ToSlash(strings.TrimPrefix(rel, ".satelle/"))] = true
	}
	for _, rel := range restored {
		if !authoredSet[rel] {
			t.Errorf("restored tree has an unexpected file %q (guardrail leak?)", rel)
		}
	}
	for _, forbidden := range []string{"satelle.db", "satelle.db-wal", "satelle.db-shm"} {
		if _, err := os.Stat(filepath.Join(dst, ".satelle", forbidden)); err == nil {
			t.Errorf("local-only %q must never be backed up/restored (AC5)", forbidden)
		}
	}

	// Idempotent re-push (AC2): nothing changed.
	rePush, err := projectCmd(bin, repo, xdg, "project", "push", "--server", ts.URL).CombinedOutput()
	if err != nil {
		t.Fatalf("re-push: %v\n%s", err, rePush)
	}
	if !strings.Contains(string(rePush), "up to date") {
		t.Errorf("identical re-push should report idempotence:\n%s", rePush)
	}
}

func gitRun(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// gitTracked returns the repo-relative (forward-slash) paths git tracks under
// .satelle/.
func gitTracked(t *testing.T, repo string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "ls-files", "--", ".satelle").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var paths []string
	for _, p := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, filepath.ToSlash(p))
		}
	}
	return paths
}

// walkRel returns every regular file under root, as forward-slash paths relative
// to root.
func walkRel(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}
