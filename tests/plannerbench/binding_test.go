//go:build plannerbench

package plannerbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AC11: the study must measure the tool policy the planner actually SHIPS with,
// and any divergence must be an explicit sample variable that never wears the
// shipped policy's name.

func writeShippedAgents(t *testing.T, grant string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".satelle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[planner]\nrole = \"agent\"\neffort = \"high\"\n" +
		"command = \"claude -p --model {model} --allowedTools {tools}\"\n" +
		"tools = \"" + grant + "\"\nmodel = \"opus\"\n"
	path := filepath.Join(dir, "agents.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestShippedGrantIsReadFromTheRepoNotRestated(t *testing.T) {
	// The live file is the point of the check: the benchmark must track it.
	shipped, err := loadShippedGrant(filepath.Join("..", "..", ".satelle", "agents.toml"))
	if err != nil {
		t.Fatalf("read this repo's shipped planner grant: %v", err)
	}
	if strings.TrimSpace(shipped.Grant) == "" || shipped.SHA256 == "" {
		t.Fatalf("shipped grant not resolved: %+v", shipped)
	}
	if _, err := loadShippedGrant(filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Fatal("a non-agents.toml path must be refused rather than silently resolved")
	}
}

func TestDefaultBindingMirrorsTheShippedGrantVerbatim(t *testing.T) {
	shipped, err := loadShippedGrant(writeShippedAgents(t, "Read,Grep,Glob,Bash(satelle:*)"))
	if err != nil {
		t.Fatal(err)
	}
	s := study{Bindings: []studyBinding{{
		ID: "mirror", Provider: "p", Model: "m", Effort: "high",
		Command: "claude -p --model {model}",
	}}}
	resolved, err := resolveBindings(s, shipped.Grant)
	if err != nil {
		t.Fatal(err)
	}
	if resolved[0].grant != shipped.Grant {
		t.Fatalf("generated grant %q does not mirror the shipped grant %q", resolved[0].grant, shipped.Grant)
	}
	if resolved[0].policy != shippedPolicyName {
		t.Fatalf("a mirroring binding must carry the shipped policy name, got %q", resolved[0].policy)
	}
	if resolved[0].divergence != nil {
		t.Fatalf("a mirroring binding must record no divergence: %+v", resolved[0].divergence)
	}
	if !strings.Contains(resolved[0].agentsTOML(), "tools = \""+shipped.Grant+"\"") {
		t.Fatalf("rendered agents.toml does not carry the shipped grant:\n%s", resolved[0].agentsTOML())
	}
}

func TestDivergentBindingMustDeclareAPolicyNameAndAReason(t *testing.T) {
	const shipped = "Read,Grep,Glob,Bash(satelle:*)"
	base := studyBinding{
		ID: "diverge", Provider: "p", Model: "m", Effort: "high",
		Command: "grok agent stdio", Tools: "read_file,grep,list_dir",
	}
	t.Run("no policy name", func(t *testing.T) {
		_, err := resolveBindings(study{Bindings: []studyBinding{base}}, shipped)
		if err == nil || !strings.Contains(err.Error(), "must declare its own tool_policy name") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("reuses the shipped policy name", func(t *testing.T) {
		b := base
		b.ToolPolicy = shippedPolicyName
		b.Divergence = &toolDivergence{Reason: "provider-native names"}
		_, err := resolveBindings(study{Bindings: []studyBinding{b}}, shipped)
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("reuses the legacy read-only label", func(t *testing.T) {
		b := base
		b.ToolPolicy = legacyPolicyName
		b.Divergence = &toolDivergence{Reason: "provider-native names"}
		_, err := resolveBindings(study{Bindings: []studyBinding{b}}, shipped)
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("the previous harness's %q mislabel must be refused: %v", legacyPolicyName, err)
		}
	})
	t.Run("no reason", func(t *testing.T) {
		b := base
		b.ToolPolicy = "grok-native-read-only"
		_, err := resolveBindings(study{Bindings: []studyBinding{b}}, shipped)
		if err == nil || !strings.Contains(err.Error(), "tool_policy_divergence.reason") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("properly declared", func(t *testing.T) {
		b := base
		b.ToolPolicy = "grok-native-read-only"
		b.Divergence = &toolDivergence{Reason: "grok does not accept Claude tool names"}
		resolved, err := resolveBindings(study{Bindings: []studyBinding{b}}, shipped)
		if err != nil {
			t.Fatal(err)
		}
		got := resolved[0]
		if got.policy != "grok-native-read-only" || got.divergence == nil {
			t.Fatalf("divergence not surfaced: %+v", got)
		}
		if got.divergence.Shipped != shipped || got.divergence.Used != b.Tools {
			t.Fatalf("divergence does not record both grants: %+v", got.divergence)
		}
		// The divergence must reach the recorded dimensions, so report.go can
		// hold tool_policy constant and keep this binding out of a shipped-grant
		// comparison.
		dims := got.dims(study{ID: "s", ContextBuckets: []contextBucket{{Name: "small", MaxBytes: 0}}},
			"fixture", 10, 1, 1)
		if dims.ToolPolicy != "grok-native-read-only" || dims.ToolGrant != b.Tools {
			t.Fatalf("dims do not carry the divergence: %+v", dims)
		}
	})
}

func TestMirroringBindingMayNotBeMislabelled(t *testing.T) {
	const shipped = "Read,Grep,Glob"
	b := studyBinding{
		ID: "mirror", Provider: "p", Model: "m", Effort: "high",
		Command: "claude -p", Tools: shipped, ToolPolicy: "something-else",
	}
	if _, err := resolveBindings(study{Bindings: []studyBinding{b}}, shipped); err == nil ||
		!strings.Contains(err.Error(), shippedPolicyName) {
		t.Fatalf("a mirroring binding labelled otherwise must be refused: %v", err)
	}
	b.ToolPolicy = ""
	b.Divergence = &toolDivergence{Reason: "none"}
	if _, err := resolveBindings(study{Bindings: []studyBinding{b}}, shipped); err == nil ||
		!strings.Contains(err.Error(), "records a divergence") {
		t.Fatalf("a mirroring binding claiming a divergence must be refused: %v", err)
	}
}

func TestIsolatedBindingWithoutACommandIsRefused(t *testing.T) {
	b := studyBinding{ID: "no-command", Provider: "p", Model: "m", Effort: "high"}
	if _, err := resolveBindings(study{Bindings: []studyBinding{b}}, "Read"); err == nil ||
		!strings.Contains(err.Error(), "must declare a command") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnavailableReasonIsAFilesystemFactNotAnOutputScan(t *testing.T) {
	b := studyBinding{ID: "missing", Command: "definitely-not-a-real-binary-9f2c --flag", topology: topologyIsolated}
	if reason := b.unavailableReason(); !strings.Contains(reason, "not on PATH") {
		t.Fatalf("reason = %q", reason)
	}
	resolvable := studyBinding{ID: "sh", Command: "sh -c true", topology: topologyIsolated}
	if reason := resolvable.unavailableReason(); reason != "" {
		t.Fatalf("a resolvable binary must be available: %q", reason)
	}
	inLoop := studyBinding{ID: "inloop", topology: topologyInLoop}
	if reason := inLoop.unavailableReason(); !strings.Contains(reason, "ingested") {
		t.Fatalf("in-loop must never be spawned: %q", reason)
	}
}

func TestDefaultStudyLoadsAndResolvesAgainstTheShippedGrant(t *testing.T) {
	s, err := loadStudy("study.json")
	if err != nil {
		t.Fatalf("the shipped default study must load: %v", err)
	}
	shipped, err := loadShippedGrant(filepath.Join("..", "..", ".satelle", "agents.toml"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveBindings(s, shipped.Grant)
	if err != nil {
		t.Fatalf("the shipped default study must satisfy the divergence rules: %v", err)
	}
	mirroring := 0
	for _, b := range resolved {
		if b.policy == shippedPolicyName {
			mirroring++
			if b.grant != shipped.Grant {
				t.Errorf("binding %s wears the shipped policy name with a different grant", b.ID)
			}
		}
	}
	if mirroring == 0 {
		t.Fatal("the default study must contain at least one binding on the shipped grant")
	}
}
