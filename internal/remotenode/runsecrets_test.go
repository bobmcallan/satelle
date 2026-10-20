package remotenode

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

func secretDef() remotedef.Definition {
	return remotedef.Definition{
		Identity: remotedef.Identity{Key: "SATELLE_REMOTE_KEY"},
		Secret: []remotedef.SecretDecl{
			{Key: "CLAUDE_CODE_OAUTH_TOKEN", Scope: remotedef.ScopePersonal, From: remotedef.FromEnv},
			{Key: "TEAM_KEY", Scope: remotedef.ScopeTeam, Workspace: "0f7b1c55-3b8e-4c8a-9d7a-5d2d3c1a9b10", From: remotedef.FromEnv},
		},
	}
}

// The carrier survives the environment: Env then RunSecretsFromEnv is the identity.
func TestRunSecretsEnvRoundTrip(t *testing.T) {
	rs := NewRunSecrets(secretDef(), "rmt_a", "https://satelle.dev", "trg_9")
	env := map[string]string{}
	for _, kv := range rs.Env() {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	for k, want := range map[string]string{
		config.RemoteServerRunEnv: "trg_9", config.RemoteIDEnv: "rmt_a", config.RemoteServerEnv: "https://satelle.dev",
		config.RemoteIdentityEnv: "key:SATELLE_REMOTE_KEY",
		config.RemoteSecretsEnv:  "CLAUDE_CODE_OAUTH_TOKEN:personal,TEAM_KEY:team:0f7b1c55-3b8e-4c8a-9d7a-5d2d3c1a9b10",
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	back := RunSecretsFromEnv(func(k string) string { return env[k] })
	if !reflect.DeepEqual(back, rs) {
		t.Errorf("round trip\n got %+v\nwant %+v", back, rs)
	}
	if err := back.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	if !back.Active() {
		t.Error("a carrier with a run and secrets is active")
	}
}

func TestRunSecretsValidateNamesWhatIsMissing(t *testing.T) {
	err := RunSecrets{}.Validate()
	if err == nil {
		t.Fatal("an empty carrier must not validate")
	}
	for _, want := range []string{"server run id", "remote id", "satelle.dev server", "identity"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	bad := NewRunSecrets(secretDef(), "rmt_a", "https://x", "trg/../9")
	if bad.Validate() == nil {
		t.Error("a run id that is not path-safe must be refused")
	}
}

func TestOIDCIdentityCarriesItsTokenCommand(t *testing.T) {
	def := remotedef.Definition{Secret: secretDef().Secret, Identity: remotedef.Identity{OIDC: &remotedef.OIDCIdentity{Token: []string{"mint", "--aud={aud}"}}}}
	rs := NewRunSecrets(def, "rmt_a", "https://s", "trg_1")
	if !rs.IsOIDC() || rs.IdentityEnv() != "" {
		t.Fatalf("identity = %q", rs.Identity)
	}
	argv, err := rs.OIDCTokenArgv()
	if err != nil || !reflect.DeepEqual(argv, []string{"mint", "--aud={aud}"}) {
		t.Errorf("argv = %v, %v", argv, err)
	}
	if err := rs.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}
