package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

const wiringExecutor = "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n"

// piCoderBinding loads the repo's pi extension, the shape of the machine-wide
// pi-coder profile.
const piCoderBinding = "[coder]\nrole = \"agent\"\ncommand = \"pi -p --no-session --no-extensions -e " + piExtensionRel +
	" --append-system-prompt {system} --tools {tools} --model {model} {payload}\"\ntools = \"read,bash,edit,write\"\n"

// wiringRepo scaffolds a repo whose agents.toml is wiringExecutor+bindings, with
// a route allocating the coded step to [coder], and no .pi/.
func wiringRepo(t *testing.T, bindings string) string {
	t.Helper()
	repo := tempRepo(t)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir, "[\"*\"]\nobligations = [\"raised\", \"coded\", \"closed\"]\n",
		"[raised]\nstatus = \"backlog\"\nstart = true\n\n"+
			"[coded]\nstatus = \"in_progress\"\nagent = \"coder\"\nrequires = [\"raised\"]\n\n"+
			"[closed]\nstatus = \"done\"\nterminal = true\nrequires = [\"coded\"]\n")
	if err := os.WriteFile(filepath.Join(wfDir, config.AgentsConfigName), []byte(wiringExecutor+bindings), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(repo, ".satelle", config.AgentsConfigName))
	return repo
}

// sty_d15f6713 AC1: agent validate fails a pi binding whose -e extension is
// absent from the repo, naming the binding, the path and the fix. AC3: after
// `satelle agents install pi` the same binding passes.
func TestAgentValidateFailsAbsentPiExtension(t *testing.T) {
	repo := wiringRepo(t, piCoderBinding)
	t.Chdir(repo)
	out, err := runRoot(t, "agent", "validate")
	if err == nil {
		t.Fatalf("agent validate must fail while %s is absent:\n%s", piExtensionRel, out)
	}
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "FAIL") && strings.Contains(l, piExtensionRel) {
			line = l
		}
	}
	for _, want := range []string{"[coder]", piExtensionRel, "satelle agents install pi"} {
		if !strings.Contains(line, want) {
			t.Errorf("FAIL line must name %q, got %q:\n%s", want, line, out)
		}
	}

	if iout, err := runRoot(t, "agents", "install", "pi"); err != nil {
		t.Fatalf("agents install pi: %v\n%s", err, iout)
	}
	out, err = runRoot(t, "agent", "validate")
	if err != nil {
		t.Fatalf("agent validate must pass once the extension is installed: %v\n%s", err, out)
	}
	if strings.Contains(out, "satelle agents install pi") {
		t.Errorf("installed wiring must not be reported:\n%s", out)
	}
}

// sty_d15f6713 AC3: a pi seat loading only an absolute extension names no
// declared wiring path, so neither agent validate nor the push preflight reports
// it; the push preflight does report the coder that loads the absent scaffold.
func TestAgentValidateIgnoresBindingNamingNoWiring(t *testing.T) {
	absExt := filepath.Join(t.TempDir(), "pi-cursor")
	if err := os.WriteFile(absExt, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewer := "[reviewer]\nrole = \"reviewer\"\ncommand = \"pi -p --no-session --no-extensions -e " + absExt +
		" --system-prompt {system} --tools {tools}\"\ntools = \"read,grep\"\n\n"
	repo := wiringRepo(t, reviewer+"[coder]\nrole = \"agent\"\ncommand = \"in-loop\"\n")
	t.Chdir(repo)
	out, err := runRoot(t, "agent", "validate")
	if err != nil || strings.Contains(out, "satelle agents install") {
		t.Fatalf("a binding naming no declared wiring must not fail: %v\n%s", err, out)
	}
	var buf bytes.Buffer
	validateSystem(&buf, filepath.Join(repo, ".satelle"), true)
	if strings.Contains(buf.String(), health.IDAgentsWiringAbsent) {
		t.Errorf("push preflight must not report a binding naming no declared wiring:\n%s", buf.String())
	}

	repo = wiringRepo(t, reviewer+piCoderBinding)
	buf.Reset()
	validateSystem(&buf, filepath.Join(repo, ".satelle"), true)
	if !strings.Contains(buf.String(), health.IDAgentsWiringAbsent) {
		t.Errorf("push preflight must report the coder loading the absent scaffold:\n%s", buf.String())
	}
}
