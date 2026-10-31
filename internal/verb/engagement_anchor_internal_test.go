package verb

import (
	"context"
	"strings"
	"testing"
)

// TestRefuseForeignTreeDiffUnanchored (sty_c098dc2d AC7): the anchor rule is
// opt-in by construction. With no recorded tree — a baseline written before the
// anchor existed, or a non-git checkout — the diff proceeds exactly as it did,
// so upgrading cannot start refusing an in-flight story's own diff. No
// working_on row is recorded here, which is also the never-engaged shape.
func TestRefuseForeignTreeDiffUnanchored(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name             string
		baseline, invoke string
	}{
		{"no recorded tree", "", "/w/b"},
		{"no invocation tree", "/w/a", ""},
		{"neither", "", ""},
		{"same tree", "/w/a", "/w/a"},
	}
	for _, c := range cases {
		if err := refuseForeignTreeDiff(ctx, "sty_x", c.baseline, c.invoke); err != nil {
			t.Errorf("%s: must not refuse: %v", c.name, err)
		}
	}
}

// TestRefuseForeignTreeDiffAnchored: with both trees known and different, the
// refusal names BOTH — where the story is anchored and where the caller is —
// because the operator's next action is to re-run it in the named tree.
func TestRefuseForeignTreeDiffAnchored(t *testing.T) {
	err := refuseForeignTreeDiff(context.Background(), "sty_x", "/w/a", "/w/b")
	if err == nil {
		t.Fatal("foreign tree must be refused")
	}
	for _, want := range []string{"sty_x", "/w/a", "/w/b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q: %v", want, err)
		}
	}
}
