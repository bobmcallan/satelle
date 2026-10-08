package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/gatehandle"
)

// The pi extension's gate-verdict delivery (sty_7ebeda10), traced end to end:
// the RENDERED extension runs under node against a stub of pi's ExtensionAPI and
// calls the REAL hook handlers — this test binary, re-executed as `satelle` —
// over a real handle store. Nothing about the hook is faked, so what is asserted
// is the path a pi session takes: agent_settled -> `satelle hook stopcheck
// --harness pi` -> the handle's exclusive Claim -> pi.sendUserMessage.
//
// This is a stand-in for a live pi run, not one: the pi binary itself is not
// driven. The pi side is the stub in testdata/pi_driver.mjs.

// useRealSatelle governs the rig's repo and puts the real hook on the extension's
// PATH. It returns the handle store the hook resolves, so a test can create and
// finish gates behind it.
func (r *piRig) useRealSatelle(stopWait string) *gatehandle.Store {
	t := r.t
	clearHarnessEnv(t)
	home := t.TempDir()
	cfgPath := filepath.Join(r.repo, ".satelle", "satelle.toml")
	for p, body := range map[string]string{
		cfgPath: "[review]\ngate_create = false\n",
		filepath.Join(r.repo, ".satelle", "agents.toml"): "[executor]\nharness = \"in-loop\"\n",
	} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nSATELLE_CLI_REEXEC=1 exec '" + exe + "' \"$@\"\n"
	for _, p := range []string{filepath.Join(r.repo, ".satelle", "satelle"), filepath.Join(r.binDir, "satelle")} {
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{
		"SATELLE_HOME":            home,
		"SATELLE_CONFIG":          cfgPath,
		"SATELLE_SERVER_ENDPOINT": "none",
		stopGateWaitEnv:           stopWait,
	}
	for k, v := range env {
		r.env = append(r.env, k+"="+v)
		if k != stopGateWaitEnv {
			t.Setenv(k, v)
		}
	}
	return gateStoreForTest(t)
}

// finishGateOn finishes the gate once the signal file exists, never on a timer,
// so the finish is ordered behind whatever step creates it and not behind the
// machine's speed. If the signal never comes the gate stays running and the
// test fails on its own assertions.
func (r *piRig) finishGateOn(store *gatehandle.Store, id, signal, verdict string) {
	go func() {
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(signal); err == nil {
				_ = os.WriteFile(store.VerdictPath(id), []byte(verdict+"\n"), 0o644)
				_ = store.Finish(id, gatehandle.Result{})
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
}

func (r *piRig) trace(out piOut) {
	b, _ := json.MarshalIndent(map[string]any{"messages": out.Messages, "notices": out.Notices}, "", "  ")
	r.t.Logf("pi extension trace:\n%s", b)
}

func one(n int) *int { return &n }

// AC1+AC2: a gate still running when the agent settles does not re-prompt: the
// stop is allowed, and the verdict is sent as ONE user message when the handle
// finishes. A later settle does not repeat it, and the next prompt's
// additionalContext does not carry it again.
func TestPiTrace_RunningGateIsSentOnceWhenItFinishes(t *testing.T) {
	r := newPiRig(t)
	store := r.useRealSatelle("1s")
	g := runningGate(t, store, "sty_slow")
	signal := filepath.Join(t.TempDir(), "settled")
	r.finishGateOn(store, g.ID, signal, "accepted plan→in_progress")

	out := r.drive(true,
		// The settle waits its one bound (1s), finds the gate still going, allows
		// the stop and arms the waiter.
		piStep{Event: "agent_settled", Ctx: map[string]any{"idle": true, "hasUI": true}},
		// The gate finishes only now, after that settle has returned.
		piStep{Touch: signal},
		piStep{WaitMessages: one(1), TimeoutMS: 20000},
		piStep{Event: "agent_settled", Ctx: map[string]any{"idle": true, "hasUI": true}},
		piStep{Event: "before_agent_start", Arg: map[string]any{"prompt": "next", "systemPrompt": "SYS"}},
	)
	r.trace(out)

	if len(out.Messages) != 1 {
		t.Fatalf("the verdict must be sent exactly once, and nothing before it: %+v", out.Messages)
	}
	m := out.Messages[0]
	if !strings.Contains(m.Text, g.ID) || !strings.Contains(m.Text, "accepted plan→in_progress") || strings.Contains(m.Text, "still running") {
		t.Errorf("the one message is not the finished verdict: %q", m.Text)
	}
	if m.Opts != nil {
		t.Errorf("an idle session starts a turn with a plain user message: %+v", m.Opts)
	}
	var pending bool
	for _, n := range out.Notices {
		pending = pending || strings.Contains(n.Msg, "still running")
	}
	if !pending {
		t.Errorf("the pending gate was not noted to the operator: %+v", out.Notices)
	}
	prompt, _ := out.Results[4].Result["systemPrompt"].(string)
	if strings.Contains(prompt, g.ID) {
		t.Errorf("the prompt catch-up repeated a verdict already sent as a user message:\n%s", prompt)
	}
	if !store.Delivered(g.ID) {
		t.Error("the handle was never claimed")
	}
}

// AC1: a verdict that finishes between turns reaches the model as the next
// prompt's additionalContext, and the stop that follows does not send it again.
func TestPiTrace_FinishedBetweenTurnsRidesTheNextPrompt(t *testing.T) {
	r := newPiRig(t)
	store := r.useRealSatelle("1s")
	g := runningGate(t, store, "sty_between")
	finishGateID(t, store, g.ID, "accepted")

	out := r.drive(true,
		piStep{Event: "before_agent_start", Arg: map[string]any{"prompt": "next", "systemPrompt": "SYS"}},
		piStep{Event: "agent_settled", Ctx: map[string]any{"idle": true, "hasUI": true}},
	)
	r.trace(out)

	prompt, _ := out.Results[0].Result["systemPrompt"].(string)
	if !strings.Contains(prompt, g.ID) || !strings.Contains(prompt, "accepted") {
		t.Errorf("the verdict did not ride the prompt:\n%s", prompt)
	}
	if len(out.Messages) != 0 {
		t.Errorf("the stop re-sent a verdict the prompt delivered: %+v", out.Messages)
	}
}

// AC3: a non-interactive run that exits on settle sends nothing and waits for
// nothing; the undelivered gate gets an adapter-named limitation, is not marked
// delivered, and the next session still receives its verdict.
func TestPiTrace_NonInteractiveSettleRecordsALimitation(t *testing.T) {
	r := newPiRig(t)
	store := r.useRealSatelle("30s")
	g := runningGate(t, store, "sty_headless")

	start := time.Now()
	out := r.drive(true,
		piStep{Event: "agent_settled", Ctx: map[string]any{"idle": true, "hasUI": false}},
	)
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("a non-interactive settle waited for its gate (%s)", elapsed)
	}
	r.trace(out)

	if len(out.Messages) != 0 {
		t.Errorf("a non-interactive settle sent a message nothing can receive: %+v", out.Messages)
	}
	if !store.Limited(g.ID) || store.Delivered(g.ID) {
		t.Fatalf("limited=%v delivered=%v — the gate must carry a limitation and stay undelivered", store.Limited(g.ID), store.Delivered(g.ID))
	}
	b, _ := os.ReadFile(filepath.Join(store.Dir(), g.ID, "delivery-limited"))
	if !strings.HasPrefix(string(b), "pi: ") || !strings.Contains(string(b), "non-interactive") {
		t.Errorf("the limitation is not adapter-named: %q", b)
	}

	// The verdict was not lost: the next session's prompt delivers it.
	finishGateID(t, store, g.ID, "accepted")
	out = r.drive(true,
		piStep{Event: "before_agent_start", Arg: map[string]any{"prompt": "later", "systemPrompt": "SYS"}},
	)
	if prompt, _ := out.Results[0].Result["systemPrompt"].(string); !strings.Contains(prompt, g.ID) {
		t.Errorf("the next session did not receive the undelivered verdict:\n%s", prompt)
	}
	if !store.Delivered(g.ID) {
		t.Error("the catch-up did not claim the handle")
	}
}
