package agentstep

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// progressingRunner emits a real event every interval for duration, then
// returns out — a fake agent that is slow but genuinely working (sty_752c4ef2
// AC1).
type progressingRunner struct {
	interval, duration time.Duration
	out                string
	events             int
}

func (r *progressingRunner) Name() string    { return "progressing" }
func (r *progressingRunner) Command() string { return "progressing" }
func (r *progressingRunner) Run(ctx context.Context, req agentcli.Request) ([]byte, error) {
	deadline := time.Now().Add(r.duration)
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.C:
			r.events++
			if req.OnEvent != nil {
				req.OnEvent(agentcli.Event{Kind: agentcli.EventToolStart, Tool: "Bash"})
				req.OnEvent(agentcli.Event{Kind: agentcli.EventToolEnd, Tool: "Bash"})
			}
		}
	}
	return []byte(r.out), nil
}

// heartbeatOnlyRunner emits ONE real event (a tool start) then goes silent
// except for heartbeats until ctx is cancelled — a fake agent that is stuck,
// not merely slow (sty_752c4ef2 AC2 evidence #2: a heartbeat proves the
// process exists, not that it is progressing).
type heartbeatOnlyRunner struct{}

func (r *heartbeatOnlyRunner) Name() string    { return "heartbeat-only" }
func (r *heartbeatOnlyRunner) Command() string { return "heartbeat-only" }
func (r *heartbeatOnlyRunner) Run(ctx context.Context, req agentcli.Request) ([]byte, error) {
	if req.OnEvent != nil {
		req.OnEvent(agentcli.Event{Kind: agentcli.EventToolStart, Tool: "Bash"})
	}
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.C:
			if req.OnEvent != nil {
				req.OnEvent(agentcli.Event{Kind: agentcli.EventHeartbeat})
			}
		}
	}
}

// TestProgressingAgentNotKilled (AC1): a fake runner keeps emitting real
// events past the old 20m/10m default, with idle_timeout set small and no
// hard timeout configured — the dispatch must complete, never be cut off by
// elapsed time alone.
func TestProgressingAgentNotKilled(t *testing.T) {
	r := &progressingRunner{interval: 20 * time.Millisecond, duration: 300 * time.Millisecond, out: "DONE"}
	g := New(r, fakeDocs{}, "/repo", "")
	g.idleTimeout = 100 * time.Millisecond // << the old wall-clock defaults; proves elapsed time alone never kills it
	g.agentTimeout = 0                     // no hard ceiling

	start := time.Now()
	res := g.Invoke(context.Background(), InvokeRequest{
		Binding: config.AgentBinding{Command: "claude", Principles: config.PrinciplesNone},
		Section: "coder",
		Rubric:  "do it",
		Payload: map[string]string{},
		Expect:  ExpectPerform,
		Runner:  r,
	})
	if res.Err != nil {
		t.Fatalf("a progressing agent must not be killed: %v", res.Err)
	}
	if string(res.Stdout) != "DONE" {
		t.Errorf("res.Stdout = %q, want %q", res.Stdout, "DONE")
	}
	if elapsed := time.Since(start); elapsed < r.duration {
		t.Errorf("returned before the fake agent finished: elapsed %v, want >= %v", elapsed, r.duration)
	}
	if r.events == 0 {
		t.Fatal("fake agent never got to emit an event — test is not exercising the watchdog")
	}
}

