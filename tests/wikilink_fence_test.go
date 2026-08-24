//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPrincipleValidateIgnoresFencedWikilinks proves the user flow: a substrate
// file that QUOTES TOML array-of-tables syntax inside a fenced code block is not
// a dangling wikilink, while a genuine prose reference to a missing document
// still fails. Both halves run against the same seeded repo so the exemption is
// shown to be narrow, not a blinding of the check.
func TestPrincipleValidateIgnoresFencedWikilinks(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	principle := filepath.Join(repo, ".satelle", "principles", "fence-quoting.md")
	clean := "---\n" +
		"name: fence-quoting\n" +
		"type: principle\n" +
		"tags: [type:principle]\n" +
		"applies_to: [\"*\"]\n" +
		"description: A principle whose body quotes TOML array-of-tables syntax in a fenced block.\n" +
		"---\n" +
		"\n" +
		"# Fence quoting\n" +
		"\n" +
		"Allocation is authored as TOML:\n" +
		"\n" +
		"```toml\n" +
		"[[gate]]\n" +
		"skill = \"some-review\"\n" +
		"```\n"
	writeFile(t, principle, clean)

	out, err := run(t, testBin, repo, "principle", "validate")
	if err != nil {
		t.Fatalf("validate should pass with TOML [[gate]] inside a fence: %v\n%s", err, out)
	}
	if strings.Contains(out, "dangling wikilink") {
		t.Fatalf("no dangling wikilink should be reported:\n%s", out)
	}

	// Negative half: a real prose reference to a name nothing provides must
	// still fail, naming the target.
	writeFile(t, principle, clean+"\nProse reference: [[definitely-not-a-real-doc]].\n")

	out, err = run(t, testBin, repo, "principle", "validate")
	if err == nil {
		t.Fatalf("validate should fail on a genuine dangling wikilink:\n%s", out)
	}
	if !strings.Contains(out, "definitely-not-a-real-doc") {
		t.Fatalf("the failure should name the dangling target:\n%s", out)
	}
}
