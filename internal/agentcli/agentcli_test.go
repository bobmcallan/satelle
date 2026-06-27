package agentcli

import (
	"context"
	"strings"
	"testing"
)

func TestNewRunnerMapping(t *testing.T) {
	cases := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{"", CLIClaude, false}, // empty defaults to claude
		{"claude", CLIClaude, false},
		{"CLAUDE", CLIClaude, false}, // case-insensitive
		{"codex", CLICodex, false},
		{"gpt", "", true},
	}
	for _, c := range cases {
		r, err := NewRunner(c.name)
		if c.wantErr {
			if err == nil {
				t.Errorf("NewRunner(%q): expected error", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("NewRunner(%q): %v", c.name, err)
			continue
		}
		if r.Name() != c.want {
			t.Errorf("NewRunner(%q).Name() = %q, want %q", c.name, r.Name(), c.want)
		}
	}
}

func TestCodexStubErrorsClearly(t *testing.T) {
	r, err := NewRunner(CLICodex)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background(), Request{SystemPrompt: "x", Payload: "y"})
	if err == nil {
		t.Fatal("codex Run should error until implemented")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("codex error should be explicit, got: %v", err)
	}
}
