package agentinstall

import (
	"encoding/json"
	"strings"
	"testing"
)

// sty_2439f4fd AC4: a slot with a TimeoutS is written with it, a shorter or absent
// one is raised, a longer operator-set one is kept, and a stale command keeps the
// longer timeout too.

func wantWithStopTimeout(sec int) []CursorHook {
	w := wantSet()
	w[3].TimeoutS = sec
	return w
}

func stopEntry(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var f struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &f); err != nil || len(f.Hooks["stop"]) != 1 {
		t.Fatalf("hooks.json stop entries: %v\n%s", err, raw)
	}
	return f.Hooks["stop"][0]
}

func TestRenderCursorHooksStopTimeout(t *testing.T) {
	const want = 1800
	const stopCmd = `"command": "PATH=$HOME/.local/bin:$PATH satelle hook stopcheck --harness cursor"`

	fresh, err := RenderCursorHooks(nil, wantWithStopTimeout(want))
	if err != nil {
		t.Fatal(err)
	}
	if got := stopEntry(t, fresh)["timeout"]; got != float64(want) {
		t.Errorf("fresh stop timeout = %v, want %d", got, want)
	}
	if strings.Count(string(fresh), "timeout") != 1 {
		t.Errorf("only the stop entry carries a timeout:\n%s", fresh)
	}
	if short := CursorHooksShortTimeout(fresh, wantWithStopTimeout(want)); len(short) != 0 {
		t.Errorf("a fresh file has short timeouts: %v", short)
	}
	again, _ := RenderCursorHooks(fresh, wantWithStopTimeout(want))
	if string(again) != string(fresh) {
		t.Errorf("a second render changed the file:\n%s", again)
	}

	legacy, _ := RenderCursorHooks(nil, wantSet()) // an entry installed before the timeout existed
	if short := CursorHooksShortTimeout(legacy, wantWithStopTimeout(want)); len(short) != 1 || short[0] != "stop" {
		t.Errorf("a legacy stop entry has short timeouts %v, want [stop]", short)
	}
	healed, _ := RenderCursorHooks(legacy, wantWithStopTimeout(want))
	if got := stopEntry(t, healed)["timeout"]; got != float64(want) {
		t.Errorf("legacy stop timeout healed to %v, want %d", got, want)
	}

	shorter := strings.Replace(string(legacy), stopCmd, stopCmd+`, "timeout": 60`, 1)
	if short := CursorHooksShortTimeout([]byte(shorter), wantWithStopTimeout(want)); len(short) != 1 {
		t.Errorf("a shorter timeout is not reported: %v", short)
	}
	healed, _ = RenderCursorHooks([]byte(shorter), wantWithStopTimeout(want))
	if got := stopEntry(t, healed)["timeout"]; got != float64(want) {
		t.Errorf("shorter stop timeout healed to %v, want %d", got, want)
	}

	longer := strings.Replace(shorter, `"timeout": 60`, `"timeout": 7200`, 1)
	kept, _ := RenderCursorHooks([]byte(longer), wantWithStopTimeout(want))
	if got := stopEntry(t, kept)["timeout"]; got != float64(7200) {
		t.Errorf("an operator's longer timeout was lowered to %v", got)
	}
	if short := CursorHooksShortTimeout([]byte(longer), wantWithStopTimeout(want)); len(short) != 0 {
		t.Errorf("a longer timeout is reported short: %v", short)
	}

	stale := strings.Replace(longer, "satelle hook stopcheck --harness cursor", "satelle hook stopcheck --old", 1)
	replaced, _ := RenderCursorHooks([]byte(stale), wantWithStopTimeout(want))
	e := stopEntry(t, replaced)
	if cmd, _ := e["command"].(string); e["timeout"] != float64(7200) || !strings.HasSuffix(cmd, "--harness cursor") {
		t.Errorf("a stale command was not replaced in place keeping the longer timeout: %v", e)
	}
}