// TestRetrospectHonorsNoImplicitCheckTimeoutCap (AC1, integration round-2 fix):
// Retrospect used to pass g.checkTimeout (defaultCheckTimeout, a
// functional-check bound out of this story's scope) as its hard ceiling, so a
// progressing retrospective could be cut off at 20m even with no operator
// timeout set. It must resolve its own bound the same way DispatchExecutor
// does — binding.TimeoutDuration(g.agentTimeout) / g.idleTimeoutFor — so a
// small g.checkTimeout never reaches it.
func TestRetrospectHonorsNoImplicitCheckTimeoutCap(t *testing.T) {
	r := &progressingRunner{interval: 20 * time.Millisecond, duration: 200 * time.Millisecond, out: "DONE"}
	g := New(r, fakeDocs{}, "/repo", "")
	g.newRunner = func(string, string) (agentcli.Runner, error) { return r, nil }
	g.checkTimeout = 50 * time.Millisecond // functional-check bound — must NOT reach the retrospective
	g.agentTimeout = 0                     // no hard ceiling configured
	g.idleTimeout = 500 * time.Millisecond // wide enough that the progressing runner never idles out
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name != retrospectAgent {
			return config.AgentBinding{}, false
		}
		return config.AgentBinding{Command: "claude", Tools: "Bash(satelle:*)", Principles: config.PrinciplesNone}, true
	})

	start := time.Now()
	res, err := g.Retrospect(context.Background(), workitem.Item{ID: "sty_1", Status: "done"}, "")
	if err != nil {
		t.Fatalf("Retrospect must not be cut by g.checkTimeout: %v", err)
	}
	if elapsed := time.Since(start); elapsed < r.duration {
		t.Errorf("Retrospect returned before the fake agent finished: elapsed %v, want >= %v", elapsed, r.duration)
	}
	if !res.Dispatched {
		t.Fatal("res.Dispatched = false, want true")
	}
}

// TestHeartbeatOnlyAgentStalls_ExpectPerform (AC2): a dispatch that emits no
// real event for idle_timeout is stopped even though heartbeats keep
// arriving, on the perform path (a named dispatch, e.g. the coder).
func TestHeartbeatOnlyAgentStalls_ExpectPerform(t *testing.T) {
	r := &heartbeatOnlyRunner{}
	g := New(r, fakeDocs{}, "/repo", "")
	var stalled []map[string]any
	g.SetTelemetry(func(_ context.Context, storyID, actor, kind string, data map[string]any) {
		if kind == "agent-stalled" {
			stalled = append(stalled, data)
		}
	})
	g.idleTimeout = 60 * time.Millisecond
	g.agentTimeout = 0

	res := g.Invoke(context.Background(), InvokeRequest{
		Binding: config.AgentBinding{Command: "claude", Principles: config.PrinciplesNone},
		Section: "coder",
		Rubric:  "do it",
		Payload: map[string]string{},
		Expect:  ExpectPerform,
		Runner:  r,
		StoryID: "sty_1",
	})
	if res.Err == nil {
		t.Fatal("a heartbeat-only agent must be judged stalled, not left to run forever")
	}
	var se *StallError
	if !errors.As(res.Err, &se) {
		t.Fatalf("res.Err = %v, want a *StallError", res.Err)
	}
	if se.Idle < g.idleTimeout {
		t.Errorf("StallError.Idle = %v, want >= %v", se.Idle, g.idleTimeout)
	}
	if se.LastEvent != "tool: Bash" {
		t.Errorf("StallError.LastEvent = %q, want %q", se.LastEvent, "tool: Bash")
	}
	if classifyOutcome(res.Err) != "stalled" {
		t.Errorf("classifyOutcome = %q, want %q", classifyOutcome(res.Err), "stalled")
	}
	if len(stalled) != 1 {
		t.Fatalf("expected exactly one agent-stalled telemetry event, got %d: %v", len(stalled), stalled)
	}
	if res.Err.Error() == "" || !containsStalled(res.Err.Error()) {
		t.Errorf("refusal text must name the stall, got %q", res.Err.Error())
	}
	if containsDeadlineExceeded(res.Err.Error()) {
		t.Errorf("refusal text must not read like a hard-timeout deadline: %q", res.Err.Error())
	}
}

