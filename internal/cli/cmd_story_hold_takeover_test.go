package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// AC5 (sty_6e5fc41c): a takeover moves the hold only when the holder is out of
// the way, the status is in step and the code is in step. These run the real
// verb over a real bare remote with two clones — this machine's and the
// holder's — against the fake hosted server and hold surface.

type takeoverWorld struct {
	fx      *holdFixture
	bare    string
	holder  string // the holder's clone
	exec    string // the route's executor state
	left    string // a state after it
	baseSHA string // the seed commit both clones have
}

func newTakeoverWorld(t *testing.T) *takeoverWorld {
	t.Helper()
	fx := newHoldFixture(t, holdNoGit)
	writeRoute(t, filepath.Join(fx.repo, ".satelle", "workflows"),
		`["*"]
obligations = ["raised", "coded", "integrated", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[integrated]
status = "integration"
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["integrated"]
`)
	bare := holdRemote(t)
	makeRepoAClone(t, fx.repo, bare)
	w := &takeoverWorld{fx: fx, bare: bare, holder: cloneOf(t, bare), exec: "in_progress", left: "integration"}
	w.baseSHA = gitIn(t, fx.repo, "rev-parse", "HEAD")
	return w
}

// holderCommit commits a file in the holder's clone, optionally pushing it to
// the shared remote, and returns the commit.
func (w *takeoverWorld) holderCommit(t *testing.T, name string, push bool) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(w.holder, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, w.holder, "add", "-A")
	gitIn(t, w.holder, "commit", "-m", name)
	if push {
		gitIn(t, w.holder, "push", "origin", "HEAD:main")
	}
	return gitIn(t, w.holder, "rev-parse", "HEAD")
}

// leaveWork records, as the holder's ledger would, that the story left its
// executor state at head.
func (w *takeoverWorld) leaveWork(t *testing.T, id, head string) {
	t.Helper()
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, now := context.Background(), time.Now().UTC()
	if _, err := a.Store.Stories.SetStatus(ctx, id, w.left, now); err != nil {
		t.Fatal(err)
	}
	tr, _ := json.Marshal(map[string]string{"from": w.exec, "to": w.left})
	cr, _ := json.Marshal(map[string]any{"from": w.exec, "to": w.left, "head_sha": head, "files": []string{}})
	for _, in := range []ledger.AppendInput{
		{StoryID: id, Kind: ledger.KindStatusTransition, Actor: "executor", Body: w.exec + " → " + w.left, Payload: tr},
		{StoryID: id, Kind: ledger.KindChangeRecord, Actor: "executor", Body: "change", Payload: cr},
	} {
		if _, err := a.Store.Ledger.Append(ctx, in, now); err != nil {
			t.Fatal(err)
		}
	}
}

// story creates a story, puts it in its state (published to the hosted copy), and
// has the holder hold it, last seen seen.
func (w *takeoverWorld) story(t *testing.T, title, seen string, place func(id string)) string {
	t.Helper()
	id := createStoryID(t, title)
	place(id)
	w.fx.push(t)
	w.fx.hs.setHolderSeen(id, otherLoc, "desk", seen)
	return id
}

func (w *takeoverWorld) takeover(t *testing.T, id string, extra ...string) (string, error) {
	t.Helper()
	return runRoot(t, append([]string{"story", "hold", "takeover", id, "--server", w.fx.ts.URL}, extra...)...)
}

func (w *takeoverWorld) refused(t *testing.T, id string, extra ...string) string {
	t.Helper()
	out, err := w.takeover(t, id, extra...)
	if err == nil {
		t.Fatalf("the takeover must be refused:\n%s", out)
	}
	if got := w.fx.hs.holder(id).LocationID; got != otherLoc {
		t.Fatalf("a refused takeover moved the hold to %q", got)
	}
	if n := w.fx.hs.takeoverIDs(); len(n) != 0 {
		t.Fatalf("a refused takeover called the server: %v", n)
	}
	t.Logf("refusal:\n%s", err)
	return err.Error()
}

func (w *takeoverWorld) hasCommit(sha string) bool {
	return exec.Command("git", "-C", w.fx.repo, "cat-file", "-e", sha+"^{commit}").Run() == nil
}

