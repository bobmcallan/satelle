package agentcli

import "testing"

func TestRunnerFromHarness(t *testing.T) {
	cases := []struct {
		harness   string
		want      string // expected runner name (the template binary)
		nilRunner bool
		wantErr   bool
	}{
		{"claude", "claude", false, false},                                    // single-token preset
		{"codex", "codex", false, false},                                      // single-token preset (stub)
		{"claude -p --append-system-prompt {system}", "claude", false, false}, // literal template
		{"myagent review {system} {tools}", "myagent", false, false},          // arbitrary literal template
		{"", "", true, false},                                                 // unset → nil (keep default)
		{"in-loop", "", true, false},                                          // in-loop → nil
		{"bogus", "", true, true},                                             // unknown single-token preset → error
	}
	for _, c := range cases {
		r, err := RunnerFromHarness(c.harness)
		switch {
		case c.wantErr:
			if err == nil {
				t.Errorf("%q: want error, got nil", c.harness)
			}
		case err != nil:
			t.Errorf("%q: unexpected error %v", c.harness, err)
		case c.nilRunner:
			if r != nil {
				t.Errorf("%q: want nil runner, got %q", c.harness, r.Name())
			}
		case r == nil || r.Name() != c.want:
			t.Errorf("%q: runner = %v, want %q", c.harness, r, c.want)
		}
	}
}

// A multi-token literal harness must carry its FULL argv template, not just the
// binary — the whole point of the template seam.
func TestRunnerFromHarnessLiteralKeepsArgs(t *testing.T) {
	r, err := RunnerFromHarness("claude -p --allowedTools {tools}")
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := r.(templateRunner)
	if !ok {
		t.Fatalf("expected templateRunner, got %T", r)
	}
	if !contains(tr.argTemplate, "-p") || !contains(tr.argTemplate, "{tools}") {
		t.Errorf("literal template lost its args: %#v", tr.argTemplate)
	}
}