// TestHeartbeatOnlyAgentStalls_ExpectVerdict (AC2): same stall detection on
// the verdict path (a gate reviewer).
func TestHeartbeatOnlyAgentStalls_ExpectVerdict(t *testing.T) {
	r := &heartbeatOnlyRunner{}
	g := New(r, fakeDocs{}, "/repo", "")
	var stalled []map[string]any
	g.SetTelemetry(func(_ context.Context, storyID, actor, kind string, data map[string]any) {
		if kind == "agent-stalled" {
			stalled = append(stalled, data)
		}
	})
	g.idleTimeout = 60 * time.Millisecond
	g.agentTimeout = 0
	g.backoff = func(int) time.Duration { return 0 }

	res := g.Invoke(context.Background(), InvokeRequest{
		Binding: config.AgentBinding{Command: "claude", Principles: config.PrinciplesNone},
		Section: "reviewer",
		Rubric:  "judge",
		Payload: map[string]string{},
		Expect:  ExpectVerdict,
		Runner:  r,
		Skill:   "test-skill",
		StoryID: "sty_1",
	})
	if res.Err == nil {
		t.Fatal("a heartbeat-only reviewer must be judged stalled, not retried forever")
	}
	var se *StallError
	if !errors.As(res.Err, &se) {
		t.Fatalf("res.Err = %v, want a *StallError", res.Err)
	}
	if classifyOutcome(res.Err) != "stalled" {
		t.Errorf("classifyOutcome = %q, want %q", classifyOutcome(res.Err), "stalled")
	}
	if len(stalled) != 1 {
		t.Fatalf("expected exactly one agent-stalled telemetry event (no blind retry of a stuck reviewer), got %d", len(stalled))
	}
	if !containsStalled(res.Err.Error()) {
		t.Errorf("refusal text must name the stall, got %q", res.Err.Error())
	}
}

// TestIdleTimeoutConfigMovesStallPoint (AC3): idle_timeout is read from
// configuration; changing it moves the stall point with no code change.
func TestIdleTimeoutConfigMovesStallPoint(t *testing.T) {
	for _, idle := range []time.Duration{50 * time.Millisecond, 200 * time.Millisecond} {
		t.Run(idle.String(), func(t *testing.T) {
			r := &heartbeatOnlyRunner{}
			g := New(r, fakeDocs{}, "/repo", "")
			g.idleTimeout = idle
			g.agentTimeout = 0

			start := time.Now()
			res := g.Invoke(context.Background(), InvokeRequest{
				Binding: config.AgentBinding{Command: "claude", Principles: config.PrinciplesNone},
				Section: "coder",
				Rubric:  "do it",
				Payload: map[string]string{},
				Expect:  ExpectPerform,
				Runner:  r,
			})
			elapsed := time.Since(start)
			var se *StallError
			if !errors.As(res.Err, &se) {
				t.Fatalf("res.Err = %v, want a *StallError", res.Err)
			}
			// Generous slack: the point under test is that the stall point MOVES
			// with configuration, not exact timing (sty_752c4ef2 integration-
			// coverage-review note on AC3).
			if elapsed < idle {
				t.Errorf("stalled after %v, want >= configured idle_timeout %v", elapsed, idle)
			}
			if elapsed > idle+idle {
				t.Errorf("stalled after %v, want close to configured idle_timeout %v (within slack)", elapsed, idle)
			}
		})
	}
}

