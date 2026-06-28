package agentcli

import "testing"

func TestRunnerFromHarness(t *testing.T) {
	cases := []struct {
		harness   string
		want      string // expected runner name
		nilRunner bool
		wantErr   bool
	}{
		{"claude -p", "claude", false, false},
		{"codex -p", "codex", false, false},
		{"claude", "claude", false, false},
		{"", "", true, false},
		{"in-loop", "", true, false},
		{"bogus -p", "", true, true},
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
