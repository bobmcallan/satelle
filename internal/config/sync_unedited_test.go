package config

import (
	"reflect"
	"testing"
)

// embeddedBody returns the shipped body of one embedded default.
func embeddedBody(t *testing.T, kind, name string) string {
	t.Helper()
	for _, d := range EmbeddedDefaults() {
		if d.Kind == kind && d.Name == name {
			return d.Body
		}
	}
	t.Fatalf("no embedded default %s/%s", kind, name)
	return ""
}

// stampedCopy is body as init writes it: stamped against its own bytes.
func stampedCopy(body string) string { return ApplyStamp(body, SHA(body)) }

// uneditedTree writes the tree every sty_097833e8 test shares: unedited copies in
// every form (current stamped, older stamped, stampless identical, a TOML route,
// a task), an edited seed in both forms, and an authored file.
func uneditedTree(t *testing.T, repo string) {
	t.Helper()
	step := embeddedBody(t, "skills", "satelle-step-summary")
	writeFile(t, repo, ".satelle/skills/satelle-step-summary.md", stampedCopy(step))
	writeFile(t, repo, ".satelle/skills/satelle-estimate-actual-review.md", embeddedBody(t, "skills", "satelle-estimate-actual-review"))
	goals := embeddedBody(t, "principles", "satelle-agent-goals")
	writeFile(t, repo, ".satelle/principles/satelle-agent-goals.md", stampedCopy(goals+"\nan older default\n"))
	writeFile(t, repo, ".satelle/workflows/done.toml", embeddedBody(t, "workflows", "done"))
	writeFile(t, repo, ".satelle/tasks/tsk_substrate-audit.md", stampedCopy(embeddedBody(t, "tasks", "tsk_substrate-audit")))

	// Edited: the stamp no longer matches the body (stamped), or the body differs
	// from the default (stampless).
	adv := embeddedBody(t, "skills", "satelle-workflow-advisor")
	writeFile(t, repo, ".satelle/skills/satelle-workflow-advisor.md", ApplyStamp(adv+"\noperator edit\n", SHA(adv)))
	writeFile(t, repo, ".satelle/skills/satelle-docs-only-check.md", embeddedBody(t, "skills", "satelle-docs-only-check")+"\noperator edit\n")

	writeFile(t, repo, ".satelle/skills/my-own-skill.md", "---\nname: my-own-skill\ntype: skill\n---\nauthored\n")
	writeFile(t, repo, ".satelle/skills/secret.local.md", "SECRET\n")
}

var uneditedPaths = []string{
	"principles/satelle-agent-goals.md",
	"skills/satelle-estimate-actual-review.md",
	"skills/satelle-step-summary.md",
	"tasks/tsk_substrate-audit.md",
	"workflows/done.toml",
}

var keptPaths = []string{
	"skills/my-own-skill.md",
	"skills/satelle-docs-only-check.md",
	"skills/satelle-workflow-advisor.md",
}

func syncAllPersonal() Config {
	return Config{Sync: map[string]string{"all": "personal"}}
}

func bundlePaths(files []ConfigFile) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

