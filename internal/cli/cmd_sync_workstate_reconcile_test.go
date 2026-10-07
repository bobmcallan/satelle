package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// After a work-state pull, each pulled story whose status claims code the git
// remote cannot be shown to hold is named (sty_78e20d15). These drive a real bare
// remote and a real clone as the repo root; only the hosted server is faked.

const absentHead = "0123456789abcdef0123456789abcdef01234567"

var reconcileBase = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// step is the nth second after reconcileBase, so seeded ledger rows keep order.
func step(n int) time.Time { return reconcileBase.Add(time.Duration(n) * time.Second) }

func seedTransition(f *fakeWorkstateServer, story, from, to string, n int) {
	seedHostedLedger(f, "led_tr_"+story+"_"+to+"_"+from, story, "status_transition",
		map[string]any{"from": from, "to": to}, step(n))
}

func seedChange(f *fakeWorkstateServer, story, from, to, head string, n int) {
	p := map[string]any{"from": from, "to": to, "files": []string{}}
	if head != "" {
		p["head_sha"] = head
	}
	seedHostedLedger(f, "led_ch_"+story+"_"+to+"_"+from, story, "change_record", p, step(n))
}

func seedBaseline(f *fakeWorkstateServer, story, tree string, n int) {
	seedHostedLedger(f, "led_bl_"+story, story, "engagement_baseline",
		map[string]any{"head_sha": absentHead, "worktree": tree}, step(n))
}

// reconcileWorld is a satelle repo that is a clone of a bare remote, with the
// route's executor and terminal state names and the heads the cases use.
type reconcileWorld struct {
	f          *fakeWorkstateServer
	url        string
	repo       string
	exec, term string
	onOrigin   string // a commit on the remote
	localOnly  string // a commit in the repo no remote ref contains
}

func newReconcileWorld(t *testing.T, git bool) *reconcileWorld {
	t.Helper()
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	w := &reconcileWorld{f: f, url: ts.URL}
	w.repo = workstateRepo(t, holdSyncToml)
	if git {
		makeRepoAClone(t, w.repo, holdRemote(t))
		w.onOrigin = gitIn(t, w.repo, "rev-parse", "HEAD")
		if err := os.WriteFile(filepath.Join(w.repo, "local.go"), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, w.repo, "add", "-A")
		gitIn(t, w.repo, "commit", "-m", "local only")
		w.localOnly = gitIn(t, w.repo, "rev-parse", "HEAD")
	}
	// One local story so the route can be read; it is not part of the pull.
	w.exec, w.term = executorStateFor(t, createStoryID(t, "route probe"))
	return w
}

func (w *reconcileWorld) pull(t *testing.T, extra ...string) string {
	t.Helper()
	out, err := runRoot(t, append([]string{"sync", "workstate", "pull", "--server", w.url}, extra...)...)
	if err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	return out
}

// worked seeds a story that entered the executor state and then left it to the
// terminal state, recording head on the change record for leaving ("" for none).
func (w *reconcileWorld) worked(id, head string) {
	seedHostedStory(w.f, id, id, w.term, step(10))
	seedTransition(w.f, id, "plan", w.exec, 1)
	seedChange(w.f, id, "plan", w.exec, w.onOrigin, 2) // pre-work head: never proof
	seedTransition(w.f, id, w.exec, w.term, 3)
	if head != "" {
		seedChange(w.f, id, w.exec, w.term, head, 4)
	}
}

func TestPullReportsStoriesWhoseCodeTheRemoteCannotShow(t *testing.T) {
	w := newReconcileWorld(t, true)

	// In flight: its only head is the pre-work head, which IS on origin.
	seedHostedStory(w.f, "sty_flight01", "in flight", w.exec, step(10))
	seedTransition(w.f, "sty_flight01", "plan", w.exec, 1)
	seedChange(w.f, "sty_flight01", "plan", w.exec, w.onOrigin, 2)
	seedBaseline(w.f, "sty_flight01", "/elsewhere/tree", 1)

	w.worked("sty_pushed01", w.onOrigin)
	w.worked("sty_local001", w.localOnly)
	w.worked("sty_absent01", absentHead)
	w.worked("sty_nohead01", "")
	seedHostedStory(w.f, "sty_never001", "never worked", "backlog", step(10))

	out := w.pull(t)

	flight := "stranded sty_flight01 (" + w.exec + "): in " + w.exec + " since " + step(1).Format(time.RFC3339) +
		" — no commit recorded after work started; its code may exist only in /elsewhere/tree"
	for _, want := range []string{
		flight,
		"stranded sty_local001 (" + w.term + "): head " + w.localOnly[:8] + " is not on any remote branch",
		"stranded sty_absent01 (" + w.term + "): head " + absentHead[:8] + " is not in this repository",
		"1 stories left work with no recorded head (--verbose to list)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	for _, silent := range []string{"sty_pushed01", "sty_never001", "sty_nohead01"} {
		if strings.Contains(out, silent) {
			t.Errorf("%s must not be named without --verbose:\n%s", silent, out)
		}
	}
}

