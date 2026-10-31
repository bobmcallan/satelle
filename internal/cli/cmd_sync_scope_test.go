package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/syncscope"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// Sync scope comes from the account's binding alone (sty_b336c2b0): the repo
// holds [sync] project and nothing else that decides scope.

// scopeLine runs `satelle sync status` against server and returns its first
// line, the resolved scope. Status may exit non-zero for an area not in step;
// the scope line is printed before any area is checked.
func scopeLine(t *testing.T, server string) string {
	t.Helper()
	cmd, buf := testCmd()
	err := runSyncStatus(cmd, server)
	line, _, _ := strings.Cut(buf.String(), "\n")
	if !strings.HasPrefix(line, "scope: ") {
		t.Fatalf("sync status printed no scope line first (%v):\n%s", err, buf.String())
	}
	return line
}

// AC3: binding, rebinding, and the account joining or leaving the project's team
// each change what the next sync resolves, with no file edit beyond the bind's
// own [sync] project.
func TestSyncScopeFollowsBindingAndMembership(t *testing.T) {
	testutil.IsolateHome(t)
	ts, _ := newSnapServer(t)
	signInTo(t, ts.URL)
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}
	acct := &testAccount{fallback: kindMissing, kinds: map[string]string{"solo": kindPersonal, "acme": kindTeam}}
	useAccount(t, acct)
	repo := syncConfigRepo(t, "[review]\ngate_create = false\n")
	pointAt(t, repo)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	setMember := func(kind string) {
		acct.mu.Lock()
		acct.kinds["acme"] = kind
		acct.mu.Unlock()
	}
	bind := func(slug string) {
		t.Helper()
		cmd, buf := testCmd()
		if err := runProjectBind(cmd, slug); err != nil {
			t.Fatalf("project bind %s: %v\n%s", slug, err, buf.String())
		}
		cfg, _, err := config.Load(cfgPath)
		if err != nil || cfg.SyncProject() != slug {
			t.Fatalf("bind %s: [sync] project = %q (%v)", slug, cfg.SyncProject(), err)
		}
		if found, err := config.FindSyncScopeKeys(filepath.Dir(cfgPath)); err != nil || len(found) != 0 {
			t.Fatalf("bind %s wrote a scope key: %v (%v)", slug, found, err)
		}
	}
	read := func() string {
		t.Helper()
		b, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	steps := []struct {
		name   string
		change func()
		want   string
	}{
		{"unbound", func() {}, "scope: local — " + syncscope.ReasonUnbound},
		{"bind a personal-team project", func() { bind("solo") },
			"scope: personal — account " + testAccountEmail + ", team Ada (personal), project solo"},
		{"rebind to a team project", func() { bind("acme") },
			"scope: team — account " + testAccountEmail + ", team Acme (w-team), project acme"},
		{"leave the team", func() { setMember(kindForeign) },
			"scope: local — " + testAccountEmail + " is not a member of project acme's team"},
		{"rejoin the team", func() { setMember(kindTeam) },
			"scope: team — account " + testAccountEmail + ", team Acme (w-team), project acme"},
	}
	for _, s := range steps {
		before := read()
		s.change()
		after := read()
		if got := scopeLine(t, ts.URL); got != s.want {
			t.Errorf("%s: scope line = %q, want %q", s.name, got, s.want)
		}
		if read() != after {
			t.Errorf("%s: sync status edited satelle.toml", s.name)
		}
		if strings.HasPrefix(s.name, "leave") || strings.HasPrefix(s.name, "rejoin") {
			if after != before {
				t.Errorf("%s: a membership change edited satelle.toml:\n%s", s.name, after)
			}
		}
	}
}

// AC5: status names the resolved scope and where it came from — the account,
// the team and the project — and a local scope says why, asking the account
// nothing when the repo is unbound or the machine is signed out.
func TestSyncStatusNamesScopeSource(t *testing.T) {
	for _, c := range []struct {
		name     string
		kind     string
		bound    bool
		signedIn bool
		want     []string
		noCalls  bool
	}{
		{"team", kindTeam, true, true, []string{"scope: team", "account " + testAccountEmail, "team Acme (w-team)", "project probe"}, false},
		{"personal", kindPersonal, true, true, []string{"scope: personal", "account " + testAccountEmail, "team Ada (personal)", "project probe"}, false},
		{"unbound", kindTeam, false, true, []string{"scope: local — " + syncscope.ReasonUnbound}, true},
		{"signed out", kindTeam, true, false, []string{"scope: local — " + syncscope.ReasonSignedOut}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			testutil.IsolateHome(t)
			ts, _ := newSnapServer(t)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if c.signedIn {
				seedCred(t, ts.URL)
			}
			acct := useScope(t, c.kind)
			toml := "[review]\ngate_create = false\n"
			if c.bound {
				toml = snapToml
			}
			pointAt(t, syncConfigRepo(t, toml))
			line := scopeLine(t, ts.URL)
			for _, w := range c.want {
				if !strings.Contains(line, w) {
					t.Errorf("scope line %q does not carry %q", line, w)
				}
			}
			if c.noCalls && acct.callCount() != 0 {
				t.Errorf("a %s status asked the account %d time(s)", c.name, acct.callCount())
			}
		})
	}
}
