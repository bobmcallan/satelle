package ctxaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func refEnv() RefEnv {
	return RefEnv{
		Names:  map[string]bool{"satelle-live-skill": true, "satelle-unwired-skill": true, "satelle-context-audit": true, "satelle-a-principle": true, "satelle-gone-skill": true},
		Skills: map[string]bool{"satelle-live-skill": true, "satelle-unwired-skill": true, "satelle-context-audit": true, "satelle-gone-skill": true},
		Wired:  map[string]bool{"satelle-live-skill": true, "satelle-context-audit": true},
	}
}

func problemsOf(t *testing.T, body string) map[RefKind][]RefProblem {
	t.Helper()
	out := map[RefKind][]RefProblem{}
	for _, p := range CheckRefs(body, "skills/x.md", "x", refEnv()) {
		out[p.Kind] = append(out[p.Kind], p)
	}
	return out
}

func TestCheckRefsBacktickedNameThatResolvesToNothing(t *testing.T) {
	got := problemsOf(t, "Run `satelle-no-such-thing` first.")
	if len(got[RefUnresolved]) != 1 {
		t.Fatalf("want one unresolved, got %+v", got)
	}
	p := got[RefUnresolved][0]
	if p.Where != "skills/x.md" || p.Ref != "`satelle-no-such-thing`" {
		t.Errorf("must name the referring file and the reference: %+v", p)
	}
}

func TestCheckRefsRetiredNameIsUnresolvedEvenWhenAStaleCopyExists(t *testing.T) {
	env := refEnv()
	env.Names["satelle-skill-review"] = true // a stale on-disk copy
	got := CheckRefs("The `satelle-skill-review` structure reviewer.", "constitution.md", "constitution", env)
	if len(got) != 1 || got[0].Kind != RefUnresolved || !strings.Contains(got[0].Detail, "retired in") {
		t.Fatalf("a retired name is unresolved and says so: %+v", got)
	}
}

func TestCheckRefsNamedButUnwiredSkill(t *testing.T) {
	got := problemsOf(t, "Then `satelle-unwired-skill` and [[satelle-unwired-skill]].")
	if len(got[RefUnwired]) != 1 || got[RefUnwired][0].Ref != "[[satelle-unwired-skill]]" && got[RefUnwired][0].Ref != "`satelle-unwired-skill`" {
		t.Fatalf("want one unwired (reported once per name), got %+v", got)
	}
	if len(got[RefUnresolved]) != 0 {
		t.Errorf("an unwired skill resolves: %+v", got)
	}
}

// A skill a task wires is wired — it is not reported.
func TestCheckRefsWiredSkillAndPrincipleAreQuiet(t *testing.T) {
	got := problemsOf(t, "Use `satelle-live-skill`, `satelle-context-audit` and `satelle-a-principle`.")
	if len(got) != 0 {
		t.Fatalf("wired skills and principles must not be reported: %+v", got)
	}
}

func TestCheckRefsIgnoresSyntaxFencesAndSelf(t *testing.T) {
	body := "Pattern `satelle-<object>-review` and `satelle hook context`.\n\n" +
		"```\n`satelle-in-a-fence`\n```\n\n" +
		"Self: `satelle-x`.\n"
	if got := CheckRefs(body, "skills/x.md", "satelle-x", refEnv()); len(got) != 0 {
		t.Fatalf("prose about syntax, fenced examples and self-reference are not references: %+v", got)
	}
}

func TestCheckRefsWikilinkDanglingUsesTheSharedResolver(t *testing.T) {
	got := problemsOf(t, "See [[satelle-missing-thing]] and [[satelle-live-skill]].")
	if len(got[RefUnresolved]) != 1 || got[RefUnresolved][0].Ref != "[[satelle-missing-thing]]" {
		t.Fatalf("got %+v", got)
	}
}

func TestStaleIndexReportsMissingDocuments(t *testing.T) {
	data := t.TempDir()
	docs := filepath.Join(data, "documents")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "real.md"), []byte("# real"), 0o644); err != nil {
		t.Fatal(err)
	}
	index := "---\nokf_version: \"0.1\"\n---\n\n# Documents\n\n" +
		"* [Real one](real.md) - kept\n" +
		"* [Deleted one](deleted.md) - gone\n" +
		"* [External](https://example.com/x.md)\n"
	if err := os.WriteFile(filepath.Join(docs, "index.md"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	got := StaleIndex(data)
	if len(got) != 1 || !strings.Contains(got[0], "deleted.md") {
		t.Fatalf("want exactly the deleted entry, got %v", got)
	}
	if StaleIndex(t.TempDir()) != nil {
		t.Error("no index is not a stale index")
	}
}
