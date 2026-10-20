package remotedef

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/remote.toml")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Parse reads the secrets, repos and allowlist of the definition satelle-server
// accepts (testdata/remote.toml, copied from its validTOML), and nothing else.
func TestParseReadsWhatTheNodeNeedsFromTheServersAcceptedDefinition(t *testing.T) {
	d, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if want := []SecretDecl{{Key: "CLAUDE_CODE_OAUTH_TOKEN", Scope: ScopePersonal, From: FromEnv}}; !reflect.DeepEqual(d.Secret, want) {
		t.Errorf("secrets = %+v, want %+v", d.Secret, want)
	}
	if want := []Repo{{URL: "https://github.com/acme/widgets.git", Ref: "main"}}; !reflect.DeepEqual(d.Repos, want) {
		t.Errorf("repos = %+v, want %+v", d.Repos, want)
	}
	if want := []string{"api.example.com", "*.example.org"}; !reflect.DeepEqual(d.Allow(), want) {
		t.Errorf("allow = %v, want %v", d.Allow(), want)
	}
}

// The fixture carries keys the node does not read, so a parser that refused
// undecoded keys would fail the test above. This pins that the fixture really has
// them.
func TestTheFixtureCarriesKeysTheNodeDoesNotRead(t *testing.T) {
	var d Definition
	md, err := toml.Decode(string(fixture(t)), &d)
	if err != nil {
		t.Fatal(err)
	}
	undecoded := map[string]bool{}
	for _, k := range md.Undecoded() {
		undecoded[k.String()] = true
	}
	for _, want := range []string{"satelle_version", "identity.label", "host.start", "segment", "step"} {
		if !undecoded[want] {
			t.Errorf("the fixture should carry %q, which Parse does not read (undecoded: %v)", want, md.Undecoded())
		}
	}
}

// A secret's From is "env" or an argv, and a team secret names its workspace.
func TestParseSecretFromIsAnArgvOrEnv(t *testing.T) {
	d, err := Parse([]byte(`
[[secret]]
key = "A"
scope = "team"
workspace = "ws-1"
from = ["print-it", "A"]
`))
	if err != nil {
		t.Fatal(err)
	}
	want := SecretDecl{Key: "A", Scope: ScopeTeam, Workspace: "ws-1", From: []any{"print-it", "A"}}
	if len(d.Secret) != 1 || !reflect.DeepEqual(d.Secret[0], want) {
		t.Errorf("secret = %+v, want %+v", d.Secret, want)
	}
}

func TestParseRefusesWhatItCannotRead(t *testing.T) {
	for name, body := range map[string]string{
		"malformed TOML":      "[[secret\nkey = 1",
		"wrong-typed allow":   "[network]\nallow = \"x\"\n",
		"wrong-typed repo":    "[[repo]]\nurl = 3\n",
		"wrong-typed secrets": "secret = \"x\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(body)); err == nil || !strings.Contains(err.Error(), "remote.toml") {
				t.Errorf("Parse(%q) = %v, want an error naming remote.toml", body, err)
			}
		})
	}
}

func TestParseOfNothingIsAnEmptyDefinition(t *testing.T) {
	d, err := Parse(nil)
	if err != nil || len(d.Secret)+len(d.Repos)+len(d.Allow()) != 0 {
		t.Errorf("Parse(nil) = %+v, %v", d, err)
	}
}

func TestRepoName(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/acme/widgets.git": "widgets",
		"https://github.com/acme/widgets/":    "widgets",
		"https://git.example.com/team/tool":   "tool",
	} {
		if got := RepoName(url); got != want {
			t.Errorf("RepoName(%q) = %q, want %q", url, got, want)
		}
	}
}
