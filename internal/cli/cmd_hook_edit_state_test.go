package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestEditPermitted(t *testing.T) {
	base := engagement{
		ItemID: "sty_x", State: "in_progress", StoryStatus: "in_progress",
		StateAgent: "executor", Engaged: true, EditCapable: true,
		EditStates: []string{"in_progress", "release"},
	}
	cases := []struct {
		name   string
		info   engagement
		marker dispatchMarker
		want   bool
	}{
		{"driving executor state", base, dispatchMarker{}, true},
		{"driving transition in flight", withEngagement(base, func(s *engagement) { s.InFlight = true; s.State = "release" }), dispatchMarker{}, false},
		{"planning state", withEngagement(base, func(s *engagement) {
			s.EditCapable = false
			s.State = "plan"
			s.StoryStatus = "plan"
			s.StateAgent = "planner"
		}), dispatchMarker{}, false},
		{"not engaged", engagement{}, dispatchMarker{}, false},
		{"dispatched planner exact", withEngagement(base, func(s *engagement) {
			s.InFlight = true
			s.EditCapable = false
			s.State = "plan"
			s.StoryStatus = "backlog"
			s.StateAgent = "planner"
		}), dispatchMarker{Agent: "planner", Step: "plan", Item: "sty_x"}, true},
		{"dispatched target while committed state remains engaging", withEngagement(base, func(s *engagement) {
			s.InFlight = true
			s.EditCapable = false
			s.State = "plan"
			s.TargetState = "integration"
			s.StoryStatus = "plan"
			s.StateAgent = "coder"
			s.DispatchAgents = map[string][]string{"integration": {"coder"}}
		}), dispatchMarker{Agent: "coder", Step: "integration", Item: "sty_x"}, true},
		{"dispatched wrong agent", withEngagement(base, func(s *engagement) {
			s.InFlight = true
			s.State = "plan"
			s.StateAgent = "planner"
		}), dispatchMarker{Agent: "coder", Step: "plan", Item: "sty_x"}, false},
		{"dispatched wrong item", withEngagement(base, func(s *engagement) {
			s.InFlight = true
			s.State = "plan"
			s.StateAgent = "planner"
		}), dispatchMarker{Agent: "planner", Step: "plan", Item: "sty_other"}, false},
		// Entry dispatch is retired (sty_05a5e203): a reviewer park node allocates
		// no performer, so a marker claiming to be a dispatched agent on it is not
		// a dispatch this engagement authorises.
		{"park entry dispatches nothing", withEngagement(base, func(s *engagement) {
			s.InFlight = true
			s.State = "parked"
			s.StateAgent = "reviewer"
		}), dispatchMarker{Agent: "triage", Step: "parked", Item: "sty_x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := editPermitted(tc.info, tc.marker); got != tc.want {
				t.Fatalf("editPermitted(%+v, %+v) = %v, want %v", tc.info, tc.marker, got, tc.want)
			}
		})
	}
}

func withEngagement(in engagement, mutate func(*engagement)) engagement {
	mutate(&in)
	return in
}

func TestEditPermissionDenyReasonsNameRecovery(t *testing.T) {
	inFlight := engagement{
		ItemID: "sty_x", State: "integration", StoryStatus: "in_progress",
		StateAgent: "executor", Engaged: true, InFlight: true, Mine: true,
	}
	got := editPermissionDenyReason(inFlight, nil, "")
	for _, want := range []string{"IN FLIGHT", "sty_x", "integration", "satelle story attach"} {
		if !strings.Contains(got, want) {
			t.Errorf("in-flight reason missing %q: %s", want, got)
		}
	}
	plan := engagement{
		ItemID: "sty_x", State: "plan", StoryStatus: "plan",
		StateAgent: "planner", Engaged: true, EditStates: []string{"in_progress", "release"},
	}
	got = editPermissionDenyReason(inFlight, nil, "")
	if !strings.Contains(got, "the driving session cannot edit") {
		t.Errorf("driver in-flight must address the driver: %s", got)
	}
	inFlight.Mine = false
	got = editPermissionDenyReason(inFlight, nil, "")
	if strings.Contains(got, "the driving session cannot edit") {
		t.Errorf("unstamped/non-mine must not be addressed as the driver: %s", got)
	}
	if strings.Contains(got, "not the driver") {
		t.Errorf("unstamped/non-mine must not claim this session is not the driver: %s", got)
	}
	if !strings.Contains(got, "IN FLIGHT") || !strings.Contains(got, "sty_x") {
		t.Errorf("unstamped/non-mine must still name the in-flight story: %s", got)
	}
	got = editPermissionDenyReason(plan, nil, "")
	for _, want := range []string{`at "plan"`, `"planner"`, "in_progress, release", "Do not work ahead", "Read-only"} {
		if !strings.Contains(got, want) {
			t.Errorf("state reason missing %q: %s", want, got)
		}
	}
}