// TestConfigFilesLeavesUneditedDefaultsOut (sty_097833e8 AC1, AC2): an unedited
// embedded default — current or older stamped, or stampless identical, markdown
// or TOML, task or not — is not in the bundle; edited and authored files are, byte
// for byte; and the two exclusion reasons are carried in distinct fields.
func TestConfigFilesLeavesUneditedDefaultsOut(t *testing.T) {
	repo := t.TempDir()
	uneditedTree(t, repo)

	b, err := ConfigFiles(syncAllPersonal(), repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	if got := bundlePaths(b.Files); !reflect.DeepEqual(got, keptPaths) {
		t.Errorf("Files = %v, want %v", got, keptPaths)
	}
	if !reflect.DeepEqual(b.UneditedDefaults, uneditedPaths) {
		t.Errorf("UneditedDefaults = %v, want %v", b.UneditedDefaults, uneditedPaths)
	}
	if want := []string{"skills/secret.local.md"}; !reflect.DeepEqual(b.Withheld, want) {
		t.Errorf("Withheld = %v, want %v (the .local list stays its own)", b.Withheld, want)
	}
	if n := b.ExcludedCount(); n != len(uneditedPaths)+1 {
		t.Errorf("ExcludedCount = %d, want %d", n, len(uneditedPaths)+1)
	}
	for _, f := range b.Files {
		if f.Path == "skills/satelle-workflow-advisor.md" && string(f.Content) == "" {
			t.Errorf("edited seed pushed with no content")
		}
	}
}

// TestAreaFilesLeavesUneditedDefaultsOut: the local side of a pull comparison is
// the same view a push has, so the two cannot disagree (sty_097833e8 AC1, AC6).
func TestAreaFilesLeavesUneditedDefaultsOut(t *testing.T) {
	repo := t.TempDir()
	uneditedTree(t, repo)

	files, err := AreaFiles(syncAllPersonal(), repo, "skills")
	if err != nil {
		t.Fatalf("AreaFiles: %v", err)
	}
	want := []string{"skills/my-own-skill.md", "skills/satelle-docs-only-check.md", "skills/satelle-workflow-advisor.md"}
	if got := bundlePaths(files); !reflect.DeepEqual(got, want) {
		t.Errorf("AreaFiles(skills) = %v, want %v", got, want)
	}
}

// TestUneditedSeedPathsCoversTasks (AC4): the one enumeration behind prune and
// migrate lists unedited task copies too, and never an edited or authored file.
func TestUneditedSeedPathsCoversTasks(t *testing.T) {
	repo := t.TempDir()
	uneditedTree(t, repo)
	// An edited task and an authored task beside the unedited one.
	audit := embeddedBody(t, "tasks", "tsk_context-audit")
	writeFile(t, repo, ".satelle/tasks/tsk_context-audit.md", ApplyStamp(audit+"\nmine\n", SHA(audit)))
	writeFile(t, repo, ".satelle/tasks/tsk_mine.md", "---\nid: tsk_mine\ntype: task\n---\n# mine\n")

	got := UneditedSeedPaths(repo + "/.satelle")
	if !reflect.DeepEqual(got, uneditedPaths) {
		t.Errorf("UneditedSeedPaths = %v, want %v", got, uneditedPaths)
	}
}

// TestUneditedDefaultClassification pins the rule at the predicate.
func TestUneditedDefaultClassification(t *testing.T) {
	body := embeddedBody(t, "skills", "satelle-step-summary")
	cases := []struct {
		name string
		rel  string
		in   string
		want bool
	}{
		{"stampless identical", "skills/satelle-step-summary.md", body, true},
		{"stamped current", "skills/satelle-step-summary.md", stampedCopy(body), true},
		{"stamped older", "skills/satelle-step-summary.md", stampedCopy(body + "old\n"), true},
		{"stampless edited", "skills/satelle-step-summary.md", body + "edit\n", false},
		{"stamp mismatch", "skills/satelle-step-summary.md", ApplyStamp(body+"edit\n", SHA(body)), false},
		{"authored name, no stamp", "skills/other.md", body, false},
		{"no frontmatter", "skills/other.md", "plain\n", false},
	}
	for _, c := range cases {
		if got := UneditedDefault(c.rel, []byte(c.in)); got != c.want {
			t.Errorf("%s: UneditedDefault = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestConvergingASeedIsNotAnEdit (AC8): moving an unedited seed from embedded
// body A to body B — what an upgrade does — leaves the bundle, and so what a push
// would send, unchanged.
func TestConvergingASeedIsNotAnEdit(t *testing.T) {
	repo := t.TempDir()
	uneditedTree(t, repo)
	before, err := ConfigFiles(syncAllPersonal(), repo)
	if err != nil {
		t.Fatal(err)
	}

	// Converge the older-stamped principle to the current embedded body.
	goals := embeddedBody(t, "principles", "satelle-agent-goals")
	writeFile(t, repo, ".satelle/principles/satelle-agent-goals.md", stampedCopy(goals))

	after, err := ConfigFiles(syncAllPersonal(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bundlePaths(before.Files), bundlePaths(after.Files)) {
		t.Errorf("converging a seed changed the pushed set: %v -> %v", bundlePaths(before.Files), bundlePaths(after.Files))
	}
	for i := range before.Files {
		if string(before.Files[i].Content) != string(after.Files[i].Content) {
			t.Errorf("converging a seed changed %s", before.Files[i].Path)
		}
	}
	if !reflect.DeepEqual(before.UneditedDefaults, after.UneditedDefaults) {
		t.Errorf("seed set changed: %v -> %v", before.UneditedDefaults, after.UneditedDefaults)
	}
}
