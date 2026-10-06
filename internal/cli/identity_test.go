package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/store"
)

const (
	testGitEmail  = "git.author@example.test"
	testAcctEmail = "account@example.test"
)

// identityRig isolates the credential store and global config, and returns a
// git repo whose user.email is testGitEmail. The hosted server resolves to
// config.DefaultHostedServer (no global config in the isolated SATELLE_HOME).
func identityRig(t *testing.T) (repo, server string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SATELLE_HOME", t.TempDir())
	repo = t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", testGitEmail}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo, config.DefaultHostedServer
}

func saveCred(t *testing.T, server string, c hosted.Credential) {
	t.Helper()
	c.ServerURL, c.AccessToken, c.RefreshToken = server, "a", "r"
	if err := (hosted.FileStore{}).Save(c); err != nil {
		t.Fatal(err)
	}
}

// AC1: local-only is the git user; signed in is the account, never the git user,
// even when the identity fields are empty.
func TestResolveUser(t *testing.T) {
	repo, server := identityRig(t)

	u := resolveUser(config.Config{}, repo)
	if u.Source != userSourceGit || u.Email != testGitEmail || u.SignedIn() {
		t.Fatalf("local-only = %+v, want git source with %s", u, testGitEmail)
	}

	saveCred(t, server, hosted.Credential{Email: testAcctEmail, DisplayName: "Acct", PrincipalID: "P1"})
	u = resolveUser(config.Config{}, repo)
	if u.Source != userSourceAccount || u.Email != testAcctEmail || u.PrincipalID != "P1" {
		t.Fatalf("signed in = %+v, want the account %s (not the git author)", u, testAcctEmail)
	}

	saveCred(t, server, hosted.Credential{PrincipalID: "P2"})
	u = resolveUser(config.Config{}, repo)
	if u.Source != userSourceAccount || u.PrincipalID != "P2" || u.Email != "" || u.DisplayName != "" {
		t.Fatalf("signed in, empty identity = %+v, want account source, PrincipalID P2, no email (no git fallback)", u)
	}
}

// AC2: the header text follows the resolver and a signed-in credential never
// shows the git author.
func TestUserDisplay(t *testing.T) {
	repo, server := identityRig(t)
	cases := []struct {
		name string
		cred *hosted.Credential
		want string
	}{
		{"local-only", nil, testGitEmail},
		{"email", &hosted.Credential{Email: testAcctEmail, DisplayName: "Acct"}, testAcctEmail},
		{"display name", &hosted.Credential{DisplayName: "Acct"}, "Acct"},
		{"empty identity", &hosted.Credential{}, signedInIdentityHint},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.cred != nil {
				saveCred(t, server, *c.cred)
			}
			got := resolveUser(config.Config{}, repo).Display()
			if got != c.want {
				t.Fatalf("Display = %q, want %q", got, c.want)
			}
			if c.cred != nil && strings.Contains(got, testGitEmail) {
				t.Fatalf("signed-in Display %q shows the git author", got)
			}
		})
	}
}

// AC2: the snapshot's identity meta carries the resolver's user.
func TestBuildUISnapshotIdentityFollowsUser(t *testing.T) {
	repo, server := identityRig(t)
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a := &app.App{Config: config.Config{}, RepoRoot: repo, Store: db}

	footer := func() string {
		snap, err := buildUISnapshot(context.Background(), a)
		if err != nil {
			t.Fatal(err)
		}
		var id struct {
			FooterEmail string `json:"footer_email"`
		}
		if err := json.Unmarshal(snap.Identity, &id); err != nil {
			t.Fatal(err)
		}
		return id.FooterEmail
	}

	if got := footer(); got != testGitEmail {
		t.Fatalf("local-only footer = %q, want %q", got, testGitEmail)
	}
	saveCred(t, server, hosted.Credential{Email: testAcctEmail})
	if got := footer(); got != testAcctEmail {
		t.Fatalf("signed-in footer = %q, want the account %q, never the git author", got, testAcctEmail)
	}
	saveCred(t, server, hosted.Credential{})
	if got := footer(); got != signedInIdentityHint {
		t.Fatalf("signed-in, empty identity footer = %q, want %q", got, signedInIdentityHint)
	}
}

// AC5: the `project status` sign-in line is the resolver's source, so the CLI
// and the header agree — including for a credential with empty identity fields.
func TestProjectShowSignInLineFollowsResolver(t *testing.T) {
	repo, server := identityRig(t)
	dir := filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := filepath.Join(dir, "satelle.toml")
	if err := os.WriteFile(toml, []byte("data_dir = \".satelle\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", toml)
	line := func() string {
		cmd, buf := testCmd()
		if err := runProjectShow(cmd, server); err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.HasPrefix(l, "sign-in state: ") {
				return strings.TrimPrefix(l, "sign-in state: ")
			}
		}
		t.Fatalf("no sign-in line in %q", buf.String())
		return ""
	}
	want := func() string {
		if resolveUserFor(server, repo).SignedIn() {
			return "signed in"
		}
		return "signed out"
	}
	if got := line(); got != "signed out" || got != want() {
		t.Fatalf("local-only line %q, resolver says %q", got, want())
	}
	saveCred(t, server, hosted.Credential{})
	if got := line(); got != "signed in" || got != want() {
		t.Fatalf("signed-in (empty identity) line %q, resolver says %q", got, want())
	}
}

// AC3 + AC4: the holder is the account PrincipalID when signed in and empty
// local-only (including a signed-in credential with an empty PrincipalID); the
// ledger actor is the PrincipalID signed in and the git email local-only.
func TestUserHolderAndActor(t *testing.T) {
	repo, server := identityRig(t)

	u := resolveUser(config.Config{}, repo)
	if u.Holder() != "" || u.Actor() != testGitEmail {
		t.Fatalf("local-only holder %q actor %q, want empty and %q", u.Holder(), u.Actor(), testGitEmail)
	}

	saveCred(t, server, hosted.Credential{Email: testAcctEmail, PrincipalID: "P1"})
	u = resolveUser(config.Config{}, repo)
	if u.Holder() != "P1" || u.Actor() != "P1" {
		t.Fatalf("signed in holder %q actor %q, want P1 for both", u.Holder(), u.Actor())
	}

	saveCred(t, server, hosted.Credential{Email: testAcctEmail})
	u = resolveUser(config.Config{}, repo)
	if u.Holder() != "" || u.Actor() != "" {
		t.Fatalf("signed in, no PrincipalID holder %q actor %q, want empty (never the git email)", u.Holder(), u.Actor())
	}
}
