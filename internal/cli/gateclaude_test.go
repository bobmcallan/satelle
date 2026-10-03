package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/gatehandle"
)

// Gate wake on claude (sty_7e4393fc, epic:gate-wake): a finished verdict is
// delivered at the next Stop without waiting for a slower gate, and a turn past
// claude's block cap — or one claude ended itself — gets the verdict by resuming
// the same session. The payloads are claude-shaped: snake_case, with a
// transcript under ~/.claude, which is what the harness sniff recognises.

const claudeSession = "7f4809d9-0c1e-4c6a-9a55-3d1f5c0b2e11"

func claudeEvent(t *testing.T, event string, active bool) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"session_id":       claudeSession,
		"transcript_path":  "/home/u/.claude/projects/-repo/" + claudeSession + ".jsonl",
		"cwd":              os.TempDir(), // a real directory: the resume runs in it
		"permission_mode":  "acceptEdits",
		"hook_event_name":  event,
		"stop_hook_active": active,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// claudeWakeRepo is a governed repo whose driving session is claude, with the
// block cap at cap.
func claudeWakeRepo(t *testing.T, wait string, cap int) *gatehandle.Store {
	t.Helper()
	store := stopWakeRepo(t, wait)
	t.Setenv(config.SessionEnv, claudeSession)
	t.Setenv("CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", strconv.Itoa(cap))
	return store
}

func claudeGate(t *testing.T, store *gatehandle.Store, story string) gatehandle.Meta {
	t.Helper()
	m := runningGateForSession(t, store, story, claudeSession)
	m.Harness = agentcli.HarnessClaude
	return m
}

// AC1: one gate has finished and another is still running. The next Stop hands
// over the finished verdict at once — it does not wait for the slow gate — and a
// later Stop delivers the other. Both the claude wake and the path an
// unrecognised harness keeps are covered; the old behaviour waited for every
// gate, which here would hold the Stop for the whole 20s wait.
func TestStopDeliversAFinishedGateWithoutWaitingForASlowOne(t *testing.T) {
	const wait = 20 * time.Second
	for _, tc := range []struct {
		name  string
		event func(*testing.T, bool) string
		setup func(*testing.T) (*gatehandle.Store, string)
	}{
		{
			name:  "claude wake",
			event: func(t *testing.T, active bool) string { return claudeEvent(t, "Stop", active) },
			setup: func(t *testing.T) (*gatehandle.Store, string) {
				resumeSeams(t)
				return claudeWakeRepo(t, wait.String(), 8), claudeSession
			},
		},
		{
			name: "no resume wake",
			event: func(t *testing.T, active bool) string {
				return fmt.Sprintf(`{"session_id":"other","hook_event_name":"Stop","stop_hook_active":%v}`, active)
			},
			setup: func(t *testing.T) (*gatehandle.Store, string) {
				store := stopWakeRepo(t, wait.String())
				t.Setenv(config.SessionEnv, "other-session")
				return store, "other-session"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, owner := tc.setup(t)
			fast := runningGateForSession(t, store, "sty_fast", owner)
			slow := runningGateForSession(t, store, "sty_slow", owner)
			finishGate(t, store, fast, "accepted fast")

			start := time.Now()
			blk, ok := stopOnce(t, tc.event(t, false))
			if !ok || !strings.Contains(blk.Reason, "accepted fast") {
				t.Fatalf("the finished verdict was not delivered: %+v ok=%v", blk, ok)
			}
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("the Stop waited %s for the slow gate", elapsed)
			}
			if strings.Contains(blk.Reason, slow.ID) {
				t.Errorf("the running gate was reported as delivered: %s", blk.Reason)
			}
			if store.Delivered(slow.ID) || !store.Delivered(fast.ID) {
				t.Fatalf("delivered: fast=%v slow=%v", store.Delivered(fast.ID), store.Delivered(slow.ID))
			}

			finishGate(t, store, slow, "accepted slow")
			blk, ok = stopOnce(t, tc.event(t, true))
			if !ok || !strings.Contains(blk.Reason, "accepted slow") || strings.Contains(blk.Reason, "accepted fast") {
				t.Fatalf("a later Stop did not deliver the other verdict alone: %+v ok=%v", blk, ok)
			}
		})
	}
}