func TestPullVerboseListsStoriesThatLeftWorkWithNoHead(t *testing.T) {
	w := newReconcileWorld(t, true)
	w.worked("sty_nohead01", "")
	w.worked("sty_nohead02", "")

	out := w.pull(t, "--verbose")
	for _, want := range []string{"2 stories left work with no recorded head", "sty_nohead01", "sty_nohead02"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// AC5: nothing is reported when every story is consistent with the remote.
func TestPullConsistentStoriesReportNothing(t *testing.T) {
	w := newReconcileWorld(t, true)
	w.worked("sty_pushed01", w.onOrigin)
	seedHostedStory(w.f, "sty_never001", "never worked", "backlog", step(10))

	out := w.pull(t)
	if !strings.Contains(out, "Pulled work-state") {
		t.Fatalf("pull did not run:\n%s", out)
	}
	for _, banned := range []string{"stranded", "no commit recorded", "no recorded head", "reachability unavailable"} {
		if strings.Contains(out, banned) {
			t.Errorf("consistent pull printed %q:\n%s", banned, out)
		}
	}
}

func TestPullWithoutGitSaysReachabilityUnavailable(t *testing.T) {
	w := newReconcileWorld(t, false)
	w.onOrigin = absentHead
	w.worked("sty_pushed01", absentHead)

	out := w.pull(t)
	if !strings.Contains(out, "reachability unavailable (") {
		t.Errorf("want a reachability unavailable line:\n%s", out)
	}
	if strings.Contains(out, "is not in this repository") || strings.Contains(out, "is not on any remote branch") {
		t.Errorf("must not claim a head is missing when git cannot answer:\n%s", out)
	}
}

// writeAuthoredRoute replaces the shipped route with an authored one and indexes it.
func writeAuthoredRoute(t *testing.T, repo, done, steps string) {
	t.Helper()
	meta := func(name string) string {
		return "[meta]\nname = \"" + name + "\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"test route\"\n\n"
	}
	writeRepoFile(t, repo, ".satelle/workflows/done.toml", meta("done")+done)
	writeRepoFile(t, repo, ".satelle/workflows/step.toml", meta("step")+steps)
	out, err := runRoot(t, "reindex")
	if err != nil || strings.Contains(out, "FAIL") {
		t.Fatalf("reindex: %v\n%s", err, out)
	}
}

func TestPullUnresolvedWorkflowSaysSo(t *testing.T) {
	w := newReconcileWorld(t, true)
	// An authored route that does not parse is broken, not absent: nothing
	// resolves, so the story's status cannot be read against a route.
	meta := "[meta]\nname = \"%s\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"broken route\"\n\n"
	writeRepoFile(t, w.repo, ".satelle/workflows/done.toml", strings.Replace(meta, "%s", "done", 1)+"[\"*\"]\nobligations = [\"raised\", \n")
	writeRepoFile(t, w.repo, ".satelle/workflows/step.toml", strings.Replace(meta, "%s", "step", 1)+"[raised]\nstatus = \"backlog\"\nstart = true\n")
	if out, err := runRoot(t, "reindex"); err != nil {
		t.Fatalf("reindex: %v\n%s", err, out)
	}
	seedHostedStory(w.f, "sty_noroute1", "no route", w.term, step(10))
	out := w.pull(t)
	if !strings.Contains(out, "reachability unavailable (workflow not resolved): sty_noroute1") {
		t.Errorf("want the unresolved-workflow line:\n%s", out)
	}
}

// A route whose cancel state is not called "cancelled": a story parked there
// claims no code, so it is not reported; the same history in `done` is.
func TestPullSkipsTheRoutesOwnCancelState(t *testing.T) {
	w := newReconcileWorld(t, true)
	done := "[\"*\"]\nobligations = [\"raised\", \"coded\", \"closed\"]\npark = { state = \"blocked\", gate = \"gate-blocked\" }\ncancel = { state = \"abandoned\", gate = \"gate-cancel\" }\n"
	stepToml := "[raised]\nstatus = \"backlog\"\nstart = true\n\n[coded]\nstatus = \"in_progress\"\nagent = \"executor\"\nrequires = [\"raised\"]\n\n" +
		"[closed]\nstatus = \"done\"\nreviewers = [\"gate-close\"]\nreviewer_agent = \"reviewer\"\nterminal = true\nrequires = [\"coded\"]\n"
	writeAuthoredRoute(t, w.repo, done, stepToml)
	w.exec, w.term = executorStateFor(t, createStoryID(t, "authored route probe"))
	if w.exec != "in_progress" || w.term != "done" {
		t.Fatalf("authored route not in force: exec=%q term=%q", w.exec, w.term)
	}

	for _, id := range []string{"sty_dropped01", "sty_done00001"} {
		status := w.term
		if id == "sty_dropped01" {
			status = "abandoned"
		}
		seedHostedStory(w.f, id, id, status, step(10))
		seedTransition(w.f, id, "plan", w.exec, 1)
		seedTransition(w.f, id, w.exec, status, 3)
	}

	out := w.pull(t)
	if strings.Contains(out, "sty_dropped01") {
		t.Errorf("a story in the route's cancel state must not be reported:\n%s", out)
	}
	if !strings.Contains(out, "1 stories left work with no recorded head") {
		t.Errorf("the done story with the same history must be counted:\n%s", out)
	}
}
