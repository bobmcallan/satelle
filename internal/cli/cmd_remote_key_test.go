//go:build !windows

package cli

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// stubLister answers every listing with the same metas.
type stubLister []hosted.SecretMeta

func (s stubLister) ListSecrets(context.Context, string, string) ([]hosted.SecretMeta, error) {
	return s, nil
}

// editRemote replaces old with repl in the remote's definition.
func editRemote(t *testing.T, repo, old, repl string) {
	t.Helper()
	p := filepath.Join(remoteDir(repo, "edge"), "remote.toml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("remote.toml has no %q", old)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, repl, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// treeContains reports the path of the first file under any root whose bytes hold
// needle, raw.
func treeContains(t *testing.T, needle string, roots ...string) string {
	t.Helper()
	var hit string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || hit != "" {
				return nil
			}
			if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), needle) {
				hit = p
			}
			return nil
		})
	}
	return hit
}

// AC3: create registers the public half on satelle.dev, prints the private half
// once, and neither half is written to satelle's files beyond the public key.
func TestRemoteKeyCreateRegistersThePublicHalfAndPrintsTheSeedOnce(t *testing.T) {
	d := newDevStub(t)
	repo := secretRepo(t, d, `remote_id = "rmt_abc"`, "")
	editRemote(t, repo, `label = "your-host-label"`, "label = \"your-host-label\"\nkey = \"SATELLE_REMOTE_KEY\"")
	home := os.Getenv("SATELLE_HOME")

	out, err := runRoot(t, "remote", "key", "create", "edge", "--server", d.ts.URL)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	puts := d.find("PUT", "/api/v1/remotes/rmt_abc/key")
	if len(puts) != 1 {
		t.Fatalf("calls: %v", d.all())
	}
	pubB64 := d.body(puts[0])["public_key"]
	pub, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		t.Fatalf("registered key = %q (%v)", pubB64, err)
	}

	var seedB64 string
	for _, line := range strings.Split(out, "\n") {
		if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(line)); err == nil && len(raw) == ed25519.SeedSize {
			seedB64 = strings.TrimSpace(line)
		}
	}
	if seedB64 == "" || strings.Count(out, seedB64) != 1 {
		t.Fatalf("the private key must be printed exactly once:\n%s", out)
	}
	seed, _ := base64.StdEncoding.DecodeString(seedB64)
	if got := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey); string(got) != string(pub) {
		t.Error("the printed private key is not the registered key's pair")
	}
	if !strings.Contains(out, "SATELLE_REMOTE_KEY") || !strings.Contains(out, "SHA256:fp") {
		t.Errorf("output must say where to store it and name the fingerprint:\n%s", out)
	}

	for _, needle := range []string{seedB64, string(seed), hex.EncodeToString(seed), strings.TrimRight(seedB64, "=")} {
		if hit := treeContains(t, needle, repo, home, os.Getenv("XDG_CONFIG_HOME")); hit != "" {
			t.Errorf("the private key was written to %s", hit)
		}
	}
	if hit := treeContains(t, pubB64, filepath.Join(repo, ".satelle", "config")); hit != "" {
		t.Errorf("satelle wrote even the public key into its config at %s", hit)
	}
}

func TestRemoteKeyRotateAndRegister(t *testing.T) {
	d := newDevStub(t)
	repo := secretRepo(t, d, `remote_id = "rmt_abc"`, "")
	editRemote(t, repo, `label = "your-host-label"`, "label = \"your-host-label\"\nkey = \"SATELLE_REMOTE_KEY\"")

	out, err := runRoot(t, "remote", "key", "rotate", "edge", "--server", d.ts.URL)
	if err != nil || !strings.Contains(out, "rotated to") || !strings.Contains(out, "no longer verifies") {
		t.Fatalf("rotate: %v\n%s", err, out)
	}

	public := base64.StdEncoding.EncodeToString(make([]byte, 32))
	out, err = runRoot(t, "remote", "key", "register", "edge", "--public-key", public, "--server", d.ts.URL)
	if err != nil || !strings.Contains(out, "registered the public key SHA256:fp") {
		t.Fatalf("register: %v\n%s", err, out)
	}
	if got := d.find("PUT", "/api/v1/remotes/rmt_abc/key"); len(got) != 2 || d.body(got[1])["public_key"] != public {
		t.Errorf("key calls = %v", got)
	}
	n := len(d.all())
	if out, err = runRoot(t, "remote", "key", "register", "edge", "--public-key", "not-a-key", "--server", d.ts.URL); err == nil || len(d.all()) != n {
		t.Errorf("a malformed public key must be refused before any request: %v\n%s", err, out)
	}
}

