package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

const (
	accessSentinel  = "ACCESS_STATUS_SENTINEL"
	refreshSentinel = "REFRESH_STATUS_SENTINEL"
)

// statusIdentity runs satelle status in an isolated home against an unroutable
// hosted server and returns the full output and its identity row. The server is
// never reachable, so a passing run also shows the row needs no network. token
// is the SATELLE_TOKEN value ("" leaves it unset); cred, when set, is saved as
// the stored login for that server.
func statusIdentity(t *testing.T, token string, cred *hosted.Credential) (out, row, server string) {
	t.Helper()
	tempRepo(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(hosted.SessionTokenEnv, token)
	server = "http://127.0.0.1:1"
	if err := config.SaveGlobalHostedServer(server); err != nil {
		t.Fatal(err)
	}
	if cred != nil {
		c := *cred
		c.ServerURL, c.AccessToken, c.RefreshToken = server, accessSentinel, refreshSentinel
		if err := (hosted.FileStore{}).Save(c); err != nil {
			t.Fatal(err)
		}
	}
	stubHealthz(t, false)
	out, err := runRoot(t, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "identity") {
			return out, line, server
		}
	}
	t.Fatalf("status has no identity row:\n%s", out)
	return out, "", server
}

func assertAbsent(t *testing.T, out string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if s != "" && strings.Contains(out, s) {
			t.Errorf("output leaks %q:\n%s", s, out)
		}
	}
}

// TestStatusIdentitySessionToken (sty_e82d5f49 AC1, AC4, AC5): a session token
// is named as the source, beside the hosted rows, and never printed.
func TestStatusIdentitySessionToken(t *testing.T) {
	const token = "sat_pat_STATUS_SENTINEL"
	out, row, server := statusIdentity(t, token, nil)
	if want := "session token (SATELLE_TOKEN)"; !strings.Contains(row, want) {
		t.Errorf("identity row = %q, want %q", row, want)
	}
	if i, j := strings.Index(out, "hosted project"), strings.Index(out, "identity"); i < 0 || j < i {
		t.Errorf("identity row is not beside the hosted rows:\n%s", out)
	}
	cred, err := hosted.DefaultStore().Load(server)
	if err != nil {
		t.Fatal(err)
	}
	assertAbsent(t, out, token, hosted.SessionFingerprint(cred))
}

// TestStatusIdentityStoredLogin (AC2, AC4): with no token, a stored login for
// the hosted server is the source, and its tokens and account are not printed.
func TestStatusIdentityStoredLogin(t *testing.T) {
	out, row, _ := statusIdentity(t, "", &hosted.Credential{Email: "acct@example.test", PrincipalID: "P1"})
	if want := "stored login (satelle login)"; !strings.Contains(row, want) {
		t.Errorf("identity row = %q, want %q", row, want)
	}
	assertAbsent(t, out, accessSentinel, refreshSentinel, "acct@example.test")
}

// TestStatusIdentityLocalOnly (AC3): neither a token nor a login is local-only
// and names the way to sign in.
func TestStatusIdentityLocalOnly(t *testing.T) {
	_, row, _ := statusIdentity(t, "", nil)
	for _, want := range []string{"none", "local-only", "satelle login"} {
		if !strings.Contains(row, want) {
			t.Errorf("identity row = %q, missing %q", row, want)
		}
	}
}

// TestIdentitySourceValue pins the fixed text per source; none of it carries
// the principal, email or fingerprint a userIdentity holds.
func TestIdentitySourceValue(t *testing.T) {
	u := userIdentity{Email: "e@x", PrincipalID: "P", SessionFP: "FP0123456789"}
	for source, want := range map[string]string{
		userSourceSession: "session token (SATELLE_TOKEN)",
		userSourceAccount: "stored login (satelle login)",
		userSourceGit:     "none — local-only (git user); run satelle login",
	} {
		u.Source = source
		got := identitySourceValue(u)
		if got != want {
			t.Errorf("%s = %q, want %q", source, got, want)
		}
		assertAbsent(t, got, u.Email, u.PrincipalID, u.SessionFP)
	}
}