const silentSince = "2026-09-01T00:00:00Z" // far past [sync] hold_stale_after

// Code not in step: the commit the story was engaged at exists only in the
// holder's clone, and a fetch cannot find it.
func TestTakeoverRefusedWhenAClaimedCommitIsMissing(t *testing.T) {
	w := newTakeoverWorld(t)
	unpushed := w.holderCommit(t, "unpushed.go", false)
	id := w.story(t, "engaged at an unpushed commit", silentSince, func(id string) {
		putInState(t, id, w.exec, w.holder, unpushed, true)
	})

	msg := w.refused(t, id)

	short := unpushed[:12]
	for _, want := range []string{"takeover of " + id + " refused", "code:", short, "not in this repository", "--force --reason"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "liveness:") || strings.Contains(msg, "status:") {
		t.Errorf("only the code check fails here:\n%s", msg)
	}
}

// Status not in step: this machine's copy is ahead of the hosted one. The
// snapshot records the conflict, leaves the local copy alone, and the takeover is
// refused naming both statuses.
func TestTakeoverRefusedWhenTheStatusDiverged(t *testing.T) {
	w := newTakeoverWorld(t)
	id := w.story(t, "diverged", silentSince, func(string) {})
	time.Sleep(1100 * time.Millisecond) // time-subject: the local row must be a second newer than the hosted one
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.Stories.SetStatus(context.Background(), id, w.left, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()

	msg := w.refused(t, id)

	for _, want := range []string{"status:", "local status " + w.left, "hosted status backlog", "satelle sync workstate pull " + id} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "code:") || strings.Contains(msg, "liveness:") {
		t.Errorf("only the status check fails here:\n%s", msg)
	}
	if got := w.fx.status(t, id); got != w.left {
		t.Errorf("the check changed the local status to %q", got)
	}
	if c := w.fx.conflicts(t)[id]; c.Reason != conflictLocalAhead {
		t.Errorf("the conflict was not recorded: %+v", w.fx.conflicts(t))
	}
}

// Liveness: a holder seen a moment ago is live; the hold stays and the refusal
// says to ask it to release.
func TestTakeoverRefusedWhileTheHolderIsLive(t *testing.T) {
	w := newTakeoverWorld(t)
	seen := time.Now().UTC().Format(time.RFC3339)
	id := w.story(t, "live holder", seen, func(string) {})
	rec := w.fx.f.hostedItem("probe", id)
	updated, err := time.Parse(time.RFC3339Nano, rec["updated_at"].(string))
	if err != nil {
		t.Fatalf("hosted record updated_at: %v", err)
	}

	msg := w.refused(t, id)

	for _, want := range []string{
		"liveness:", "desk", "is live", "story hold release " + id, "hold_stale_after",
		// Both times, each under its own name (sty_aa9eaaba).
		"heartbeat last seen " + seen,
		"story record last updated " + updated.UTC().Format(time.RFC3339),
		// What the heartbeat does not prove, and what to do about it.
		"only that the holder's daemon is running, not that its session is working on this story",
		"confirm with the holder", "--force --reason",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "stalled") {
		t.Errorf("the refusal must not declare the holder stalled:\n%s", msg)
	}
	if strings.Contains(msg, "code:") || strings.Contains(msg, "status:") {
		t.Errorf("only the liveness check fails here:\n%s", msg)
	}
}

// Code not in step, the other way: work started on the holder but nothing after
// it was recorded, so the code may exist only in the holder's tree.
func TestTakeoverRefusedWhenWorkStartedWithNoPostWorkHead(t *testing.T) {
	w := newTakeoverWorld(t)
	id := w.story(t, "work in flight", silentSince, func(id string) {
		putInState(t, id, w.exec, w.holder, w.baseSHA, true)
	})

	msg := w.refused(t, id)

	for _, want := range []string{"code:", "work started on the holder", "may exist only in the holder's tree"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal missing %q:\n%s", want, msg)
		}
	}
}