// TestDispatchExecutorHonorsDefaultsIdleTimeout (AC3): the resolution ladder
// production wiring uses — config.AgentsConfig.ResolveIdleTimeout, wired
// through SetIdleTimeoutResolver exactly as internal/cli/app.go's
// PersistentPreRunE does — moves the stall point when ONLY [defaults]
// idle_timeout changes in real agents.toml text, with no per-binding
// idle_timeout override and no code change. Unlike
// TestIdleTimeoutConfigMovesStallPoint (which pokes g.idleTimeout directly),
// this drives the real DispatchExecutor path a one-shot dispatch takes, so it
// exercises the [defaults] ladder a raw g.idleTimeout poke never reaches. A
// SetIdleTimeoutResolver left unwired (the app.go regression this story's
// AC3 review round found) would fall back to the binding alone against the
// engine-wide default, so both configured values below would stall at the
// same point and this test would fail.
func TestDispatchExecutorHonorsDefaultsIdleTimeout(t *testing.T) {
	for _, want := range []time.Duration{50 * time.Millisecond, 200 * time.Millisecond} {
		t.Run(want.String(), func(t *testing.T) {
			dir := t.TempDir()
			body := fmt.Sprintf("[defaults]\nidle_timeout = %q\n\n[architect]\ncommand = \"fake -p {system}\"\ntools = \"read_file\"\n", want.String())
			if err := os.WriteFile(filepath.Join(dir, config.AgentsConfigName), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			ac, err := config.LoadAgents(dir)
			if err != nil {
				t.Fatal(err)
			}

			docs := fakeDocs{workflow: dispatchWF, skillBody: "alignment rubric", skillFound: true}
			g, _ := newEngine(t, "", docs)
			r := &heartbeatOnlyRunner{}
			g.newRunner = func(string, string) (agentcli.Runner, error) { return r, nil }
			g.SetNamedAgents(ac.NamedBinding)
			g.SetIdleTimeoutResolver(func(section string, b config.AgentBinding) (time.Duration, error) {
				return ac.ResolveIdleTimeout(b, DefaultIdleTimeout)
			})

			start := time.Now()
			_, err = g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "plan")
			elapsed := time.Since(start)
			var se *StallError
			if !errors.As(err, &se) {
				t.Fatalf("err = %v, want a *StallError", err)
			}
			if elapsed < want || elapsed > want+want {
				t.Errorf("stalled after %v, want close to the configured [defaults] idle_timeout %v", elapsed, want)
			}
		})
	}
}

// TestHardTimeoutStillCapsProgressingAgent (AC3): a configured hard timeout
// still bounds total time even when the agent keeps progressing — the
// optional ceiling, when authored, wins over the stall detector.
func TestHardTimeoutStillCapsProgressingAgent(t *testing.T) {
	r := &progressingRunner{interval: 5 * time.Millisecond, duration: 2 * time.Second, out: "DONE"}
	g := New(r, fakeDocs{}, "/repo", "")
	g.idleTimeout = time.Minute // idle would never fire first

	start := time.Now()
	res := g.Invoke(context.Background(), InvokeRequest{
		Binding: config.AgentBinding{Command: "claude", Principles: config.PrinciplesNone},
		Section: "coder",
		Rubric:  "do it",
		Payload: map[string]string{},
		Expect:  ExpectPerform,
		// A named dispatch's hard ceiling is resolved by its CALLER
		// (DispatchExecutor: binding.TimeoutDuration(g.agentTimeout)) and
		// passed explicitly — ExpectPerform does not fall back to
		// g.agentTimeout on its own, only ExpectVerdict does. Set it here the
		// way a real caller would.
		Timeout: 50 * time.Millisecond,
		Runner:  r,
	})
	if res.Err == nil {
		t.Fatal("a configured hard timeout must still stop a progressing agent")
	}
	var se *StallError
	if errors.As(res.Err, &se) {
		t.Fatalf("hard-timeout stop must not be reported as a stall: %v", se)
	}
	if !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Errorf("res.Err = %v, want context.DeadlineExceeded", res.Err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("hard timeout did not bound the run: took %v", elapsed)
	}
}

