package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/gatehandle"
)

// sty_2439f4fd: a gate started from a cursor-agent Shell call has its verdict
// delivered to that conversation by cursor's stop hook, once, within the turn's
// continuation budget.

const cursorConversation = "17e98b90-f524-4ba9-a53e-eddfcff87713" // testdata/cursor/21-*

// cursorStopPayload is the captured probe-21 stop payload (21-stop.log) with the
// fields a real cursor stop carries beside it (cursor_version, status), for the
// conversation conv at loop_count loop. An empty conv keeps the captured id.
func cursorStopPayload(t *testing.T, conv string, loop int) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cursorFixtureDir, "21-stop.log"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["conversation_id"] != cursorConversation || m["session_id"] != cursorConversation {
		t.Fatalf("the capture no longer names %s: %v", cursorConversation, m)
	}
	if conv != "" {
		m["conversation_id"], m["session_id"] = conv, conv
	}
	m["loop_count"] = loop
	m["cursor_version"] = "2026.10.01-e373342"
	m["status"] = "completed"
	b, _ := json.Marshal(m)
	return b
}

// cursorStopRepo is a governed temp repo, no harness env, and the delivery rows
// the hook records collected.
func cursorStopRepo(t *testing.T) (*gatehandle.Store, *[]gatehandle.Meta) {
	t.Helper()
	store := stopWakeRepo(t, "200ms")
	prev := hookHarnessFlag
	hookHarnessFlag = ""
	t.Cleanup(func() { hookHarnessFlag = prev })
	var rows []gatehandle.Meta
	old := recordGateDelivered
	recordGateDelivered = func(d []gatehandle.Meta) { rows = append(rows, d...) }
	t.Cleanup(func() { recordGateDelivered = old })
	return store, &rows
}

// cursorStop runs cursor's stop hook the way the installed hook does: the session
// is bound from the payload first, then the stop is answered. It returns the
// followup_message, "" when the hook said nothing.
func cursorStop(t *testing.T, payload []byte) string {
	t.Helper()
	bindSessionID(payload)
	var out bytes.Buffer
	if err := runHookStopcheck(payload, &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) == "" {
		return ""
	}
	var doc map[string]string
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || len(doc) != 1 || doc["followup_message"] == "" {
		t.Fatalf("a cursor stop answers with exactly one followup_message, got %q (%v)", out.String(), err)
	}
	return doc["followup_message"]
}

