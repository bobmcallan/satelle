//go:build !windows

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const secretTables = `
[[secret]]
key = "CLAUDE_CODE_OAUTH_TOKEN"
scope = "personal"
from = "env"
expires = "2030-06-01"

[[secret]]
key = "MINTED"
scope = "personal"
from = ["sh", "-c", "printf 'minted-value'"]
ttl = "36h"

[[secret]]
key = "TEAM_KEY"
scope = "team"
workspace = "` + secretWS + `"
from = "env"
`

const secretDotEnv = "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-FROM-DOTENV\nTEAM_KEY=team-value-9\n"

// AC2: push sends each declared secret under its scope, with its declared
// lifetime, and grants it to the remote_id of the definition.
func TestSecretPushSendsAndGrantsEachDeclaredSecret(t *testing.T) {
	d := newDevStub(t)
	repo := secretRepo(t, d, `remote_id = "rmt_abc"`, secretTables)
	writeDotEnvFile(t, repo, secretDotEnv)
	// [vars] is not a secrets source: a value there must never be pushed.
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "from-process-env")

	before := time.Now()
	out, err := runRoot(t, "secret", "push", "edge", "--server", d.ts.URL)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}

	personal := d.find("PUT", "/api/v1/secrets/CLAUDE_CODE_OAUTH_TOKEN")
	if len(personal) != 1 {
		t.Fatalf("personal put calls = %v", d.all())
	}
	b := d.body(personal[0])
	if b["value"] != "sk-ant-oat01-FROM-DOTENV" || b["expires_at"] != "2030-06-01T00:00:00Z" {
		t.Errorf("personal body = %v (the value must come from .env)", b)
	}
	if personal[0].Auth != "Bearer good" {
		t.Errorf("the owner's login must authenticate the push, got %q", personal[0].Auth)
	}

	minted := d.body(d.find("PUT", "/api/v1/secrets/MINTED")[0])
	exp, err := time.Parse(time.RFC3339, minted["expires_at"])
	if minted["value"] != "minted-value" || err != nil || exp.Before(before.Add(36*time.Hour-time.Minute)) || exp.After(time.Now().Add(36*time.Hour+time.Minute)) {
		t.Errorf("a ttl secret is pushed with expires_at = now + ttl: %v (%v)", minted, err)
	}

	team := d.find("PUT", "/api/v1/workspaces/"+secretWS+"/secrets/TEAM_KEY")
	if len(team) != 1 || d.body(team[0])["value"] != "team-value-9" || strings.Contains(team[0].Body, "expires_at") {
		t.Errorf("team put = %v", team)
	}

	for _, p := range []string{
		"/api/v1/secrets/CLAUDE_CODE_OAUTH_TOKEN/grants/rmt_abc",
		"/api/v1/secrets/MINTED/grants/rmt_abc",
		"/api/v1/workspaces/" + secretWS + "/secrets/TEAM_KEY/grants/rmt_abc",
	} {
		if len(d.find("PUT", p)) != 1 {
			t.Errorf("no grant at %s; calls: %v", p, d.all())
		}
	}
	for _, v := range []string{"sk-ant-oat01-FROM-DOTENV", "minted-value", "team-value-9", "from-process-env"} {
		if strings.Contains(out, v) {
			t.Errorf("push printed a value: %q", out)
		}
	}
	if !strings.Contains(out, "pushed CLAUDE_CODE_OAUTH_TOKEN (personal), granted to rmt_abc") {
		t.Errorf("output:\n%s", out)
	}
}

// A server refusal is printed as the server's own code and reason against the
// secret's key; it is not a trigger rejection.
func TestSecretPushPrintsTheServersRefusal(t *testing.T) {
	d := newDevStub(t)
	d.answer = func(c devCall) (int, string) {
		if c.Method == "PUT" && strings.Contains(c.Path, "/grants/") {
			return 403, `{"error":"refused","reason":"personal_rules"}`
		}
		return 0, ""
	}
	repo := secretRepo(t, d, `remote_id = "rmt_abc"`, "[[secret]]\nkey = \"CLAUDE_CODE_OAUTH_TOKEN\"\nscope = \"personal\"\nfrom = \"env\"\n")
	writeDotEnvFile(t, repo, secretDotEnv)
	out, err := runRoot(t, "secret", "push", "edge", "--server", d.ts.URL)
	if err == nil {
		t.Fatalf("a refused grant must fail the push:\n%s", out)
	}
	if !strings.Contains(out, "secret CLAUDE_CODE_OAUTH_TOKEN: refused: personal_rules") {
		t.Errorf("want the server's code and reason against the key:\n%s", out)
	}
	if strings.Contains(out, "trigger") {
		t.Errorf("a secret refusal must not read as a trigger rejection:\n%s", out)
	}
}

