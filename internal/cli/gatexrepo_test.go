package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/gatehandle"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
)

// A gate handle written into another repo's store than the one whose hooks serve
// its session (sty_8f10499d, epic:gate-wake). The case is gw_603dd2a036: a session
// anchored in repo A ran a satelle command that acted on repo B, the handle went
// under B, and A's hooks never looked there.

const xrepoSession = "sess-xrepo"

// siblingRepo makes a second governed repo beside the one tempRepo made, under
// the same SATELLE_HOME, and returns its root. It sets no environment.
func siblingRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"satelle.toml": "[review]\ngate_create = false\n",
		"agents.toml":  "[executor]\nharness = \"in-loop\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// inRepo points the process at root the way a command run there sees it.
func inRepo(t *testing.T, root string) {
	t.Helper()
	t.Setenv("SATELLE_CONFIG", filepath.Join(root, ".satelle", "satelle.toml"))
}

// twoRepos is repo A (the session's) and repo B (where the gate acts), with their
// handle stores. The process starts in A.
func twoRepos(t *testing.T) (aRoot, bRoot string, a, b *gatehandle.Store) {
	t.Helper()
	aRoot = tempRepo(t)
	clearHarnessEnv(t)
	// No session pin unless a test sets one, whatever the suite runs inside.
	t.Setenv("SATELLE_PROJECT_DIR", "")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	bRoot = siblingRepo(t)
	var ok bool
	if a, ok = gateStoreAt(aRoot); !ok {
		t.Fatal("no store for repo A")
	}
	if b, ok = gateStoreAt(bRoot); !ok {
		t.Fatal("no store for repo B")
	}
	if a.RuntimeDir() == b.RuntimeDir() {
		t.Fatalf("the two repos share a runtime dir %s", a.RuntimeDir())
	}
	return aRoot, bRoot, a, b
}

