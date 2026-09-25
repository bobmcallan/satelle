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

func TestHarnessFromHookEvent(t *testing.T) {
	fixtures := map[string]string{
		"claude_bash":       HarnessClaude,
		"claude_edit":       HarnessClaude,
		"grok_bash":         HarnessGrok,
		"grok_edit":         HarnessGrok,
		"codex_shell":       HarnessCodex,
		"codex_apply_patch": HarnessCodex,
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
		"empty":           `{}`,
		"null tool_input": `{"tool_input":null}`,
		"unrelated":       `{"foo":1}`,
		"bare snake":      `{"tool_input":{"file_path":"/x.go"}}`,
		"both keys":       `{"tool_input":{},"toolInput":{}}`,
		"not json":        `nope`,
	} {
		if got := HarnessFromHookEvent([]byte(raw)); got != HarnessUnknown {
			t.Errorf("%s: HarnessFromHookEvent = %q, want unknown", name, got)
		}
	}
}