// TestActivityDetailThrottledDuringDispatch (sty_752c4ef2 AC5): a fake mirror
// pusher (the SetActivityDetail sink) records at least one push before a long
// dispatch returns, and its EventAt/EventCount advance strictly between
// pushes — proving the throttle refreshes, rather than stamping once and going
// stale for the rest of a multi-minute dispatch.
func TestActivityDetailThrottledDuringDispatch(t *testing.T) {
	old := activityDetailThrottle
	activityDetailThrottle = 20 * time.Millisecond
	t.Cleanup(func() { activityDetailThrottle = old })

	r := &progressingRunner{interval: 5 * time.Millisecond, duration: 150 * time.Millisecond, out: "DONE"}
	g := New(r, fakeDocs{}, "/repo", "")
	g.idleTimeout = time.Minute

	var mu sync.Mutex
	var pushes []ActivityDetail
	g.SetActivityDetail(func(itemID string, d ActivityDetail) {
		mu.Lock()
		defer mu.Unlock()
		pushes = append(pushes, d)
	})

	res := g.Invoke(context.Background(), InvokeRequest{
		Binding: config.AgentBinding{Command: "claude", Model: "sonnet", Principles: config.PrinciplesNone},
		Section: "coder",
		Rubric:  "do it",
		Payload: map[string]string{},
		Expect:  ExpectPerform,
		Runner:  r,
		StoryID: "sty_1",
	})
	if res.Err != nil {
		t.Fatalf("dispatch failed: %v", res.Err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(pushes) < 2 {
		t.Fatalf("expected at least 2 throttled pushes over a %v dispatch with a %v throttle, got %d", r.duration, activityDetailThrottle, len(pushes))
	}
	for i, p := range pushes {
		if p.Agent != "coder" || p.Model != "sonnet" {
			t.Errorf("push[%d] = %+v, want agent=coder model=sonnet", i, p)
		}
		if i > 0 {
			if !p.EventAt.After(pushes[i-1].EventAt) {
				t.Errorf("push[%d].EventAt did not advance: prev=%v cur=%v", i, pushes[i-1].EventAt, p.EventAt)
			}
			if p.EventCount <= pushes[i-1].EventCount {
				t.Errorf("push[%d].EventCount did not advance: prev=%d cur=%d", i, pushes[i-1].EventCount, p.EventCount)
			}
		}
	}
}

// TestCommandTransportHonorsWatchdog (sty_752c4ef2 AC4): the watchdog needs no
// transport-specific code because it wraps the SAME ctx every runner spawns
// its child through (exec.CommandContext). This proves it against a REAL
// command-interface Runner and a real subprocess, not just a fake Runner: a
// shell script that prints nothing (so no real event ever fires past the
// transport's own EventStart) while alive must be killed once idle_timeout
// elapses, not run to completion.
func TestCommandTransportHonorsWatchdog(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "silent-agent.sh")
	// exec replaces the shell's own process image with sleep, so the watchdog's
	// kill (SIGKILL on this one PID) closes the stdout pipe immediately. A plain
	// "sleep 5 &" child would inherit the pipe fd and keep it open after the
	// shell dies, masking the kill behind pipe-EOF latency — a test-script
	// artifact, not something runProcess needs to handle specially.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner, err := agentcli.RunnerFromBinding(agentcli.InterfaceCommand, script+" --noop")
	if err != nil {
		t.Fatal(err)
	}
	g := New(runner, fakeDocs{}, "/repo", "")
	g.idleTimeout = 80 * time.Millisecond
	g.agentTimeout = 0

	start := time.Now()
	_, _, err = g.runOnce(context.Background(), runner, agentcli.Request{SystemPrompt: "x"}, 0, g.idleTimeout)
	if err == nil {
		t.Fatal("a silent subprocess must be judged stalled, not run to completion")
	}
	var se *StallError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *StallError", err)
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Errorf("subprocess was not killed by the watchdog: took %v (script sleeps 5s)", elapsed)
	}
}