// crossRepoGate is a finished handle in b for session, with a pointer in a: what
// a hand-off from a session anchored in a leaves behind.
func crossRepoGate(t *testing.T, a, b *gatehandle.Store, aRoot, bRoot, session, story string) gatehandle.Meta {
	t.Helper()
	m, err := b.Create(gatehandle.Meta{
		Verb: gateTestVerb, Story: story, Session: session, ServeRoot: aRoot, Repo: bRoot,
		Argv: []string{"story", "set", story, "--status", "ready"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Forward(session, m.ID, b.RuntimeDir()); err != nil {
		t.Fatal(err)
	}
	return m
}

// AC1: the real hand-off. A session anchored in A runs a gate-starting command
// that acts on B; B's store holds the handle; A's hook delivers its verdict.
func TestXRepo_HandOffFromASessionInAnotherRepoIsDeliveredByItsHooks(t *testing.T) {
	aRoot, bRoot, a, b := twoRepos(t)
	args := []string{"gatetest", "--ms", "1500", "--say", "xrepo"}
	useGateHandOff(t, "300ms", args)
	t.Setenv("SATELLE_PROJECT_DIR", aRoot)
	t.Setenv(config.SessionEnv, xrepoSession)

	inRepo(t, bRoot) // the command acts on B
	out, err := runRoot(t, args...)
	if err != nil && err.Error() != errGateHandedOff.Error() {
		t.Fatalf("pending must exit clean: %v\n%s", err, out)
	}
	var p pendingPayload
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &p); jerr != nil || p.Gate != "pending" {
		t.Fatalf("not a pending hand-off: %v\n%s", jerr, out)
	}
	m, merr := b.Meta(p.Handle)
	if merr != nil {
		t.Fatalf("the handle is not in B's store: %v", merr)
	}
	if m.ServeRoot != aRoot || m.Repo != bRoot || m.Session != xrepoSession {
		t.Fatalf("the handle does not record who serves it: %+v", m)
	}
	if len(a.Undelivered()) != 0 {
		t.Fatalf("A's store holds the handle itself: %v", a.Undelivered())
	}

	inRepo(t, aRoot) // the session's hooks fire in A
	var rows []gatehandle.Meta
	old := recordGateDelivered
	recordGateDelivered = func(d []gatehandle.Meta) { rows = append(rows, d...) }
	t.Cleanup(func() { recordGateDelivered = old })

	if got := gateDeliveryFor(0); got != "" {
		t.Fatalf("A's prompt hook delivered a run that had not finished:\n%s", got)
	}
	got := stopGateDeliveryFor(15 * time.Second)
	if !strings.Contains(got, p.Handle) {
		t.Fatalf("A's Stop hook did not deliver B's handle:\n%s", got)
	}
	assertVerdictOnly(t, "the cross-repo delivery", got, "accepted: xrepo")
	if !b.Delivered(p.Handle) {
		t.Error("the handle was not claimed in the store that holds it")
	}
	if len(rows) != 1 || rows[0].Repo != bRoot {
		t.Errorf("the delivery row is not aimed at the repo holding the story: %+v", rows)
	}
	// Neither route tells the session twice.
	if again := gateDeliveryFor(0); again != "" {
		t.Errorf("a delivered cross-repo run was delivered again:\n%s", again)
	}
	if f := a.Forwards(xrepoSession); len(f) != 0 {
		t.Errorf("a delivered run's pointer was left behind: %v", f)
	}
}

// AC1 (prompt hook): a gate that finished between turns rides the next prompt.
func TestXRepo_PromptHookDeliversAFinishedCrossRepoGate(t *testing.T) {
	aRoot, bRoot, a, b := twoRepos(t)
	t.Setenv(config.SessionEnv, xrepoSession)
	m := crossRepoGate(t, a, b, aRoot, bRoot, xrepoSession, "sty_x1")
	finishGate(t, b, m, "accepted plan→in_progress by satelle-x")

	var out strings.Builder
	if err := runHookPrompt(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), m.ID) || !strings.Contains(out.String(), "accepted plan→in_progress") {
		t.Fatalf("the prompt hook did not deliver the cross-repo verdict:\n%s", out.String())
	}
}

// AC2: whichever repo's hook gets there first, the verdict reaches the session
// once: both read the one claim in the store that holds the handle.
func TestXRepo_ClaimedOnceWhicheverRouteIsFirst(t *testing.T) {
	for _, first := range []string{"serving", "holding"} {
		t.Run(first, func(t *testing.T) {
			aRoot, bRoot, a, b := twoRepos(t)
			t.Setenv(config.SessionEnv, xrepoSession)
			m := crossRepoGate(t, a, b, aRoot, bRoot, xrepoSession, "sty_x2")
			finishGate(t, b, m, "accepted once")

			routes := []string{aRoot, bRoot}
			if first == "holding" {
				routes = []string{bRoot, aRoot}
			}
			delivered := 0
			for _, root := range routes {
				inRepo(t, root)
				if strings.Contains(gateDeliveryFor(0), m.ID) {
					delivered++
				}
			}
			if delivered != 1 {
				t.Fatalf("the verdict was delivered %d times", delivered)
			}
		})
	}
}

// AC2: concurrent hooks of the two repos race on the one claim.
func TestXRepo_ConcurrentRoutesDeliverOnce(t *testing.T) {
	aRoot, bRoot, a, b := twoRepos(t)
	t.Setenv(config.SessionEnv, xrepoSession)
	m := crossRepoGate(t, a, b, aRoot, bRoot, xrepoSession, "sty_x3")
	finishGate(t, b, m, "accepted raced")

	var wg sync.WaitGroup
	var mu sync.Mutex
	n := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The stores are what each hook resolves; the claim is the shared part.
			var text string
			if i%2 == 0 {
				text = deliverRefs(ownedGates(handleStoresFor(a, xrepoSession), xrepoSession), 0, modeCatchUp).text
			} else {
				text, _ = deliverGates(b, xrepoSession, 0)
			}
			if strings.Contains(text, m.ID) {
				mu.Lock()
				n++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if n != 1 {
		t.Fatalf("%d routes delivered the verdict, want exactly 1", n)
	}
}

// AC2: a session that does not own the handle never receives it — not through a
// pointer, and not by A's hooks reading the rest of B's store.
func TestXRepo_NeverDeliveredToANonOwner(t *testing.T) {
	aRoot, bRoot, a, b := twoRepos(t)
	m := crossRepoGate(t, a, b, aRoot, bRoot, xrepoSession, "sty_x4")
	finishGate(t, b, m, "accepted private")
	// A run in B with no session identity: B's own hooks may tell anyone, but A's
	// must not reach into B's store for it.
	loose, _ := b.Create(gatehandle.Meta{Verb: gateTestVerb, Story: "sty_loose", Argv: []string{"x"}})
	finishGate(t, b, loose, "accepted loose")

	t.Setenv(config.SessionEnv, "sess-other")
	if got := gateDeliveryFor(0); got != "" {
		t.Fatalf("a session that owns nothing was told:\n%s", got)
	}
	// Even a pointer filed under the wrong session cannot hand it over.
	if err := a.Forward("sess-other", m.ID, b.RuntimeDir()); err != nil {
		t.Fatal(err)
	}
	if got := gateDeliveryFor(0); got != "" {
		t.Fatalf("a mis-filed pointer delivered another session's verdict:\n%s", got)
	}
	t.Setenv(config.SessionEnv, xrepoSession)
	got := gateDeliveryFor(0)
	if !strings.Contains(got, m.ID) || strings.Contains(got, loose.ID) {
		t.Fatalf("the owner got %q; want its own handle and not B's session-less one", got)
	}
	if !b.Delivered(m.ID) || b.Delivered(loose.ID) {
		t.Error("the claim state is wrong: only the owner's handle may be claimed")
	}
}

// A pointer whose handle is gone is harmless and is dropped.
func TestXRepo_StalePointerIsHarmless(t *testing.T) {
	_, bRoot, a, b := twoRepos(t)
	t.Setenv(config.SessionEnv, xrepoSession)
	if err := a.Forward(xrepoSession, "gw_gone000000", b.RuntimeDir()); err != nil {
		t.Fatal(err)
	}
	if err := a.Forward(xrepoSession, "gw_nostore000", filepath.Join(bRoot, "no-such-runtime")); err != nil {
		t.Fatal(err)
	}
	if got := gateDeliveryFor(0); got != "" {
		t.Fatalf("a stale pointer produced output:\n%s", got)
	}
	if f := a.Forwards(xrepoSession); len(f) != 0 {
		t.Errorf("stale pointers were kept: %v", f)
	}
}

// AC3: with the session anchored in the repo the command acts on, nothing is
// forwarded and the handle names no other repo — the same-repo shape is as before.
func TestXRepo_SameRepoHandOffWritesNoPointer(t *testing.T) {
	aRoot, _, a, _ := twoRepos(t)
	args := []string{"gatetest", "--ms", "1500", "--say", "same"}
	useGateHandOff(t, "300ms", args)
	t.Setenv(config.SessionEnv, xrepoSession)
	for _, pin := range []string{aRoot, ""} { // anchored in this repo, and unanchored
		if pin != "" {
			t.Setenv("SATELLE_PROJECT_DIR", pin)
		}
		out, _ := runRoot(t, args...)
		var p pendingPayload
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &p); err != nil {
			t.Fatalf("pin %q: %v\n%s", pin, err, out)
		}
		m, err := a.Meta(p.Handle)
		if err != nil || m.ServeRoot != "" || m.Repo != "" {
			t.Fatalf("pin %q: a same-repo handle names another repo: %+v (%v)", pin, m, err)
		}
		if _, err := os.Stat(filepath.Join(a.Dir(), ".xrepo")); err == nil {
			t.Fatalf("pin %q: a same-repo hand-off wrote a pointer", pin)
		}
	}
	if got := handleStoresFor(a, xrepoSession); len(got) != 1 || got[0].store != a {
		t.Errorf("the same-repo store set is %d stores", len(got))
	}
}

// AC1: a harness need not hand its session's commands a project-dir pin. With no
// pin, the repo the session's own hooks last published is the one that serves it.
func TestXRepo_HandOffFindsTheServingRepoFromItsPublishedHooks(t *testing.T) {
	aRoot, bRoot, _, b := twoRepos(t)
	args := []string{"gatetest", "--ms", "1500", "--say", "published"}
	useGateHandOff(t, "300ms", args)
	t.Setenv("SATELLE_PROJECT_DIR", "")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv(config.SessionEnv, xrepoSession)

	// A hook fires in A for the session.
	publishServeRoot(xrepoSession)
	if got := config.PublishedSessionRoot(xrepoSession); got != aRoot {
		t.Fatalf("the hook published %q, want %q", got, aRoot)
	}

	inRepo(t, bRoot)
	out, _ := runRoot(t, args...)
	var p pendingPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &p); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if m, err := b.Meta(p.Handle); err != nil || m.ServeRoot != aRoot {
		t.Fatalf("the handle does not name the published serving repo: %+v (%v)", m, err)
	}
	inRepo(t, aRoot)
	if got := stopGateDeliveryFor(15 * time.Second); !strings.Contains(got, p.Handle) {
		t.Fatalf("A's Stop hook did not deliver the handle found through the published root:\n%s", got)
	}
}

