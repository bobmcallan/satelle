//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateSkillEvidence drives the real binary (sty_23ceeca2 AC3): a step
// skill that declares no evidence is warned about, naming the consequence; a
// skill that declares evidence passes without the warning; a malformed
// declaration fails `satelle skill validate` with the key named.
func TestValidateSkillEvidence(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// The shipped route names no step skill, so the repo's step catalogue overlays
	// the `coded` step with one.
	stepCatalogue := "[meta]\nname = \"step\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"a step catalogue\"\n\n" +
		"[coded]\nstatus = \"in_progress\"\nagent = \"executor\"\nskills = [\"my-step\"]\nrequires = [\"raised\"]\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "workflows", "step.toml"), []byte(stepCatalogue), 0o644); err != nil {
		t.Fatal(err)
	}
	write := func(fm string) {
		t.Helper()
		body := "---\nname: my-step\ntype: skill\ndescription: a step skill\n" + fm + "---\n\n# Step skill\n\nDo the step carefully.\n"
		if err := os.WriteFile(filepath.Join(repo, ".satelle", "skills", "my-step.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		mustRun(t, testBin, repo, "reindex", "--validate=false")
	}

	write("")
	out := mustRun(t, testBin, repo, "skill", "validate", "my-step")
	if !strings.Contains(out, "WARN  skills/my-step — step skill declares no evidence") || !strings.Contains(out, "only its delivery is checked") {
		t.Errorf("a step skill with no evidence_* should draw a warning naming the consequence:\n%s", out)
	}

	write("evidence_sections: Premise check\nevidence_commands: go test\n")
	out = mustRun(t, testBin, repo, "skill", "validate", "my-step")
	if !strings.Contains(out, "PASS  skills/my-step") || strings.Contains(out, "declares no evidence") {
		t.Errorf("a skill declaring evidence should pass with no warning:\n%s", out)
	}

	write("evidence_command: go test\n")
	out, err := run(t, testBin, repo, "skill", "validate", "my-step")
	if err == nil || !strings.Contains(out, "FAIL") || !strings.Contains(out, "evidence_command") {
		t.Errorf("a malformed declaration should fail naming the key (err=%v):\n%s", err, out)
	}
}
