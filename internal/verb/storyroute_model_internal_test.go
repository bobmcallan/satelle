package verb

import "testing"

// TestModelNote pins the "story route" surface of AC6/AC7 (sty_87b86044):
// an LLM reviewer's verdict (Command set) always prints a model chip, showing
// the resolved id when recorded and the alias marked unknown when not, while a
// functional check (no Command, no agent invoked) prints nothing at all.
func TestModelNote(t *testing.T) {
	cases := []struct {
		name string
		v    ReviewerVerdict
		want string
	}{
		{
			name: "resolved model shown",
			v:    ReviewerVerdict{Command: "claude -p ...", Model: "opus", ModelResolved: "claude-opus-5-5"},
			want: " (model claude-opus-5-5)",
		},
		{
			name: "alias only marks unknown",
			v:    ReviewerVerdict{Command: "claude -p ...", Model: "opus"},
			want: " (model opus (unknown))",
		},
		{
			name: "legacy verdict predates both fields",
			v:    ReviewerVerdict{Command: "claude -p ..."},
			want: " (model unknown)",
		},
		{
			name: "functional check has no command and prints nothing",
			v:    ReviewerVerdict{Model: "opus", ModelResolved: "claude-opus-5-5"},
			want: "",
		},
	}
	for _, c := range cases {
		if got := modelNote(c.v); got != c.want {
			t.Errorf("%s: modelNote = %q, want %q", c.name, got, c.want)
		}
	}
}
