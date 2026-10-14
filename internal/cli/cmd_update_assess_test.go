package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/releasestate"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// updateRelease is an httptest release mirror for the post-update assessment
// tests (sty_15a374a8): the CLI at v0.0.99, and optionally a serve release.
type updateRelease struct {
	installDir string
	cliScript  []byte
}

// newUpdateRelease points the update verb at a mirror whose newest CLI release
// (v0.0.99) is a stub reporting 0.0.99.
func newUpdateRelease(t *testing.T, serveTag string, serveBody []byte) *updateRelease {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is POSIX-only")
	}
	resetUpdateFlags(t)
	t.Cleanup(func() { resetUpdateFlags(t) })
	testutil.IsolateHome(t)
	r := &updateRelease{installDir: t.TempDir()}
	t.Setenv("SATELLE_INSTALL_DIR", r.installDir)
	r.cliScript = []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo 'satelle 0.0.99 (commit new, built t)'; fi\n")
	cliSum := sha256.Sum256(r.cliScript)
	cliName := assetNameFor("satelle", "v0.0.99")

	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v0.0.99"}`)
	})
	mux.HandleFunc("/releases", func(w http.ResponseWriter, _ *http.Request) {
		if serveTag == "" {
			fmt.Fprint(w, `[]`)
			return
		}
		fmt.Fprintf(w, `[{"tag_name":%q,"draft":false}]`, serveTag)
	})
	mux.HandleFunc("/download/v0.0.99/"+cliName, func(w http.ResponseWriter, _ *http.Request) { w.Write(r.cliScript) })
	mux.HandleFunc("/download/v0.0.99/"+cliName+".sha256", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(cliSum[:]), cliName)
	})
	if serveTag != "" {
		serveName := assetNameFor("satelled", serveTag)
		serveSum := sha256.Sum256(serveBody)
		mux.HandleFunc("/download/"+serveTag+"/"+serveName, func(w http.ResponseWriter, _ *http.Request) { w.Write(serveBody) })
		mux.HandleFunc("/download/"+serveTag+"/"+serveName+".sha256", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(serveSum[:]), serveName)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("SATELLE_RELEASE_API", srv.URL+"/releases/latest")
	t.Setenv("SATELLE_RELEASE_LIST_API", srv.URL+"/releases")
	t.Setenv("SATELLE_RELEASE_BASE", srv.URL+"/download")
	return r
}

// installOld puts a 0.0.50 CLI stub in the install dir.
func (r *updateRelease) installOld(t *testing.T) {
	t.Helper()
	old := []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo 'satelle 0.0.50 (commit old, built t)'; fi\n")
	if err := os.WriteFile(filepath.Join(r.installDir, "satelle"), old, 0o755); err != nil {
		t.Fatal(err)
	}
}

// installCurrent puts the published v0.0.99 CLI in the install dir.
func (r *updateRelease) installCurrent(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.installDir, "satelle"), r.cliScript, 0o755); err != nil {
		t.Fatal(err)
	}
}

// stubAssessment replaces the runAssessment seam, recording each argv and
// returning err.
func stubAssessment(t *testing.T, err error) *[][]string {
	t.Helper()
	var calls [][]string
	prev := runAssessment
	runAssessment = func(_ context.Context, out io.Writer, bin string, args ...string) error {
		calls = append(calls, append([]string{bin}, args...))
		fmt.Fprintln(out, "doctor: stub findings")
		return err
	}
	t.Cleanup(func() { runAssessment = prev })
	return &calls
}

// chdirSatelleRepo chdirs into a temp dir holding a .satelle/ (a satelle repo);
// pin also drops a .satelle/satelle stub reporting 0.0.42.
func chdirSatelleRepo(t *testing.T, pin bool) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".satelle", config.ConfigName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if pin {
		script := []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo 'satelle 0.0.42 (commit pin, built t)'; fi\n")
		if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle"), script, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(repo)
	return repo
}

