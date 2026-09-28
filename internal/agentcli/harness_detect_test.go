package agentcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureEnv(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "harness", name+".env"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

func TestInLoopHarnessFromEnv(t *testing.T) {
	cases := []struct {
		fixture string
		want    string
		ok      bool
	}{
		{"claude", HarnessClaude, true},
		{"grok", HarnessGrok, true},
		{"codex", HarnessCodex, true},
		{"plain", "", false},
	}
	for _, c := range cases {
		got, ok := InLoopHarnessFromEnv(fixtureEnv(t, c.fixture))
		if got != c.want || ok != c.ok {
			t.Errorf("%s: InLoopHarnessFromEnv = (%q,%v), want (%q,%v)", c.fixture, got, ok, c.want, c.ok)
		}
	}
	if _, ok := InLoopHarnessFromEnv([]string{"GROK_AGENT=0"}); ok {
		t.Error("GROK_AGENT=0 must not count as an in-loop session")
	}
	if got := DetectSessionHarnesses([]string{"CODEX_SANDBOX=seatbelt"}); !got[HarnessCodex] {
		t.Error("CODEX_SANDBOX must mark codex")
	}
}

func TestDetectSessionHarnesses_Antigravity(t *testing.T) {
	for _, c := range []struct {
		env  []string
		want map[string]bool
	}{
		{[]string{"ANTIGRAVITY_AGENT=1"}, map[string]bool{HarnessAntigravity: true}},
		{[]string{"ANTIGRAVITY_AGENT=true"}, map[string]bool{HarnessAntigravity: true}},
		{[]string{"ANTIGRAVITY_AGENT=0"}, map[string]bool{}},
		{[]string{"ANTIGRAVITY_AGENT="}, map[string]bool{}},
		{[]string{"ANTIGRAVITY_AGENT_X=1"}, map[string]bool{}},
		{[]string{"CLAUDECODE=1", "ANTIGRAVITY_AGENT=1"}, map[string]bool{HarnessClaude: true, HarnessAntigravity: true}},
	} {
		got := DetectSessionHarnesses(c.env)
		if len(got) != len(c.want) {
			t.Errorf("%v: DetectSessionHarnesses = %v, want %v", c.env, got, c.want)
			continue
		}
		for h := range c.want {
			if !got[h] {
				t.Errorf("%v: DetectSessionHarnesses = %v, want %v", c.env, got, c.want)
			}
		}
	}
	if h, ok := InLoopHarnessFromEnv([]string{"ANTIGRAVITY_AGENT=1"}); !ok || h != HarnessAntigravity {
		t.Errorf("InLoopHarnessFromEnv = (%q,%v), want antigravity", h, ok)
	}
	if _, ok := InLoopHarnessFromEnv([]string{"ANTIGRAVITY_AGENT=0"}); ok {
		t.Error("ANTIGRAVITY_AGENT=0 must not count as an in-loop session")
	}
}

// The antigravity fixtures live with the hook tests in internal/cli; they are
// synthetic (see their README), not live captures.
func TestHarnessFromHookEvent_Antigravity(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "cli", "testdata", "antigravity", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no antigravity fixtures found: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if got := HarnessFromHookEvent(b); got != HarnessAntigravity {
			t.Errorf("%s: HarnessFromHookEvent = %q, want antigravity", filepath.Base(f), got)
		}
	}
	for name, raw := range map[string]string{
		"toolCall only":       `{"toolCall":{"name":"run_command","args":{}}}`,
		"conversationId only": `{"conversationId":"abc"}`,
	} {
		if got := HarnessFromHookEvent([]byte(raw)); got != HarnessAntigravity {
			t.Errorf("%s: HarnessFromHookEvent = %q, want antigravity", name, got)
		}
	}
	// A toolCall riding beside a claude/grok tool input key is not antigravity.
	for name, raw := range map[string]string{
		"toolCall with tool_input": `{"toolCall":{},"tool_input":{"file_path":"/x.go"},"tool_name":"Edit"}`,
		"toolCall with toolInput":  `{"toolCall":{},"toolInput":{"filePath":"/x.go"}}`,
		"null conversationId":      `{"conversationId":null}`,
	} {
		if got := HarnessFromHookEvent([]byte(raw)); got == HarnessAntigravity {
			t.Errorf("%s: classified antigravity", name)
		}
	}
}

func TestHarnessFromHookEvent(t *testing.T) {
	fixtures := map[string]string{
		"claude_bash":        HarnessClaude,
		"claude_edit":        HarnessClaude,
		"grok_bash":          HarnessGrok,
		"grok_edit":          HarnessGrok,
		"grok_session_start": HarnessGrok, // real capture, sty_719c4a7b AC1/AC3
		"grok_prompt_submit": HarnessGrok, // real capture — the AC3 regression case
		"grok_pre_tool_use":  HarnessGrok, // real capture
		"grok_stop":          HarnessGrok, // real capture
		"codex_shell":        HarnessCodex,
		"codex_apply_patch":  HarnessCodex,
	}
	for name, want := range fixtures {
		b, err := os.ReadFile(filepath.Join("testdata", "hooks", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if got := HarnessFromHookEvent(b); got != want {
			t.Errorf("%s: HarnessFromHookEvent = %q, want %q", name, got, want)
		}
	}
	for name, raw := range map[string]string{
		"empty":                        `{}`,
		"null tool_input":              `{"tool_input":null}`,
		"unrelated":                    `{"foo":1}`,
		"bare snake":                   `{"tool_input":{"file_path":"/x.go"}}`,
		"both keys":                    `{"tool_input":{},"toolInput":{}}`,
		"not json":                     `nope`,
		"bare permission_mode":         `{"permission_mode":"default"}`,
		"permission_mode + tool_input": `{"permission_mode":"default","tool_input":{"file_path":"/x.go"}}`,
	} {
		if got := HarnessFromHookEvent([]byte(raw)); got != HarnessUnknown {
			t.Errorf("%s: HarnessFromHookEvent = %q, want unknown", name, got)
		}
	}
}
