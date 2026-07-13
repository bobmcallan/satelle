package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

func TestWikiLinkReIgnoresBashAndMatchesMarkdown(t *testing.T) {
	body := `
See [[satelle-agent-model]] and [[satelle-constitution]].
Bash: if [[ -z "$x" ]]; then :; fi
Class: [[:space:]]
Label: [[satelle-done-is-last|done last]]
Anchor: [[satelle-residency#tiers]]
`
	m := wikiLinkRe.FindAllStringSubmatch(body, -1)
	got := map[string]bool{}
	for _, sub := range m {
		got[sub[1]] = true
	}
	for _, want := range []string{"satelle-agent-model", "satelle-constitution", "satelle-done-is-last", "satelle-residency"} {
		if !got[want] {
			t.Errorf("expected match %q in %v", want, got)
		}
	}
	for _, ban := range []string{"-z \"$x\"", ":space:"} {
		if got[ban] {
			t.Errorf("must not match bash/class %q", ban)
		}
	}
}

func TestAuditWikilinksFindsDanglerAndPassesClean(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, ".satelle")
	if err := os.MkdirAll(filepath.Join(data, "principles"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Known principle.
	write := func(rel, body string) {
		p := filepath.Join(data, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("principles/satelle-agent-goals.md", "---\nname: satelle-agent-goals\n---\n# g\nSee [[satelle-missing-thing]].\n")
	write("constitution.md", "# c\nSee [[satelle-agent-goals]].\n")

	embedded := []config.EmbeddedDefault{
		{Kind: "principles", Name: "satelle-agent-goals", Body: "x"},
	}
	probs := auditWikilinks(data, embedded)
	if len(probs) != 1 || !strings.Contains(probs[0], "satelle-missing-thing") {
		t.Fatalf("want one dangler for missing-thing, got %v", probs)
	}
	// Constitution alias resolves.
	write("principles/satelle-agent-goals.md", "---\nname: satelle-agent-goals\n---\n# g\nSee [[satelle-constitution]].\n")
	probs = auditWikilinks(data, embedded)
	if len(probs) != 0 {
		t.Fatalf("constitution alias should resolve, got %v", probs)
	}
}

func TestAuditWikilinksRepoAgnostic(t *testing.T) {
	// Source must not hardcode concrete this-repo story ids (only the sty_/tsk_
	// prefix filter, which is a universal id shape).
	src, err := os.ReadFile("wikilink.go")
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`sty_[0-9a-f]{8,}`).Match(src) {
		t.Error("wikilink.go must not embed this-repo story ids")
	}
}

func TestEmbeddedWikilinksResolve(t *testing.T) {
	// Every [[ref]] in embedded defaults must resolve against the embedded
	// catalog alone (fresh-repo path with no on-disk substrate).
	embedded := config.EmbeddedDefaults()
	// Virtual dataDir with only constitution empty — catalog is embedded-only
	// plus constitution aliases.
	dir := t.TempDir()
	probs := auditWikilinks(dir, embedded)
	if len(probs) > 0 {
		t.Fatalf("embedded substrate has dangling wikilinks:\n%s", strings.Join(probs, "\n"))
	}
}
