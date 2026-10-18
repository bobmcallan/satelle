package textdiff

import (
	"bytes"
	"strings"
	"testing"
)

func TestUnified(t *testing.T) {
	var buf bytes.Buffer
	Unified(&buf, "old", "new", "a\nb\nc\nd\ne\nf\ng\nh\n", "a\nb\nC\nd\ne\nf\ng\nh\nz")
	got := buf.String()
	for _, want := range []string{"--- old\n+++ new\n", "@@ -1,8 +1,9 @@", " a\n", "-c\n+C\n", "+z\n\\ No newline at end of file\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	buf.Reset()
	Unified(&buf, "old", "new", "same\n", "same\n")
	if strings.Contains(buf.String(), "@@") {
		t.Errorf("equal inputs must have no hunk:\n%s", buf.String())
	}
	buf.Reset()
	Unified(&buf, "old", "new", "", "only\n")
	if !strings.Contains(buf.String(), "@@ -0,0 +1,1 @@") || !strings.Contains(buf.String(), "+only\n") {
		t.Errorf("an empty old side:\n%s", buf.String())
	}
}
