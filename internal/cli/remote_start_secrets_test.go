//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/remotenode/devtest"
)

// secretRig is the remote start rig with a declared secret: the owner's calls go
// to a stub satelle.dev (devStub), the node's signed reads to a verifying stub
// mounted on the relay's host, and the agent the node runs dumps its environment.
type secretRig struct {
	*remoteRig
	owner    *devStub
	reads    *devtest.Dev
	agentEnv string
}

func newSecretRig(t *testing.T, remoteID string) *secretRig {
	t.Helper()
	r := newRemoteRig(t, "shutdown", "fake-agent-env --system {system} --tools {tools}")
	s := &secretRig{remoteRig: r, owner: newDevStub(t), reads: devtest.NewHandler()}
	r.relay.Mount(s.reads.Handler())

	bin := t.TempDir()
	s.agentEnv = filepath.Join(t.TempDir(), "agent-env")
	writeExec(t, filepath.Join(bin, "fake-agent-env"), "#!/bin/sh\nenv > \"$AGENT_ENV_OUT\"\ncat\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SATELLE_TEST_REMOTE_KEY", devtest.SeedB64)

	prev := secretHostedClient
	secretHostedClient = func(string) *hosted.Client {
		return hosted.NewClient(s.owner.ts.URL, &e2eStore{cred: hosted.Credential{ServerURL: s.owner.ts.URL, AccessToken: "good"}}, s.owner.ts.Client())
	}
	t.Cleanup(func() { secretHostedClient = prev })

	if remoteID != "" {
		editRemote(t, r.repo, `wake_key = "REMOTE_WAKE_KEY"`, "wake_key = \"REMOTE_WAKE_KEY\"\nremote_id = \""+remoteID+"\"")
	}
	editRemote(t, r.repo, `label = "edge-1"`, "label = \"edge-1\"\nkey = \"SATELLE_REMOTE_KEY\"")
	editRemote(t, r.repo, "[host.env]\n", "[host.env]\nSATELLE_REMOTE_KEY = \"${SATELLE_TEST_REMOTE_KEY}\"\nAGENT_ENV_OUT = \""+s.agentEnv+"\"\n")
	p := filepath.Join(remoteDir(r.repo, "edge"), "remote.toml")
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, append(b, []byte("\n[[secret]]\nkey = \"CLAUDE_CODE_OAUTH_TOKEN\"\nscope = \"personal\"\nfrom = \"env\"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	return s
}

// AC4, start path: the owner registers one run for the definition's remote_id; the
// node, told that run in its sealed Setup, reads the secret signed and bound to
// it and hands the value only to the agent's environment — never the node's key.
func TestRemoteStartReadsItsSecretsUnderTheOwnersRun(t *testing.T) {
	s := newSecretRig(t, "rmt_abc")
	out, err := runRoot(t, "remote", "start", "edge", "--server", s.server, "--story", "sty_sec", "--message", "hello node")
	if err != nil {
		t.Fatalf("remote start: %v\n%s", err, out)
	}

	if posts := s.owner.find("POST", "/api/v1/remotes/rmt_abc/runs"); len(posts) != 1 {
		t.Fatalf("want exactly one run registered for the definition's remote_id, calls: %v", s.owner.all())
	}
	if len(s.owner.find("DELETE", "/api/v1/remotes/rmt_abc/runs/trg_run1")) != 1 {
		t.Errorf("the owner's run was not ended with the node: %v", s.owner.all())
	}

	reads := s.reads.Calls()
	if len(reads) != 1 || reads[0].Path != "/api/v1/remotes/rmt_abc/runs/trg_run1/secrets/CLAUDE_CODE_OAUTH_TOKEN" || !reads[0].Verified {
		t.Fatalf("the node's reads = %+v", reads)
	}
	if reads[0].Body["scope"] != "personal" {
		t.Errorf("read body = %v", reads[0].Body)
	}

	env, err := os.ReadFile(s.agentEnv)
	if err != nil {
		t.Fatalf("the agent never ran: %v", err)
	}
	if !strings.Contains(string(env), "CLAUDE_CODE_OAUTH_TOKEN="+devtest.SecretValue+"\n") {
		t.Errorf("the agent did not receive the secret in its environment")
	}
	// (SATELLE_TEST_REMOTE_KEY is the test process's own variable that host.env was
	// expanded from; the node's key variable is SATELLE_REMOTE_KEY.)
	for _, line := range strings.Split(string(env), "\n") {
		if strings.HasPrefix(line, "SATELLE_REMOTE_KEY=") {
			t.Errorf("the node's private key reached the agent: %s", line[:len("SATELLE_REMOTE_KEY=")])
		}
	}

	if strings.Contains(out, devtest.SecretValue) {
		t.Errorf("the value was printed:\n%s", out)
	}
	// Neither machine's files — the repo, the local satelle home (store, ledger,
	// logs), the node's home and root, the host log — hold the value.
	for _, root := range []string{s.repo, os.Getenv("SATELLE_HOME"), s.nodeHome, s.nodeRoot, filepath.Dir(s.hostLog)} {
		if hit := treeContains(t, devtest.SecretValue, root); hit != "" {
			t.Errorf("the secret value is on disk at %s", hit)
		}
	}
}

// A declared secret with no remote_id in the definition refuses the start before
// any request is made.
func TestRemoteStartWithoutRemoteIDRefusesBeforeAnyRegistration(t *testing.T) {
	s := newSecretRig(t, "")
	out, err := runRoot(t, "remote", "start", "edge", "--server", s.server, "--story", "sty_sec")
	if err == nil || !strings.Contains(err.Error(), "remote edge: remote_id is not set") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if n := len(s.owner.all()); n != 0 {
		t.Errorf("a refused start registered %d request(s)", n)
	}
	if n := len(s.reads.Calls()); n != 0 {
		t.Errorf("a refused start read %d secret(s)", n)
	}
	if log := s.log(); strings.Contains(log, "start edge-1") {
		t.Errorf("a refused start ran the host:\n%s", log)
	}
}

// A secret satelle.dev refuses stops the node's agent job before the agent starts,
// printing the server's own reason.
func TestRemoteStartRefusedReadDoesNotStartTheAgent(t *testing.T) {
	s := newSecretRig(t, "rmt_abc")
	s.reads.Refuse = func(devtest.Call) (int, string) { return 403, `{"error":"refused","reason":"no_grant"}` }
	out, err := runRoot(t, "remote", "start", "edge", "--server", s.server, "--story", "sty_sec", "--message", "hello node")
	if err == nil || !strings.Contains(err.Error()+out, "secret CLAUDE_CODE_OAUTH_TOKEN refused: refused: no_grant") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if _, serr := os.Stat(s.agentEnv); serr == nil {
		t.Error("the agent ran although its secret was refused")
	}
}
