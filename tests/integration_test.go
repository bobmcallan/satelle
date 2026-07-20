//go:build integration

// Package tests holds satelle's black-box integration tests: they build the
// real binary and drive it end-to-end through the dogfood flow (init → story →
// index → status → serve), asserting on actual process output. Gated behind the
// `integration` build tag so the default `go test ./...` stays fast and
// hermetic; run with:
//
//	go test -tags integration ./tests/...
package tests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
)

// testBin is the satelle binary under test, resolved once by TestMain.
var testBin string

// testHomes maps t.Name() → isolated SATELLE_HOME for that test. Each test gets
// one empty global registry reused across its run() calls so multi-step flows
// (init then reindex, second init idempotency, etc.) stay coherent, while never
// touching the host ~/.satelle (sty_ee7f40c6).
var testHomes sync.Map

// TestMain resolves the binary the suite drives. If SATELLE_BIN points at an
// existing binary it is used as-is — so the suite can run against a prebuilt or
// installed binary without a rebuild:
//
//	cd tests && SATELLE_BIN=$(command -v satelle) go test -tags integration .
//
// Otherwise satelle is built once into a temp dir shared across all tests.
//
// Before any test runs, SATELLE_HOME is forced to an isolated temp dir so nothing
// in this package can resolve to the host ~/.satelle (sty_ee7f40c6). After the
// suite, the FULL host production surface is re-snapshotted and the process exits
// non-zero if anything changed (sty_6d824d6a): host SATELLE_HOME tree,
// ~/.config/satelle credentials, and ~/.local/bin/satelle. Production listen
// port 8787 is off-limits for test serves (see never-bind-8787 assertions).
func TestMain(m *testing.M) {
	// Resolve host roots BEFORE isolating SATELLE_HOME, then snapshot those fixed
	// paths before and after the suite. Re-resolving via getenv after Setenv would
	// measure the sandbox and false-trip (or hide real host pollution).
	hostRoots := resolveHostRoots()
	beforeSurface := captureHostSurfaceAt(hostRoots)

	backstop, err := os.MkdirTemp("", "satelle-itest-home-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkdtemp home:", err)
		os.Exit(1)
	}
	if err := os.Setenv("SATELLE_HOME", backstop); err != nil {
		fmt.Fprintln(os.Stderr, "setenv SATELLE_HOME:", err)
		_ = os.RemoveAll(backstop)
		os.Exit(1)
	}

	// os.Exit skips defers; clean up and enforce the host-surface guard explicitly.
	exit := func(code int) {
		afterSurface := captureHostSurfaceAt(hostRoots)
		if diffs := diffHostSurface(beforeSurface, afterSurface); len(diffs) > 0 {
			fmt.Fprintf(os.Stderr, "FATAL: host production surface changed during the integration suite (isolation failed).\n")
			fmt.Fprintf(os.Stderr, "  Production port 8787 is off-limits; host ~/.satelle, ~/.config/satelle, and ~/.local/bin/satelle must be untouched.\n")
			for _, d := range diffs {
				fmt.Fprintf(os.Stderr, "  - %s\n", d)
			}
			code = 1
		}
		_ = os.RemoveAll(backstop)
		os.Exit(code)
	}

	if env := os.Getenv("SATELLE_BIN"); env != "" {
		abs, err := filepath.Abs(env)
		if err != nil || !fileExists(abs) {
			fmt.Fprintf(os.Stderr, "SATELLE_BIN=%q not usable: %v\n", env, err)
			exit(1)
		}
		testBin = abs
		exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "satelle-itest")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkdtemp:", err)
		exit(1)
	}
	testBin = filepath.Join(dir, "satelle")
	// The test runs from tests/, so the module root is one level up.
	build := exec.Command("go", "build", "-o", testBin, "./cmd/satelle")
	build.Dir = ".."
	if out, berr := build.CombinedOutput(); berr != nil {
		fmt.Fprintf(os.Stderr, "build satelle: %v\n%s", berr, out)
		_ = os.RemoveAll(dir)
		exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	exit(code)
}

// hostSurface is a content-addressed snapshot of production host state the
// integration suite must not mutate (sty_6d824d6a). Missing roots yield empty
// maps / empty binary fingerprint — skip-safe on fresh CI with no install.
type hostSurface struct {
	// relpath → "dir" or "file:<sha256>:<size>:<mtime_ns>"
	satelleHome map[string]string
	xdgConfig   map[string]string // credentials under ~/.config/satelle
	// installed binary fingerprint; "" when ~/.local/bin/satelle is absent
	installedBin string
	// resolved roots (for diagnostics only)
	satelleHomeRoot  string
	xdgConfigRoot    string
	installedBinPath string
}

