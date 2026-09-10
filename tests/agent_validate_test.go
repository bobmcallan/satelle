//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentValidateHealthyAndBroken proves `satelle agent validate` (sty_93eec36d):
// exit 0 + grant surface on a healthy init; non-zero + actionable name on a
// broken agents.toml binding.
func TestAgentValidateHealthyAndBroken(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "agent", "validate")
	if !strings.Contains(out, "GRANT [executor]") || !strings.Contains(out, "GRANT [reviewer]") {
		t.Errorf("validate should surface grants:\n%s", out)
	}
	if !strings.Contains(out, "PASS  agent validate green") {
		t.Errorf("healthy repo should pass:\n%s", out)
	}

	// Break the reviewer command with a known-invalid preset (force-write —
	// the scaffold embeds a full command template and comments that mention
	// `command = "claude"`, so a naive replace is unreliable).
	agents := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	writeFile(t, agents, "[executor]\ncommand = \"in-loop\"\n\n[reviewer]\ncommand = \"not-a-real-cli\"\n")

	out, err := run(t, testBin, repo, "agent", "validate")
	if err == nil {
		t.Fatalf("broken agents.toml must fail validate:\n%s", out)
	}
	if !strings.Contains(out, "not-a-real") && !strings.Contains(out, "FAIL") {
		t.Errorf("failure should name the broken command:\n%s", out)
	}
}

// reworkValidateFixture writes a minimal derived route whose coded step declares
// rework = { consult = "consultant", rounds = 3 }, plus a live [coder]. The
// consultant binding is left for the caller to append (or omit).
func reworkValidateFixture(t *testing.T) (repo, agentsPath string) {
	t.Helper()
	repo = t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"),
		"[meta]\nname = \"done\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"rework validate fixture\"\n\n"+
			"[\"*\"]\nobligations = [\"raised\", \"coded\", \"closed\"]\n")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.toml"),
		"[meta]\nname = \"step\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"rework validate fixture\"\n\n"+
			"[raised]\nstatus = \"backlog\"\nstart = true\n\n"+
			"[coded]\nstatus = \"in_progress\"\nagent = \"coder\"\nrequires = [\"raised\"]\n"+
			"rework = { consult = \"consultant\", rounds = 3 }\n\n"+
			"[closed]\nstatus = \"done\"\nterminal = true\nrequires = [\"coded\"]\n")
	agentsPath = filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	f, err := os.OpenFile(agentsPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(
		"\n[coder]\nrole = \"agent\"\ninterface = \"stream\"\n" +
			"command = \"claude -p --input-format stream-json --output-format stream-json --verbose --allowedTools {tools} --model {model}\"\n" +
			"tools = \"Read,Grep,Glob,Edit,Write,Bash(satelle:*)\"\nmodel = \"opus\"\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return repo, agentsPath
}

// TestAgentValidateReworkConsultWarnings pins sty_cec967b5 AC4 through the
// built binary: missing and non-live consult bindings WARN (healthy exit);
// a live-capable consult produces no rework warning; doctor shares the finding.
func TestAgentValidateReworkConsultWarnings(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		repo, _ := reworkValidateFixture(t)
		mustRun(t, testBin, repo, "reindex")
		out := mustRun(t, testBin, repo, "agent", "validate")
		if !strings.Contains(out, "PASS  agent validate green") {
			t.Fatalf("missing consult must stay healthy (advisory WARN, never refuse):\n%s", out)
		}
		for _, want := range []string{"WARN", "rework consult=consultant", "rounds=3", "no [consultant] binding"} {
			if !strings.Contains(out, want) {
				t.Errorf("agent validate missing %q:\n%s", want, out)
			}
		}
		doctor := mustRun(t, testBin, repo, "doctor")
		for _, want := range []string{"rework consult=consultant", "no [consultant] binding"} {
			if !strings.Contains(doctor, want) {
				t.Errorf("doctor must show the same finding (%q):\n%s", want, doctor)
			}
		}
	})

	t.Run("interface=command", func(t *testing.T) {
		repo, agents := reworkValidateFixture(t)
		f, err := os.OpenFile(agents, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(
			"\n[consultant]\nrole = \"reviewer\"\ninterface = \"command\"\n" +
				"command = \"claude -p --append-system-prompt {system} --output-format json --allowedTools {tools} --model {model}\"\n" +
				"tools = \"Read,Grep,Glob\"\nmodel = \"opus\"\n"); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		mustRun(t, testBin, repo, "reindex")
		out := mustRun(t, testBin, repo, "agent", "validate")
		if !strings.Contains(out, "PASS  agent validate green") {
			t.Fatalf("non-live consult must WARN, never refuse:\n%s", out)
		}
		for _, want := range []string{"WARN", "rework consult=consultant", "rounds=3", "cannot open a session"} {
			if !strings.Contains(out, want) {
				t.Errorf("agent validate missing %q:\n%s", want, out)
			}
		}
	})

	for _, iface := range []string{"stream", "acp"} {
		iface := iface
		t.Run("live="+iface, func(t *testing.T) {
			repo, agents := reworkValidateFixture(t)
			cmd := "claude -p --input-format stream-json --output-format stream-json --verbose --allowedTools {tools} --model {model}"
			if iface == "acp" {
				cmd = "grok agent stdio"
			}
			f, err := os.OpenFile(agents, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(
				"\n[consultant]\nrole = \"reviewer\"\ninterface = \"" + iface + "\"\n" +
					"command = \"" + cmd + "\"\n" +
					"tools = \"Read,Grep,Glob\"\nmodel = \"opus\"\n"); err != nil {
				t.Fatal(err)
			}
			_ = f.Close()
			mustRun(t, testBin, repo, "reindex")
			out := mustRun(t, testBin, repo, "agent", "validate")
			if strings.Contains(out, "rework consult=") {
				t.Errorf("live-capable consult must not warn about rework:\n%s", out)
			}
			if strings.Contains(out, "[consultant] is orphaned") {
				t.Errorf("rework consult must count as an allocation:\n%s", out)
			}
		})
	}
}