// A dispatched process runs a gate for its dispatch, not for the session it
// inherited the identity of: its handles are never routed to that session.
func TestXRepo_DispatchedProcessWritesNoPointer(t *testing.T) {
	aRoot, bRoot, a, _ := twoRepos(t)
	args := []string{"gatetest", "--ms", "1500", "--say", "dispatched"}
	useGateHandOff(t, "300ms", args)
	t.Setenv("SATELLE_PROJECT_DIR", aRoot)
	t.Setenv(config.SessionEnv, xrepoSession)
	t.Setenv(config.DispatchAgentEnv, "coder")
	inRepo(t, bRoot)
	if out, _ := runRoot(t, args...); !strings.Contains(out, `"gate":"pending"`) {
		t.Fatalf("not a pending hand-off:\n%s", out)
	}
	if f := a.Forwards(xrepoSession); len(f) != 0 {
		t.Errorf("a dispatched process left a pointer for the driving session: %v", f)
	}
}

// A hand-off with no session has nobody to route to: nothing is forwarded.
func TestXRepo_NoSessionNoPointer(t *testing.T) {
	aRoot, bRoot, a, b := twoRepos(t)
	args := []string{"gatetest", "--ms", "1500", "--say", "anon"}
	useGateHandOff(t, "300ms", args)
	t.Setenv("SATELLE_PROJECT_DIR", aRoot)
	inRepo(t, bRoot)
	out, _ := runRoot(t, args...)
	var p pendingPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &p); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if m, _ := b.Meta(p.Handle); m.ServeRoot != "" {
		t.Errorf("a session-less handle names a serving repo: %+v", m)
	}
	if _, err := os.Stat(filepath.Join(a.Dir(), ".xrepo")); err == nil {
		t.Error("a session-less hand-off wrote a pointer")
	}
}