// hostRoots are fixed host paths resolved once before SATELLE_HOME isolation.
type hostRoots struct {
	satelleHome  string
	xdgConfig    string
	installedBin string
	// preExistingKeys: key-dir names already under satelleHome before the suite
	// (sty_c36c211f). Live service may mutate those trees; NEW key dirs are
	// pollution and must fail the host-surface guard.
	preExistingKeys map[string]struct{}
}

// resolveHostRoots picks the real machine paths to guard. Honors a pre-existing
// SATELLE_HOME / XDG_CONFIG_HOME when already set (caller sandboxed).
func resolveHostRoots() hostRoots {
	homeDir, _ := os.UserHomeDir()
	r := hostRoots{}
	if v := strings.TrimSpace(os.Getenv("SATELLE_HOME")); v != "" {
		r.satelleHome = v
	} else if homeDir != "" {
		r.satelleHome = filepath.Join(homeDir, ".satelle")
	}
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		r.xdgConfig = filepath.Join(v, "satelle")
	} else if homeDir != "" {
		r.xdgConfig = filepath.Join(homeDir, ".config", "satelle")
	}
	if homeDir != "" {
		r.installedBin = filepath.Join(homeDir, ".local", "bin", "satelle")
	}
	r.preExistingKeys = listRuntimeKeyNames(r.satelleHome)
	return r
}

// listRuntimeKeyNames returns the set of home-keyed runtime dir basenames under home.
func listRuntimeKeyNames(home string) map[string]struct{} {
	out := map[string]struct{}{}
	if home == "" {
		return out
	}
	ents, err := os.ReadDir(home)
	if err != nil {
		return out
	}
	// Match RepoKey shape without importing config (tests package is standalone).
	runtimeKey := regexp.MustCompile(`^[^/]+-[0-9a-f]{8}$`)
	for _, e := range ents {
		if e.IsDir() && runtimeKey.MatchString(e.Name()) {
			out[e.Name()] = struct{}{}
		}
	}
	return out
}

// captureHostSurfaceAt hashes the given fixed host roots (not re-resolved from env).
func captureHostSurfaceAt(r hostRoots) hostSurface {
	return hostSurface{
		satelleHome:      hashTree(r.satelleHome, r.preExistingKeys),
		xdgConfig:        hashTree(r.xdgConfig, nil),
		installedBin:     fingerprintFile(r.installedBin),
		satelleHomeRoot:  r.satelleHome,
		xdgConfigRoot:    r.xdgConfig,
		installedBinPath: r.installedBin,
	}
}

// diffHostSurface returns human-readable change lines; empty when identical.
func diffHostSurface(before, after hostSurface) []string {
	var diffs []string
	diffs = append(diffs, diffTreeMaps("host SATELLE_HOME ("+before.satelleHomeRoot+")", before.satelleHome, after.satelleHome)...)
	diffs = append(diffs, diffTreeMaps("host ~/.config/satelle ("+before.xdgConfigRoot+")", before.xdgConfig, after.xdgConfig)...)
	if before.installedBin != after.installedBin {
		label := before.installedBinPath
		if label == "" {
			label = "installed binary"
		}
		switch {
		case before.installedBin == "" && after.installedBin != "":
			diffs = append(diffs, label+": appeared during suite")
		case before.installedBin != "" && after.installedBin == "":
			diffs = append(diffs, label+": removed during suite")
		default:
			diffs = append(diffs, label+": content/mtime changed during suite")
		}
	}
	return diffs
}

