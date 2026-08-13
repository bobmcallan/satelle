package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestEditPermitted(t *testing.T) {
	base := seatInfo{
		ItemID: "sty_x", State: "in_progress", StoryStatus: "in_progress",
		StateAgent: "executor", Engaged: true, EditCapable: true,
		EditStates: []string{"in_progress", "release"},
	}
	cases := []struct {
		name   string
		info   seatInfo
		marker dispatchMarker
		want   bool
	}{
		{"driving executor state", base, dispatchMarker{}, true},
		{"driving transition in flight", withSeat(base, func(s *seatInfo) { s.InFlight = true; s.State = "release" }), dispatchMarker{}, false},
		{"planning state", withSeat(base, func(s *seatInfo) {
			s.EditCapable = false
			s.State = "plan"
			s.StoryStatus = "plan"
			s.StateAgent = "planner"
		}), dispatchMarker{}, false},
		{"stale", withSeat(base, func(s *seatInfo) { s.Stale = true }), dispatchMarker{}, false},
		{"no seat", seatInfo{}, dispatchMarker{}, false},
		{"dispatched planner exact", withSeat(base, func(s *seatInfo) {
			s.InFlight = true
			s.EditCapable = false
			s.State = "plan"
			s.StoryStatus = "backlog"
			s.StateAgent = "planner"
		}), dispatchMarker{Agent: "planner", Step: "plan", Item: "sty_x"}, true},
		{"dispatched target while committed state remains engaging", withSeat(base, func(s *seatInfo) {
			s.InFlight = true
			s.EditCapable = false
			s.State = "plan"
			s.TargetState = "integration"
			s.StoryStatus = "plan"
			s.StateAgent = "coder"
			s.DispatchAgents = map[string][]string{"integration": {"coder"}}
		}), dispatchMarker{Agent: "coder", Step: "integration", Item: "sty_x"}, true},
		{"dispatched wrong agent", withSeat(base, func(s *seatInfo) {
			s.InFlight = true
			s.State = "plan"
			s.StateAgent = "planner"
		}), dispatchMarker{Agent: "coder", Step: "plan", Item: "sty_x"}, false},
		{"dispatched wrong item", withSeat(base, func(s *seatInfo) {
			s.InFlight = true
			s.State = "plan"
			s.StateAgent = "planner"
		}), dispatchMarker{Agent: "planner", Step: "plan", Item: "sty_other"}, false},
		// Entry dispatch is retired (sty_05a5e203): a reviewer park node allocates
		// no performer, so a marker claiming to be a dispatched agent on it is not
		// a dispatch this seat authorises.
		{"park entry dispatches nothing", withSeat(base, func(s *seatInfo) {
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

func withSeat(in seatInfo, mutate func(*seatInfo)) seatInfo {
	mutate(&in)
	return in
}

func TestEditPermissionDenyReasonsNameRecovery(t *testing.T) {
	now := time.Now().UTC()
	inFlight := seatInfo{
		ItemID: "sty_x", State: "integration", StoryStatus: "in_progress",
		StateAgent: "executor", Engaged: true, InFlight: true, Mine: true,
	}
	got := editPermissionDenyReason(inFlight, now)
	for _, want := range []string{"IN FLIGHT", "sty_x", "integration", "satelle story attach"} {
		if !strings.Contains(got, want) {
			t.Errorf("in-flight reason missing %q: %s", want, got)
		}
	}
	plan := seatInfo{
		ItemID: "sty_x", State: "plan", StoryStatus: "plan",
		StateAgent: "planner", Engaged: true, EditStates: []string{"in_progress", "release"},
	}
	got = editPermissionDenyReason(inFlight, now)
	if !strings.Contains(got, "the driving session cannot edit") {
		t.Errorf("driver in-flight must address the driver: %s", got)
	}
	inFlight.Mine = false
	got = editPermissionDenyReason(inFlight, now)
	if strings.Contains(got, "the driving session cannot edit") {
		t.Errorf("unstamped/non-mine must not be addressed as the driver: %s", got)
	}
	if strings.Contains(got, "not the driver") {
		t.Errorf("unstamped/non-mine must not claim this session is not the driver: %s", got)
	}
	if !strings.Contains(got, "IN FLIGHT") || !strings.Contains(got, "sty_x") {
		t.Errorf("unstamped/non-mine must still name the in-flight story: %s", got)
	}
	got = editPermissionDenyReason(plan, now)
	for _, want := range []string{`at "plan"`, `"planner"`, "in_progress, release", "Do not work ahead", "Read-only"} {
		if !strings.Contains(got, want) {
			t.Errorf("state reason missing %q: %s", want, got)
		}
	}
}

func editStateRepo(t *testing.T, status, leaseState string, inFlight bool) string {
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
	if leaseState == "" {
		leaseState = status
	}
	if _, _, _, err := db.Leases.Acquire(ctx, story.ID, "story", lease.ResolveOwner(), leaseState, true); err != nil {
		t.Fatal(err)
	}
	if !inFlight {
		if err := db.Leases.Confirm(ctx, story.ID, status); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// TestHookGateDispatchedPerformerSeesUnstampedInFlight: an isolated performer
// (SATELLE_DISPATCH_*, own hook session_id) must still resolve an unstamped
// in-flight seat so editPermitted's dispatch-marker branch can fire.
func TestHookGateDispatchedPerformerSeesUnstampedInFlight(t *testing.T) {
	editStateRepo(t, "in_progress", "integration", true)
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	origTree := sessionWorktree
	t.Cleanup(func() { sessionWorktree = origTree })
	sessionWorktree = func() string { return repo }
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	leases, err := db.Leases.List(ctx)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if len(leases) != 1 {
		_ = db.Close()
		t.Fatalf("want one lease, got %d", len(leases))
	}
	storyID := leases[0].ItemID
	_ = db.Leases.ForceRelease(ctx, storyID)
	if _, _, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: storyID, Kind: "story", Owner: lease.ResolveOwner(),
		State: "integration", StorySeat: true, Worktree: repo,
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	t.Setenv(config.DispatchAgentEnv, "executor")
	t.Setenv(config.DispatchStepEnv, "integration")
	t.Setenv(config.DispatchItemEnv, storyID)

	out, err := runRootIn(t, `{"session_id":"sess-performer","tool_input":{"file_path":"internal/foo.go"}}`, "hook", "gate")
	if err != nil {
		t.Fatalf("dispatched performer must see the unstamped in-flight seat: %v\n%s", err, out)
	}
}

// TestHookGatePrefersEnvStampOverPayload: a driving session that stamped the
// lease from SATELLE_SESSION must still resolve that seat when the harness
// payload carries a different session_id (the identity namespaces must meet).
func TestHookGatePrefersEnvStampOverPayload(t *testing.T) {
	editStateRepo(t, "in_progress", "in_progress", false)
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	origTree := sessionWorktree
	t.Cleanup(func() { sessionWorktree = origTree })
	sessionWorktree = func() string { return repo }

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	leases, err := db.Leases.List(ctx)
	if err != nil || len(leases) != 1 {
		_ = db.Close()
		t.Fatalf("leases: %v n=%d", err, len(leases))
	}
	storyID := leases[0].ItemID
	_ = db.Leases.ForceRelease(ctx, storyID)
	if _, _, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: storyID, Kind: "story", Owner: lease.ResolveOwner(),
		State: "in_progress", StorySeat: true, Worktree: repo, SessionID: "sess-A",
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Leases.Confirm(ctx, storyID, "in_progress"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	t.Setenv(config.SessionEnv, "sess-A")
	out, err := runRootIn(t, `{"session_id":"harness-uuid","tool_input":{"file_path":"internal/foo.go"}}`, "hook", "gate")
	if err != nil {
		t.Fatalf("driver must keep its stamped seat when hook payload differs: %v\n%s", err, out)
	}
}

// TestHookGatePublishedIdentityStampsAndSkipsSibling: SessionStart-shaped
// publish makes ResolveSession return the harness id; a sibling hook id does
// not inherit the stamped in-flight seat.
func TestHookGatePublishedIdentityStampsAndSkipsSibling(t *testing.T) {
	repo, storyID := liveSeatRepo(t)
	config.PublishSession("sess-A")
	if got := config.ResolveSession(); got != "sess-A" {
		t.Fatalf("publish must be visible to Acquire, got %q", got)
	}
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = db.Leases.ForceRelease(ctx, storyID)
	if _, _, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: storyID, Kind: "story", Owner: lease.ResolveOwner(),
		State: "in_progress", StorySeat: true, SessionID: config.ResolveSession(),
		Worktree: repo,
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	origTree := sessionWorktree
	t.Cleanup(func() { sessionWorktree = origTree })
	sessionWorktree = func() string { return repo }

	out, err := runRootIn(t, `{"session_id":"sess-B","tool_input":{"file_path":"internal/foo.go"}}`, "hook", "gate")
	if err == nil {
		t.Fatalf("sess-B should not inherit sess-A in-flight; got allow:\n%s", out)
	}
	if strings.Contains(out, "IN FLIGHT") {
		t.Fatalf("sess-B must not inherit in-flight gating:\n%s", out)
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
		{"codex", `{"tool_input":{"patch":"*** Begin Patch"}}`, `"permissionDecision":"deny"`},
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
	if out, err := runRootIn(t, `{"tool_input":{"command":["rg","TODO","internal"]}}`, "hook", "gate", "--harness", "codex"); err != nil {
		t.Fatalf("read-only shell denied: %v\n%s", err, out)
	}
	out, err := runRootIn(t, `{"tool_input":{"command":["sed","-i","s/a/b/","internal/foo.go"]}}`, "hook", "gate", "--harness", "codex")
	if err == nil || !strings.Contains(err.Error(), `at "plan"`) {
		t.Fatalf("mutating shell not denied at plan: err=%v out=%s", err, out)
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
