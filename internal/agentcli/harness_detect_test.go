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
	// Neutral detection: an unrecognised marker is no harness at all.
	if got := DetectSessionHarnesses([]string{"FOO_AGENT=1", "FOO_SESSION_ID=abc"}); len(got) != 0 {
		t.Errorf("unrecognised markers must mark nothing, got %v", got)
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
		"foreign turn envelope":        `{"session_id":"s1","turn_id":"t1","tool_name":"Bash","tool_input":{"command":"x"}}`,
		"foreign call envelope":        `{"conversationId":"abc","toolCall":{"name":"run","args":{}}}`,
		"bare permission_mode":         `{"permission_mode":"default"}`,
		"permission_mode + tool_input": `{"permission_mode":"default","tool_input":{"file_path":"/x.go"}}`,
	} {
		if got := HarnessFromHookEvent([]byte(raw)); got != HarnessUnknown {
			t.Errorf("%s: HarnessFromHookEvent = %q, want unknown", name, got)
		}
	}
}