// AC1 (resume): a pending cross-repo handle arms a watcher that finds the session
// in the serving repo's records and arms the handle in the repo that holds it.
func TestXRepo_PendingHandleArmsAWatcherAcrossRepos(t *testing.T) {
	aRoot, bRoot, a, b := twoRepos(t)
	t.Setenv(config.SessionEnv, grokSession)
	var armedIn []*gatehandle.Store
	var jobs []resumeJob
	old := spawnResumeWatcher
	spawnResumeWatcher = func(s *gatehandle.Store, j resumeJob) error {
		armedIn, jobs = append(armedIn, s), append(jobs, j)
		return nil
	}
	t.Cleanup(func() { spawnResumeWatcher = old })

	m := crossRepoGate(t, a, b, aRoot, bRoot, grokSession, "sty_x5")
	m.Harness = agentcli.HarnessGrok

	armResumeForPending(b, m) // no hook has recorded the session yet
	if len(jobs) != 0 {
		t.Fatalf("armed a watcher for a session no hook has spoken for: %+v", jobs)
	}
	// The hooks ran in A, so the session record is in A — and only there.
	a.NoteSession(grokSession, gatehandle.HarnessSession{Harness: agentcli.HarnessGrok, Session: grokSession, Cwd: aRoot})
	armResumeForPending(b, m)
	if len(jobs) != 1 || jobs[0].Handle != m.ID || jobs[0].Session != grokSession || jobs[0].Cwd != aRoot {
		t.Fatalf("no watcher armed from A's session record: %+v", jobs)
	}
	if jobs[0].ServeRuntime != a.RuntimeDir() || armedIn[0].RuntimeDir() != b.RuntimeDir() {
		t.Errorf("planes crossed: serve %q, handle store %q", jobs[0].ServeRuntime, armedIn[0].RuntimeDir())
	}
	if !b.ResumeArmed(m.ID) || len(a.Undelivered()) != 0 {
		t.Error("the handle must be armed in B and never appear in A")
	}
}