// AC2: more than eight sequential wakes in one turn all deliver. The first cap
// arrive as Stop-blocks; the hook then emits nothing, closes the turn, and the
// rest arrive as the prompt of a resumed turn of the same session. None is
// claimed without being shown. cap 2 is the dogfood's setting.
func TestClaudeWakesPastTheBlockCapAreDeliveredByResume(t *testing.T) {
	for _, cap := range []int{8, 2} {
		t.Run(fmt.Sprintf("cap %d", cap), func(t *testing.T) {
			store := claudeWakeRepo(t, "50ms", cap)
			jobs := resumeSeams(t)
			argvs := resumeCapture(t)

			total := cap + 3
			var shown []string // verdicts the model was shown, by whichever route
			for i := 1; i <= total; i++ {
				m := claudeGate(t, store, fmt.Sprintf("sty_wake%d", i))
				verdict := fmt.Sprintf("accepted wake #%d#", i)
				if i <= cap+1 {
					// A gate the Stop hook sees: finished by the time the driver stops.
					finishGate(t, store, m, verdict)
					out := stopOut(t, claudeEvent(t, "Stop", i > 1))
					if i <= cap {
						var blk stopBlockOut
						if err := json.Unmarshal([]byte(out), &blk); err != nil || blk.Decision != "block" || !strings.Contains(blk.Reason, verdict) {
							t.Fatalf("wake %d of %d was not a Stop-block: %q", i, cap, out)
						}
						shown = append(shown, verdict)
						continue
					}
					if out != "" {
						t.Fatalf("a block past the cap: %q", out)
					}
					if !store.Delivered(m.ID) && len(*jobs) == 0 {
						t.Fatalf("wake %d: neither delivered nor handed to a resume", i)
					}
					if store.Delivered(m.ID) {
						t.Fatalf("wake %d claimed with no route to show it", i)
					}
				} else {
					// Started after the last Stop: the hand-off arms the watcher.
					armResumeForPending(store, m)
					finishGate(t, store, m, verdict)
				}
				if len(*jobs) == 0 {
					t.Fatalf("wake %d: no watcher armed", i)
				}
				if err := runGateResume(store, (*jobs)[len(*jobs)-1]); err != nil {
					t.Fatal(err)
				}
			}
			for _, a := range *argvs {
				if a[0] != "claude" || a[1] != "-p" || strings.Join(a[3:], " ") != "--resume "+claudeSession+" --permission-mode acceptEdits" {
					t.Errorf("not a resume of the same session: %q", a)
				}
				shown = append(shown, a[2])
			}
			if len(*argvs) != total-cap {
				t.Errorf("%d resumes, want %d", len(*argvs), total-cap)
			}
			for i := 1; i <= total; i++ {
				want := fmt.Sprintf("accepted wake #%d#", i)
				n := 0
				for _, s := range shown {
					if strings.Contains(s, want) {
						n++
					}
				}
				if n != 1 {
					t.Errorf("%q was shown %d times", want, n)
				}
			}
			for _, id := range store.Undelivered() {
				t.Errorf("handle %s was never delivered", id)
			}
		})
	}
}

