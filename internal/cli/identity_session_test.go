package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

const cliSessionToken = "sat_pat_cli_session_secret"

// sessionRig isolates the home, points the machine server at a fake whose
// /api/v1/me hit count is observable (it must stay zero) and whose location
// registration either answers with userID or, when userID is "", 401s; seeds a
// stored login for a DIFFERENT user; and sets SATELLE_TOKEN.
type sessionRig struct {
	repo, server string
	meHits       atomic.Int32
	bearers      []string
	ts           *httptest.Server
}

func newSessionRig(t *testing.T, userID string) *sessionRig {
	t.Helper()
	repo, _ := identityRig(t)
	xdg := os.Getenv("XDG_CONFIG_HOME")
	hosted.LocationStatePathOverride = filepath.Join(xdg, "satelle", "location-state.json")
	t.Cleanup(func() { hosted.LocationStatePathOverride = "" })

	rig := &sessionRig{repo: repo}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) { rig.meHits.Add(1) })
	mux.HandleFunc("POST /api/v1/locations", func(w http.ResponseWriter, r *http.Request) {
		rig.bearers = append(rig.bearers, r.Header.Get("Authorization"))
		if userID == "" {
			http.Error(w, "refused", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "loc", "user_id": userID})
	})
	rig.ts = httptest.NewServer(mux)
	t.Cleanup(rig.ts.Close)
	rig.server = rig.ts.URL
	if err := config.SaveGlobalHostedServer(rig.server); err != nil {
		t.Fatal(err)
	}
	saveCred(t, rig.server, hosted.Credential{Email: "other@x", PrincipalID: "u_other"})
	t.Setenv(hosted.SessionTokenEnv, cliSessionToken)
	return rig
}

func (r *sessionRig) mustNotHaveCalledMe(t *testing.T) {
	t.Helper()
	if n := r.meHits.Load(); n != 0 {
		t.Errorf("/api/v1/me hit %d times; a session token must never use it", n)
	}
}

// Unknown state: never blank, never "signed out", never the stale stored login.
func TestSessionIdentityUnresolved(t *testing.T) {
	rig := newSessionRig(t, "u_123")
	u := resolveUser(config.Config{}, rig.repo)

	if !u.IsSession() || !u.SignedIn() || !u.Unresolved() {
		t.Fatalf("identity = %+v, want an unresolved session", u)
	}
	if got := u.Display(); !strings.Contains(got, "session token, user not yet resolved") {
		t.Errorf("Display = %q", got)
	}
	if got := u.Actor(); !strings.HasPrefix(got, "session:") || len(got) != len("session:")+8 || strings.Contains(got, cliSessionToken) {
		t.Errorf("Actor = %q, want session:<fp8>", got)
	}
	if got := u.Holder(); got != "" {
		t.Errorf("Holder = %q, want empty while unresolved", got)
	}
	if strings.Contains(u.Display(), "other@x") || u.PrincipalID == "u_other" {
		t.Errorf("stale stored login leaked into the session identity: %+v", u)
	}

	cmd, buf := testCmd()
	repoConfig(t, "")
	if err := runProjectShow(cmd, ""); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "sign-in state: session token, user not yet resolved") {
		t.Errorf("project status = %q", out)
	}
	rig.mustNotHaveCalledMe(t)
}

// The engage guard refuses while the user cannot be resolved, naming the way
// out — with a refusing server and with no server reachable.
func TestEnsureSessionPrincipalRefusesWhileUnresolved(t *testing.T) {
	rig := newSessionRig(t, "")
	err := ensureSessionPrincipal(context.Background(), config.Config{}, rig.repo)
	if err == nil || !strings.Contains(err.Error(), "session token, user not yet resolved") || !strings.Contains(err.Error(), "satelle whoami") {
		t.Fatalf("refusing server: err = %v", err)
	}
	rig.ts.Close()
	err = ensureSessionPrincipal(context.Background(), config.Config{}, rig.repo)
	if err == nil || !strings.Contains(err.Error(), "satelle whoami") {
		t.Fatalf("unreachable server: err = %v", err)
	}
	rig.mustNotHaveCalledMe(t)
}

func TestWhoamiSessionUnresolved(t *testing.T) {
	rig := newSessionRig(t, "")
	cmd, buf := testCmd()
	err := runWhoami(cmd, rig.server)
	if err == nil {
		t.Fatal("whoami with an unresolvable session token must exit non-zero")
	}
	msg := err.Error() + buf.String()
	if !strings.Contains(msg, "session token, user not yet resolved") || !strings.Contains(msg, "refused") {
		t.Errorf("whoami = %q, want the unresolved text and the refusal reason", msg)
	}
	p, _ := hosted.SessionIdentityPath()
	if _, serr := os.Stat(p); !os.IsNotExist(serr) {
		t.Errorf("a session-identity entry was written for an unresolved user: %v", serr)
	}
	rig.mustNotHaveCalledMe(t)
}

// Known state: whoami resolves through the registration, then every surface
// reports that user and the session, and the wrong-holder guard has a holder.
func TestSessionIdentityKnown(t *testing.T) {
	rig := newSessionRig(t, "u_123")

	cmd, buf := testCmd()
	if err := runWhoami(cmd, rig.server); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "session token") || !strings.Contains(out, "u_123") {
		t.Errorf("whoami = %q", out)
	}

	u := resolveUser(config.Config{}, rig.repo)
	if u.Unresolved() || u.Holder() != "u_123" || u.Actor() != "u_123" {
		t.Fatalf("identity = %+v, want Holder and Actor u_123", u)
	}
	if got := u.Display(); got != "user u_123 (session token)" {
		t.Errorf("Display = %q", got)
	}

	cmd, buf = testCmd()
	repoConfig(t, "")
	if err := runProjectShow(cmd, ""); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "sign-in state: session token (user u_123)") {
		t.Errorf("project status = %q", out)
	}

	// Resolved, so the engage guard passes without another call.
	n := len(rig.bearers)
	if err := ensureSessionPrincipal(context.Background(), config.Config{}, rig.repo); err != nil {
		t.Errorf("guard refused a resolved session: %v", err)
	}
	if len(rig.bearers) != n {
		t.Error("guard re-resolved a known principal")
	}

	// The token is never on disk: not in the cache, not in the credentials file.
	for _, getPath := range []func() (string, error){hosted.SessionIdentityPath, hosted.CredentialsPath} {
		p, _ := getPath()
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), cliSessionToken) {
			t.Errorf("%s contains the session token", p)
		}
	}
	rig.mustNotHaveCalledMe(t)

	// A new token (another user, perhaps) is not resolved to the cached one.
	t.Setenv(hosted.SessionTokenEnv, "sat_pat_a_different_token")
	if !resolveUser(config.Config{}, rig.repo).Unresolved() {
		t.Error("a different token resolved to the earlier principal")
	}
}

// With the token unset the stored login is the account, as before.
func TestSessionTokenUnsetKeepsAccountIdentity(t *testing.T) {
	rig := newSessionRig(t, "u_123")
	t.Setenv(hosted.SessionTokenEnv, "")
	u := resolveUser(config.Config{}, rig.repo)
	if u.Source != userSourceAccount || u.PrincipalID != "u_other" || u.IsSession() {
		t.Fatalf("identity = %+v, want the stored account", u)
	}
}
