package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The serve-version gate (scripts/check-serve-version.sh) demands a
// satelled.version bump when a source file the daemon binary is built from
// changes. Its watch set used to be a hand-kept four-entry array while the
// binary compiled in sixteen in-repo packages, so a change to
// `internal/serve` — which runs ONLY inside the service — released with no
// bump, `satelle update` reported "already up to date", and the running service
// silently kept the old code (sty_a8853e85).
//
// These tests live HERE, beside deps_test.go, and not under tests/: every file
// in tests/ carries `//go:build integration`, which GitHub CI deliberately does
// not run. A guarantee against a silent gate belongs where every push checks
// it. They are the mirror of TestServeBinaryImportIsolation — that one asserts
// what must NOT link, these assert that everything which DOES link is watched.

// gateScript is the checked script, module-relative.
const gateScript = "scripts/check-serve-version.sh"

// repoRoot walks up to the directory holding go.mod.
//
// It also READS the gate script, which looks pointless and is not: these tests
// exercise the script through a subprocess, and `go test` keys its cache on the
// files the TEST PROCESS opens. Without this read, editing the script leaves a
// stale pass in the cache and the tests answer a question about the previous
// version. Verified: reverting the derivation by hand produced a cached "ok".
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			if _, err := os.ReadFile(filepath.Join(d, gateScript)); err != nil {
				t.Fatalf("cannot read %s: %v", gateScript, err)
			}
			return d
		}
		if filepath.Dir(d) == d {
			t.Fatal("no go.mod above this package")
		}
	}
}

