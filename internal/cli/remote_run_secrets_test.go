//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// AC4, run path: `remote run` registers exactly one run for the definition's
// remote_id and the story command runs with that run as SATELLE_SERVER_RUN_ID
// while SATELLE_RUN_ID stays the local run; the node's identity names ride along.
func TestRemoteRunExportsTheOwnersRunToTheStoryCommand(t *testing.T) {
	s := newSecretRig(t, "rmt_abc")
	dump := filepath.Join(t.TempDir(), "env")
	s.declareStoryCommand("env > " + dump + "-{story}")
	useRunSync(t, movingSync{status: "done"})
	story := newStory(t, "backlog")
	record := newStory(t, "backlog")

	out, err := runRoot(t, "remote", "run", "edge", "--server", s.server, "--story", story, "--story-of-record", record)
	if err != nil {
		t.Fatalf("remote run: %v\n%s", err, out)
	}
	if posts := s.owner.find("POST", "/api/v1/remotes/rmt_abc/runs"); len(posts) != 1 {
		t.Fatalf("want exactly one run registered, calls: %v", s.owner.all())
	}
	b, err := os.ReadFile(dump + "-" + story)
	if err != nil {
		t.Fatalf("the story command did not run: %v\n%s", err, out)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	for k, want := range map[string]string{
		"SATELLE_SERVER_RUN_ID":   "trg_run1",
		"SATELLE_REMOTE_ID":       "rmt_abc",
		"SATELLE_REMOTE_SERVER":   s.server,
		"SATELLE_REMOTE_IDENTITY": "key:SATELLE_REMOTE_KEY",
		"SATELLE_REMOTE_SECRETS":  "CLAUDE_CODE_OAUTH_TOKEN:personal",
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	if !strings.HasPrefix(env["SATELLE_RUN_ID"], "run_") || env["SATELLE_RUN_ID"] == env["SATELLE_SERVER_RUN_ID"] {
		t.Errorf("SATELLE_RUN_ID must stay the local run id, got %q", env["SATELLE_RUN_ID"])
	}
	if len(s.owner.find("DELETE", "/api/v1/remotes/rmt_abc/runs/trg_run1")) != 1 {
		t.Errorf("the run was not ended with the harness: %v", s.owner.all())
	}
}

// AC5: before the run is registered, a ttl secret whose copy on satelle.dev has
// lapsed is obtained again and pushed with expires_at = now + ttl; a fresh one is
// left alone, and nothing is written to disk to remember either.
func TestRemoteStartRefreshesAStaleTTLSecretOnly(t *testing.T) {
	s := newSecretRig(t, "rmt_abc")
	p := filepath.Join(remoteDir(s.repo, "edge"), "remote.toml")
	b, _ := os.ReadFile(p)
	tables := "\n[[secret]]\nkey = \"STALE_TTL\"\nscope = \"personal\"\nfrom = [\"sh\", \"-c\", \"printf minted-stale\"]\nttl = \"36h\"\n" +
		"\n[[secret]]\nkey = \"FRESH_TTL\"\nscope = \"personal\"\nfrom = [\"sh\", \"-c\", \"printf minted-fresh\"]\nttl = \"36h\"\n"
	if err := os.WriteFile(p, append(b, []byte(tables)...), 0o644); err != nil {
		t.Fatal(err)
	}
	s.owner.secrets["/api/v1/secrets"] = `[
	  {"key":"STALE_TTL","scope":"personal","has_value":true,"readers":["rmt_abc"],"expires_at":"2020-01-01T00:00:00Z","expired":true},
	  {"key":"FRESH_TTL","scope":"personal","has_value":true,"readers":["rmt_abc"],"expires_at":"` + time.Now().Add(30*time.Hour).UTC().Format(time.RFC3339) + `"},
	  {"key":"CLAUDE_CODE_OAUTH_TOKEN","scope":"personal","has_value":true,"readers":["rmt_abc"]}]`
	before := readTree(t, filepath.Join(s.repo, ".satelle", "config"))

	out, err := runRoot(t, "remote", "start", "edge", "--server", s.server, "--story", "sty_ttl")
	if err != nil {
		t.Fatalf("remote start: %v\n%s", err, out)
	}
	got := s.owner.find("PUT", "/api/v1/secrets/STALE_TTL")
	if len(got) != 1 || s.owner.body(got[0])["value"] != "minted-stale" {
		t.Fatalf("a lapsed ttl secret must be pushed again, calls: %v", s.owner.all())
	}
	if exp, perr := time.Parse(time.RFC3339, s.owner.body(got[0])["expires_at"]); perr != nil || time.Until(exp) < 35*time.Hour || time.Until(exp) > 37*time.Hour {
		t.Errorf("expires_at must be now + ttl: %v (%v)", s.owner.body(got[0]), perr)
	}
	if len(s.owner.find("PUT", "/api/v1/secrets/FRESH_TTL")) != 0 {
		t.Error("a fresh ttl secret must not be pushed again")
	}
	if len(s.owner.find("PUT", "/api/v1/secrets/CLAUDE_CODE_OAUTH_TOKEN")) != 0 {
		t.Error("a secret with no ttl is not refreshed by a start")
	}
	var order []string
	for _, c := range s.owner.all() {
		order = append(order, c.Method+" "+c.Path)
	}
	iPut, iRun := indexOf(order, "PUT /api/v1/secrets/STALE_TTL"), indexOf(order, "POST /api/v1/remotes/rmt_abc/runs")
	if iPut < 0 || iRun < iPut {
		t.Errorf("the refresh must precede the run's registration: %v", order)
	}
	after := readTree(t, filepath.Join(s.repo, ".satelle", "config"))
	if len(after) != len(before) {
		t.Errorf("a refresh must keep no local state: %d files before, %d after", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed during a refresh", k)
		}
	}
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// hostEnv: a [[secret]] definition with an empty host.env still gets the names a
// node reads its secrets by; one with no [[secret]] and no host.env gets none.
func TestHostEnvCarriesTheSecretNamesAndNothingElseWithoutHostEnv(t *testing.T) {
	d := newDevStub(t)
	repo := secretRepo(t, d, `remote_id = "rmt_abc"`, "[[secret]]\nkey = \"A_B\"\nscope = \"personal\"\nfrom = \"env\"\n")
	editRemote(t, repo, `label = "your-host-label"`, "label = \"your-host-label\"\nkey = \"SATELLE_REMOTE_KEY\"")
	def, err := loadRemoteDef("edge")
	if err != nil {
		t.Fatal(err)
	}
	a := &app.App{}
	env, err := hostEnv(a, def, "rmt_a", "")
	if err != nil || env == nil {
		t.Fatalf("env = %v, %v", env, err)
	}
	got := strings.Join(env, "\n")
	for _, want := range []string{"SATELLE_REMOTE_ID=rmt_a", "SATELLE_REMOTE_SECRETS=A_B:personal", "SATELLE_REMOTE_IDENTITY=key:SATELLE_REMOTE_KEY", "SATELLE_REMOTE_SERVER=" + config.ResolveHostedServer(a.PlaneConfig())} {
		if !strings.Contains(got, want) {
			t.Errorf("env lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "SATELLE_SERVER_RUN_ID") {
		t.Error("hostEnv carries no run: the run id comes from the fire or the owner's registration")
	}
	if env, _ := hostEnv(a, def, "rmt_a", "https://flag.example"); !strings.Contains(strings.Join(env, "\n"), "SATELLE_REMOTE_SERVER=https://flag.example") {
		t.Errorf("an explicit server wins: %v", env)
	}

	if env, err := hostEnv(a, remotedef.Definition{}, "", ""); err != nil || env != nil {
		t.Errorf("no [[secret]] and no host.env: env = %v, %v", env, err)
	}
}