func TestSecretPushWithoutRemoteIDRefusesBeforeAnyRequest(t *testing.T) {
	d := newDevStub(t)
	secretRepo(t, d, "", secretTables)
	out, err := runRoot(t, "secret", "push", "edge", "--server", d.ts.URL)
	if err == nil || !strings.Contains(err.Error(), "remote edge: remote_id is not set; register the remote on satelle.dev, then set remote_id") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if n := len(d.all()); n != 0 {
		t.Errorf("a refused push made %d request(s)", n)
	}
}

func TestSecretPushReportsAMissingEnvValueWithoutPushingIt(t *testing.T) {
	d := newDevStub(t)
	repo := secretRepo(t, d, `remote_id = "rmt_abc"`, "[[secret]]\nkey = \"CLAUDE_CODE_OAUTH_TOKEN\"\nscope = \"personal\"\nfrom = \"env\"\n")
	writeDotEnvFile(t, repo, "OTHER=1\n")
	out, err := runRoot(t, "secret", "push", "edge", "--server", d.ts.URL)
	if err == nil || !strings.Contains(out, "CLAUDE_CODE_OAUTH_TOKEN has no value in") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if len(d.all()) != 0 {
		t.Errorf("nothing may be sent for a secret with no value: %v", d.all())
	}
}

// AC2: list shows key, scope, readers and expiry; its decode has no value field.
func TestSecretListShowsNoValue(t *testing.T) {
	d := newDevStub(t)
	d.secrets["/api/v1/secrets"] = `[{"key":"CLAUDE_CODE_OAUTH_TOKEN","scope":"personal","has_value":true,"readers":["rmt_abc"],"expires_at":"2030-06-01T00:00:00Z","value":"leaked-by-a-broken-server"},{"key":"OLD","scope":"personal","has_value":true,"readers":[],"expires_at":"2020-01-01T00:00:00Z","expired":true}]`
	secretRepo(t, d, `remote_id = "rmt_abc"`, "")
	out, err := runRoot(t, "secret", "list", "--server", d.ts.URL)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"KEY", "SCOPE", "READERS", "EXPIRES", "CLAUDE_CODE_OAUTH_TOKEN", "personal", "rmt_abc", "2030-06-01T00:00:00Z", "OLD", "(expired)"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "leaked-by-a-broken-server") {
		t.Errorf("list printed a value:\n%s", out)
	}
}

// runSecretSource: a cancelled context kills the whole process group, a child that
// started a grandchild included, as host commands are.
func TestRunSecretSourceKillsTheProcessGroupOnCancel(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, err := runSecretSource(ctx, []string{"sh", "-c", `sleep 300 & echo $! > "$0"; wait`, pidFile}, t.TempDir())
	if err == nil {
		t.Fatal("a cancelled source must fail")
	}
	b, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("the grandchild never started: %v", rerr)
	}
	var pid int
	if _, serr := fmt.Sscan(strings.TrimSpace(string(b)), &pid); serr != nil || pid <= 0 {
		t.Fatalf("pid file = %q", b)
	}
	eventuallyCli(t, "the grandchild to be killed", func() bool {
		return syscall.Kill(pid, 0) != nil
	})
}

func TestRunSecretSourceTrimsNewlineAndRefusesEmptyOutput(t *testing.T) {
	v, err := runSecretSource(context.Background(), []string{"sh", "-c", "printf 'abc\\n'"}, t.TempDir())
	if err != nil || v != "abc" {
		t.Errorf("value = %q, %v", v, err)
	}
	if _, err := runSecretSource(context.Background(), []string{"true"}, t.TempDir()); err == nil {
		t.Error("a command that prints nothing has no value")
	}
	_, err = runSecretSource(context.Background(), []string{"sh", "-c", "echo sekrit >&2; exit 3"}, t.TempDir())
	if err == nil || strings.Contains(err.Error(), "sekrit") {
		t.Errorf("a failing source must fail without its output: %v", err)
	}
}
