//go:build !windows

package remotenode

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotenode/devtest"
	"github.com/bobmcallan/satelle/internal/remotesign"
)

const testWS = "0f7b1c55-3b8e-4c8a-9d7a-5d2d3c1a9b10"

// carrier is the run's carrier for the stub satelle.dev.
func carrier(d *devtest.Dev, runID string) RunSecrets {
	def := secretDef()
	def.Secret[1].Workspace = testWS
	return NewRunSecrets(def, "rmt_a", d.URL, runID)
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

var keyEnv = map[string]string{"SATELLE_REMOTE_KEY": devtest.SeedB64}

// AC4: each read is a signed request bound to the run, carrying the scope and
// workspace the declaration names; the value comes back to the caller only.
func TestReadsAreSignedAndBoundToTheRun(t *testing.T) {
	d := devtest.New(t)
	values, _, err := SecretsForRun(context.Background(), carrier(d, "trg_9"), envOf(keyEnv), WithHTTPClient(d.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if values["CLAUDE_CODE_OAUTH_TOKEN"] != devtest.SecretValue || values["TEAM_KEY"] != devtest.SecretValue {
		t.Fatalf("values = %v", values)
	}
	calls := d.Calls()
	if len(calls) != 2 {
		t.Fatalf("want one read per key, got %d calls", len(calls))
	}
	want := []struct{ path, scope, ws string }{
		{"/api/v1/remotes/rmt_a/runs/trg_9/secrets/CLAUDE_CODE_OAUTH_TOKEN", "personal", ""},
		{"/api/v1/remotes/rmt_a/runs/trg_9/secrets/TEAM_KEY", "team", testWS},
	}
	for i, w := range want {
		c := calls[i]
		if c.Method != "POST" || c.Path != w.path || c.Body["scope"] != w.scope || c.Body["workspace"] != w.ws {
			t.Errorf("call %d = %+v", i, c)
		}
		if !c.Verified {
			t.Errorf("call %d: the signature does not verify over %q", i, c.Message)
		}
		if c.Auth != "" {
			t.Errorf("call %d carries a bearer %q; the signature is the authentication", i, c.Auth)
		}
		if !strings.HasPrefix(c.Message, remotesign.Version+"\nPOST\n"+w.path+"\ntrg_9\n") {
			t.Errorf("call %d: the signed message is not bound to the run: %q", i, c.Message)
		}
	}
	if calls[0].Header.Get(remotesign.HeaderNonce) == calls[1].Header.Get(remotesign.HeaderNonce) {
		t.Error("two requests shared a nonce")
	}
}

// Refresh and end are signed with empty key, scope and workspace.
func TestRefreshAndEndAreSignedWithEmptyFields(t *testing.T) {
	d := devtest.New(t)
	_, r, err := SecretsForRun(context.Background(), carrier(d, "trg_9"), envOf(keyEnv), WithHTTPClient(d.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.End(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := d.Calls()[2:]
	if len(calls) != 2 || calls[0].Method != "POST" || !strings.HasSuffix(calls[0].Path, "/runs/trg_9/refresh") || calls[1].Method != "DELETE" || !strings.HasSuffix(calls[1].Path, "/runs/trg_9") {
		t.Fatalf("calls = %+v", calls)
	}
	for _, c := range calls {
		if !c.Verified {
			t.Errorf("%s %s: signature does not verify over %q", c.Method, c.Path, c.Message)
		}
		if lines := strings.Split(c.Message, "\n"); lines[4] != "" || lines[5] != "" || lines[6] != "" {
			t.Errorf("key, scope and workspace must be empty lines: %q", c.Message)
		}
	}
}

func TestKeepAliveRefreshesUntilStopped(t *testing.T) {
	d := devtest.New(t)
	rs := carrier(d, "trg_9")
	auth, err := NewAuth(rs, envOf(keyEnv))
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(rs, auth, WithHTTPClient(d.Client()))
	if err != nil {
		t.Fatal(err)
	}
	stop := r.KeepAlive(context.Background(), 10*time.Millisecond)
	eventually(t, "a refresh", func() bool { return len(d.Calls()) >= 2 })
	stop()
	n := len(d.Calls())
	time.Sleep(60 * time.Millisecond)
	if len(d.Calls()) != n {
		t.Error("refreshes continued after stop")
	}
}

// A refusal is the server's {error, reason} verbatim, as a *SecretRefusal wrapping
// the typed API error; it is not a trigger rejection, and no value is returned.
func TestRefusalIsVerbatimAndStopsTheRead(t *testing.T) {
	d := devtest.New(t)
	d.Refuse = func(devtest.Call) (int, string) {
		return http.StatusForbidden, `{"error":"refused","reason":"no_grant"}`
	}
	values, _, err := SecretsForRun(context.Background(), carrier(d, "trg_9"), envOf(keyEnv), WithHTTPClient(d.Client()))
	var ref *SecretRefusal
	if !errors.As(err, &ref) {
		t.Fatalf("err = %T %v", err, err)
	}
	var ae *hosted.APIError
	if !errors.As(err, &ae) || ae.Code != "refused" || ae.Reason != "no_grant" || ae.Status != 403 {
		t.Errorf("APIError = %+v", ae)
	}
	if ref.Key != "CLAUDE_CODE_OAUTH_TOKEN" || err.Error() != "secret CLAUDE_CODE_OAUTH_TOKEN refused: refused: no_grant" {
		t.Errorf("message = %q", err)
	}
	if values != nil {
		t.Errorf("a refused read returned values: %v", values)
	}
	if len(d.Calls()) != 1 {
		t.Errorf("the first refusal must stop the reads: %d calls", len(d.Calls()))
	}
}

func TestNoIdentityMeansNoRead(t *testing.T) {
	d := devtest.New(t)
	_, _, err := SecretsForRun(context.Background(), carrier(d, "trg_9"), envOf(nil), WithHTTPClient(d.Client()))
	if err == nil || !strings.Contains(err.Error(), "SATELLE_REMOTE_KEY") {
		t.Fatalf("err = %v", err)
	}
	if len(d.Calls()) != 0 {
		t.Error("nothing may be sent without the remote's key")
	}
	_, _, err = SecretsForRun(context.Background(), carrier(d, "trg_9"), envOf(map[string]string{"SATELLE_REMOTE_KEY": "not-a-key"}), WithHTTPClient(d.Client()))
	if err == nil || strings.Contains(err.Error(), "not-a-key") {
		t.Errorf("a bad key must be refused without echoing it: %v", err)
	}
}

// A workload identity token is minted per request through Commands.Token, with the
// run's audience filled in, and is the request's bearer.
func TestOIDCTokenIsTheBearerForTheRunsAudience(t *testing.T) {
	d := devtest.New(t)
	def := secretDef()
	def.Identity = remotedef.Identity{OIDC: &remotedef.OIDCIdentity{Token: []string{"sh", "-c", `printf 'tok:%s' "$0"`, "{aud}"}}}
	def.Secret = def.Secret[:1]
	rs := NewRunSecrets(def, "rmt_a", d.URL, "trg_9")
	values, _, err := SecretsForRun(context.Background(), rs, envOf(nil), WithHTTPClient(d.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if values["CLAUDE_CODE_OAUTH_TOKEN"] != devtest.SecretValue {
		t.Fatalf("values = %v", values)
	}
	c := d.Calls()[0]
	if want := "Bearer tok:" + d.URL + "/runs/trg_9"; c.Auth != want {
		t.Errorf("Authorization = %q, want %q", c.Auth, want)
	}
	if c.Header.Get(remotesign.HeaderSignature) != "" {
		t.Error("a workload identity request carries no signature")
	}
}

func TestCommandsTokenFillsAudAndRefusesEmptyOutput(t *testing.T) {
	c := &Commands{Def: remotedef.Definition{Identity: remotedef.Identity{OIDC: &remotedef.OIDCIdentity{Token: []string{"sh", "-c", `printf '  t-%s \n' "$0"`, "{aud}"}}}}}
	tok, err := c.Token(context.Background(), "https://s/runs/r1")
	if err != nil || tok != "t-https://s/runs/r1" {
		t.Errorf("Token = %q, %v", tok, err)
	}
	empty := &Commands{Def: remotedef.Definition{Identity: remotedef.Identity{OIDC: &remotedef.OIDCIdentity{Token: []string{"true", "{aud}"}}}}}
	if _, err := empty.Token(context.Background(), "a"); err == nil {
		t.Error("a command that prints nothing must fail")
	}
	if _, err := (&Commands{}).Token(context.Background(), "a"); err == nil {
		t.Error("a definition with no token command must fail")
	}
}

// Reading writes nothing: the value is not in the working directory or the temp
// directory.
func TestReadingNeverWritesTheValueToDisk(t *testing.T) {
	work, tmp := t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Chdir(work)
	d := devtest.New(t)
	_, r, err := SecretsForRun(context.Background(), carrier(d, "trg_9"), envOf(keyEnv), WithHTTPClient(d.Client()))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Refresh(context.Background())
	_ = r.End(context.Background())
	for _, root := range []string{work, tmp} {
		_ = filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
			if err == nil && !e.IsDir() {
				b, _ := os.ReadFile(p)
				if strings.Contains(string(b), devtest.SecretValue) {
					t.Errorf("the secret value is on disk at %s", p)
				}
			}
			return nil
		})
	}
}
