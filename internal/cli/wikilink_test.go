package cli

import (
	"strings"
	"testing"
)

// The [[wikilink]] resolver's own tests live with it in internal/ctxaudit; what
// stays here is the migrate rewriter that consumes its masking.

func TestRewriteWikilinkTarget(t *testing.T) {
	in := "See [[satelle-dot-standard]] and [[satelle-dot-standard|label]] and [[satelle-dot-standard#a]]."
	out, n := rewriteWikilinkTarget(in, "satelle-dot-standard", "satelle-route-standard")
	if n != 3 {
		t.Fatalf("n=%d want 3", n)
	}
	if strings.Contains(out, "satelle-dot-standard") {
		t.Fatalf("old remains: %s", out)
	}
	if !strings.Contains(out, "[[satelle-route-standard|label]]") {
		t.Fatalf("label not preserved: %s", out)
	}
}

func TestRewriteWikilinkTargetLeavesQuotedCode(t *testing.T) {
	in := "Prose [[satelle-dot-standard]] rewrites.\n" +
		"\n" +
		"```toml\n" +
		"# [[satelle-dot-standard]] — quoted, must survive\n" +
		"```\n" +
		"\n" +
		"Inline `[[satelle-dot-standard]]` also survives.\n"
	out, n := rewriteWikilinkTarget(in, "satelle-dot-standard", "satelle-route-standard")
	if n != 1 {
		t.Fatalf("n=%d want 1 (prose only)", n)
	}
	if strings.Count(out, "[[satelle-dot-standard]]") != 2 {
		t.Fatalf("quoted occurrences must survive verbatim:\n%s", out)
	}
	if !strings.Contains(out, "Prose [[satelle-route-standard]] rewrites.") {
		t.Fatalf("prose not rewritten:\n%s", out)
	}
}
