package agentinstall

import (
	"encoding/json"
	"strings"
	"testing"
)

// sty_7d098d50 AC1/AC2: the cursor hooks.json merge. The commands are the
// caller's; these tests pin the file shape, idempotence and the ownership
// boundary.

func wantSet() []CursorHook {
	return []CursorHook{
		{Event: "preToolUse", Command: "sh /r/.satelle/hooks/satelle-hook.sh gate cursor", Role: ".satelle/hooks/satelle-hook.sh gate "},
		{Event: "preToolUse", Command: "sh /r/.satelle/hooks/satelle-hook.sh commitgate cursor", Role: ".satelle/hooks/satelle-hook.sh commitgate "},
		{Event: "sessionStart", Command: "PATH=$HOME/.local/bin:$PATH satelle hook context --harness cursor", Role: "satelle hook context"},
		{Event: "stop", Command: "PATH=$HOME/.local/bin:$PATH satelle hook stopcheck --harness cursor", Role: "satelle hook stopcheck"},
	}
}

func TestRenderCursorHooksFreshGolden(t *testing.T) {
	got, err := RenderCursorHooks(nil, wantSet())
	if err != nil {
		t.Fatal(err)
	}
	const golden = `{
  "version": 1,
  "hooks": {
    "preToolUse": [
      {
        "command": "sh /r/.satelle/hooks/satelle-hook.sh gate cursor"
      },
      {
        "command": "sh /r/.satelle/hooks/satelle-hook.sh commitgate cursor"
      }
    ],
    "sessionStart": [
      {
        "command": "PATH=$HOME/.local/bin:$PATH satelle hook context --harness cursor"
      }
    ],
    "stop": [
      {
        "command": "PATH=$HOME/.local/bin:$PATH satelle hook stopcheck --harness cursor"
      }
    ]
  }
}
`
	if string(got) != golden {
		t.Fatalf("fresh hooks.json:\n%s\nwant:\n%s", got, golden)
	}
	// No entry carries a matcher: cursor's gate sees every tool call.
	if strings.Contains(string(got), "matcher") {
		t.Error("cursor entries must be matcher-less")
	}
	again, err := RenderCursorHooks(got, wantSet())
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(got) {
		t.Fatalf("a second render changed the file:\n%s", again)
	}
}

func TestRenderCursorHooksKeepsUserContent(t *testing.T) {
	user := `{
  "zeta": {"keep": true},
  "version": 7,
  "hooks": {
    "afterFileEdit": [{"command": "./fmt.sh", "timeout": 5}],
    "preToolUse": [
      {"command": "/opt/mine/audit.sh", "matcher": "Shell"},
      {"command": "sh /old/.satelle/hooks/satelle-hook.sh gate cursor"}
    ]
  },
  "alpha": [1, 2]
}`
	got, err := RenderCursorHooks([]byte(user), wantSet())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if string(doc["version"]) != "7" {
		t.Errorf("version = %s, want the user's 7", doc["version"])
	}
	if !strings.Contains(string(got), `"zeta"`) || !strings.Contains(string(got), `"alpha"`) {
		t.Errorf("unknown top-level keys lost:\n%s", got)
	}
	// Key order survives: zeta before version before hooks before alpha.
	iz, iv, ih, ia := strings.Index(string(got), `"zeta"`), strings.Index(string(got), `"version"`), strings.Index(string(got), `"hooks"`), strings.Index(string(got), `"alpha"`)
	if !(iz < iv && iv < ih && ih < ia) {
		t.Errorf("key order changed (zeta %d version %d hooks %d alpha %d)", iz, iv, ih, ia)
	}
	for _, mine := range []string{`"./fmt.sh"`, `"timeout": 5`, `/opt/mine/audit.sh`, `"matcher": "Shell"`} {
		if !strings.Contains(string(got), mine) {
			t.Errorf("user content %s lost:\n%s", mine, got)
		}
	}
	// The stale gate entry (another root) is rewritten in place, not duplicated.
	if n := strings.Count(string(got), "satelle-hook.sh gate cursor"); n != 1 {
		t.Errorf("gate entries = %d, want 1:\n%s", n, got)
	}
	if strings.Contains(string(got), "/old/") {
		t.Errorf("stale wrapper path kept:\n%s", got)
	}
	again, _ := RenderCursorHooks(got, wantSet())
	if string(again) != string(got) {
		t.Errorf("not idempotent over a user file")
	}
}