// editStateRepo scaffolds a repo with a story at status engaged from the working
// tree; inFlight adds an open transition toward target.
func editStateRepo(t *testing.T, status, target string, inFlight bool) string {
	t.Helper()
	repo := tempRepo(t)
	t.Chdir(repo)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRoute(t, wfDir,
		`["*"]
obligations = ["raised", "planned", "coded", "integrated", "released", "closed"]
park = { state = "blocked" }
`,
		`[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "planner"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["planned"]

[integrated]
status = "integration"
agent = "executor"
requires = ["coded"]

[released]
status = "release"
agent = "executor"
requires = ["integrated"]

[closed]
status = "done"
terminal = true
requires = ["released"]
`)
	if err := os.MkdirAll(filepath.Join(repo, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "foo.go"), []byte("package internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	story, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "edit state", Body: "goal",
		AcceptanceCriteria: "1. ok", Status: status, Category: "feature",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	recordEngagementOn(t, db, story.ID, status, sessionWorktreeUncached(), config.ResolveSession())
	if inFlight {
		if target == "" {
			target = status
		}
		startTransitionOn(t, db, story.ID, target)
	}
	return repo
}

// TestHookGateDispatchedPerformerSeesInFlight: an isolated performer
// (SATELLE_DISPATCH_*, own hook session_id) resolves its item's in-flight
// transition so editPermitted's dispatch-marker branch can fire.
func TestHookGateDispatchedPerformerSeesInFlight(t *testing.T) {
	repo := editStateRepo(t, "in_progress", "integration", true)
	stubWorktree(t, repo)
	storyID := onlyStoryID(t)

	t.Setenv(config.DispatchAgentEnv, "executor")
	t.Setenv(config.DispatchStepEnv, "integration")
	t.Setenv(config.DispatchItemEnv, storyID)

	out, err := runRootIn(t, `{"session_id":"sess-performer","tool_input":{"file_path":"internal/foo.go"}}`, "hook", "gate")
	if err != nil {
		t.Fatalf("dispatched performer must see its item's in-flight transition: %v\n%s", err, out)
	}
}

// TestHookGateDecidesFromTheTreeNotTheSession: the gate attributes an edit by
// the tree it lands in. A session other than the one that engaged the story is
// allowed at an executor step, and is refused by an in-flight transition the
// same as the engaging session — addressed as an observer, not the driver.
func TestHookGateDecidesFromTheTreeNotTheSession(t *testing.T) {
	repo := editStateRepo(t, "in_progress", "", false)
	stubWorktree(t, repo)
	storyID := onlyStoryID(t)
	recordEngagement(t, storyID, "in_progress", repo, "sess-A")

	t.Setenv(config.SessionEnv, "sess-B")
	if out, err := runRootIn(t, `{"session_id":"sess-B","tool_input":{"file_path":"internal/foo.go"}}`, "hook", "gate"); err != nil {
		t.Fatalf("a second session in the engaged tree must be allowed: %v\n%s", err, out)
	}

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	startTransitionOn(t, db, storyID, "integration")
	_ = db.Close()
	out, err := runRootIn(t, `{"session_id":"sess-B","tool_input":{"file_path":"internal/foo.go"}}`, "hook", "gate")
	if err == nil {
		t.Fatalf("an in-flight transition must refuse every session in the tree; got allow:\n%s", out)
	}
	if !strings.Contains(out, "IN FLIGHT") || strings.Contains(out, "the driving session cannot edit") {
		t.Fatalf("sess-B must be refused as an observer of the in-flight transition:\n%s", out)
	}
}

func TestGateDeniesPlanningAndInFlightAcrossHarnesses(t *testing.T) {
	prevHarness := hookHarnessFlag
	t.Cleanup(func() { hookHarnessFlag = prevHarness })
	harnesses := []struct {
		name    string
		event   string
		wantOut string
	}{
		{"claude", `{"tool_input":{"file_path":"internal/foo.go"}}`, `"permissionDecision":"deny"`},
		{"grok", `{"toolInput":{"file_path":"internal/foo.go"}}`, `"decision":"deny"`},
	}
	for _, state := range []struct {
		name, status, target string
		inFlight             bool
		want                 string
	}{
		{"plan", "plan", "plan", false, `at "plan"`},
		{"reviewer in flight", "in_progress", "integration", true, "IN FLIGHT"},
	} {
		for _, h := range harnesses {
			t.Run(state.name+"/"+h.name, func(t *testing.T) {
				editStateRepo(t, state.status, state.target, state.inFlight)
				out, err := runRootIn(t, h.event, "hook", "gate", "--harness", h.name)
				if err == nil {
					t.Fatalf("gate allowed mutation:\n%s", out)
				}
				if !strings.Contains(out, h.wantOut) || !strings.Contains(err.Error(), state.want) {
					t.Fatalf("deny output missing harness/state evidence:\n%s", out)
				}
			})
		}
	}
}

func TestGateAllowsDrivingExecutorStates(t *testing.T) {
	prevHarness := hookHarnessFlag
	hookHarnessFlag = ""
	t.Cleanup(func() { hookHarnessFlag = prevHarness })
	for _, status := range []string{"in_progress", "integration", "release"} {
		t.Run(status, func(t *testing.T) {
			editStateRepo(t, status, status, false)
			if out, err := runRootIn(t, `{"tool_input":{"file_path":"internal/foo.go"}}`, "hook", "gate"); err != nil {
				t.Fatalf("gate denied executor state: %v\n%s", err, out)
			}
		})
	}
}

func TestGateAllowsReadOnlyShellDuringPlanButDeniesMutation(t *testing.T) {
	prevHarness := hookHarnessFlag
	t.Cleanup(func() { hookHarnessFlag = prevHarness })
	editStateRepo(t, "plan", "plan", false)
	if out, err := runRootIn(t, `{"tool_input":{"command":"rg TODO internal"}}`, "hook", "gate", "--harness", "claude"); err != nil {
		t.Fatalf("read-only shell denied: %v\n%s", err, out)
	}
	out, err := runRootIn(t, `{"tool_input":{"command":"sed -i s/a/b/ internal/foo.go"}}`, "hook", "gate", "--harness", "claude")
	if err == nil || !strings.Contains(err.Error(), `at "plan"`) {
		t.Fatalf("mutating shell not denied at plan: err=%v out=%s", err, out)
	}
}

// An interpreter fed a program on stdin writes where the command line cannot show
// it, so the gate judges it as it does an Edit: refused outside an executor step,
// allowed in one (sty_5fac62b9).
func TestGateJudgesInterpreterHeredocLikeAnEdit(t *testing.T) {
	prevHarness := hookHarnessFlag
	hookHarnessFlag = ""
	t.Cleanup(func() { hookHarnessFlag = prevHarness })
	cmd := "python3 - <<'PY'\nopen('internal/foo.go','w').write('x')\nPY"
	for _, handler := range []string{"gate", "commitgate"} {
		editStateRepo(t, "plan", "plan", false)
		if out, err := runRootIn(t, bashEvent(cmd), "hook", handler); err == nil || !strings.Contains(err.Error(), `at "plan"`) {
			t.Fatalf("%s allowed a heredoc interpreter write outside an executor step: err=%v out=%s", handler, err, out)
		}
		editStateRepo(t, "in_progress", "in_progress", false)
		if out, err := runRootIn(t, bashEvent(cmd), "hook", handler); err != nil {
			t.Fatalf("%s denied a heredoc interpreter write in an executor step: %v\n%s", handler, err, out)
		}
	}
}

func TestGateFailsClosedForUnknownWorkflowStatus(t *testing.T) {
	prevHarness := hookHarnessFlag
	hookHarnessFlag = ""
	t.Cleanup(func() { hookHarnessFlag = prevHarness })
	editStateRepo(t, "unknown_state", "unknown_state", false)
	out, err := runRootIn(t, `{"tool_input":{"file_path":"internal/foo.go"}}`, "hook", "gate")
	if err == nil {
		t.Fatalf("gate allowed mutation when workflow status could not be classified:\n%s", out)
	}
	for _, want := range []string{"deny", "not declared", "cannot classify edit permission"} {
		if !strings.Contains(out+err.Error(), want) {
			t.Errorf("fail-closed output missing %q:\nout=%s\nerr=%v", want, out, err)
		}
	}
}

func TestCommitGateDeniesInTreeShellMutationOutsideEditState(t *testing.T) {
	prevHarness := hookHarnessFlag
	hookHarnessFlag = ""
	t.Cleanup(func() { hookHarnessFlag = prevHarness })
	editStateRepo(t, "plan", "plan", false)
	out, err := runRootIn(t, bashEvent("sed -i s/a/b/ internal/foo.go"), "hook", "commitgate")
	if err == nil || !strings.Contains(err.Error(), `at "plan"`) {
		t.Fatalf("commitgate allowed in-tree mutation outside an edit state: err=%v out=%s", err, out)
	}
}

func TestHookEditPermittedRelay(t *testing.T) {
	base := engagement{
		ItemID: "sty_x", State: "in_progress", StoryStatus: "in_progress",
		StateAgent: "coder", Engaged: true, EditCapable: false,
		DispatchAgents: map[string][]string{"in_progress": {"coder"}},
		EditStates:     []string{"integration", "release"},
	}
	cases := []struct {
		name string
		info engagement
		dm   dispatchMarker
		rm   relayMarker
		want bool
	}{
		{"relay coder allocated", base, dispatchMarker{}, relayMarker{Binding: "coder", Item: "sty_x"}, true},
		{"relay binding not allocated", base, dispatchMarker{}, relayMarker{Binding: "reviewer-consult", Item: "sty_x"}, false},
		{"relay wrong item", base, dispatchMarker{}, relayMarker{Binding: "coder", Item: "sty_other"}, false},
		{"relay in flight", withEngagement(base, func(s *engagement) { s.InFlight = true }), dispatchMarker{}, relayMarker{Binding: "coder", Item: "sty_x"}, false},
		{"no marker driving at in_progress", base, dispatchMarker{}, relayMarker{}, false},
		{"no marker at executor status", withEngagement(base, func(s *engagement) {
			s.StoryStatus = "integration"
			s.State = "integration"
			s.StateAgent = "executor"
			s.EditCapable = true
			s.DispatchAgents = map[string][]string{"integration": {"executor"}}
		}), dispatchMarker{}, relayMarker{}, true},
		{"in-flight dispatch marker unchanged", withEngagement(base, func(s *engagement) {
			s.InFlight = true
			s.State = "integration"
			s.TargetState = "integration"
			s.StoryStatus = "in_progress"
			s.StateAgent = "coder"
			s.DispatchAgents = map[string][]string{"integration": {"coder"}}
		}), dispatchMarker{Agent: "coder", Step: "integration", Item: "sty_x"}, relayMarker{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hookEditPermitted(tc.info, tc.dm, tc.rm); got != tc.want {
				t.Fatalf("hookEditPermitted = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRelayDenyReason(t *testing.T) {
	resolved := engagement{
		ItemID: "sty_x", State: "in_progress", StoryStatus: "in_progress",
		StateAgent: "coder", Engaged: true, EditCapable: false,
		DispatchAgents: map[string][]string{"in_progress": {"coder"}},
	}
	cases := []struct {
		name   string
		info   engagement
		rm     relayMarker
		want   []string
		forbid []string
	}{
		{
			name:   "item not performing",
			info:   engagement{},
			rm:     relayMarker{Binding: "coder", Item: "sty_x"},
			want:   []string{"sty_x", "rework relay", "not performing", "committed status"},
			forbid: []string{"without a performing story"},
		},
		{
			name: "in flight",
			info: withEngagement(resolved, func(s *engagement) { s.InFlight = true; s.TargetState = "integration" }),
			rm:   relayMarker{Binding: "coder", Item: "sty_x"},
			want: []string{"in flight", "integration"},
		},
		{
			name:   "binding not allocated",
			info:   resolved,
			rm:     relayMarker{Binding: "reviewer-consult", Item: "sty_x"},
			want:   []string{"rework relay", "reviewer-consult", "[coder]"},
			forbid: []string{"allocated to agent=executor"},
		},
		{
			name: "item mismatch",
			info: resolved,
			rm:   relayMarker{Binding: "coder", Item: "sty_other"},
			want: []string{"sty_other"},
		},
		{
			name:   "allocated binding resolved never names executor rule",
			info:   resolved,
			rm:     relayMarker{Binding: "coder", Item: "sty_x"},
			forbid: []string{"allocated to agent=executor"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := relayDenyReason(tc.info, tc.rm)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in: %s", w, got)
				}
			}
			for _, f := range tc.forbid {
				if strings.Contains(got, f) {
					t.Errorf("forbidden %q in: %s", f, got)
				}
			}
		})
	}
}

func TestUnmarkedDenyReasonUnchanged(t *testing.T) {
	// The deny text reads [gate] exemptions through app config → GlobalDir.
	testutil.IsolateHome(t)
	t.Chdir(t.TempDir())
	cases := []engagement{
		{}, // not engaged
		{ItemID: "sty_x", State: "plan", StoryStatus: "plan", StateAgent: "planner", Engaged: true},
		{ItemID: "sty_x", State: "integration", StoryStatus: "in_progress", StateAgent: "executor", Engaged: true, InFlight: true, Mine: true},
	}
	for i, info := range cases {
		want := editPermissionDenyReason(info, nil, "")
		got := hookDenyReason(info, nil, dispatchMarker{}, relayMarker{}, "")
		if got != want {
			t.Errorf("case %d: unmarked hookDenyReason diverged from editPermissionDenyReason\n got: %s\nwant: %s", i, got, want)
		}
	}
}

func TestHookGateAllowsRelayCoderAndDeniesUnmarked(t *testing.T) {
	prevHarness := hookHarnessFlag
	hookHarnessFlag = ""
	t.Cleanup(func() { hookHarnessFlag = prevHarness })

	leaseA := "a4de0ec7-5809-4d35-8356-9f7c01769098"
	repo, storyID := coderAllocatedRepo(t, leaseA)
	origTree := sessionWorktree
	t.Cleanup(func() { sessionWorktree = origTree })
	sessionWorktree = func() string { return repo }

	t.Setenv(config.SessionEnv, leaseA)
	t.Setenv(config.RelayBindingEnv, "coder")
	t.Setenv(config.RelayItemEnv, storyID)
	if out, err := runRootIn(t, `{"tool_input":{"file_path":"internal/cli/foo.go"}}`, "hook", "gate"); err != nil {
		t.Fatalf("gate must allow marked relay coder: %v\n%s", err, out)
	}
	if out, err := runRootIn(t, bashEvent("sed -i s/a/b/ internal/cli/foo.go"), "hook", "commitgate"); err != nil {
		t.Fatalf("commitgate must allow marked relay coder: %v\n%s", err, out)
	}

	t.Setenv(config.RelayBindingEnv, "")
	t.Setenv(config.RelayItemEnv, "")
	out, err := runRootIn(t, `{"tool_input":{"file_path":"internal/cli/foo.go"}}`, "hook", "gate")
	if err == nil {
		t.Fatalf("unmarked session at coder-allocated status must deny:\n%s", out)
	}
	if strings.Contains(err.Error(), "rework relay") {
		t.Fatalf("unmarked deny must stay on editPermissionDenyReason, got: %v", err)
	}
	if !strings.Contains(err.Error(), "allocated to agent=executor") {
		t.Fatalf("unmarked deny should name executor rule: %v", err)
	}
}

// TestHookGateRelayIgnoresTheEngagingSession: a marked relay coder is judged by
// its item's status and allocation, not by which session engaged the story — a
// relay run under another session id is not refused for it (sty_9c782f0e).
func TestHookGateRelayIgnoresTheEngagingSession(t *testing.T) {
	prevHarness := hookHarnessFlag
	hookHarnessFlag = ""
	t.Cleanup(func() { hookHarnessFlag = prevHarness })

	engagedA := "a4de0ec7-5809-4d35-8356-9f7c01769098"
	sessionB := "31261381-b59d-49af-8306-a8a2c925a9fe"
	repo, storyID := coderAllocatedRepo(t, engagedA)
	origTree := sessionWorktree
	t.Cleanup(func() { sessionWorktree = origTree })
	sessionWorktree = func() string { return repo }

	t.Setenv(config.SessionEnv, sessionB)
	t.Setenv(config.RelayBindingEnv, "coder")
	t.Setenv(config.RelayItemEnv, storyID)
	if out, err := runRootIn(t, `{"tool_input":{"file_path":"internal/cli/foo.go"}}`, "hook", "gate"); err != nil {
		t.Fatalf("a relay coder at its allocated status is permitted whatever session engaged the story: %v\n%s", err, out)
	}
}
