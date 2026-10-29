package ctxaudit

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
	m := WikiLinkRe.FindAllStringSubmatch(body, -1)
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
	probs := AuditWikilinks(data, embedded)
	if len(probs) != 1 || !strings.Contains(probs[0], "satelle-missing-thing") {
		t.Fatalf("want one dangler for missing-thing, got %v", probs)
	}
	// Constitution alias resolves.
	write("principles/satelle-agent-goals.md", "---\nname: satelle-agent-goals\n---\n# g\nSee [[satelle-constitution]].\n")
	probs = AuditWikilinks(data, embedded)
	if len(probs) != 0 {
		t.Fatalf("constitution alias should resolve, got %v", probs)
	}
}

func TestAuditWikilinksSkipsConfigBundlesByDeclaredType(t *testing.T) {
	const toml = "[[gate]]\nskill = \"x\"\n"
	for _, c := range []struct {
		name     string
		file     string
		docType  string
		reported bool
	}{
		{"bundle-shaped name, not a bundle", "config-111111111111-222222222222.md", "plan", true},
		{"other name, declared bundle", "bundle.md", "config-bundle", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), ".satelle")
			p := filepath.Join(data, "tasks", "tsk_y", c.file)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			body := "---\nstory: tsk_y\ntype: " + c.docType + "\nname: n\n---\n\n" + toml
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			probs := AuditWikilinks(data, nil)
			got := len(probs) == 1 && strings.Contains(probs[0], c.file) && strings.Contains(probs[0], "[[gate]]")
			if got != c.reported || (!c.reported && len(probs) != 0) {
				t.Fatalf("reported = %v, want %v: %v", got, c.reported, probs)
			}
		})
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
	dir := t.TempDir()
	probs := AuditWikilinks(dir, embedded)
	if len(probs) > 0 {
		t.Fatalf("embedded substrate has dangling wikilinks:\n%s", strings.Join(probs, "\n"))
	}
}

func TestDanglingEnrichesRetired(t *testing.T) {
	catalog := map[string]bool{"satelle-route-standard": true}
	// rename
	got := Dangling("see [[satelle-dot-standard]] please", "principles/x.md", catalog)
	if len(got) != 1 || !strings.Contains(got[0], "renamed to [[satelle-route-standard]]") {
		t.Fatalf("rename enrich: %v", got)
	}
	// removal
	got = Dangling("[[satelle-configuration-over-code]]", "skills/y.md", catalog)
	if len(got) != 1 || !strings.Contains(got[0], "no replacement") {
		t.Fatalf("removal enrich: %v", got)
	}
	// unknown still plain
	got = Dangling("[[totally-unknown-xyz]]", "z.md", catalog)
	if len(got) != 1 || strings.Contains(got[0], "retired") {
		t.Fatalf("unknown: %v", got)
	}
	// still a problem (count)
	if len(Dangling("[[satelle-dot-standard]]", "a.md", catalog)) != 1 {
		t.Fatal("retired name must still count as dangling")
	}
}

func TestDanglingIgnoresFencedCodeBlocks(t *testing.T) {
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

	got := Dangling(body, "skills/x.md", catalog)
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
	// Byte length must survive masking — the migrate rewriter slices the
	// original body by offsets found in the masked copy. Multi-byte runes
	// (em dash) inside quoted code are the trap.
	for _, body := range []string{
		"```toml\n[[gate]] — quoted\n```\ntail\n",
		"prose `[[gate]] — inline` tail\n",
		"unterminated:\n```toml\n[[gate]] — no close\n",
		"no quoted code at all\n",
	} {
		masked := MaskQuotedCode(body)
		if len(masked) != len(body) {
			t.Errorf("length changed %d→%d for %q", len(body), len(masked), body)
		}
		if strings.Count(masked, "\n") != strings.Count(body, "\n") {
			t.Errorf("newline count changed for %q → %q", body, masked)
		}
	}
}
