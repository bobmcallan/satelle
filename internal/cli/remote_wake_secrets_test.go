//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

const wakeSecretTables = `
[[secret]]
key = "CLAUDE_CODE_OAUTH_TOKEN"
scope = "personal"
from = "env"

[[secret]]
key = "TEAM_KEY"
scope = "team"
workspace = "` + secretWS + `"
from = "env"
`

// wakeEnvRepo is remote "edge" whose story command dumps the host process's
// environment to $MARK_DIR/env-<story>; secrets adds [[secret]] and identity.key.
func wakeEnvRepo(t *testing.T, secrets bool, topLevel string) (mark string, story string) {
	t.Helper()
	repo := remoteWithTriggers(t)
	mark = t.TempDir()
	t.Setenv("SATELLE_TEST_MARK", mark)
	declareStoryHost(t, repo, "env > $MARK_DIR/env-{story}")
	if topLevel != "" {
		editRemote(t, repo, `wake_key = "REMOTE_WAKE_KEY"`, `wake_key = "REMOTE_WAKE_KEY"`+"\n"+topLevel)
	}
	if secrets {
		editRemote(t, repo, `label = "your-host-label"`, "label = \"your-host-label\"\nkey = \"SATELLE_REMOTE_KEY\"")
		p := filepath.Join(remoteDir(repo, "edge"), "remote.toml")
		b, _ := os.ReadFile(p)
		if err := os.WriteFile(p, append(b, []byte(wakeSecretTables)...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return mark, newStory(t, "backlog")
}

func readEnvDump(t *testing.T, mark, story string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(mark, "env-"+story))
	if err != nil {
		t.Fatalf("the story command did not run: %v", err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	return env
}

// AC4: a managed wake carries the fire's trigger_id as the server run id and the
// request's remote_id, plus the names a node reads its secrets by; the wake makes
// no run of its own (it makes no request at all).
func TestRemoteWakeCarriesTheRunAndTheSecretNames(t *testing.T) {
	mark, story := wakeEnvRepo(t, true, "")
	out, err := runRootIn(t, `{"trigger_id":"trg_fire77","remote":"edge","remote_id":"rmt_srv"}`, "remote", "wake", "edge")
	if err != nil {
		t.Fatalf("wake: %v\n%s", err, out)
	}
	env := readEnvDump(t, mark, story)
	for k, want := range map[string]string{
		config.RemoteServerRunEnv: "trg_fire77",
		config.RemoteIDEnv:        "rmt_srv",
		config.RemoteIdentityEnv:  "key:SATELLE_REMOTE_KEY",
		config.RemoteSecretsEnv:   "CLAUDE_CODE_OAUTH_TOKEN:personal,TEAM_KEY:team:" + secretWS,
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	if env[config.RemoteServerEnv] == "" {
		t.Error("the satelle.dev server must be carried")
	}
	if !strings.HasPrefix(env[config.RemoteRunEnv], "run_") || env[config.RemoteRunEnv] == env[config.RemoteServerRunEnv] {
		t.Errorf("SATELLE_RUN_ID must stay the local run id, got %q", env[config.RemoteRunEnv])
	}
	for k, v := range env {
		if strings.Contains(v, "sk-ant") {
			t.Errorf("%s carries a secret value", k)
		}
	}
}

// A wake for another remote than the definition's own is refused, and the host
// never runs.
func TestRemoteWakeRefusesAMismatchedRemoteID(t *testing.T) {
	mark, story := wakeEnvRepo(t, true, `remote_id = "rmt_def"`)
	out, err := runRootIn(t, `{"trigger_id":"trg_1","remote":"edge","remote_id":"rmt_other"}`, "remote", "wake", "edge")
	if err == nil || !strings.Contains(err.Error(), "wake for rmt_other does not match remote_id rmt_def of remote edge") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if _, serr := os.Stat(filepath.Join(mark, "env-"+story)); serr == nil {
		t.Error("the host ran for a wake that was refused")
	}
	// The matching id passes.
	mark2, story2 := wakeEnvRepo(t, true, `remote_id = "rmt_def"`)
	if out, err := runRootIn(t, `{"trigger_id":"trg_1","remote":"edge","remote_id":"rmt_def"}`, "remote", "wake", "edge"); err != nil {
		t.Fatalf("a matching wake: %v\n%s", err, out)
	}
	if readEnvDump(t, mark2, story2)[config.RemoteIDEnv] != "rmt_def" {
		t.Error("SATELLE_REMOTE_ID missing on the matching wake")
	}
}

// A remote with no [[secret]] exports none of it.
func TestRemoteWakeWithoutSecretsExportsNoCarrier(t *testing.T) {
	mark, story := wakeEnvRepo(t, false, "")
	if out, err := runRootIn(t, `{"trigger_id":"trg_1","remote":"edge","remote_id":"rmt_srv"}`, "remote", "wake", "edge"); err != nil {
		t.Fatalf("wake: %v\n%s", err, out)
	}
	env := readEnvDump(t, mark, story)
	for _, k := range []string{config.RemoteServerRunEnv, config.RemoteIDEnv, config.RemoteServerEnv, config.RemoteSecretsEnv, config.RemoteIdentityEnv} {
		if _, ok := env[k]; ok {
			t.Errorf("%s was exported by a remote with no [[secret]]", k)
		}
	}
}
