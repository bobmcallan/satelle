package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotenode"
	"github.com/bobmcallan/satelle/internal/remotenode/devtest"
)

// carrierEnv sets the environment a host.run process receives for a run.
func carrierEnv(t *testing.T, server string) {
	t.Helper()
	def := remotedef.Definition{
		Identity: remotedef.Identity{Key: "SATELLE_REMOTE_KEY"},
		Secret:   []remotedef.SecretDecl{{Key: "CLAUDE_CODE_OAUTH_TOKEN", Scope: remotedef.ScopePersonal, From: remotedef.FromEnv}},
	}
	for _, kv := range remotenode.NewRunSecrets(def, "rmt_abc", server, "trg_run1").Env() {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	t.Setenv("SATELLE_REMOTE_KEY", devtest.SeedB64)
}

// AC4: `story drive` reads the run's secrets before any agent starts, hands them to
// every engine of the process through one source, and drops the identity variable;
// stopping clears them.
func TestWireRunSecretsReadsTheRunAndInstallsThem(t *testing.T) {
	d := devtest.New(t)
	carrierEnv(t, d.URL)
	var stderr bytes.Buffer
	stop, err := wireRunSecrets(context.Background(), &stderr, remotenode.WithHTTPClient(d.Client()))
	if err != nil {
		t.Fatal(err)
	}
	values, unset := runSecretsSource()
	if values["CLAUDE_CODE_OAUTH_TOKEN"] != devtest.SecretValue {
		t.Errorf("values = %v", values)
	}
	if len(unset) != 1 || unset[0] != "SATELLE_REMOTE_KEY" {
		t.Errorf("the node's key variable must be unset for every agent: %v", unset)
	}
	calls := d.Calls()
	if len(calls) != 1 || !calls[0].Verified || calls[0].Path != "/api/v1/remotes/rmt_abc/runs/trg_run1/secrets/CLAUDE_CODE_OAUTH_TOKEN" {
		t.Errorf("reads = %+v", calls)
	}
	if strings.Contains(stderr.String(), devtest.SecretValue) {
		t.Error("the value was printed")
	}
	stop()
	if v, u := runSecretsSource(); len(v) != 0 || len(u) != 0 {
		t.Errorf("stop must clear the values: %v %v", v, u)
	}
}

func TestWireRunSecretsRefusalFailsTheDriveAndPrintsTheServersReason(t *testing.T) {
	d := devtest.New(t)
	d.Refuse = func(devtest.Call) (int, string) { return 403, `{"error":"refused","reason":"run_expired"}` }
	carrierEnv(t, d.URL)
	var stderr bytes.Buffer
	_, err := wireRunSecrets(context.Background(), &stderr, remotenode.WithHTTPClient(d.Client()))
	if err == nil || !strings.Contains(err.Error(), "no agent was started") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(stderr.String(), "secret CLAUDE_CODE_OAUTH_TOKEN refused: refused: run_expired") {
		t.Errorf("stderr = %q", stderr.String())
	}
	t.Cleanup(func() { setRunSecrets(nil, nil) })
}

// With no carrier in the environment (a local drive, a remote with no secrets)
// nothing is read or unset.
func TestWireRunSecretsWithoutACarrierDoesNothing(t *testing.T) {
	for _, k := range []string{config.RemoteServerRunEnv, config.RemoteIDEnv, config.RemoteServerEnv, config.RemoteSecretsEnv, config.RemoteIdentityEnv} {
		t.Setenv(k, "")
	}
	stop, err := wireRunSecrets(context.Background(), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if v, u := runSecretsSource(); len(v) != 0 || len(u) != 0 {
		t.Errorf("source = %v %v", v, u)
	}
}

// The identity variable is unset even when there is no run to read for.
func TestWireRunSecretsUnsetsTheKeyEvenWithoutARun(t *testing.T) {
	carrierEnv(t, "https://satelle.test")
	t.Setenv(config.RemoteServerRunEnv, "")
	t.Cleanup(func() { setRunSecrets(nil, nil) })
	stop, err := wireRunSecrets(context.Background(), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, u := runSecretsSource(); len(u) != 1 || u[0] != "SATELLE_REMOTE_KEY" {
		t.Errorf("unset = %v", u)
	}
}