// AC1/AC2 (turn plane): the Stop-side hooks keep the session's turn in A and
// arm a cross-repo handle in B.
func TestXRepo_TurnStateStaysInTheServingRepo(t *testing.T) {
	aRoot, bRoot, a, b := twoRepos(t)
	t.Setenv(config.SessionEnv, grokSession)
	var jobs []resumeJob
	old := spawnResumeWatcher
	spawnResumeWatcher = func(_ *gatehandle.Store, j resumeJob) error { jobs = append(jobs, j); return nil }
	t.Cleanup(func() { spawnResumeWatcher = old })

	m := crossRepoGate(t, a, b, aRoot, bRoot, grokSession, "sty_x6")
	if err := os.WriteFile(b.OutPath(m.ID), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.SetPID(m.ID, os.Getpid()); err != nil { // running
		t.Fatal(err)
	}

	w := resumeWakeFor([]byte(grokStopEvent(t, false)))
	if w == nil {
		t.Fatal("grok has no resume wake")
	}
	w.activity()
	w.closeTurn() // the harness ended the turn on its own

	if _, err := os.Stat(filepath.Join(b.Dir(), ".turns")); err == nil {
		t.Error("turn state was written into the handle's store")
	}
	if _, ok := a.SessionFor(w.owner); !ok {
		t.Error("the session record is not in the serving store")
	}
	if !a.TurnIdle(grokSession, 8, resumeQuiet) {
		t.Error("closeTurn did not close the turn in the serving store")
	}
	if len(jobs) != 1 || jobs[0].Handle != m.ID || jobs[0].ServeRuntime != a.RuntimeDir() || !b.ResumeArmed(m.ID) {
		t.Fatalf("the cross-repo handle was not armed in its own store: %+v", jobs)
	}
}

// AC1/AC2 (watcher): the turn is judged in the serving repo. B has no turn file,
// which TurnIdle reads as idle — a watcher that looked there would resume the
// session under A's open turn.
func TestXRepo_WatcherWaitsOutTheServingRepoTurn(t *testing.T) {
	aRoot, bRoot, a, b := twoRepos(t)
	t.Setenv(config.SessionEnv, grokSession)
	oldPoll := resumeWatchPoll
	resumeWatchPoll = 20 * time.Millisecond
	t.Cleanup(func() { resumeWatchPoll = oldPoll })
	argvs := resumeCapture(t)

	m := crossRepoGate(t, a, b, aRoot, bRoot, grokSession, "sty_x7")
	finishGate(t, b, m, "accepted xrepo watch")
	a.OpenTurn(grokSession)

	job := resumeJob{Handle: m.ID, Harness: agentcli.HarnessGrok, Session: grokSession, Owner: grokSession, ServeRuntime: a.RuntimeDir()}
	done := make(chan error, 1)
	go func() { done <- runGateResume(b, job) }()

	select {
	case err := <-done:
		t.Fatalf("the watcher ran (%v) while the session's turn was open in A", err)
	case <-time.After(300 * time.Millisecond):
	}
	if b.Delivered(m.ID) {
		t.Fatal("the handle was claimed while the serving repo's turn was open")
	}

	a.CloseTurn(grokSession)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher did not resume once the turn closed")
	}
	if len(*argvs) != 1 || !strings.Contains((*argvs)[0][2], "accepted xrepo watch") {
		t.Fatalf("resume argv = %v", *argvs)
	}
	if !b.Delivered(m.ID) {
		t.Error("the handle was not claimed in its own store")
	}
	if _, err := os.Stat(filepath.Join(b.Dir(), ".turns")); err == nil {
		t.Error("the resume lock or turn state landed in the handle's store")
	}
}

// AC1 (ledger): the delivery row is written into the ledger of the repo the
// story lives in, not the one whose hook delivered it.
func TestXRepo_DeliveryRowLandsInTheHoldingRepoLedger(t *testing.T) {
	aRoot, bRoot, a, b := twoRepos(t)
	t.Setenv(config.SessionEnv, xrepoSession)
	m := crossRepoGate(t, a, b, aRoot, bRoot, xrepoSession, "sty_x8")
	finishGate(t, b, m, "accepted ledger")

	withVerbWiring(t)
	_, changed := verb.SnapshotWiring()
	if got := gateDeliveryFor(0); !strings.Contains(got, m.ID) {
		t.Fatalf("not delivered:\n%s", got)
	}
	if got := changed(); len(got) != 0 {
		t.Errorf("the real delivery recorder left verb wiring changed: %v", got)
	}
	if n := driverRowsFor(t, bRoot, "sty_x8"); n != 1 {
		t.Errorf("B's ledger has %d delivery rows for the story, want 1", n)
	}
	if n := driverRowsFor(t, aRoot, "sty_x8"); n != 0 {
		t.Errorf("A's ledger has %d delivery rows for B's story", n)
	}
}

func driverRowsFor(t *testing.T, root, story string) int {
	t.Helper()
	cfg, repoRoot, ok := repoAt(root)
	if !ok {
		t.Fatalf("%s is not a governed repo", root)
	}
	db, err := store.Open(cfg.ResolveDB(repoRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	n := 0
	_ = db.Ledger.ForEachKind(context.Background(), story, ledger.KindDriverUsage, func(ledger.Entry) error {
		n++
		return nil
	})
	return n
}