// TestUpdateAssessesRepoAfterInstall (sty_15a374a8 AC1): a global install in an
// unpinned satelle repo runs the NEW binary's doctor there under a heading
// naming the new version, with the estate guidance still following.
func TestUpdateAssessesRepoAfterInstall(t *testing.T) {
	r := newUpdateRelease(t, "", nil)
	r.installOld(t)
	calls := stubAssessment(t, nil)
	chdirSatelleRepo(t, false)

	out, err := runRoot(t, "update", "--no-restart")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	want := [][]string{{filepath.Join(r.installDir, "satelle"), "doctor"}}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("assessment calls = %v, want %v", *calls, want)
	}
	for _, s := range []string{"assessing this repo against satelle v0.0.99", "doctor: stub findings", "Repos you already have"} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q:\n%s", s, out)
		}
	}
	if strings.Index(out, "doctor: stub findings") > strings.Index(out, "Repos you already have") {
		t.Errorf("assessment must print before the estate guidance:\n%s", out)
	}
}

// TestUpdatePinnedRepoIsNotAssessedUnderNewVersion (AC1): a repo that pins its
// own binary keeps running it, so it is told so, not judged by the new one.
func TestUpdatePinnedRepoIsNotAssessedUnderNewVersion(t *testing.T) {
	r := newUpdateRelease(t, "", nil)
	r.installOld(t)
	calls := stubAssessment(t, nil)
	chdirSatelleRepo(t, true)

	out, err := runRoot(t, "update", "--no-restart")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if len(*calls) != 0 {
		t.Fatalf("pinned repo must not run doctor, got %v", *calls)
	}
	for _, s := range []string{"pinned binary 0.0.42", "satelle update --local"} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "assessing this repo against") {
		t.Errorf("pinned repo must not present an assessment under the new version:\n%s", out)
	}
}

// TestUpdateAssessmentRunsOnlyWhereItApplies (AC1): outside a satelle repo, on a
// no-op update, under --check, and under --local there is no in-repo assessment.
func TestUpdateAssessmentRunsOnlyWhereItApplies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		inRepo  bool
		current bool // the installed CLI is already the latest
	}{
		{"outside a repo", []string{"update", "--no-restart"}, false, false},
		{"no-op update", []string{"update", "--no-restart"}, true, true},
		{"--check", []string{"update", "--check"}, true, false},
		{"--local", []string{"update", "--local", "--no-restart"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newUpdateRelease(t, "", nil)
			if tc.current {
				r.installCurrent(t)
			} else {
				r.installOld(t)
			}
			calls := stubAssessment(t, nil)
			if tc.inRepo {
				chdirSatelleRepo(t, false)
			} else {
				t.Chdir(t.TempDir())
			}
			out, err := runRoot(t, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v\n%s", tc.args, err, out)
			}
			if len(*calls) != 0 || strings.Contains(out, "assessing this repo") {
				t.Fatalf("no assessment expected; calls=%v\n%s", *calls, out)
			}
		})
	}
}

