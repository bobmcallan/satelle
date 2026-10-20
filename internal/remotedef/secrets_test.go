package remotedef

import (
	"os"
	"strings"
	"testing"
	"time"
)

const teamWorkspace = "0f7b1c55-3b8e-4c8a-9d7a-5d2d3c1a9b10"

// AC1: each refusal names the secret as secret[i] (KEY) and never a value.
func TestSecretDeclarationsAreRefusedByKey(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"unknown scope", "key = \"A_B\"\nscope = \"global\"\nfrom = \"env\"", "secret[0] (A_B).scope"},
		{"scope missing", "key = \"A_B\"\nfrom = \"env\"", "secret[0] (A_B).scope"},
		{"literal value field", "key = \"A_B\"\nscope = \"personal\"\nfrom = \"env\"\nvalue = \"hunter2\"", "secret[0] (A_B).value"},
		{"literal in from", "key = \"A_B\"\nscope = \"personal\"\nfrom = \"hunter2\"", "secret[0] (A_B).from"},
		{"empty argv", "key = \"A_B\"\nscope = \"personal\"\nfrom = []", "secret[0] (A_B).from"},
		{"expires and ttl", "key = \"A_B\"\nscope = \"personal\"\nfrom = \"env\"\nexpires = \"2030-01-01\"\nttl = \"24h\"", "secret[0] (A_B)"},
		{"bad expires", "key = \"A_B\"\nscope = \"personal\"\nfrom = \"env\"\nexpires = \"next week\"", "secret[0] (A_B).expires"},
		{"bad ttl", "key = \"A_B\"\nscope = \"personal\"\nfrom = \"env\"\nttl = \"-3h\"", "secret[0] (A_B).ttl"},
		{"team with no workspace", "key = \"A_B\"\nscope = \"team\"\nfrom = \"env\"", "secret[0] (A_B).workspace"},
		{"bad key", "key = \"a b\"\nscope = \"personal\"\nfrom = \"env\"", "secret[0] (a b).key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := scaffolded(t)
			appendTo(t, dir, "remote.toml", "\n[[secret]]\n"+tc.body+"\n")
			fs := Validate(dir)
			if !hasFinding(fs, "remote.toml", tc.want) {
				t.Fatalf("want a finding at %q, got:\n%s", tc.want, strings.Join(details(fs), "\n"))
			}
			for _, f := range fs {
				if strings.Contains(f.Detail, "hunter2") {
					t.Errorf("a finding must never echo the value: %s", f.Detail)
				}
			}
		})
	}
}

func TestSecretDeclarationsAccepted(t *testing.T) {
	dir := scaffolded(t)
	appendTo(t, dir, "remote.toml", `
[[secret]]
key = "CLAUDE_CODE_OAUTH_TOKEN"
scope = "personal"
from = "env"
expires = "2027-10-01"

[[secret]]
key = "MINTED"
scope = "personal"
from = ["mint-token", "--short"]
ttl = "36h"

[[secret]]
key = "TEAM_KEY"
scope = "team"
workspace = "`+teamWorkspace+`"
from = "env"
ttl = "30d"
`)
	if fs := Validate(dir); len(fs) != 0 {
		t.Fatalf("a well-formed declaration must pass, got:\n%s", strings.Join(details(fs), "\n"))
	}
	def, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(def.Secret) != 3 || !def.Secret[0].IsEnv() || strings.Join(def.Secret[1].Argv(), " ") != "mint-token --short" {
		t.Fatalf("secrets decoded wrong: %+v", def.Secret)
	}
	if at, ok := def.Secret[0].ExpiresAt(); !ok || !at.Equal(time.Date(2027, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("ExpiresAt = %v, %v", at, ok)
	}
	if d, ok := def.Secret[1].TTLDuration(); !ok || d != 36*time.Hour {
		t.Errorf("TTLDuration = %v, %v", d, ok)
	}
	if d, ok := def.Secret[2].TTLDuration(); !ok || d != 30*24*time.Hour {
		t.Errorf("30d = %v, %v", d, ok)
	}
}

// The legacy name list is gone: it is refused as an unknown key, naming [[secret]].
func TestLegacySecretsListIsRefused(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, "remote.toml", `wake_key = "REMOTE_WAKE_KEY"`, "secrets = [\"A_B\"]\nwake_key = \"REMOTE_WAKE_KEY\"")
	fs := Validate(dir)
	if !hasFinding(fs, "remote.toml", "secrets") {
		t.Fatalf("got:\n%s", strings.Join(details(fs), "\n"))
	}
	if !strings.Contains(strings.Join(details(fs), "\n"), "[[secret]]") {
		t.Errorf("the refusal must name [[secret]]:\n%s", strings.Join(details(fs), "\n"))
	}
}

