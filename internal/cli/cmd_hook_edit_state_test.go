package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		{"on-enter exact", withSeat(base, func(s *seatInfo) {
			s.InFlight = true
			s.State = "parked"
			s.StateAgent = "reviewer"
			s.OnEnterAgent = "triage"
		}), dispatchMarker{Agent: "triage", Step: "parked", Item: "sty_x"}, true},
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
		StateAgent: "executor", Engaged: true, InFlight: true,
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
	body := `---
name: edit-state-wf
type: workflow
applies_to: ["*"]
---

` + "```dot\n" + `digraph w {
  backlog [shape=Mdiamond]
  plan [agent=planner]
  in_progress [agent=executor]
  integration [agent=executor]
  release [agent=executor]
  blocked [agent=reviewer]
  done [shape=Msquare]
  backlog -> plan -> in_progress -> integration -> release -> done
  in_progress -> blocked
  blocked -> in_progress
}
` + "```\n"
	if err := os.WriteFile(filepath.Join(wfDir, "edit-state-wf.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
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