// TestUpdateBareDotSatelleIsNotARepo (AC1): a directory holding a bare .satelle/
// with no satelle.toml is not a satelle repo, so running the update beneath it
// assesses nothing.
func TestUpdateBareDotSatelleIsNotARepo(t *testing.T) {
	r := newUpdateRelease(t, "", nil)
	r.installOld(t)
	calls := stubAssessment(t, nil)
	bare := t.TempDir()
	sub := filepath.Join(bare, "work", "dir")
	for _, d := range []string{filepath.Join(bare, ".satelle"), sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(sub)

	out, err := runRoot(t, "update", "--no-restart")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if len(*calls) != 0 || strings.Contains(out, "assessing this repo") {
		t.Fatalf("bare .satelle/ must not be assessed; calls=%v\n%s", *calls, out)
	}
}

// TestUpdateGlobalHomeIsNeverARepo (AC1): the global home is a .satelle/ under
// the user's home; run from there, update assesses nothing and the pin helper
// does not mistake a binary in it for a repo pin.
func TestUpdateGlobalHomeIsNeverARepo(t *testing.T) {
	r := newUpdateRelease(t, "", nil)
	r.installOld(t)
	calls := stubAssessment(t, nil)
	home := t.TempDir()
	global := filepath.Join(home, ".satelle")
	if err := os.MkdirAll(global, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_HOME", global)
	if err := os.WriteFile(filepath.Join(global, localBinaryName), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)

	if pin, ok := repoPin(home); ok {
		t.Fatalf("repoPin matched the global home: %q", pin)
	}
	out, err := runRoot(t, "update", "--no-restart")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if len(*calls) != 0 || strings.Contains(out, "assessing this repo") || strings.Contains(out, "pinned binary") {
		t.Fatalf("global home must not be assessed or read as a pin; calls=%v\n%s", *calls, out)
	}
}

// TestUpdateUnhealthyAssessmentKeepsInstall (AC2): a failing doctor after a
// successful install fails the verb, names `satelle init`, and leaves the new
// binary installed.
func TestUpdateUnhealthyAssessmentKeepsInstall(t *testing.T) {
	r := newUpdateRelease(t, "", nil)
	r.installOld(t)
	stubAssessment(t, &exec.ExitError{})
	chdirSatelleRepo(t, false)

	out, err := runRoot(t, "update", "--no-restart")
	if err == nil {
		t.Fatalf("unhealthy assessment must fail the verb:\n%s", out)
	}
	for _, s := range []string{"binary updated to v0.0.99", "satelle init"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q missing %q", err, s)
		}
	}
	got, _ := os.ReadFile(filepath.Join(r.installDir, "satelle"))
	if string(got) != string(r.cliScript) {
		t.Fatalf("install must stand after an unhealthy assessment, got %q", got)
	}
	if !strings.Contains(out, "Repos you already have") {
		t.Errorf("estate guidance must still print before the failure:\n%s", out)
	}
}

// TestUpdateNamesBothVersionsAndRefreshesServe (AC3): satelled is refreshed even
// though the CLI was already current, and the output names both versions.
func TestUpdateNamesBothVersionsAndRefreshesServe(t *testing.T) {
	serveNew := []byte("#!/bin/sh\necho 'satelled 0.0.7 (commit sss, built t)'\n")
	r := newUpdateRelease(t, "serve-v0.0.7", serveNew)
	r.installCurrent(t)
	serveOld := []byte("#!/bin/sh\necho 'satelled 0.0.5 (commit old, built t)'\n")
	servePath := filepath.Join(r.installDir, "satelled")
	if err := os.WriteFile(servePath, serveOld, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	out, err := runRoot(t, "update", "--no-restart")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(servePath); string(got) != string(serveNew) {
		t.Fatalf("satelled not refreshed while the CLI was current: %q", got)
	}
	if !strings.Contains(out, "satelle 0.0.99 · satelled 0.0.7") {
		t.Fatalf("output must name both versions:\n%s", out)
	}
}

// TestUpdateWritesReleaseCache (sty_15a374a8 AC5): both `update` and
// `update --check` record the resolved releases under GlobalDir().
func TestUpdateWritesReleaseCache(t *testing.T) {
	for _, args := range [][]string{{"update", "--no-restart"}, {"update", "--check"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			serveBody := []byte("#!/bin/sh\necho 'satelled 0.0.7 (commit sss, built t)'\n")
			r := newUpdateRelease(t, "serve-v0.0.7", serveBody)
			r.installOld(t)
			if err := os.WriteFile(filepath.Join(r.installDir, "satelled"), serveBody, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(t.TempDir())
			cache := filepath.Join(config.GlobalDir(), "release-check.json")
			if _, err := os.Stat(cache); err == nil {
				t.Fatalf("cache must not pre-exist")
			}

			if out, err := runRoot(t, args...); err != nil {
				t.Fatalf("%v: %v\n%s", args, err, out)
			}
			st, ok, err := releasestate.Load(config.GlobalDir())
			if err != nil || !ok {
				t.Fatalf("release cache not written at %s (ok=%v err=%v)", cache, ok, err)
			}
			if st.CLILatest != "v0.0.99" || st.ServeLatest != "serve-v0.0.7" || st.ServeRunning != "0.0.7" || st.CheckedAt.IsZero() {
				t.Fatalf("cache = %+v", st)
			}
		})
	}
}
