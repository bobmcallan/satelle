//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setExemptGate overwrites repo's satelle.toml [gate] table with the given
// prefix list and glob list. An empty glob slice writes edit_exempt_globs = []
// (deliberate opt-out), which is the AC1 "pattern removed" half.
func setExemptGate(t *testing.T, repo string, prefixes, globs []string) {
	t.Helper()
	quote := func(items []string) string {
		quoted := make([]string, 0, len(items))
		for _, p := range items {
			quoted = append(quoted, `"`+p+`"`)
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	}
	body := "[gate]\nedit_exempt_paths = " + quote(prefixes) + "\nedit_exempt_globs = " + quote(globs) + "\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write satelle.toml: %v", err)
	}
}

// gateEventOutput runs `satelle hook gate` and returns allow (exit 0) plus
// combined stdout/stderr so a deny-text assertion can read the reason.
func gateEventOutput(t *testing.T, repo, filePath string) (allowed bool, out string) {
	t.Helper()
	cmd := exec.Command(testBin, "hook", "gate")
	cmd.Dir = repo
	cmd.Env = isolatedEnv(t)
	cmd.Stdin = strings.NewReader(`{"tool_input":{"file_path":"` + filePath + `"}}`)
	raw, err := cmd.CombinedOutput()
	return err == nil, string(raw)
}

// TestHookGateDumpExemptConfigDecides (sty_fefc88cd AC1/AC4/AC5): seeded
// dump-name globs allow sty_*_body.md / sty_*_ac.md with no performing story;
// emptying the authored list denies the same Write and names the sanctioned
// locations; product paths stay gated either way.
func TestHookGateDumpExemptConfigDecides(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	body := filepath.Join(repo, "sty_abc123_body.md")
	ac := filepath.Join(repo, "sty_abc123_ac.md")
	code := filepath.Join(repo, "internal", "cli", "x.go")

	if !gateEvent(t, repo, body) {
		t.Error("edit gate blocked sty_*_body.md under the seeded globs (should be exempt)")
	}
	if !gateEvent(t, repo, ac) {
		t.Error("edit gate blocked sty_*_ac.md under the seeded globs (should be exempt)")
	}
	if gateEvent(t, repo, code) {
		t.Error("edit gate allowed a product-path edit with no engaged story")
	}

	setExemptGate(t, repo,
		[]string{".satelle/", ".gitignore", ".claude/", ".grok/", ".codex/"},
		[]string{})

	allowed, out := gateEventOutput(t, repo, body)
	if allowed {
		t.Error("edit gate allowed sty_*_body.md after edit_exempt_globs was emptied (config decides)")
	}
	for _, banned := range []string{"sty_*_body.md", "sty_*_ac.md"} {
		if strings.Contains(out, banned) {
			t.Errorf("deny text must not promise emptied glob %q:\n%s", banned, out)
		}
	}
	if gateEvent(t, repo, ac) {
		t.Error("edit gate allowed sty_*_ac.md after edit_exempt_globs was emptied")
	}
	if gateEvent(t, repo, code) {
		t.Error("edit gate allowed a product-path edit after emptying globs")
	}
}