func TestRemoteKeyRefusesWithoutRemoteIDOrOnAnOIDCRemote(t *testing.T) {
	d := newDevStub(t)
	secretRepo(t, d, "", "")
	for _, verb := range []string{"create", "rotate"} {
		out, err := runRoot(t, "remote", "key", verb, "edge", "--server", d.ts.URL)
		if err == nil || !strings.Contains(err.Error(), "remote_id is not set") {
			t.Errorf("%s with no remote_id: %v\n%s", verb, err, out)
		}
	}
	if len(d.all()) != 0 {
		t.Errorf("a refused key verb made requests: %v", d.all())
	}

	d2 := newDevStub(t)
	repo := secretRepo(t, d2, `remote_id = "rmt_abc"`, "")
	editRemote(t, repo, `label = "your-host-label"`, "label = \"your-host-label\"\n[identity.oidc]\nissuer = \"https://oidc.example\"\nsubject = \"org:app:*\"\ntoken = [\"cli\", \"token\", \"{aud}\"]")
	out, err := runRoot(t, "remote", "key", "create", "edge", "--server", d2.ts.URL)
	if err == nil || !strings.Contains(err.Error(), "workload identity") || len(d2.all()) != 0 {
		t.Errorf("a key on an oidc remote: %v\n%s\ncalls %v", err, out, d2.all())
	}
}

func TestRemoteIdentitySetAndClear(t *testing.T) {
	d := newDevStub(t)
	repo := secretRepo(t, d, `remote_id = "rmt_abc"`, "")
	editRemote(t, repo, `label = "your-host-label"`, "label = \"your-host-label\"\n[identity.oidc]\nissuer = \"https://oidc.example\"\nsubject = \"org:app:*\"\ntoken = [\"cli\", \"token\", \"{aud}\"]")

	if out, err := runRoot(t, "remote", "identity", "set", "edge", "--server", d.ts.URL); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	put := d.find("PUT", "/api/v1/remotes/rmt_abc/identity")
	if len(put) != 1 || d.body(put[0])["issuer"] != "https://oidc.example" || d.body(put[0])["subject"] != "org:app:*" {
		t.Fatalf("identity put = %v", put)
	}
	if out, err := runRoot(t, "remote", "identity", "clear", "edge", "--server", d.ts.URL); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(d.find("DELETE", "/api/v1/remotes/rmt_abc/identity")) != 1 {
		t.Errorf("calls: %v", d.all())
	}

	d.answer = func(c devCall) (int, string) { return 409, `{"error":"remote has a key"}` }
	out, err := runRoot(t, "remote", "identity", "set", "edge", "--server", d.ts.URL)
	if err == nil || !strings.Contains(err.Error(), "remote has a key") {
		t.Errorf("a refused bind must show the server's code: %v\n%s", err, out)
	}
}

// ---- remote check ----

