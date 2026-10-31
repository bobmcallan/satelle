package ledger

import (
	"encoding/json"
	"testing"
	"time"
)

// transitionRow builds one transition-kind row for story at at.
func transitionRow(t *testing.T, id, story, kind string, at time.Time, payload any) Entry {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return Entry{ID: id, StoryID: story, Kind: kind, Payload: b, CreatedAt: at}
}

// stubHost pins this host's name and which pids are alive for one test.
func stubHost(t *testing.T, host string, alive func(int) bool) {
	t.Helper()
	origHost, origAlive := Hostname, PidAlive
	t.Cleanup(func() { Hostname, PidAlive = origHost, origAlive })
	Hostname = func() string { return host }
	PidAlive = alive
}

// A transition is in flight from its transition_started row until a
// transition_ended row names it, its process on this host dies, or it ages past
// InFlightTTL (sty_9c782f0e).
func TestInFlightOpenEndedDeadPidAndAged(t *testing.T) {
	const live, dead = 101, 202
	stubHost(t, "here", func(pid int) bool { return pid == live })
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	start := func(id string, at time.Time, pid int, host string) Entry {
		return transitionRow(t, id, "sty_a", KindTransitionStarted, at,
			TransitionStartedPayload{From: "plan", To: "in_progress", Pid: pid, Host: host})
	}
	end := func(id, startID string, at time.Time) Entry {
		return transitionRow(t, id, "sty_a", KindTransitionEnded, at, TransitionEndedPayload{Start: startID, Outcome: "accepted"})
	}
	for _, tc := range []struct {
		name string
		rows []Entry
		want bool
	}{
		{"open, process alive", []Entry{start("s1", now.Add(-time.Minute), live, "here")}, true},
		{"ended", []Entry{start("s1", now.Add(-time.Minute), live, "here"), end("e1", "s1", now)}, false},
		{"process dead on this host", []Entry{start("s1", now.Add(-time.Minute), dead, "here")}, false},
		{"another host's process is not checked", []Entry{start("s1", now.Add(-time.Minute), dead, "elsewhere")}, true},
		{"aged past the TTL", []Entry{start("s1", now.Add(-InFlightTTL-time.Second), live, "here")}, false},
		{"an end for another start leaves this one open", []Entry{start("s1", now.Add(-time.Minute), live, "here"), end("e1", "s0", now)}, true},
		{"no start", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, ok := InFlight(tc.rows, now)
			if ok != tc.want {
				t.Fatalf("InFlight = %v (%+v), want %v", ok, tr, tc.want)
			}
			if ok && (tr.ID != "s1" || tr.To != "in_progress") {
				t.Errorf("open transition = %+v, want s1 → in_progress", tr)
			}
		})
	}
}

// OpenTransitions keeps the newest open start per story and carries its latest
// phase and dispatch; LatestUnended names a dead start a recovery report cites.
func TestOpenTransitionsPerStoryWithActivity(t *testing.T) {
	stubHost(t, "here", func(int) bool { return true })
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	rows := []Entry{
		transitionRow(t, "a1", "sty_a", KindTransitionStarted, now.Add(-3*time.Minute), TransitionStartedPayload{To: "plan", Pid: 1, Host: "here"}),
		transitionRow(t, "a2", "sty_a", KindTransitionStarted, now.Add(-2*time.Minute), TransitionStartedPayload{To: "in_progress", Pid: 1, Host: "here"}),
		transitionRow(t, "x1", "sty_a", KindTransitionActivity, now.Add(-time.Minute), TransitionActivityPayload{Start: "a2", Label: "gate", Index: 1, Total: 2}),
		transitionRow(t, "x2", "sty_a", KindTransitionActivity, now.Add(-30*time.Second), TransitionActivityPayload{Start: "a2", Agent: "coder", Model: "m", Pid: 9}),
		transitionRow(t, "b1", "sty_b", KindTransitionStarted, now.Add(-time.Minute), TransitionStartedPayload{To: "integration"}),
		transitionRow(t, "b2", "sty_b", KindTransitionEnded, now, TransitionEndedPayload{Start: "b1"}),
	}
	open := OpenTransitions(rows, now)
	if len(open) != 1 {
		t.Fatalf("open = %+v, want only sty_a", open)
	}
	a := open["sty_a"]
	if a.ID != "a2" || a.Label != "gate" || a.Index != 1 || a.Total != 2 || a.Agent != "coder" || a.AgentPid != 9 {
		t.Errorf("sty_a open transition = %+v, want a2 with its phase and dispatch", a)
	}
	if u, ok := LatestUnended(rows[4:]); ok {
		t.Errorf("an ended start is not unended: %+v", u)
	}
	stubHost(t, "here", func(int) bool { return false })
	if u, ok := LatestUnended(rows[:2]); !ok || u.ID != "a2" {
		t.Errorf("LatestUnended = %+v %v, want a2 though its process is dead", u, ok)
	}
}
