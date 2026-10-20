package remotedef

import (
	"reflect"
	"testing"
)

func TestIdentityRefNamesAVariableOrACommandNeverAKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   Identity
		want string
	}{
		{"key", Identity{Key: "SATELLE_REMOTE_KEY"}, "key:SATELLE_REMOTE_KEY"},
		{"oidc", Identity{OIDC: &OIDCIdentity{Token: []string{"mint", "--aud={aud}"}}}, `oidc:["mint","--aud={aud}"]`},
		{"oidc wins over key", Identity{Key: "K", OIDC: &OIDCIdentity{Token: []string{"mint"}}}, `oidc:["mint"]`},
		{"neither", Identity{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Definition{Identity: tc.id}).IdentityRef(); got != tc.want {
				t.Errorf("IdentityRef = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTokenArgvFillsOnlyTheAudience(t *testing.T) {
	d := Definition{Identity: Identity{OIDC: &OIDCIdentity{Token: []string{"mint", "--aud={aud}", "{label}", "{command}"}}}}
	want := []string{"mint", "--aud=https://s/runs/r1", "{label}", "{command}"}
	if got := d.TokenArgv("https://s/runs/r1"); !reflect.DeepEqual(got, want) {
		t.Errorf("TokenArgv = %v, want %v", got, want)
	}
	// An audience that carries the placeholder is not expanded a second time.
	if got := d.TokenArgv("{aud}"); got[1] != "--aud={aud}" {
		t.Errorf("a substituted value was expanded again: %v", got)
	}
	if got := (Definition{}).TokenArgv("a"); got != nil {
		t.Errorf("no workload identity has no token command, got %v", got)
	}
}

func TestValidRemoteID(t *testing.T) {
	for id, want := range map[string]bool{"rmt_abc": true, "rmt_A-9_z": true, "": false, "rmt_": false, "abc": false, "rmt_a/b": false} {
		if got := ValidRemoteID(id); got != want {
			t.Errorf("ValidRemoteID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestRepoNameIsTheLastPathElementWithoutDotGit(t *testing.T) {
	for url, want := range map[string]string{
		"https://git.example.com/team/tool.git":  "tool",
		"https://git.example.com/team/tool.git/": "tool",
		"https://git.example.com/team/tool":      "tool",
	} {
		if got := RepoName(url); got != want {
			t.Errorf("RepoName(%q) = %q, want %q", url, got, want)
		}
	}
}
