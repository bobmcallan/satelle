package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// A close releases the engagement seat, and with it the permission to commit the
// slice. These tests drive the real story-set verb over a real store and a real
// git tree, with the verb's dirty-path probe wired to the edit gate's own
// classifier (dirtyGatedPaths), the way app.go wires it.

// countingGater counts the gate runs a transition reaches and accepts each one.
type countingGater struct{ calls atomic.Int32 }

func (g *countingGater) Gate(context.Context, workitem.Item, string) (verb.GateDecision, error) {
	g.calls.Add(1)
	return verb.GateDecision{Gated: true, Accept: true}, nil
}

// The close route is minimal and declares no gate on any edge: backlog →
// in_progress → done, plus a park state and a cancel.
const closeRouteDone = `["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }
`

const closeRouteStep = `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`

type closeEnv struct {
	t     *testing.T
	repo  string
	db    *store.DB
	gater *countingGater
}

// closeGuardEnv is a committed git repo carrying the close route, the verb layer
// wired to its own store, and a counting gater on every edge.
func closeGuardEnv(t *testing.T) *closeEnv {
	t.Helper()
	repo := tempRepo(t)
	t.Chdir(repo)
	// tempRepo's config exempts nothing; the edit gate's default exempts the
	// substrate directory, which is what the exempt-only case relies on.
	exempt := "[review]\ngate_create = false\n\n[gate]\nedit_exempt_paths = [\".satelle/\"]\n"
	if err := os.WriteFile(os.Getenv("SATELLE_CONFIG"), []byte(exempt), 0o644); err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir, closeRouteDone, closeRouteStep)
	gitInitCommit(t, repo)

	withVerbWiring(t)
	cfg, cfgPath, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	root := config.RepoRootFromConfigPath(cfgPath)
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.DocIndex.Sync(context.Background(), map[string]string{"workflows": wfDir}, time.Now()); err != nil {
		t.Fatalf("sync workflows: %v", err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetTxRunner(db.InTx)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetLeaseStore(db.Leases)
	verb.SetStoryDir(filepath.Join(t.TempDir(), "stories"))
	verb.SetAuthoredDirs(cfg.ResolveAuthoredDirs(root))
	verb.SetSubstrateConfigDir(cfg.ResolveDataDir(root))
	g := &countingGater{}
	verb.SetTransitionGater(g)
	verb.SetDirtyPathsProbe(func(_ context.Context, tree string) ([]string, error) { return dirtyGatedPaths(tree) })
	return &closeEnv{t: t, repo: repo, db: db, gater: g}
}

// probeWith replaces the wired dirty-path probe for the rest of the test.
func (e *closeEnv) probeWith(f func(context.Context, string) ([]string, error)) {
	withVerbWiring(e.t)
	verb.SetDirtyPathsProbe(f)
}

// story creates a backlog story.
func (e *closeEnv) story(title string) string {
	e.t.Helper()
	var it workitem.Item
	if err := json.Unmarshal(e.dispatch("story-create", map[string]any{
		"title": title, "body": "goal", "acceptance_criteria": "1. ok", "category": "chore",
	}), &it); err != nil || it.ID == "" {
		e.t.Fatalf("story-create: id %q, err %v", it.ID, err)
	}
	return it.ID
}

func (e *closeEnv) dispatch(name string, req map[string]any) json.RawMessage {
	e.t.Helper()
	raw, _ := json.Marshal(req)
	out, err := verb.Dispatch(context.Background(), name, raw)
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	return out
}

func (e *closeEnv) set(id, status string) error {
	raw, _ := json.Marshal(map[string]any{"id": id, "status": status})
	_, err := verb.Dispatch(context.Background(), "story-set", raw)
	return err
}

func (e *closeEnv) status(id string) string {
	e.t.Helper()
	it, err := e.db.Stories.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return it.Status
}

// seatHeld reports whether the story still holds a live seat.
func (e *closeEnv) seatHeld(id string) bool {
	l, err := e.db.Leases.Get(context.Background(), id)
	if errors.Is(err, lease.ErrNotFound) {
		return false
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return lease.Alive(l, time.Now())
}

func (e *closeEnv) write(rel, body string) {
	e.t.Helper()
	writeRepoFile(e.t, e.repo, rel, body)
}

func (e *closeEnv) git(args ...string) {
	e.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", e.repo}, args...)...).CombinedOutput()
	if err != nil {
		e.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// engage puts a fresh story in_progress and returns it.
func (e *closeEnv) engage() string {
	e.t.Helper()
	id := e.story("close guard")
	if err := e.set(id, "in_progress"); err != nil {
		e.t.Fatalf("engage: %v", err)
	}
	if !e.seatHeld(id) {
		e.t.Fatal("engage did not take the seat")
	}
	return id
}

// AC1, AC3, AC6: a close with a gated uncommitted file is refused with the file
// named, nothing changes and no gate runs; after the commit the same close is
// accepted and releases the seat. The route declares no gate of its own, so the
// refusal does not lean on one.
func TestTerminalCloseRefusedWhileGatedChangesUncommitted(t *testing.T) {
	e := closeGuardEnv(t)
	id := e.engage()
	e.gater.calls.Store(0)
	// A root-level file: porcelain reports an untracked directory as `dir/`.
	e.write("foo.go", "package foo\n")

	err := e.set(id, "done")
	if err == nil {
		t.Fatal("close with an uncommitted gated file must be refused")
	}
	for _, want := range []string{"foo.go", "commit or discard"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name %q, got: %v", want, err)
		}
	}
	if got := e.status(id); got != "in_progress" {
		t.Errorf("status after a refused close = %q, want in_progress", got)
	}
	if !e.seatHeld(id) {
		t.Error("a refused close must leave the seat held")
	}
	if n := e.gater.calls.Load(); n != 0 {
		t.Errorf("a refused close must dispatch no gate, ran %d", n)
	}

	e.git("add", "foo.go")
	e.git("commit", "-qm", "slice")
	if err := e.set(id, "done"); err != nil {
		t.Fatalf("close after the commit: %v", err)
	}
	if got := e.status(id); got != "done" {
		t.Errorf("status = %q, want done", got)
	}
	if e.seatHeld(id) {
		t.Error("the accepted close must release the seat")
	}
	if n := e.gater.calls.Load(); n != 1 {
		t.Errorf("the accepted close runs the gate once, ran %d", n)
	}
}

// AC1: a cancel releases the seat just as done does, but the route synthesises
// the cancel sink without a terminal shape. It is refused the same way.
func TestCancelRefusedWhileGatedChangesUncommitted(t *testing.T) {
	e := closeGuardEnv(t)
	id := e.engage()
	e.write("foo.go", "package foo\n")

	err := e.set(id, "cancelled")
	if err == nil {
		t.Fatal("cancel with an uncommitted gated file must be refused")
	}
	if !strings.Contains(err.Error(), "foo.go") {
		t.Errorf("refusal must name foo.go, got: %v", err)
	}
	if got := e.status(id); got != "in_progress" {
		t.Errorf("status after a refused cancel = %q, want in_progress", got)
	}
	if !e.seatHeld(id) {
		t.Error("a refused cancel must leave the seat held")
	}
}

// AC1: a refused close dispatches no gate. The counting gater in the other
// tests cannot tell a skipped gate from an absent one, so this route gates
// the close with a check that writes a marker file; the real command tree runs
// it. The marker is the control: absent after the refusal, present after the
// commit lets the same close through to the gate.
func TestRefusedTerminalCloseRunsNoGate(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	marker := filepath.Join(t.TempDir(), "gate-ran")
	writeRoute(t, filepath.Join(repo, ".satelle", "workflows"), closeRouteDone,
		strings.Replace(closeRouteStep, `requires = ["coded"]`, `requires = ["coded"]
reviewers = ["close-marker-check"]`, 1))
	writeFile(t, repo, ".satelle/skills/close-marker-check.md",
		"---\nname: close-marker-check\ntype: skill\ndescription: records that the close gate ran\n---\n\n```check\n"+
			"touch '"+marker+"'\n```\n")
	gitInitCommit(t, repo)
	if out, err := runRoot(t, "reindex"); err != nil {
		t.Fatalf("reindex: %v\n%s", err, out)
	}

	out, err := runRoot(t, "story", "create", "--title", "gated close", "--body", "goal",
		"--acceptance", "1. ok", "--category", "chore")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	id := idOf(t, out)
	if out, err := runRoot(t, "story", "set", id, "--status", "in_progress"); err != nil {
		t.Fatalf("engage: %v\n%s", err, out)
	}
	writeRepoFile(t, repo, "foo.go", "package foo\n")

	if out, err := runRoot(t, "story", "set", id, "--status", "done"); err == nil {
		t.Fatalf("close must be refused:\n%s", out)
	} else if !strings.Contains(err.Error(), "foo.go") {
		t.Errorf("refusal must name foo.go, got: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a refused close must not run the gate, but its check ran")
	}

	gitIn(t, repo, "add", "-A") // the command tree also installs hook files
	gitIn(t, repo, "commit", "-qm", "slice")
	if out, err := runRoot(t, "story", "set", id, "--status", "done"); err != nil {
		t.Fatalf("close after the commit: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the accepted close must run the gate (control), marker: %v", err)
	}
}

// AC2: a clean tree, and a tree dirty only on edit-exempt paths, close as today.
func TestTerminalCloseUnchangedWhenCleanOrExemptOnly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		dirty func(e *closeEnv)
	}{
		{"clean", func(*closeEnv) {}},
		{"exempt_only", func(e *closeEnv) { e.write(".satelle/notes.md", "scratch\n") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := closeGuardEnv(t)
			id := e.engage()
			tc.dirty(e)
			if err := e.set(id, "done"); err != nil {
				t.Fatalf("close: %v", err)
			}
			if got := e.status(id); got != "done" {
				t.Errorf("status = %q, want done", got)
			}
			if e.seatHeld(id) {
				t.Error("the close must release the seat")
			}
		})
	}
}

// AC4: a park, a non-exit move and a failing probe are not refused on a dirty
// tree.
func TestDirtyCloseGuardLeavesParkNonExitAndProbeErrorAlone(t *testing.T) {
	t.Run("park", func(t *testing.T) {
		e := closeGuardEnv(t)
		id := e.engage()
		e.write("foo.go", "package foo\n")
		if err := e.set(id, "blocked"); err != nil {
			t.Fatalf("park on a dirty tree: %v", err)
		}
		if got := e.status(id); got != "blocked" {
			t.Errorf("status = %q, want blocked", got)
		}
	})

	// The fixture route has no plan state, so the non-exit move is the engaging
	// backlog → in_progress.
	t.Run("non_exit", func(t *testing.T) {
		e := closeGuardEnv(t)
		id := e.story("non exit")
		e.write("foo.go", "package foo\n")
		if err := e.set(id, "in_progress"); err != nil {
			t.Fatalf("non-exit move on a dirty tree: %v", err)
		}
		if got := e.status(id); got != "in_progress" {
			t.Errorf("status = %q, want in_progress", got)
		}
	})

	t.Run("probe_error", func(t *testing.T) {
		e := closeGuardEnv(t)
		id := e.engage()
		e.write("foo.go", "package foo\n")
		e.probeWith(func(context.Context, string) ([]string, error) {
			return nil, errors.New("git unresolvable")
		})
		if err := e.set(id, "done"); err != nil {
			t.Fatalf("a failing probe must fail open: %v", err)
		}
		if got := e.status(id); got != "done" {
			t.Errorf("status = %q, want done", got)
		}
	})

	t.Run("git_error", func(t *testing.T) {
		e := closeGuardEnv(t)
		id := e.engage()
		e.write("foo.go", "package foo\n")
		if err := os.WriteFile(filepath.Join(e.repo, ".git", "index"), []byte("not an index"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := e.set(id, "done"); err != nil {
			t.Fatalf("an unreadable git index must fail open: %v", err)
		}
	})
}

// AC5: a story with no live seat of its own names no paths, even while another
// story's seat sits on a dirty tree in the same checkout.
func TestSeatlessTerminalMoveIgnoresAnotherStoriesDirtyTree(t *testing.T) {
	e := closeGuardEnv(t)
	// B is engaged and then loses its seat, so the terminal move below is one
	// the guard classifies as a close, made by a story with no seat of its own.
	// A repo-root or other-seat fallback would refuse it on A's dirty file.
	b := e.engage()
	if err := e.db.Leases.ForceRelease(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	a := e.engage()
	e.write("foo.go", "package foo\n")
	before, err := e.db.Leases.Get(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}

	if err := e.set(b, "done"); err != nil {
		t.Fatalf("closing a story with no seat of its own: %v", err)
	}
	if got := e.status(b); got != "done" {
		t.Errorf("status = %q, want done", got)
	}
	after, err := e.db.Leases.Get(context.Background(), a)
	if err != nil {
		t.Fatalf("A's seat must be untouched: %v", err)
	}
	if after.Worktree != before.Worktree || !lease.Alive(after, time.Now()) {
		t.Errorf("A's seat changed: %+v -> %+v", before, after)
	}
}

// The CLI wires the probe itself: a close through the real command tree names
// the dirty file too.
func TestStorySetDoneRefusedThroughTheCommandTree(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	writeRoute(t, filepath.Join(repo, ".satelle", "workflows"), closeRouteDone, closeRouteStep)
	gitInitCommit(t, repo)

	out, err := runRoot(t, "story", "create", "--title", "cli close guard", "--body", "goal",
		"--acceptance", "1. ok", "--category", "chore")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	id, _ := created["id"].(string)
	if out, err := runRoot(t, "story", "set", id, "--status", "in_progress"); err != nil {
		t.Fatalf("engage: %v\n%s", err, out)
	}
	writeRepoFile(t, repo, "foo.go", "package foo\n")
	if out, err := runRoot(t, "story", "set", id, "--status", "done"); err == nil {
		t.Fatalf("close must be refused:\n%s", out)
	} else if !strings.Contains(err.Error(), "foo.go") {
		t.Errorf("refusal must name foo.go, got: %v", err)
	}
}