// serveDeps returns the in-repo package directories the serve binary links,
// module-relative (e.g. "internal/serve").
func serveDeps(t *testing.T, root string) []string {
	t.Helper()
	mod := run(t, root, "go", "list", "-m")
	deps := run(t, root, "go", "list", "-deps", "./cmd/satelled")
	var out []string
	for _, ln := range strings.Split(deps, "\n") {
		ln = strings.TrimSpace(ln)
		if rest, ok := strings.CutPrefix(ln, mod+"/"); ok {
			out = append(out, rest)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no in-repo dependencies resolved for the serve binary (module %q)", mod)
	}
	return out
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// watches reports whether the gate considers path part of the serve build.
// Exit 0 means watched, exit 1 unwatched; anything else is the script failing
// rather than answering, and must not be read as "unwatched".
func watches(t *testing.T, root, path string) bool {
	t.Helper()
	cmd := exec.Command("bash", gateScript, "--check-path", path)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("--check-path %s: %v\n%s", path, err, out)
	}
	if code := ee.ExitCode(); code != 1 {
		t.Fatalf("--check-path %s: exit %d (want 0 watched / 1 unwatched)\n%s", path, code, out)
	}
	return false
}

// TestServeGateWatchesEveryServeDependency is the enumeration, as a test rather
// than as a snapshot: it re-derives the serve binary's dependency set on every
// run and requires the gate to watch all of it. A new package joining the serve
// build fails here on the commit that adds it, so the drift this story fixes
// cannot silently recur.
func TestServeGateWatchesEveryServeDependency(t *testing.T) {
	root := repoRoot(t)
	deps := serveDeps(t, root)
	var missed []string
	for _, pkg := range deps {
		if !watches(t, root, pkg+"/anyfile.go") {
			missed = append(missed, pkg)
		}
	}
	if len(missed) > 0 {
		t.Errorf("the serve binary links these packages but the version gate does not watch them:\n  %s\n"+
			"A change confined to one of them would release with no satelled.version bump, and the "+
			"running service would silently keep the old code.", strings.Join(missed, "\n  "))
	}
	// A watch set that collapsed to nothing would pass the loop above vacuously.
	if len(deps) < 5 {
		t.Errorf("only %d in-repo serve dependencies resolved — the derivation looks broken, "+
			"and a gate that watches almost nothing passes this test for the wrong reason", len(deps))
	}
}

// TestServeGateWatchesInternalServe names the originating case explicitly.
// TestServeGateWatchesEveryServeDependency covers it, but a regression is
// easier to read when the story's own defect has an assertion of its own.
func TestServeGateWatchesInternalServe(t *testing.T) {
	root := repoRoot(t)
	if !watches(t, root, "internal/serve/reconcile.go") {
		t.Error("internal/serve is unwatched — it runs ONLY inside satelled, " +
			"so a change there that ships without a serve bump never reaches a running service")
	}
}

// serveVersionBumped reports whether the working tree's satelled.version
// already differs from the one in the latest serve-v* tag. When it does, the
// gate passes on the version comparison alone and no planted file can change
// that. Old tags still carry satelle-serve.version (compatibility fallback).
func serveVersionBumped(t *testing.T, root string) bool {
	t.Helper()
	base := run(t, root, "git", "tag", "-l", "serve-v*", "--sort=-v:refname")
	if i := strings.IndexByte(base, '\n'); i >= 0 {
		base = base[:i]
	}
	if base == "" {
		return false
	}
	serveVer := func(body string) string {
		var legacy string
		for _, ln := range strings.Split(body, "\n") {
			if f := strings.Fields(ln); len(f) >= 2 {
				switch f[0] {
				case "satelled.version:":
					return f[1]
				case "satelle-serve.version:":
					legacy = f[1]
				}
			}
		}
		return legacy
	}
	head, err := os.ReadFile(filepath.Join(root, ".version"))
	if err != nil {
		t.Fatalf("read .version: %v", err)
	}
	tagged := run(t, root, "git", "show", base+":.version")
	return serveVer(string(head)) != serveVer(tagged)
}

// gateRun runs the gate itself and reports whether it passed.
func gateRun(t *testing.T, root string) (bool, string) {
	t.Helper()
	cmd := exec.Command("bash", gateScript)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

// TestServeGateSeesNewFiles closes the hole a path-prefix check hides: `git
// diff` cannot see an UNTRACKED file, and the release path runs this gate before
// staging. So adding a brand-new source file to a serve package — as real a
// change as editing one — would have passed green.
//
// The _test.go leg is the matching carve-out: a test file is compiled into
// `go test`, never into the shipped binary, so it cannot make a running service
// stale and must not demand a serve release.
func TestServeGateSeesNewFiles(t *testing.T) {
	root := repoRoot(t)
	// The gate compares against the latest serve-v* tag. With no tag it exits
	// early and answers ok to everything, so a planted file would "pass" and this
	// test would report a hole that is not there — or worse, miss one that is.
	// That is not hypothetical: it is how this test first failed CI, on a default
	// shallow, tagless checkout. Refuse to conclude anything instead.
	if run(t, root, "git", "tag", "-l", "serve-v*") == "" {
		t.Skip("no serve-v* tag in this clone — the gate has no baseline, so planting a file proves nothing")
	}
	// A tree whose serve version is ALREADY bumped past the baseline tag cannot
	// be made to fail by planting anything: the gate's verdict turns on the
	// version comparison, which already passes. Asserting "planting a file makes
	// it fail" there tests nothing and fails for the wrong reason — observed on
	// exactly the release that first needed a serve bump after this test landed.
	//
	// This is the third precondition of the same kind (tagless clone, dirty
	// baseline, bumped version). They share one rule: never conclude anything
	// from a gate that could not have failed.
	if serveVersionBumped(t, root) {
		t.Skip("satelled.version is already ahead of the baseline tag — the gate cannot fail, so planting a file proves nothing")
	}
	if ok, out := gateRun(t, root); !ok {
		t.Skipf("gate is already failing in this tree, so planting a file proves nothing:\n%s", out)
	}
	// The probe must carry the package clause its directory already uses. A
	// mismatched one breaks `go list -deps`, and the gate then fails closed for
	// that reason instead of the one under test — which is correct behaviour and
	// a useless test.
	for _, c := range []struct {
		rel      string
		pkg      string
		wantPass bool
		why      string
	}{
		{"internal/serve/zz_gate_probe.go", "serve", false,
			"a new source file in a serve package must demand a serve bump"},
		{"internal/serve/zz_gate_probe_test.go", "serve", true,
			"a test file is never compiled into the shipped binary"},
		{"internal/cli/zz_gate_probe.go", "cli", true,
			"a new file outside the serve build must not demand a serve release"},
	} {
		path := filepath.Join(root, c.rel)
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("%s already exists — refusing to clobber it", c.rel)
		}
		if err := os.WriteFile(path, []byte("package "+c.pkg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ok, out := gateRun(t, root)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if ok != c.wantPass {
			t.Errorf("with %s present the gate %s — %s\n%s", c.rel,
				map[bool]string{true: "PASSED", false: "FAILED"}[ok], c.why, out)
		}
	}
}

// TestServeGateIgnoresCLIOnlyPaths is the no-false-positive half: a slice that
// touches nothing the serve binary links must not demand a serve release. The
// paths chosen are exactly the ones TestServeBinaryImportIsolation proves are
// NOT linked, so this test stays honest only while that isolation holds.
func TestServeGateIgnoresCLIOnlyPaths(t *testing.T) {
	root := repoRoot(t)
	for _, path := range []string{
		"internal/cli/drift.go",
		"internal/verb/changelog.go",
		"internal/store/store.go",
		"internal/app/app.go",
		"internal/agentstep/engine.go",
	} {
		if watches(t, root, path) {
			t.Errorf("%s is not part of the serve build but the gate watches it — "+
				"a CLI-only release would be forced to cut a serve release", path)
		}
	}
}
