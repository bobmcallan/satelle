package verb

import "testing"

func TestFormatReasoningSuffix(t *testing.T) {
	if got := formatReasoningSuffix(""); got != "" {
		t.Errorf("empty: %q", got)
	}
	if got := formatReasoningSuffix("why"); got != " reasoning=why" {
		t.Errorf("non-empty: %q", got)
	}
	// Accept body shape used by story set (mirrors reject error contract).
	body := "accepted a→b by skill: decision=accept notes=n" + formatReasoningSuffix("r")
	want := "accepted a→b by skill: decision=accept notes=n reasoning=r"
	if body != want {
		t.Errorf("accept body = %q, want %q", body, want)
	}
}
