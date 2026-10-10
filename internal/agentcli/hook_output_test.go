package agentcli

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// sty_be756616: golden bytes for every harness's PreToolUse deny. The claude,
// grok and pi literals were captured before the encoding moved here.
func TestPreToolUseDenyGoldenBytes(t *testing.T) {
	claude := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"R"}}`
	for harness, want := range map[string]string{
		HarnessClaude:  claude,
		HarnessPi:      claude,
		HarnessUnknown: claude, // nothing unrecognised is its own shape; the deny stays effective
		"":             claude,
		HarnessGrok:    `{"decision":"deny","reason":"R"}`,
		HarnessCursor:  `{"permission":"deny","user_message":"R","agent_message":"R"}`,
	} {
		if got := string(PreToolUseDeny(harness, "R")); got != want {
			t.Errorf("PreToolUseDeny(%q) = %s, want %s", harness, got, want)
		}
	}
}

func TestPreToolUseDenyEmptyReasonGetsPlaceholder(t *testing.T) {
	for _, h := range []string{HarnessClaude, HarnessGrok, HarnessCursor} {
		if got := string(PreToolUseDeny(h, "  ")); !strings.Contains(got, "no reason supplied") {
			t.Errorf("%s: empty reason not replaced: %s", h, got)
		}
	}
}

func TestHookDenyHarnessesAndMatch(t *testing.T) {
	if got, want := HookDenyHarnesses(), []string{HarnessGrok, HarnessCursor}; !reflect.DeepEqual(got, want) {
		t.Fatalf("HookDenyHarnesses = %v, want %v", got, want)
	}
	for harness, want := range map[string]string{
		HarnessGrok:   `*'"decision"'*'"deny"'*`,
		HarnessCursor: `*'"permission"'*'"deny"'*`,
		HarnessClaude: `*'"permissionDecision"'*'"deny"'*`,
		HarnessPi:     `*'"permissionDecision"'*'"deny"'*`,
	} {
		if got := PreToolUseDenyMatch(harness); got != want {
			t.Errorf("PreToolUseDenyMatch(%q) = %s, want %s", harness, got, want)
		}
	}
}

// TestPreToolUseDenyMatchRecognisesOwnShape: each harness's glob (translated to
// a regexp: * -> .*, quoted text literal) matches that harness's own deny and
// no other harness's.
func TestPreToolUseDenyMatchRecognisesOwnShape(t *testing.T) {
	harnesses := []string{HarnessClaude, HarnessGrok, HarnessCursor}
	toRe := func(glob string) *regexp.Regexp {
		lit := strings.NewReplacer(`'`, ``).Replace(glob)
		parts := strings.Split(lit, "*")
		for i, p := range parts {
			parts[i] = regexp.QuoteMeta(p)
		}
		return regexp.MustCompile("(?s)^" + strings.Join(parts, ".*") + "$")
	}
	for _, matcher := range harnesses {
		re := toRe(PreToolUseDenyMatch(matcher))
		for _, emitter := range harnesses {
			got := re.Match(PreToolUseDeny(emitter, "x"))
			if want := matcher == emitter; got != want {
				t.Errorf("%s glob on a %s deny = %v, want %v", matcher, emitter, got, want)
			}
		}
	}
}

// TestCursorDenyReasonVisible pins the cursor deny shape and the visibility of
// its reason to real cursor-agent captures (probe 12, cursor-agent
// 2026.10.01-e373342; sty_be756616).
func TestCursorDenyReasonVisible(t *testing.T) {
	// rejectedReasons returns every tool_call result.*.rejected.reason in a
	// stream-json capture.
	rejectedReasons := func(name string) []string {
		var out []string
		for _, ev := range cursorJSONLines(t, name) {
			if ev["type"] != "tool_call" || ev["subtype"] != "completed" {
				continue
			}
			call, _ := ev["tool_call"].(map[string]any)
			for _, v := range call {
				tool, _ := v.(map[string]any)
				result, _ := tool["result"].(map[string]any)
				rej, _ := result["rejected"].(map[string]any)
				if r, ok := rej["reason"].(string); ok {
					out = append(out, r)
				}
			}
		}
		return out
	}
	echoed := func(name string) map[string]any {
		m := regexp.MustCompile(`(?m)^echo '(\{.*\})'$`).FindStringSubmatch(cursorRead(t, name))
		if m == nil {
			t.Fatalf("%s: no echoed deny JSON", name)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return doc
	}
	keys := func(m map[string]any) []string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	}

	// The shape satelle emits: blocked, and the model was shown user_message.
	snake := echoed("12-snake-hook.sh")
	userMsg, _ := snake["user_message"].(string)
	if userMsg == "" {
		t.Fatalf("12-snake probe has no user_message: %v", snake)
	}
	reasons := rejectedReasons("12-snake.out")
	if len(reasons) == 0 {
		t.Fatal("12-snake.out: no rejected tool call")
	}
	for _, r := range reasons {
		if !strings.Contains(r, userMsg) {
			t.Errorf("12-snake rejected reason %q does not contain the probe's user_message %q", r, userMsg)
		}
	}
	var enc map[string]any
	if err := json.Unmarshal(PreToolUseDeny(HarnessCursor, "R"), &enc); err != nil {
		t.Fatal(err)
	}
	if enc["permission"] != "deny" || enc["user_message"] != "R" || enc["agent_message"] != "R" {
		t.Errorf("PreToolUseDeny(cursor) = %v", enc)
	}
	if !reflect.DeepEqual(keys(enc), keys(snake)) {
		t.Errorf("encoder keys %v differ from the captured shape %v", keys(enc), keys(snake))
	}

	// The shape satelle does not use: it blocks, but cursor hides the reason.
	plain := echoed("12-reason-hook.sh")
	plainReason, _ := plain["reason"].(string)
	if plainReason == "" {
		t.Fatalf("12-reason probe has no reason: %v", plain)
	}
	hidden := rejectedReasons("12-reason.out")
	if len(hidden) == 0 {
		t.Fatal("12-reason.out: no rejected tool call")
	}
	for _, r := range hidden {
		if strings.Contains(r, plainReason) {
			t.Errorf("12-reason rejected reason %q contains %q; the reason-only shape would be visible", r, plainReason)
		}
	}

	// Every shape blocked the tool: probe 12's write never landed, and probes
	// 10a/11a (a delete) left the file they targeted in place.
	for _, fs := range []string{"12-snake.fs", "12-reason.fs"} {
		if body := cursorRead(t, fs); !strings.Contains(body, "written.txt: absent") {
			t.Errorf("%s: want the target absent, got %q", fs, body)
		}
	}
	for _, fs := range []string{"10a.fs", "11a.fs"} {
		if body := cursorRead(t, fs); !strings.Contains(body, "other.txt: PRESENT") {
			t.Errorf("%s: want the delete target still present, got %q", fs, body)
		}
	}
}