// hashTree walks root and returns a map of relpath → fingerprint. Missing or
// unreadable roots return an empty map (skip-safe).
//
// Under ~/.satelle, project runtime dirs (<basename>-<8hex>/ from RepoKey) that
// were already present before the suite (preExistingKeys) are SKIPPED: a live
// satelle service legitimately mutates them. A NEW key dir is recorded as a
// leaf so diffHostSurface fails the suite (sty_c36c211f — pollution guard).
// The machine-wide push-fed plane (`serve/`) is also skipped when present —
// a live unit rewrites server.log and mirror.db WAL (sty_80233c10). Isolation
// still guards config.toml and other non-runtime host state.
func hashTree(root string, preExistingKeys map[string]struct{}) map[string]string {
	out := map[string]string{}
	if root == "" {
		return out
	}
	info, err := os.Stat(root)
	if err != nil {
		return out
	}
	if !info.IsDir() {
		out["."] = fingerprintFile(root)
		return out
	}
	// name-hex8 — RepoKey shape (e.g. satelle-16882c39, 001-a1b2c3d4).
	runtimeKey := regexp.MustCompile(`^[^/]+-[0-9a-f]{8}$`)
	_ = filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi == nil {
			return nil // skip unreadable leaves
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		if rel == "." {
			out["./"] = "dir"
			return nil
		}
		rel = filepath.ToSlash(rel)
		top := strings.SplitN(rel, "/", 2)[0]
		// Live push-fed serve plane (mirror.db, server.log) — skip contents.
		if top == "serve" {
			if fi.IsDir() && rel == top {
				return filepath.SkipDir
			}
			return nil
		}
		if runtimeKey.MatchString(top) {
			// Pre-existing: skip contents (live service may rewrite DB/logs).
			if _, ok := preExistingKeys[top]; ok {
				if fi.IsDir() && rel == top {
					return filepath.SkipDir
				}
				return nil
			}
			// New during suite: record the key dir itself as pollution evidence.
			if fi.IsDir() && rel == top {
				out[rel+"/"] = "dir"
				return filepath.SkipDir
			}
			return nil
		}
		if fi.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		out[rel] = fingerprintFile(path)
		return nil
	})
	return out
}

// fingerprintFile returns "file:<sha256>:<size>:<mtime_ns>" or "" if missing.
func fingerprintFile(path string) string {
	if path == "" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	sum := hex.EncodeToString(h.Sum(nil))
	return fmt.Sprintf("file:%s:%d:%d", sum, fi.Size(), fi.ModTime().UnixNano())
}

func diffTreeMaps(label string, before, after map[string]string) []string {
	var diffs []string
	keys := map[string]struct{}{}
	for k := range before {
		keys[k] = struct{}{}
	}
	for k := range after {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		b, bok := before[k]
		a, aok := after[k]
		switch {
		case bok && !aok:
			diffs = append(diffs, fmt.Sprintf("%s: removed %s", label, k))
		case !bok && aok:
			diffs = append(diffs, fmt.Sprintf("%s: added %s", label, k))
		case b != a:
			diffs = append(diffs, fmt.Sprintf("%s: changed %s", label, k))
		}
	}
	return diffs
}