func TestRenderCursorHooksRefusesUnsafeFile(t *testing.T) {
	for _, bad := range []string{`not json`, `[1,2]`, `{"hooks": []}`, `{"hooks": {"stop": {}}}`} {
		if out, err := RenderCursorHooks([]byte(bad), wantSet()); err == nil {
			t.Errorf("RenderCursorHooks(%q) = %s, want an error", bad, out)
		}
	}
}

func TestCursorHooksMissing(t *testing.T) {
	full, _ := RenderCursorHooks(nil, wantSet())
	if m := CursorHooksMissing(full, wantSet()); len(m) != 0 {
		t.Errorf("complete file reports missing %v", m)
	}
	m := CursorHooksMissing([]byte(`{"version":1,"hooks":{"stop":[{"command":"x"}]}}`), wantSet())
	if strings.Join(m, ",") != "preToolUse,preToolUse,sessionStart,stop" {
		t.Errorf("missing = %v", m)
	}
	if m := CursorHooksMissing([]byte(`nope`), wantSet()); len(m) != 4 {
		t.Errorf("unparseable file should report every slot missing, got %v", m)
	}
}

func TestRemoveCursorHooks(t *testing.T) {
	// A satelle-only file empties to nothing: the caller deletes it.
	full, _ := RenderCursorHooks(nil, wantSet())
	out, empty, changed, err := RemoveCursorHooks(full)
	if err != nil || !empty || !changed || out != nil {
		t.Fatalf("satelle-only: out=%s empty=%v changed=%v err=%v", out, empty, changed, err)
	}

	// A file with the user's entry keeps it, and the rest of the file.
	mixed, _ := RenderCursorHooks([]byte(`{"version":1,"extra":true,"hooks":{"afterFileEdit":[{"command":"./fmt.sh"}],"stop":[{"command":"/mine/stop.sh"}]}}`), wantSet())
	out, empty, changed, err = RemoveCursorHooks(mixed)
	if err != nil || empty || !changed {
		t.Fatalf("mixed: empty=%v changed=%v err=%v", empty, changed, err)
	}
	s := string(out)
	for _, keep := range []string{`./fmt.sh`, `/mine/stop.sh`, `"extra"`, `"version"`} {
		if !strings.Contains(s, keep) {
			t.Errorf("user content %s lost:\n%s", keep, s)
		}
	}
	for _, gone := range []string{"satelle-hook.sh", "satelle hook "} {
		if strings.Contains(s, gone) {
			t.Errorf("satelle entry %q survived remove:\n%s", gone, s)
		}
	}
	if strings.Contains(s, "preToolUse") || strings.Contains(s, "sessionStart") {
		t.Errorf("emptied events should be dropped:\n%s", s)
	}

	// Nothing of satelle's: untouched, changed=false.
	user := []byte(`{"version":1,"hooks":{"stop":[{"command":"/mine/stop.sh"}]}}`)
	out, empty, changed, err = RemoveCursorHooks(user)
	if err != nil || empty || changed || string(out) != string(user) {
		t.Errorf("user-only: out=%s empty=%v changed=%v err=%v", out, empty, changed, err)
	}
	if _, _, _, err := RemoveCursorHooks([]byte(`nope`)); err == nil {
		t.Error("unparseable file should error")
	}
}

func TestIsSatelleOwnedHookCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		"sh /r/.satelle/hooks/satelle-hook.sh gate cursor":                    true,
		"PATH=$HOME/.local/bin:$PATH satelle hook prompt":                     true,
		"PATH=$HOME/.local/bin:$PATH satelle hook stopcheck --harness cursor": true,
		"satelle reindex":    true,
		"/opt/mine/audit.sh": false,
		"echo satelle":       false,
		"":                   false,
	} {
		if got := IsSatelleOwnedHookCommand(cmd); got != want {
			t.Errorf("IsSatelleOwnedHookCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}