// --force --reason is the only override: it moves the hold and records the
// reason and the failed checks in the story's ledger.
func TestTakeoverForcedIsAllowedAndRecorded(t *testing.T) {
	w := newTakeoverWorld(t)
	id := w.story(t, "forced", silentSince, func(id string) {
		putInState(t, id, w.exec, w.holder, w.baseSHA, true)
	})

	if out, err := w.takeover(t, id, "--force"); err == nil || !strings.Contains(err.Error(), "--reason") {
		t.Fatalf("--force without --reason must be refused, got %v\n%s", err, out)
	}
	if out, err := w.takeover(t, id, "--reason", "no reason without force"); err == nil {
		t.Fatalf("--reason without --force must be refused:\n%s", out)
	}
	if got := w.fx.hs.holder(id).LocationID; got != otherLoc {
		t.Fatalf("a refused override moved the hold to %q", got)
	}

	out, err := w.takeover(t, id, "--force", "--reason", "the holder's laptop was lost")
	if err != nil {
		t.Fatalf("forced takeover: %v\n%s", err, out)
	}

	if got := w.fx.hs.holder(id).LocationID; got != w.fx.loc {
		t.Errorf("the hold is with %q, want this location %q", got, w.fx.loc)
	}
	if !strings.Contains(out, "hold moved here from "+otherLoc) {
		t.Errorf("output = %q", out)
	}
	if w.fx.recordedHolds(t)[id].Location != w.fx.loc {
		t.Errorf("the registry does not record the hold as taken: %+v", w.fx.recordedHolds(t)[id])
	}
	a, err := app.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	rows, err := a.Store.Ledger.ListByStory(context.Background(), id, ledger.KindComment)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range rows {
		var p forceTakeoverPayload
		if json.Unmarshal(e.Payload, &p) != nil || p.Event != "hold_takeover_forced" {
			continue
		}
		found = true
		if p.Reason != "the holder's laptop was lost" || len(p.Failed) != 1 || p.Failed[0].Name != "code" {
			t.Errorf("recorded override = %+v, want the reason and the one failed check (code)", p)
		}
		if !strings.Contains(e.Body, "the holder's laptop was lost") {
			t.Errorf("ledger body = %q", e.Body)
		}
	}
	if !found {
		t.Fatalf("no forced-takeover row in the story's ledger: %d comment row(s)", len(rows))
	}
}

// All in step with a silent holder: the holder pushed its commit, this machine has
// not fetched it, and the takeover's own fetch makes it reachable.
func TestTakeoverAllowedWhenEverythingIsInStep(t *testing.T) {
	w := newTakeoverWorld(t)
	pushed := w.holderCommit(t, "pushed.go", true)
	if w.hasCommit(pushed) {
		t.Fatal("precondition: this machine has not fetched the holder's commit")
	}
	id := w.story(t, "all in step", silentSince, func(id string) {
		putInState(t, id, w.exec, w.holder, w.baseSHA, true)
		w.leaveWork(t, id, pushed)
	})

	out, err := w.takeover(t, id)
	if err != nil {
		t.Fatalf("takeover: %v\n%s", err, out)
	}

	if !w.hasCommit(pushed) {
		t.Error("the takeover did not fetch the commit it checked for")
	}
	if got := w.fx.hs.holder(id).LocationID; got != w.fx.loc {
		t.Errorf("the hold is with %q, want this location %q", got, w.fx.loc)
	}
	if got := w.fx.hs.takeoverIDs(); len(got) != 1 || got[0] != id+"@"+w.fx.loc {
		t.Errorf("takeovers = %v", got)
	}
}

// A hold the holder released, and a story that finished on the hosted copy, need
// no silence: the holder is out of the way.
func TestTakeoverAllowedWhenReleasedOrFinished(t *testing.T) {
	w := newTakeoverWorld(t)
	released := w.story(t, "released", time.Now().UTC().Format(time.RFC3339), func(string) {})
	w.fx.release(released)
	if out, err := w.takeover(t, released); err != nil {
		t.Fatalf("a released hold: %v\n%s", err, out)
	}

	finished := w.story(t, "finished", time.Now().UTC().Format(time.RFC3339), func(id string) {
		putInState(t, id, "done", "", "", false)
	})
	if out, err := w.takeover(t, finished); err != nil {
		t.Fatalf("a story terminal on the hosted copy: %v\n%s", err, out)
	}
	if got := w.fx.hs.holder(finished).LocationID; got != w.fx.loc {
		t.Errorf("the finished story's hold is with %q, want this location", got)
	}
}