// finishedGateFor is a gate run stamped with session that has already finished.
func finishedGateFor(t *testing.T, store *gatehandle.Store, story, session, verdict string) gatehandle.Meta {
	t.Helper()
	m := runningGateForSession(t, store, story, session)
	if err := os.WriteFile(store.VerdictPath(m.ID), []byte(verdict+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(m.ID, gatehandle.Result{}); err != nil {
		t.Fatal(err)
	}
	return m
}

// AC1 start side: a gate started by a Shell call whose environment carries
// CURSOR_CONVERSATION_ID is stamped with it; without it, nothing is.
func TestCursorShellGateIsStampedWithTheConversation(t *testing.T) {
	for name, tc := range map[string]struct{ env, want string }{
		"cursor Shell call":         {cursorConversation, cursorConversation},
		"no cursor env (unchanged)": {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			_ = tempRepo(t)
			args := []string{"gatetest", "--say", "stamped"}
			useGateHandOff(t, "20s", args)
			t.Setenv("CURSOR_CONVERSATION_ID", tc.env)

			out, err := runRoot(t, args...)
			if err != nil && !errors.Is(err, errGateHandedOff) {
				t.Fatalf("gate: %v\n%s", err, out)
			}
			store := gateStoreForTest(t)
			entries, _ := os.ReadDir(store.Dir())
			if len(entries) != 1 {
				t.Fatalf("want one handle, got %v", entries)
			}
			meta, merr := store.Meta(entries[0].Name())
			if merr != nil {
				t.Fatalf("no meta for %s: %v", entries[0].Name(), merr)
			}
			if meta.Session != tc.want {
				t.Errorf("handle session = %q, want %q", meta.Session, tc.want)
			}
		})
	}
}

// AC1 hook side + AC2: the stop of the conversation that started a gate delivers
// its verdict as one followup_message with one delivery row, and a later stop
// delivers nothing for it; a gate of another conversation is neither delivered
// nor claimed.
func TestCursorStopDeliversOwnGateOnce(t *testing.T) {
	store, rows := cursorStopRepo(t)
	own := finishedGateFor(t, store, "sty_own", cursorConversation, "accepted plan→in_progress by satelle-story-intent-review")
	other := finishedGateFor(t, store, "sty_other", "some-other-conversation", "accepted ready→plan")

	got := cursorStop(t, cursorStopPayload(t, "", 0))
	for _, want := range []string{own.ID, "sty_own", "accepted plan→in_progress"} {
		if !strings.Contains(got, want) {
			t.Errorf("the followup lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, other.ID) || strings.Contains(got, "accepted ready→plan") {
		t.Errorf("another conversation's verdict was delivered:\n%s", got)
	}
	if !store.Delivered(own.ID) || len(*rows) != 1 || (*rows)[0].ID != own.ID {
		t.Errorf("delivery rows = %+v, want exactly one for %s", *rows, own.ID)
	}
	if store.Delivered(other.ID) {
		t.Error("another conversation's gate was claimed")
	}

	if again := cursorStop(t, cursorStopPayload(t, "", 1)); again != "" {
		t.Errorf("a later stop delivered something for a delivered gate:\n%s", again)
	}
	if len(*rows) != 1 {
		t.Errorf("a later stop wrote another delivery row: %+v", *rows)
	}

	// The same payload shape from a different conversation does not take the gate
	// the first one left, and the owner still can.
	if got := cursorStop(t, cursorStopPayload(t, "a-third-conversation", 0)); got != "" {
		t.Errorf("a stranger's stop was handed a verdict:\n%s", got)
	}
	if store.Delivered(other.ID) {
		t.Error("a stranger's stop claimed a verdict that was not its own")
	}
	if got := cursorStop(t, cursorStopPayload(t, "some-other-conversation", 0)); !strings.Contains(got, other.ID) {
		t.Errorf("the owner of the other gate was not handed it:\n%s", got)
	}
}

// AC3: at or past the cursor cap the hook says nothing — a finished verdict is
// neither claimed nor recorded, a running gate gets no still-running re-prompt —
// and the verdict is delivered at the stop of the next turn, where the count
// restarts.
func TestCursorStopPastTheCapClaimsNothing(t *testing.T) {
	store, rows := cursorStopRepo(t)

	t.Run("finished verdict", func(t *testing.T) {
		m := finishedGateFor(t, store, "sty_cap", cursorConversation, "accepted in_progress→ready")
		for _, loop := range []int{4, 5} {
			if got := cursorStop(t, cursorStopPayload(t, "", loop)); got != "" {
				t.Errorf("loop_count %d: a followup past the cap would be lost, got:\n%s", loop, got)
			}
			if store.Delivered(m.ID) || len(*rows) != 0 {
				t.Fatalf("loop_count %d: the verdict was claimed (delivered=%v rows=%+v)", loop, store.Delivered(m.ID), *rows)
			}
		}
		if got := cursorStop(t, cursorStopPayload(t, "", 3)); !strings.Contains(got, m.ID) {
			t.Fatalf("loop_count 3 is within the cap and must deliver:\n%s", got)
		}
		if len(*rows) != 1 {
			t.Errorf("rows = %+v, want one", *rows)
		}
	})

	t.Run("running gate", func(t *testing.T) {
		*rows = nil
		m := runningGateForSession(t, store, "sty_run", cursorConversation)
		start := time.Now()
		if got := cursorStop(t, cursorStopPayload(t, "", 4)); got != "" {
			t.Errorf("a still-running re-prompt at the cap would be lost, got:\n%s", got)
		}
		if time.Since(start) > 150*time.Millisecond {
			t.Errorf("the hook waited %s on a gate at the cap", time.Since(start))
		}
		if got := cursorStop(t, cursorStopPayload(t, "", 3)); !strings.Contains(got, "still running") || !strings.Contains(got, m.ID) {
			t.Errorf("loop_count 3 must re-prompt while the gate runs, got:\n%s", got)
		}

		// The gate ends while the turn is out of continuations; the next turn gets it.
		_ = os.WriteFile(store.VerdictPath(m.ID), []byte("accepted ready→done\n"), 0o644)
		_ = store.Finish(m.ID, gatehandle.Result{})
		if got := cursorStop(t, cursorStopPayload(t, "", 4)); got != "" || store.Delivered(m.ID) {
			t.Errorf("a verdict that finished at the cap was claimed there: %q", got)
		}
		if got := cursorStop(t, cursorStopPayload(t, "", 0)); !strings.Contains(got, "accepted ready→done") || len(*rows) != 1 {
			t.Errorf("the next turn's stop did not deliver the left-pending verdict: %q rows=%+v", got, *rows)
		}
	})
}

// AC4: the stop entry the scaffold installs carries stopHookTimeoutSec, longer
// than the hook's own wait; init heals an entry with none or a shorter one, and
// leaves an operator's longer one.
func TestCursorStopEntryCarriesTheStopTimeout(t *testing.T) {
	stopTimeout := func(t *testing.T, repo string) (float64, bool) {
		t.Helper()
		f, _ := readCursorHooks(t, repo)
		if len(f.Hooks["stop"]) != 1 {
			t.Fatalf("stop entries = %v", f.Hooks["stop"])
		}
		v, ok := f.Hooks["stop"][0]["timeout"].(float64)
		return v, ok
	}
	setStopTimeout := func(t *testing.T, repo string, v any) {
		t.Helper()
		f, _ := readCursorHooks(t, repo)
		if v == nil {
			delete(f.Hooks["stop"][0], "timeout")
		} else {
			f.Hooks["stop"][0]["timeout"] = v
		}
		b, _ := json.MarshalIndent(f, "", "  ")
		if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(cursorHooksRel)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	timeoutFindings := func(repo string) []ScaffoldFinding {
		var out []ScaffoldFinding
		for _, f := range driftCursorHooks(repo) {
			if f.Kind == "timeout" {
				out = append(out, f)
			}
		}
		return out
	}

	repo := tempRepo(t)
	t.Chdir(repo)
	if out, err := runRootIn(t, "", "agents", "install", "cursor"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	got, ok := stopTimeout(t, repo)
	if !ok || got != stopHookTimeoutSec {
		t.Fatalf("installed stop timeout = %v (set %v), want %d", got, ok, stopHookTimeoutSec)
	}
	if time.Duration(got)*time.Second <= stopGateWait() {
		t.Errorf("the stop timeout %vs does not exceed the hook's own wait %s", got, stopGateWait())
	}
	if drift := timeoutFindings(repo); len(drift) != 0 {
		t.Errorf("a fresh install reports timeout drift: %+v", drift)
	}
	for _, event := range []string{"preToolUse", "sessionStart"} {
		f, _ := readCursorHooks(t, repo)
		for _, e := range f.Hooks[event] {
			if _, has := e["timeout"]; has {
				t.Errorf("%s entry carries a timeout it never had: %v", event, e)
			}
		}
	}

	for name, legacy := range map[string]any{"none": nil, "shorter": float64(60)} {
		t.Run("heal "+name, func(t *testing.T) {
			setStopTimeout(t, repo, legacy)
			if drift := timeoutFindings(repo); len(drift) != 1 || !strings.Contains(drift[0].Detail, "stop") {
				t.Fatalf("drift = %+v, want the stop timeout reported", drift)
			}
			var out bytes.Buffer
			if err := healCursorHooks(&out, repo); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "timeout raised") {
				t.Errorf("heal does not say what it changed: %q", out.String())
			}
			if got, ok := stopTimeout(t, repo); !ok || got != stopHookTimeoutSec {
				t.Errorf("healed stop timeout = %v (set %v), want %d", got, ok, stopHookTimeoutSec)
			}
			if drift := timeoutFindings(repo); len(drift) != 0 {
				t.Errorf("drift after heal: %+v", drift)
			}
		})
	}

	setStopTimeout(t, repo, float64(stopHookTimeoutSec*2))
	if err := healCursorHooks(&bytes.Buffer{}, repo); err != nil {
		t.Fatal(err)
	}
	if got, _ := stopTimeout(t, repo); got != stopHookTimeoutSec*2 {
		t.Errorf("an operator's longer timeout was lowered to %v", got)
	}
}