// TestActivityDetailCarriesRealSubprocessPid (sty_752c4ef2 AC5): the
// ActivityDetail pushed to the SetActivityDetail sink carries the SPAWNED
// child's real OS pid, not zero — proven against a real command-interface
// subprocess (not a fake Runner), since the pid is not a value agentstep
// invents: agentcli stamps it on EventStart from cmd.Process.Pid.
func TestActivityDetailCarriesRealSubprocessPid(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "quick-agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner, err := agentcli.RunnerFromBinding(agentcli.InterfaceCommand, script+" --noop")
	if err != nil {
		t.Fatal(err)
	}
	g := New(runner, fakeDocs{}, dir, "")
	g.idleTimeout = time.Minute
	g.agentTimeout = 0

	var mu sync.Mutex
	var pushes []ActivityDetail
	g.SetActivityDetail(func(_ string, d ActivityDetail) {
		mu.Lock()
		defer mu.Unlock()
		pushes = append(pushes, d)
	})

	res := g.Invoke(context.Background(), InvokeRequest{
		Binding: config.AgentBinding{Command: script + " --noop", Principles: config.PrinciplesNone},
		Section: "coder",
		Rubric:  "do it",
		Payload: map[string]string{},
		Expect:  ExpectPerform,
		Runner:  runner,
		StoryID: "sty_1",
	})
	if res.Err != nil {
		t.Fatalf("dispatch failed: %v", res.Err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(pushes) == 0 {
		t.Fatal("expected at least one ActivityDetail push")
	}
	if pushes[0].Pid == 0 {
		t.Error("ActivityDetail.Pid must carry the spawned subprocess's real pid, got 0")
	}
	if pushes[0].Pid == os.Getpid() {
		t.Error("ActivityDetail.Pid must be the CHILD's pid, not the test process's own")
	}
}

// TestStreamTransportHonorsWatchdog (sty_752c4ef2 AC4): the same silent-
// subprocess proof as TestCommandTransportHonorsWatchdog, against a REAL
// interface=stream Runner. The watchdog needs no transport-specific code
// because every transport spawns its child through exec.CommandContext(ctx,
// ...) against the SAME context the watchdog wraps — this proves that holds
// for stream too, not just command.
func TestStreamTransportHonorsWatchdog(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "silent-stream-agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner, err := agentcli.RunnerFromBinding(agentcli.InterfaceStream, script+" --stream")
	if err != nil {
		t.Fatal(err)
	}
	g := New(runner, fakeDocs{}, dir, "")
	g.idleTimeout = 80 * time.Millisecond
	g.agentTimeout = 0

	start := time.Now()
	_, _, err = g.runOnce(context.Background(), runner, agentcli.Request{SystemPrompt: "x"}, 0, g.idleTimeout)
	if err == nil {
		t.Fatal("a silent stream subprocess must be judged stalled, not run to completion")
	}
	var se *StallError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *StallError", err)
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Errorf("stream subprocess was not killed by the watchdog: took %v (script sleeps 5s)", elapsed)
	}
}

// TestACPTransportHonorsWatchdog (sty_752c4ef2 AC4): same proof against a
// REAL interface=acp Runner.
func TestACPTransportHonorsWatchdog(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "silent-acp-agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner, err := agentcli.RunnerFromBinding(agentcli.InterfaceACP, script+" --acp")
	if err != nil {
		t.Fatal(err)
	}
	g := New(runner, fakeDocs{}, dir, "")
	g.idleTimeout = 80 * time.Millisecond
	g.agentTimeout = 0

	start := time.Now()
	_, _, err = g.runOnce(context.Background(), runner, agentcli.Request{SystemPrompt: "x"}, 0, g.idleTimeout)
	if err == nil {
		t.Fatal("a silent ACP subprocess must be judged stalled, not run to completion")
	}
	var se *StallError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *StallError", err)
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Errorf("ACP subprocess was not killed by the watchdog: took %v (script sleeps 5s)", elapsed)
	}
}

func containsStalled(s string) bool {
	return strings.Contains(strings.ToLower(s), "stall")
}

func containsDeadlineExceeded(s string) bool {
	return strings.Contains(strings.ToLower(s), "deadline exceeded")
}