// AC3: a turn claude ends through StopFailure or SessionEnd is recorded closed,
// and the gate it still owes — finished or pending — is resumed.
func TestClaudeTurnEndEventsResumeAFinishedOrPendingGate(t *testing.T) {
	for _, event := range []string{"StopFailure", "SessionEnd"} {
		t.Run(event+" with a finished gate", func(t *testing.T) {
			store := claudeWakeRepo(t, "50ms", 8)
			jobs := resumeSeams(t)
			argvs := resumeCapture(t)
			m := claudeGate(t, store, "sty_fin")
			finishGate(t, store, m, "accepted fin")
			store.OpenTurn(claudeSession) // mid-turn, under budget

			resumeWakeFor([]byte(claudeEvent(t, event, false))).closeTurn()
			if !store.TurnIdle(claudeSession, 8, resumeQuiet) {
				t.Fatal("the turn was not recorded as closed")
			}
			if len(*jobs) != 1 || (*jobs)[0].Handle != m.ID {
				t.Fatalf("no resume armed for the finished gate: %+v", *jobs)
			}
			if err := runGateResume(store, (*jobs)[0]); err != nil {
				t.Fatal(err)
			}
			if len(*argvs) != 1 || !strings.Contains((*argvs)[0][2], "accepted fin") {
				t.Fatalf("the verdict was not resumed: %q", *argvs)
			}
		})
		t.Run(event+" with a pending gate", func(t *testing.T) {
			store := claudeWakeRepo(t, "50ms", 8)
			jobs := resumeSeams(t)
			argvs := resumeCapture(t)
			m := claudeGate(t, store, "sty_pend")

			resumeWakeFor([]byte(claudeEvent(t, event, false))).closeTurn()
			if len(*jobs) != 1 || store.Delivered(m.ID) {
				t.Fatalf("a pending gate: jobs=%+v delivered=%v", *jobs, store.Delivered(m.ID))
			}
			finishGate(t, store, m, "accepted pend")
			if err := runGateResume(store, (*jobs)[0]); err != nil {
				t.Fatal(err)
			}
			if len(*argvs) != 1 || !strings.Contains((*argvs)[0][2], "accepted pend") {
				t.Fatalf("the verdict was not resumed: %q", *argvs)
			}
		})
	}
}

// A turn-end event for a harness with no resume path, or from a process nobody
// recorded, does nothing — and an event that carries no session arms nothing.
func TestTurnEndEventLeavesOtherHarnessesAlone(t *testing.T) {
	store := stopWakeRepo(t, "50ms")
	jobs := resumeSeams(t)
	t.Setenv(config.SessionEnv, "other-session")
	runningGateForSession(t, store, "sty_other", "other-session")
	for _, ev := range []string{`{"session_id":"s","hook_event_name":"SessionEnd"}`, `{}`, ``} {
		resumeWakeFor([]byte(ev)).closeTurn() // a nil wake: no recorded resume path
	}
	if len(*jobs) != 0 {
		t.Fatalf("armed a resume for a harness with none: %+v", *jobs)
	}
}

// An open turn that is under budget is never resumed because it went quiet: the
// Stop hook is still to be consulted, so a gate that finishes is delivered there
// and the watcher finds nothing left to resume.
func TestClaudeUnderBudgetTurnIsNeverRaced(t *testing.T) {
	store := claudeWakeRepo(t, "50ms", 8)
	resumeSeams(t)
	argvs := resumeCapture(t)
	m := claudeGate(t, store, "sty_live")
	armResumeForPending(store, m) // hand-off arms the watcher beside the live turn
	store.OpenTurn(claudeSession)
	store.AddStopCount(claudeSession)
	finishGate(t, store, m, "accepted live")

	r, _ := agentcli.StopResumeFor(agentcli.HarnessClaude)
	if store.TurnIdle(claudeSession, r.Cap, 0) {
		t.Fatal("an open under-budget turn was treated as idle")
	}
	// The Stop hook of that turn delivers the verdict in-turn...
	if blk, ok := stopOnce(t, claudeEvent(t, "Stop", true)); !ok || !strings.Contains(blk.Reason, "accepted live") {
		t.Fatalf("the Stop did not deliver in-turn: %+v ok=%v", blk, ok)
	}
	// ...so the watcher, once it looks, has nothing to claim and starts no resume.
	if err := runGateResume(store, resumeJob{Handle: m.ID, Harness: agentcli.HarnessClaude, Session: claudeSession, Owner: claudeSession}); err != nil {
		t.Fatal(err)
	}
	if len(*argvs) != 0 {
		t.Fatalf("a verdict shown in-turn was resumed too: %q", *argvs)
	}
}