func readFileOptional(path string) []byte {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// stubReviewerAccept points the repo's reviewer binding at a deterministic accept
// script, so the now-active baseline gates (materialised by init, sty_5b8bd8b2) do
// not invoke a real agent CLI in hermetic tests. Call after init, before any status
// transition. Gate CONTENT is covered separately (create_review, baseline_skills).
func stubReviewerAccept(t *testing.T, repo string) {
	t.Helper()
	verdict := filepath.Join(repo, "verdict-accept.sh")
	if err := os.WriteFile(verdict, []byte("#!/bin/sh\necho '{\"decision\":\"accept\",\"notes\":\"\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "agents.toml"),
		[]byte(fmt.Sprintf("[reviewer]\ncommand = \"%s {system} {tools} {model}\"\n", verdict)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// isolatedHome returns the per-test SATELLE_HOME (created once via t.TempDir).
// Shared across run() calls in the same test so multi-step flows keep one
// empty-at-start global registry; never the host ~/.satelle.
//
// Keyed by the TOP-LEVEL test name (strip t.Run subtest suffixes) so a parent
// that started serve and a subtest that runs mustRun share one home-keyed DB
// (sty_4660bbe1). Without that, subtests opened empty ledgers and stories
// vanished mid-browser flow.
func isolatedHome(t *testing.T) string {
	t.Helper()
	name := t.Name()
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}
	if v, ok := testHomes.Load(name); ok {
		return v.(string)
	}
	home := t.TempDir()
	if actual, loaded := testHomes.LoadOrStore(name, home); loaded {
		return actual.(string)
	}
	t.Cleanup(func() { testHomes.Delete(name) })
	return home
}

// runtimeRoot returns the home-keyed runtime dir for repo under this test's
// isolated SATELLE_HOME (~/.satelle/<repo-key>/ — sty_4660bbe1). Holds
// satelle.db, logs/, backups/, stories/. Uses Config.ResolveRuntimeDir so a
// tree that gained .git after init still resolves the original path-key dir.
func runtimeRoot(t *testing.T, repo string) string {
	t.Helper()
	// Isolate GlobalDir for this process (ResolveRuntimeDir reads SATELLE_HOME).
	prev, had := os.LookupEnv("SATELLE_HOME")
	_ = os.Setenv("SATELLE_HOME", isolatedHome(t))
	defer func() {
		if had {
			_ = os.Setenv("SATELLE_HOME", prev)
		} else {
			_ = os.Unsetenv("SATELLE_HOME")
		}
	}()
	return config.Config{}.ResolveRuntimeDir(repo).Dir
}

// isolatedEnv returns os.Environ with SATELLE_HOME pinned to this test's
// isolated home (overrides TestMain's suite backstop; last-wins for a key).
func isolatedEnv(t *testing.T) []string {
	t.Helper()
	return append(os.Environ(), "SATELLE_HOME="+isolatedHome(t))
}

// materializeDefault materializes one embedded default onto disk via
// `satelle substrate edit` for tests that need an on-disk file (sty_29e5a9a5).
func materializeDefault(t *testing.T, repo, kind, name string) {
	t.Helper()
	mustRun(t, testBin, repo, "substrate", "edit", kind, name)
}

// materializeDefaultSolution lands the baseline workflows + gate skills used by
// many integration tests that predate virtual sparse defaults.
func materializeDefaultSolution(t *testing.T, repo string) {
	t.Helper()
	for _, wf := range []string{
		"satelle-baseline-workflow", "satelle-parent-workflow",
		"satelle-task-workflow", "satelle-substrate-workflow",
	} {
		materializeDefault(t, repo, "workflows", wf)
	}
	for _, sk := range []string{
		"satelle-estimate-actual-review", "satelle-step-summary",
		"satelle-story-blocked-review", "satelle-story-cancel-review",
		"satelle-story-create-review", "satelle-story-done-review",
		"satelle-story-intent-review", "satelle-task-validate-before-review",
		"satelle-task-validate-after-review", "satelle-workflow-advisor",
		"satelle-reviewer-objective-audit", "satelle-context-audit",
		"satelle-substrate-only-check",
	} {
		materializeDefault(t, repo, "skills", sk)
	}
	for _, pr := range []string{
		"satelle-agent-goals", "satelle-agent-model", "satelle-edits-require-a-story",
		"satelle-done-is-last", "satelle-story-classification",
	} {
		materializeDefault(t, repo, "principles", pr)
	}
}

// run executes the binary in dir with args and returns combined output.
// The subprocess gets the test's isolated SATELLE_HOME so it never reads or
// writes the host machine-wide workspace registry (sty_ee7f40c6). Runtime
// state (DB/logs) is home-keyed under that home (sty_4660bbe1).
func run(t *testing.T, bin, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = isolatedEnv(t)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// mustRun fails the test if the command errors.
// After a successful `init`, hermetic tests opt OUT of create-gating via
// satelle.local.toml (gate_create=false): product init seeds gate_create=true
// (sty_83782ffb), but most suite tests create incomplete drafts to exercise
// other seams. Tests that need the create gate ON write local.toml themselves
// (see create_review_test.go).
func mustRun(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()
	out, err := run(t, bin, dir, args...)
	if err != nil {
		t.Fatalf("satelle %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	if len(args) > 0 && args[0] == "init" {
		hermeticCreateGateOff(t, dir)
	}
	return out
}

// hermeticCreateGateOff flips the init-seeded scaffold's gate_create to false
// in the committed satelle.toml so black-box tests are not forced through
// create-review unless they opt in via satelle.local.toml. Edits the scaffold
// file (not the local overlay) so tests that assert "no local.toml" still pass
// and tests that rewrite local.toml for [vars] do not wipe the opt-out.
func hermeticCreateGateOff(t *testing.T, repo string) {
	t.Helper()
	p := filepath.Join(repo, ".satelle", "satelle.toml")
	b, err := os.ReadFile(p)
	if err != nil {
		return // init may not have written config in exotic cases
	}
	body := string(b)
	if !strings.Contains(body, "gate_create = true") {
		return
	}
	body = strings.Replace(body, "gate_create = true", "gate_create = false", 1)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("hermetic create-gate off: %v", err)
	}
}

// TestSubprocessHomeIsolated proves init via run() never writes the host global
// config, and that an explicit sandbox SATELLE_HOME receives the registry write
// instead (sty_ee7f40c6). Host path is always ~/.satelle under UserHomeDir —
// independent of TestMain's process-wide backstop.
func TestSubprocessHomeIsolated(t *testing.T) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	hostCfg := filepath.Join(userHome, ".satelle", "config.toml")
	before := readFileOptional(hostCfg)

	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	after := readFileOptional(hostCfg)
	if !bytes.Equal(before, after) {
		t.Fatalf("host %s changed after run()-spawned init (isolation failed)\nbefore %d bytes, after %d bytes",
			hostCfg, len(before), len(after))
	}
	if bytes.Contains(after, []byte(repo)) {
		t.Fatalf("host config contains test repo path %q", repo)
	}

	// Positive side: init with an explicit sandbox home must write config.toml
	// there (workspace registration), so we know isolation is not "init wrote nowhere".
	sandbox := t.TempDir()
	sandboxRepo := t.TempDir()
	cmd := exec.Command(testBin, "init")
	cmd.Dir = sandboxRepo
	cmd.Env = append(os.Environ(), "SATELLE_HOME="+sandbox)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox init: %v\n%s", err, out)
	}
	if !fileExists(filepath.Join(sandbox, "config.toml")) {
		t.Fatalf("sandbox SATELLE_HOME has no config.toml after init; init did not register:\n%s", out)
	}
	// Host still untouched.
	after2 := readFileOptional(hostCfg)
	if !bytes.Equal(before, after2) {
		t.Fatalf("host %s changed after sandbox init", hostCfg)
	}
}

// TestHostSurfaceGuardTrips proves the sty_6d824d6a snapshot/diff helpers detect
// a deliberate write into a protected tree, and stay quiet on missing roots
// (skip-safe for fresh CI with no host install).
func TestHostSurfaceGuardTrips(t *testing.T) {
	root := t.TempDir()
	before := hashTree(root, nil)
	if diffs := diffTreeMaps("t", before, hashTree(root, nil)); len(diffs) != 0 {
		t.Fatalf("equal trees must not trip: %v", diffs)
	}
	// Missing roots → empty map, no panic.
	if len(hashTree(filepath.Join(root, "does-not-exist"), nil)) != 0 {
		t.Fatal("missing root must hash to empty map")
	}
	if fingerprintFile(filepath.Join(root, "does-not-exist")) != "" {
		t.Fatal("missing file fingerprint must be empty")
	}
	if err := os.WriteFile(filepath.Join(root, "pollute"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	after := hashTree(root, nil)
	if diffs := diffTreeMaps("t", before, after); len(diffs) == 0 {
		t.Fatal("expected guard to trip after deliberate write into protected tree")
	}
	// Full surface diff names the change.
	beforeS := hostSurface{satelleHome: before, xdgConfig: map[string]string{}, installedBin: ""}
	afterS := hostSurface{satelleHome: after, xdgConfig: map[string]string{}, installedBin: ""}
	if diffs := diffHostSurface(beforeS, afterS); len(diffs) == 0 {
		t.Fatal("diffHostSurface should report the deliberate write")
	}
	// Installed-binary fingerprint changes on rewrite.
	bin := filepath.Join(root, "satelle")
	if err := os.WriteFile(bin, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	fp1 := fingerprintFile(bin)
	// Ensure mtime can move on coarse filesystems.
	time.Sleep(5 * time.Millisecond)
	if err := os.WriteFile(bin, []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	fp2 := fingerprintFile(bin)
	if fp1 == "" || fp2 == "" || fp1 == fp2 {
		t.Fatalf("binary fingerprint must change on rewrite: %q → %q", fp1, fp2)
	}
	beforeS.installedBin = fp1
	afterS.installedBin = fp2
	afterS.installedBinPath = bin
	beforeS.installedBinPath = bin
	if diffs := diffHostSurface(beforeS, afterS); len(diffs) == 0 {
		t.Fatal("expected installed-binary change to trip the guard")
	}
}

func TestDogfoodFlow(t *testing.T) {
	bin := testBin
	repo := t.TempDir()

	// init scaffolds the repo. DB is home-keyed (sty_4660bbe1), not under .satelle/.
	out := mustRun(t, bin, repo, "init")
	for _, want := range []string{".satelle/satelle.toml", "+ .gitignore"} {
		if !strings.Contains(out, want) {
			t.Errorf("init output missing %q:\n%s", want, out)
		}
	}
	homeDB := filepath.Join(runtimeRoot(t, repo), config.DefaultDBName)
	if !strings.Contains(out, homeDB) && !strings.Contains(out, runtimeRoot(t, repo)) {
		t.Errorf("init output missing home-keyed runtime path %s:\n%s", homeDB, out)
	}
	for _, rel := range []string{".satelle/satelle.toml", ".satelle/workflows/README.md"} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Errorf("init did not create %s", rel)
		}
	}
	if _, err := os.Stat(homeDB); err != nil {
		t.Errorf("init did not create home-keyed db %s: %v", homeDB, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", config.DefaultDBName)); err == nil {
		t.Error("init must not write satelle.db under the repo")
	}

	// init is idempotent.
	out = mustRun(t, bin, repo, "init")
	if strings.Contains(out, "  + ") {
		t.Errorf("second init created something:\n%s", out)
	}

	// The materialised baseline gates are now active; stub the reviewer so the
	// status transitions below stay hermetic (sty_5b8bd8b2).
	stubReviewerAccept(t, repo)

	// Index the substrate init materialised (baseline workflow + step skill), as a
	// real session does at SessionStart, so a later authoring index is incremental.
	mustRun(t, bin, repo, "reindex")

	// Create a story and a task.
	out = mustRun(t, bin, repo, "story", "create", "--title", "Dogfood satelle", "--priority", "high", "--tags", "mvp,core")
	if !strings.Contains(out, `"sty_`) || !strings.Contains(out, "Dogfood satelle") {
		t.Fatalf("story create output:\n%s", out)
	}
	storyID := extractID(out, "sty_")
	mustRun(t, bin, repo, "task", "create", "--title", "write release notes")

	// The seeded default's CODED estimate gate enforces out of the box
	// (sty_f804caaa): begin-work without an estimate is rejected deterministically.
	if out, err := run(t, bin, repo, "story", "set", storyID, "--status", "in_progress"); err == nil || !strings.Contains(out, "no plan estimate recorded") {
		t.Fatalf("begin-work without an estimate should be rejected by the coded gate: err=%v\n%s", err, out)
	}
	mustRun(t, bin, repo, "story", "estimate", storyID, "--time", "10m")

	// Move the story along the seeded default workflow.
	out = mustRun(t, bin, repo, "story", "set", storyID, "--status", "in_progress")
	if !strings.Contains(out, `"status": "in_progress"`) {
		t.Errorf("story set status:\n%s", out)
	}

	// Lifecycle events landed in the ledger.
	out = mustRun(t, bin, repo, "ledger", "list", "--story", storyID)
	if !strings.Contains(out, "story_created") || !strings.Contains(out, "status_transition") {
		t.Errorf("ledger missing lifecycle events:\n%s", out)
	}

	// Author markdown and index it.
	docsDir := filepath.Join(repo, ".satelle", "documents")
	if err := os.WriteFile(filepath.Join(docsDir, "onboarding.md"), []byte("# Onboarding\n\nhi"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = mustRun(t, bin, repo, "reindex")
	if !strings.Contains(out, `"indexed": 1`) {
		t.Errorf("index output:\n%s", out)
	}
	out = mustRun(t, bin, repo, "doc", "get", "documents", "onboarding")
	if !strings.Contains(out, `"headline": "Onboarding"`) {
		t.Errorf("doc get headline:\n%s", out)
	}

	// status reflects the counts.
	out = mustRun(t, bin, repo, "status")
	for _, want := range []string{"stories", "tasks", "indexed documents   1"} {
		if !strings.Contains(out, want) {
			t.Errorf("status missing %q:\n%s", want, out)
		}
	}
}

func TestServeServesProjectPage(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	mustRun(t, bin, repo, "init")
	mustRun(t, bin, repo, "story", "create",
		"--title", "Render me",
		"--body", "visible in push-fed UI",
		"--acceptance", "1. listed",
		"--category", "chore",
	)

	const port = "8791"
	base := "http://127.0.0.1:" + port
	// Point workspace add at this serve instance.
	localBody := fmt.Sprintf("[review]\ngate_create = false\n\n[server]\nendpoint = %q\n", base)
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.local.toml"), []byte(localBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "serve", "--port", port)
	cmd.Dir = repo
	// Same home as mustRun so the home-keyed DB has the created story.
	cmd.Env = isolatedEnv(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	if !waitHealthy(t, base+"/healthz", 5*time.Second) {
		t.Fatal("server did not become healthy")
	}
	mustRun(t, bin, repo, "workspace", "add")

	slug := filepath.Base(repo)

	// / is the push-fed workspace landing — lists partitions after workspace add.
	landing := httpGet(t, base+"/")
	for _, want := range []string{"workspace", `href="/r/` + slug + `/"`, "satelle"} {
		if !strings.Contains(landing, want) {
			t.Errorf("landing missing %q:\n%s", want, landing)
		}
	}

	// The project page is under /r/{slug}/.
	body := httpGet(t, base+"/r/"+slug+"/")
	for _, want := range []string{"Render me", "Stories", "Tasks", "satelle"} {
		if !strings.Contains(body, want) {
			t.Errorf("project page missing %q", want)
		}
	}
	if code := httpStatus(t, base+"/nope"); code != 404 {
		t.Errorf("unknown path = %d, want 404", code)
	}
}

// --- helpers ---

func extractID(out, prefix string) string {
	i := strings.Index(out, prefix)
	if i < 0 {
		return ""
	}
	rest := out[i:]
	end := strings.IndexAny(rest, `"`)
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func waitHealthy(t *testing.T, url string, timeout time.Duration) bool {
	t.Helper()
	// Per-request timeout so a blackholed/half-open port fails the poll quickly
	// instead of hanging DefaultClient until TCP timeout (~minutes) and blowing
	// past the overall deadline (browser tests on contended local ports).
	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// freeListenPort binds 127.0.0.1:0 and returns the OS-assigned free port string.
// Prefer this over hardcoded ports: leftover satelle processes and blackholed
// high ports make fixed-port healthz checks false-green or false-fail.
func freeListenPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeListenPort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return strconv.Itoa(port)
}

func httpGet(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		b.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	return b.String()
}

func httpStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestInitDeploysDefaultSolution asserts a fresh init lands the COMPLETE default
// solution virtually (sty_29e5a9a5): no unedited markdown on disk, validators
// green, execution category resolves to the task-execution workflow.
func TestInitDeploysDefaultSolution(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	mustRun(t, bin, repo, "init")

	// Virtual sparse defaults: no seed files for workflows/skills.
	for _, rel := range []string{
		".satelle/workflows/satelle-baseline-workflow.md",
		".satelle/skills/satelle-step-summary.md",
	} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err == nil {
			t.Errorf("init must not seed %s", rel)
		}
	}

	// Effective process is listed and validators pass against the virtual set.
	mustRun(t, bin, repo, "reindex")
	out := mustRun(t, bin, repo, "substrate", "list", "--json")
	if !strings.Contains(out, "satelle-baseline-workflow") || !strings.Contains(out, `"provenance": "default"`) {
		t.Errorf("substrate list missing virtual baseline:\n%s", out)
	}
	mustRun(t, bin, repo, "workflow", "validate")
	mustRun(t, bin, repo, "skill", "validate")

	// An execution resolves to the task-execution workflow, not the wildcard: the
	// head (active) entry of the ordered list for the execution kind-category.
	out = mustRun(t, bin, repo, "workflow", "list", "--category", "execution")
	firstObj := out
	if i := strings.Index(out, "}"); i >= 0 {
		firstObj = out[:i]
	}
	if !strings.Contains(firstObj, `"name": "satelle-task-workflow"`) {
		t.Errorf("head workflow for an execution is not satelle-task-workflow:\n%s", out)
	}

	// And a run can be created against an authored task: init seeds no example
	// task (sty_04ec1fe6), so author one, then create a run against it.
	authored := "---\nid: tsk_run\ntype: task\nstatus: backlog\n---\n\n# Runnable\n\nACTION: do the thing. VERIFICATION: it is done.\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "tasks", "tsk_run.md"), []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, bin, repo, "reindex")
	out = mustRun(t, bin, repo, "execution", "create", "--parent", "tsk_run", "--title", "run 1")
	if !strings.Contains(out, `"exe_`) {
		t.Fatalf("execution create output:\n%s", out)
	}
}

// TestRebaseResetsSubstrate asserts `satelle rebase` aborts without confirmation,
// and with --yes backs up the customized substrate to a timestamped dir, wipes
// it, and redeploys the complete default solution (sty_a7cbd6dd).
func TestRebaseResetsSubstrate(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	mustRun(t, bin, repo, "init")

	// Customize: drift a seeded skill, add an extra authored workflow.
	skill := filepath.Join(repo, ".satelle", "skills", "satelle-code-ac-review.md")
	if err := os.WriteFile(skill, []byte("# drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(repo, ".satelle", "workflows", "extra-workflow.md")
	if err := os.WriteFile(extra, []byte("# extra\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No confirmation (empty stdin) → abort, nothing changed.
	out, err := run(t, bin, repo, "rebase")
	if err != nil || !strings.Contains(out, "aborted") {
		t.Fatalf("unconfirmed rebase: err=%v\n%s", err, out)
	}
	if _, serr := os.Stat(extra); serr != nil {
		t.Error("unconfirmed rebase removed the extra workflow")
	}

	// Confirmed rebase: backup + wipe + redeploy.
	out = mustRun(t, bin, repo, "rebase", "--yes")
	if !strings.Contains(out, "backed up") || !strings.Contains(out, "deployed") {
		t.Errorf("rebase report incomplete:\n%s", out)
	}
	if b, _ := os.ReadFile(skill); strings.Contains(string(b), "# drifted") {
		t.Error("rebase did not reset the drifted skill to the embedded default")
	}
	if _, serr := os.Stat(extra); serr == nil {
		t.Error("rebase left the extra authored workflow in the live dir")
	}
	if _, serr := os.Stat(filepath.Join(repo, ".satelle", "workflows", "satelle-baseline-workflow.md")); serr != nil {
		t.Error("rebase did not redeploy the default base workflow")
	}

	// The backup holds the pre-rebase files.
	backups := filepath.Join(runtimeRoot(t, repo), "backups")
	entries, rerr := os.ReadDir(backups)
	if rerr != nil || len(entries) != 1 {
		t.Fatalf("expected one timestamped backup dir: %v %v", entries, rerr)
	}
	backupDir := filepath.Join(backups, entries[0].Name())
	if _, serr := os.Stat(filepath.Join(backupDir, "workflows", "extra-workflow.md")); serr != nil {
		t.Error("backup missing the extra authored workflow")
	}
	if b, _ := os.ReadFile(filepath.Join(backupDir, "skills", "satelle-code-ac-review.md")); !strings.Contains(string(b), "# drifted") {
		t.Error("backup missing the drifted skill bytes")
	}
}

// TestStoryRestamp exercises the first-class re-stamp (sty_ed3386cf): a story
// stamped at create picks up a category-specific workflow authored later — the
// re-categorise → restamp flow — with the change ledgered; an invalid target is
// rejected without touching the stamp.
func TestStoryRestamp(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	mustRun(t, bin, repo, "init")
	mustRun(t, bin, repo, "reindex")

	// A feature story stamps the seeded wildcard base workflow at create.
	out := mustRun(t, bin, repo, "story", "create", "--title", "Assess the rollout", "--category", "feature")
	if !strings.Contains(out, `"workflow:satelle-baseline-workflow"`) {
		t.Fatalf("create did not stamp the seeded base workflow:\n%s", out)
	}
	id := extractID(out, "sty_")

	// A category-specific governance workflow is authored AFTER create.
	gov := `---
name: gov-workflow
scope: project
type: workflow
tags: [type:workflow]
applies_to: ["governance"]
description: Governance lifecycle moving backlog → in_progress → done with a cancelled exit.
---

# governance workflow

` + "```dot\n" + `digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  cancelled [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  backlog -> in_progress
  in_progress -> done
  backlog -> cancelled
  in_progress -> cancelled
}` + "\n```\n"
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "gov-workflow.md"), gov)
	mustRun(t, bin, repo, "reindex")

	// Re-categorise, then restamp re-resolves from the CURRENT category.
	mustRun(t, bin, repo, "story", "set", id, "--category", "governance")
	out = mustRun(t, bin, repo, "story", "restamp", id)
	if !strings.Contains(out, `"workflow:gov-workflow"`) || strings.Contains(out, `"workflow:satelle-baseline-workflow"`) {
		t.Fatalf("restamp did not swap the governing workflow:\n%s", out)
	}

	// The trail records old -> new. The ledger JSON escapes ">" (>), so
	// match the escaped body as printed.
	out = mustRun(t, bin, repo, "ledger", "list", "--story", id)
	if !strings.Contains(out, "re-stamped: satelle-baseline-workflow -\\u003e gov-workflow") {
		t.Errorf("ledger missing the re-stamp row:\n%s", out)
	}

	// An unknown target is rejected and the stamp is untouched.
	if out, err := run(t, bin, repo, "story", "restamp", id, "--workflow", "nope"); err == nil || !strings.Contains(out, "does not resolve") {
		t.Errorf("unknown-workflow restamp should fail: err=%v\n%s", err, out)
	}
	out = mustRun(t, bin, repo, "story", "get", id)
	if !strings.Contains(out, `"workflow:gov-workflow"`) {
		t.Errorf("stamp changed after a rejected restamp:\n%s", out)
	}
}

// TestInitAgentGuidance asserts real init output ends with the agent-facing note
// when the repo carries CLAUDE.md (sty_4c406061): fold satelle into the
// instruction file, preferring `satelle help` over duplicated docs.
func TestInitAgentGuidance(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "CLAUDE.md"), "# my instructions\n")
	out := mustRun(t, testBin, repo, "init")
	for _, want := range []string{"Agent note:", "CLAUDE.md", `"## satelle" section`, "satelle help"} {
		if !strings.Contains(out, want) {
			t.Errorf("init output missing %q:\n%s", want, out)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "CLAUDE.md")); string(got) != "# my instructions\n" {
		t.Errorf("init modified CLAUDE.md:\n%s", got)
	}
}
