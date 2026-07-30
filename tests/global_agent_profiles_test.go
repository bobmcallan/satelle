//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCatalog writes a machine-wide profile catalog into this test's isolated
// SATELLE_HOME — the same home every `satelle` invocation in this test resolves.
func writeCatalog(t *testing.T, body string) {
	t.Helper()
	writeFile(t, filepath.Join(isolatedHome(t), "agents.toml"), body)
}

const profileCatalog = `
[vars]
CATALOG_TOKEN = "sk-catalog-secret"

[profiles.shared-reviewer]
role       = "reviewer"
interface  = "command"
command    = "claude -p --output-format json --disallowedTools Write,Edit,NotebookEdit,Bash --append-system-prompt {system} --allowedTools {tools} --model {model}"
tools      = "Read,Grep,Glob"
model      = "catalog-model"
effort     = "high"
principles = "session"
env        = { ANTHROPIC_AUTH_TOKEN = "${CATALOG_TOKEN}" }
`

// TestGlobalProfileConsumedByRepo proves the whole seam through the real binary
// (sty_c7dfeedf): a repo that explicitly references a machine-wide profile
// inherits its execution fields, its own inline values still win, and
// `agent validate` reports each effective field with its source tier.
func TestGlobalProfileConsumedByRepo(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "reindex")
	writeCatalog(t, profileCatalog)

	// The catalog is visible on its own surface, without any repo reference.
	out := mustRun(t, testBin, repo, "agent", "profiles")
	if !strings.Contains(out, "shared-reviewer") || !strings.Contains(out, "ANTHROPIC_AUTH_TOKEN") {
		t.Errorf("agent profiles should list the catalog:\n%s", out)
	}
	if strings.Contains(out, "sk-catalog-secret") {
		t.Fatalf("a var VALUE must never be printed:\n%s", out)
	}

	// Reference it, pinning one field inline.
	agents := filepath.Join(repo, ".satelle", "agents.toml")
	writeFile(t, agents, "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n[reviewer]\nprofile = \"shared-reviewer\"\nmodel = \"repo-pinned\"\n")

	out = mustRun(t, testBin, repo, "agent", "validate")
	if !strings.Contains(out, `model="repo-pinned"`) {
		t.Errorf("the repo's inline model must win over the profile's:\n%s", out)
	}
	if !strings.Contains(out, `tools="Read,Grep,Glob"`) {
		t.Errorf("the profile's tools must be inherited:\n%s", out)
	}
	for _, want := range []string{
		`source: command = "claude -p`,
		"(profile:shared-reviewer)",
		`source: model = "repo-pinned" (repo)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("validate should report provenance %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sk-catalog-secret") {
		t.Fatalf("a resolved secret must never reach validate output:\n%s", out)
	}
	// The secret stays on the machine: nothing in the repo gained the literal.
	assertRepoSecretFree(t, repo, "sk-catalog-secret")
}

// TestUnreferencedCatalogNeverChangesARepo is the guarantee that makes a shared
// catalog safe on a machine holding pinned repositories: a profile named exactly
// like a binding, plus a role default for that role, leaves a repo that never
// asked for either completely unchanged.
func TestUnreferencedCatalogNeverChangesARepo(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "reindex")

	before := mustRun(t, testBin, repo, "agent", "validate")

	writeCatalog(t, `
[profiles.reviewer]
role    = "reviewer"
command = "hijacked -p {system}"
model   = "hijack-model"
tools   = "Read"

[roles]
reviewer = "reviewer"
`)
	after := mustRun(t, testBin, repo, "agent", "validate")

	// The catalog IS listed (it exists on this machine) — but nothing it defines
	// may appear in a resolved binding, and the GRANT lines must be identical.
	if strings.Contains(grantLines(after), "hijack") {
		t.Fatalf("an unreferenced catalog must not reach the repo's bindings:\n%s", after)
	}
	if grantLines(before) != grantLines(after) {
		t.Errorf("grants changed without any repo reference:\n before:\n%s\n after:\n%s",
			grantLines(before), grantLines(after))
	}
}

// TestGlobalCatalogCannotSupplyWorkflows proves the repo-agnostic boundary at
// the surface that would notice: process substrate dropped into the machine-wide
// home is invisible, and a profile that tries to carry workflow policy refuses
// the whole load rather than being partially honoured.
func TestGlobalCatalogCannotSupplyWorkflows(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "reindex")
	before := mustRun(t, testBin, repo, "workflow", "list")

	home := isolatedHome(t)
	for _, kind := range []string{"workflows", "skills", "principles"} {
		dir := filepath.Join(home, kind)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "hijack.md"),
			"---\nname: hijack\napplies_to: [\"*\"]\n---\n\n```dot\ndigraph h {\n  backlog [shape=Mdiamond]\n  done [shape=Msquare]\n  backlog -> done\n}\n```\n")
	}
	writeCatalog(t, "[profiles.p]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\n")

	after := mustRun(t, testBin, repo, "workflow", "list")
	if before != after {
		t.Errorf("workflow selection must not vary with the machine-wide home:\n before:\n%s\n after:\n%s", before, after)
	}
	if strings.Contains(after, "hijack") {
		t.Fatalf("a global-home workflow must never be selectable:\n%s", after)
	}

	// A profile carrying workflow policy fails the load loudly.
	writeCatalog(t, "[profiles.p]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\napplies_to = [\"*\"]\n")
	out, err := run(t, testBin, repo, "agent", "profiles")
	if err == nil {
		t.Fatalf("a policy-carrying profile must refuse:\n%s", out)
	}
	if !strings.Contains(out, "applies_to") {
		t.Errorf("the refusal should name the offending key:\n%s", out)
	}
}

// TestAgentMigrateSeedsCatalogWithoutTouchingTheRepo proves the migration path
// end to end: opt-in, idempotent, machine-side only.
func TestAgentMigrateSeedsCatalogWithoutTouchingTheRepo(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "reindex")

	agents := filepath.Join(repo, ".satelle", "agents.toml")
	before, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}

	out := mustRun(t, testBin, repo, "agent", "migrate")
	if !strings.Contains(out, "agents.toml") {
		t.Errorf("migrate should name the file it wrote:\n%s", out)
	}
	catalog, rerr := os.ReadFile(filepath.Join(isolatedHome(t), "agents.toml"))
	if rerr != nil {
		t.Fatalf("migrate must seed the catalog: %v", rerr)
	}
	// The seeded catalog loads and changes nothing until a repo references it.
	if _, verr := run(t, testBin, repo, "agent", "profiles"); verr != nil {
		t.Errorf("the seeded catalog must load: %v", verr)
	}
	mustRun(t, testBin, repo, "agent", "validate")

	after, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("migrate must not write into a repository")
	}

	out = mustRun(t, testBin, repo, "agent", "migrate")
	if !strings.Contains(out, "never overwrites") {
		t.Errorf("a second migrate must refuse to overwrite:\n%s", out)
	}
	again, _ := os.ReadFile(filepath.Join(isolatedHome(t), "agents.toml"))
	if string(catalog) != string(again) {
		t.Error("an existing catalog must survive a second migrate byte-for-byte")
	}
}

// grantLines extracts only the GRANT lines from validate output, so a comparison
// ignores the new catalog listing while still pinning every resolved binding.
func grantLines(out string) string {
	var keep []string
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "GRANT [") {
			keep = append(keep, strings.TrimSpace(ln))
		}
	}
	return strings.Join(keep, "\n")
}

// assertRepoSecretFree fails when any file under the repo carries the literal.
func assertRepoSecretFree(t *testing.T, repo, secret string) {
	t.Helper()
	err := filepath.Walk(repo, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil //nolint:nilerr // unreadable entries are not evidence of a leak
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if strings.Contains(string(b), secret) {
			t.Errorf("secret material leaked into the repo at %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