// AC4: a resume that cannot start gives its claim back, and the verdict is then
// deliverable by another route.
func TestClaudeResumeStartFailureReleasesTheClaim(t *testing.T) {
	store := claudeWakeRepo(t, "50ms", 8)
	m := claudeGate(t, store, "sty_nostart")
	finishGate(t, store, m, "accepted")
	old := resumeCommand
	resumeCommand = func(string, []string) *exec.Cmd { return exec.Command(filepath.Join(t.TempDir(), "no-such-claude")) }
	t.Cleanup(func() { resumeCommand = old })
	err := runGateResume(store, resumeJob{Handle: m.ID, Harness: agentcli.HarnessClaude, Session: claudeSession, Owner: claudeSession})
	if err == nil || store.Delivered(m.ID) {
		t.Fatalf("err=%v delivered=%v: a resume that did not start must release", err, store.Delivered(m.ID))
	}
	if blk, ok := stopOnce(t, claudeEvent(t, "Stop", false)); !ok || !strings.Contains(blk.Reason, "accepted") {
		t.Fatalf("the released verdict was not deliverable by the Stop hook: %+v ok=%v", blk, ok)
	}
}

// AC4: whichever routes race for a handle, exactly one shows it.
func TestClaudeHandleIsDeliveredOnceAcrossRoutes(t *testing.T) {
	store := claudeWakeRepo(t, "50ms", 8)
	argvs := resumeCapture(t)
	m := claudeGate(t, store, "sty_race")
	finishGate(t, store, m, "accepted race")

	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if store.Claim(m.ID) {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("%d claimants won one handle", won)
	}
	// The other routes then find it delivered.
	if _, ok := stopOnce(t, claudeEvent(t, "Stop", false)); ok {
		t.Error("a Stop delivered an already-claimed verdict")
	}
	if err := runGateResume(store, resumeJob{Handle: m.ID, Harness: agentcli.HarnessClaude, Session: claudeSession, Owner: claudeSession}); err != nil {
		t.Fatal(err)
	}
	if len(*argvs) != 0 {
		t.Errorf("an already-claimed verdict was resumed: %q", *argvs)
	}
}

// The scaffold wires the two turn-end events for claude only, and a heal adds
// them to a file written before they existed.
func TestClaudeScaffoldWiresTurnEndEvents(t *testing.T) {
	repo := t.TempDir()
	var doc struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(buildClaudeHookSettings(repo), &doc); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"StopFailure", "SessionEnd"} {
		if !strings.Contains(string(doc.Hooks[ev]), "satelle hook turnend --harness claude") {
			t.Errorf("claude scaffold lacks %s: %s", ev, doc.Hooks[ev])
		}
	}
	doc.Hooks = nil
	if err := json.Unmarshal(buildGrokHookSettings(repo), &doc); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"StopFailure", "SessionEnd"} {
		if _, ok := doc.Hooks[ev]; ok {
			t.Errorf("grok scaffold gained %s, an event it does not fire", ev)
		}
	}

	// Heal a pre-existing file that has every older event but neither new one.
	path := filepath.Join(repo, "settings.json")
	old := buildClaudeHookSettings(repo)
	var m map[string]any
	_ = json.Unmarshal(old, &m)
	hooks := m["hooks"].(map[string]any)
	delete(hooks, "StopFailure")
	delete(hooks, "SessionEnd")
	b, _ := json.Marshal(m)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if missing := incompleteHookEvents(path, "claude"); len(missing) != 2 {
		t.Fatalf("missing = %v, want the two turn-end events", missing)
	}
	added, err := ensureReinforcementHooks(path, "claude", repo)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(added, ",") != "SessionEnd,StopFailure" {
		t.Errorf("heal added %v", added)
	}
	if missing := incompleteHookEvents(path, "claude"); len(missing) != 0 {
		t.Errorf("still incomplete after the heal: %v", missing)
	}
}
