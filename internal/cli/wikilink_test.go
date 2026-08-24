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

func TestDanglingInEnrichesRetired(t *testing.T) {
	catalog := map[string]bool{"satelle-route-standard": true}
	// rename
	got := danglingIn("see [[satelle-dot-standard]] please", "principles/x.md", catalog)
	if len(got) != 1 || !strings.Contains(got[0], "renamed to [[satelle-route-standard]]") {
		t.Fatalf("rename enrich: %v", got)
	}
	// removal
	got = danglingIn("[[satelle-configuration-over-code]]", "skills/y.md", catalog)
	if len(got) != 1 || !strings.Contains(got[0], "no replacement") {
		t.Fatalf("removal enrich: %v", got)
	}
	// unknown still plain
	got = danglingIn("[[totally-unknown-xyz]]", "z.md", catalog)
	if len(got) != 1 || strings.Contains(got[0], "retired") {
		t.Fatalf("unknown: %v", got)
	}
	// still a problem (count)
	if len(danglingIn("[[satelle-dot-standard]]", "a.md", catalog)) != 1 {
		t.Fatal("retired name must still count as dangling")
	}
}

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

func TestDanglingInIgnoresFencedCodeBlocks(t *testing.T) {
	catalog := map[string]bool{"satelle-known-name": true}
	body := "# Placement\n" +
		"\n" +
		"```toml\n" +
		"[[gate]]\n" +
		"skill = \"satelle-agent-roster-check\"\n" +
		"```\n" +
		"\n" +
		"Tilde fence:\n" +
		"\n" +
		"~~~toml\n" +
		"[[table]]\n" +
		"~~~\n" +
		"\n" +
		"Inline: `[[gate]]` is a TOML header — em dash keeps the mask honest.\n" +
		"\n" +
		"Resolvable: [[satelle-known-name]].\n" +
		"\n" +
		"Real dangler AFTER the fences: [[satelle-missing-thing]].\n"

	got := danglingIn(body, "skills/x.md", catalog)
	if len(got) != 1 {
		t.Fatalf("want exactly one problem, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "satelle-missing-thing") {
		t.Fatalf("want the prose dangler reported, got %v", got)
	}
	for _, banned := range []string{"gate", "table"} {
		if strings.Contains(got[0], "[["+banned+"]]") {
			t.Fatalf("quoted %q must not be reported: %v", banned, got)
		}
	}
}

func TestMaskQuotedCodePreservesLength(t *testing.T) {
	// Byte length must survive masking — rewriteWikilinkTarget slices the
	// original body by offsets found in the masked copy. Multi-byte runes
	// (em dash) inside quoted code are the trap.
	for _, body := range []string{
		"```toml\n[[gate]] — quoted\n```\ntail\n",
		"prose `[[gate]] — inline` tail\n",
		"unterminated:\n```toml\n[[gate]] — no close\n",
		"no quoted code at all\n",
	} {
		masked := maskQuotedCode(body)
		if len(masked) != len(body) {
			t.Errorf("length changed %d→%d for %q", len(body), len(masked), body)
		}
		if strings.Count(masked, "\n") != strings.Count(body, "\n") {
			t.Errorf("newline count changed for %q → %q", body, masked)
		}
	}
}
