package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

func TestRemoteCreateThenValidate(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)

	out, err := runRoot(t, "remote", "create", "edge")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	if !strings.Contains(out, "remote edge: valid") {
		t.Errorf("create must validate its own scaffold, got:\n%s", out)
	}
	dir := filepath.Join(repo, ".satelle", filepath.FromSlash(config.RemotesDir), "edge")
	for _, rel := range []string{
		"remote.toml", "workflows/done.toml", "workflows/step.toml", "workflows/agents.toml",
		"skills/remote-work.md", "principles/remote-conduct.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("scaffold is missing %s: %v", rel, err)
		}
	}
	toml, _ := os.ReadFile(filepath.Join(dir, "remote.toml"))
	for _, key := range []string{"satelle_version", "[host]", "start =", "run =", "stop =", "[identity]", "label =", "[[segment]]", "[[trigger]]", "secrets =", "wake_key ="} {
		if !strings.Contains(string(toml), key) {
			t.Errorf("remote.toml lacks %s", key)
		}
	}
	agents, _ := os.ReadFile(filepath.Join(dir, "workflows", "agents.toml"))
	if strings.Contains(string(agents), "profile") || strings.Contains(string(agents), `command = "/`) {
		t.Errorf("the scaffold binding must carry no profile and no absolute path:\n%s", agents)
	}

	if out, err := runRoot(t, "remote", "validate", "edge"); err != nil || !strings.Contains(out, "remote edge: valid") {
		t.Errorf("validate edge: %v\n%s", err, out)
	}
	if _, err := runRoot(t, "remote", "create", "edge"); err == nil {
		t.Error("create must refuse a name that already exists")
	}
	if _, err := runRoot(t, "remote", "create", "../escape"); err == nil {
		t.Error("create must refuse a name that is not a plain directory name")
	}
	if _, err := runRoot(t, "remote", "validate", "ghost"); err == nil {
		t.Error("validate must refuse a remote that does not exist")
	}
}

func TestRemoteValidateReportsEveryErrorAndExitsNonZero(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	if _, err := runRoot(t, "remote", "create", "edge"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".satelle", filepath.FromSlash(config.RemotesDir), "edge", "remote.toml")
	b, _ := os.ReadFile(path)
	broken := strings.Replace(string(b), `label = "your-host-label"`, `label = ""`, 1)
	broken = strings.Replace(broken, `exit = "done"`, `exit = "nowhere"`, 1)
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "remote", "validate")
	if err == nil {
		t.Fatalf("validate must exit non-zero:\n%s", out)
	}
	for _, want := range []string{"remote.toml: identity.label", "remote.toml: segment[0].exit"} {
		if !strings.Contains(out, want) {
			t.Errorf("output must name %q:\n%s", want, out)
		}
	}
}