func TestRemoteIDIsValidatedWhenSet(t *testing.T) {
	dir := scaffolded(t)
	if fs := Validate(dir); len(fs) != 0 {
		t.Fatalf("an unset remote_id must pass: %v", details(fs))
	}
	replaceIn(t, dir, "remote.toml", `wake_key = "REMOTE_WAKE_KEY"`, "remote_id = \"abc\"\nwake_key = \"REMOTE_WAKE_KEY\"")
	if fs := Validate(dir); !hasFinding(fs, "remote.toml", "remote_id") {
		t.Errorf("remote_id = abc must be refused, got:\n%s", strings.Join(details(Validate(dir)), "\n"))
	}
	replaceIn(t, dir, "remote.toml", `remote_id = "abc"`, `remote_id = "rmt_a1-B_2"`)
	if fs := Validate(dir); len(fs) != 0 {
		t.Errorf("a well-formed remote_id must pass: %v", details(fs))
	}
}

func TestRequireRemoteID(t *testing.T) {
	_, err := Definition{}.RequireRemoteID("edge")
	if err == nil || !strings.Contains(err.Error(), "remote edge: remote_id is not set; register the remote on satelle.dev, then set remote_id") {
		t.Fatalf("err = %v", err)
	}
	if id, err := (Definition{RemoteID: "rmt_x"}).RequireRemoteID("edge"); err != nil || id != "rmt_x" {
		t.Errorf("got %q, %v", id, err)
	}
}

func TestIdentityKeyAndOIDC(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, "remote.toml", `label = "your-host-label"`, "label = \"your-host-label\"\nkey = \"SATELLE_REMOTE_KEY\"")
	if fs := Validate(dir); len(fs) != 0 {
		t.Fatalf("identity.key names a variable and must pass: %v", details(fs))
	}
	both := scaffolded(t)
	replaceIn(t, both, "remote.toml", `label = "your-host-label"`, "label = \"your-host-label\"\nkey = \"K\"\n[identity.oidc]\nissuer = \"https://oidc.example\"\nsubject = \"org:app:*\"\ntoken = [\"cli\", \"token\", \"{aud}\"]")
	if fs := Validate(both); !hasFinding(fs, "remote.toml", "identity") {
		t.Errorf("key and oidc together must be refused, got:\n%s", strings.Join(details(fs), "\n"))
	}
	oidc := scaffolded(t)
	replaceIn(t, oidc, "remote.toml", `label = "your-host-label"`, "label = \"your-host-label\"\n[identity.oidc]\nissuer = \"https://oidc.example\"\nsubject = \"org:app:*\"\ntoken = [\"cli\", \"token\", \"{aud}\"]")
	if fs := Validate(oidc); len(fs) != 0 {
		t.Errorf("a well-formed oidc identity must pass: %v", details(fs))
	}
	noAud := scaffolded(t)
	replaceIn(t, noAud, "remote.toml", `label = "your-host-label"`, "label = \"your-host-label\"\n[identity.oidc]\nissuer = \"http://oidc.example\"\nsubject = \"\"\ntoken = [\"cli\", \"token\"]")
	fs := Validate(noAud)
	for _, key := range []string{"identity.oidc.issuer", "identity.oidc.subject", "identity.oidc.token"} {
		if !hasFinding(fs, "remote.toml", key) {
			t.Errorf("want a finding for %s, got:\n%s", key, strings.Join(details(fs), "\n"))
		}
	}
}

func TestTokenArgvFillsAudienceThroughTheOneResolver(t *testing.T) {
	d := Definition{Identity: Identity{Label: "L", OIDC: &OIDCIdentity{Token: []string{"cli", "{label}", "--aud={aud}"}}}}
	if got := strings.Join(d.TokenArgv("https://x/runs/trg_1"), " "); got != "cli L --aud=https://x/runs/trg_1" {
		t.Errorf("TokenArgv = %q", got)
	}
	if (Definition{}).TokenArgv("x") != nil {
		t.Error("a definition with no oidc identity has no token argv")
	}
}

// The story_command warning: fires when [[secret]] is declared and the command is
// not `satelle story drive`; an absolute satelle path is not flagged.
func TestStoryCommandWarning(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		warn bool
	}{
		{"bash -c 'work {story}'", true},
		{"satelle story drive {story}", false},
		{"/usr/local/bin/satelle story drive {story}", false},
		{"satelle story get {story}", true},
	} {
		dir := scaffolded(t)
		replaceIn(t, dir, "remote.toml", "# story_command = \"your-agent-cli {story}\"", "story_command = \""+tc.cmd+"\"")
		appendTo(t, dir, "remote.toml", "\n[[secret]]\nkey = \"A_B\"\nscope = \"personal\"\nfrom = \"env\"\n")
		fs := Validate(dir)
		var warned bool
		for _, f := range fs {
			if f.Severity == "warn" && strings.Contains(f.Detail, "host.story_command") {
				warned = true
			}
		}
		if warned != tc.warn {
			t.Errorf("%q: warned = %v, want %v (%v)", tc.cmd, warned, tc.warn, details(fs))
		}
		if len(Errors(fs)) != 0 {
			t.Errorf("%q: a warning must not be an error: %v", tc.cmd, details(Errors(fs)))
		}
	}
}

// No reader of the removed name list remains.
func TestNoLegacySecretsReader(t *testing.T) {
	b, err := os.ReadFile("remotedef.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Secrets []string") {
		t.Error("remotedef.go still declares Secrets []string")
	}
}