func checkDef(t *testing.T, d *devStub, storyCommand string) string {
	t.Helper()
	tables := `
[[secret]]
key = "ABSENT"
scope = "personal"
from = "env"

[[secret]]
key = "UNGRANTED"
scope = "personal"
from = "env"

[[secret]]
key = "LAPSING"
scope = "personal"
from = "env"

[[secret]]
key = "FINE"
scope = "personal"
from = "env"
`
	repo := secretRepo(t, d, `remote_id = "rmt_abc"`, tables)
	if storyCommand != "" {
		editRemote(t, repo, "# story_command = \"your-agent-cli {story}\"", `story_command = "`+storyCommand+`"`)
	}
	soon := time.Now().Add(5 * 24 * time.Hour).UTC().Format(time.RFC3339)
	d.secrets["/api/v1/secrets"] = `[
	  {"key":"UNGRANTED","scope":"personal","has_value":true,"readers":["rmt_other"]},
	  {"key":"LAPSING","scope":"personal","has_value":true,"readers":["rmt_abc"],"expires_at":"` + soon + `"},
	  {"key":"FINE","scope":"personal","has_value":true,"readers":["rmt_abc"]}]`
	return repo
}

// AC5: check reports a secret missing, one ungranted, one near expiry and a story
// command that would not read secrets, and exits non-zero.
func TestRemoteCheckReportsEveryProblem(t *testing.T) {
	d := newDevStub(t)
	checkDef(t, d, "bash -c 'work {story}'")
	out, err := runRoot(t, "remote", "check", "edge", "--server", d.ts.URL)
	if err == nil {
		t.Fatalf("check must exit non-zero:\n%s", out)
	}
	for _, want := range []string{
		"FAIL  remote edge: secret ABSENT (personal): missing on satelle.dev",
		"FAIL  remote edge: secret UNGRANTED (personal): not granted to rmt_abc",
		"WARN  remote edge: secret LAPSING (personal): expires",
		"FAIL  remote edge: host.story_command does not run `satelle story drive`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("check lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret FINE") {
		t.Errorf("a good secret is not a finding:\n%s", out)
	}
}

func TestRemoteCheckExpiryAloneOnlyWarns(t *testing.T) {
	d := newDevStub(t)
	checkDef(t, d, "satelle story drive {story}")
	d.secrets["/api/v1/secrets"] = `[
	  {"key":"ABSENT","scope":"personal","has_value":true,"readers":["rmt_abc"]},
	  {"key":"UNGRANTED","scope":"personal","has_value":true,"readers":["rmt_abc"]},
	  {"key":"LAPSING","scope":"personal","has_value":true,"readers":["rmt_abc"],"expires_at":"` + time.Now().Add(3*24*time.Hour).UTC().Format(time.RFC3339) + `"},
	  {"key":"FINE","scope":"personal","has_value":true,"readers":["rmt_abc"]}]`
	out, err := runRoot(t, "remote", "check", "edge", "--server", d.ts.URL)
	if err != nil || !strings.Contains(out, "WARN") || strings.Contains(out, "FAIL") {
		t.Errorf("an expiry warning must not fail the check: %v\n%s", err, out)
	}
}

func TestRemoteCheckWithoutRemoteIDReportsItWithoutAnyRequest(t *testing.T) {
	d := newDevStub(t)
	secretRepo(t, d, "", "[[secret]]\nkey = \"A_B\"\nscope = \"personal\"\nfrom = \"env\"\n")
	out, err := runRoot(t, "remote", "check", "edge", "--server", d.ts.URL)
	if err == nil || !strings.Contains(out, "FAIL  remote edge: remote_id is not set") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if len(d.all()) != 0 {
		t.Errorf("check with no remote_id made requests: %v", d.all())
	}
}

func TestCheckRemoteSecretsWarnsInsideFourteenDaysOnly(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	def := remotedef.Definition{RemoteID: "rmt_abc", Secret: []remotedef.SecretDecl{{Key: "A", Scope: remotedef.ScopePersonal, From: remotedef.FromEnv}}}
	for _, tc := range []struct {
		days int
		warn bool
	}{{13, true}, {14, true}, {15, false}} {
		l := stubLister{{Key: "A", Scope: "personal", HasValue: true, Readers: []string{"rmt_abc"}, ExpiresAt: now.Add(time.Duration(tc.days) * 24 * time.Hour).Format(time.RFC3339)}}
		fs, err := checkRemoteSecrets(context.Background(), def, l, now)
		if err != nil || (len(fs) == 1) != tc.warn {
			t.Errorf("%d days: findings %v, %v", tc.days, fs, err)
		}
	}
}
