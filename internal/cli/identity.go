package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// Where the satelle user comes from.
const (
	userSourceAccount = "account"
	userSourceGit     = "git"
	// userSourceSession is a non-interactive session acting through the
	// SATELLE_TOKEN session token (sty_6ed6318d). It never falls back to a stored
	// login or the git user: the token is the only credential while it is set.
	userSourceSession = "session"
)

// sessionUnresolved is what the footer, `satelle project status` and
// `satelle whoami` say while a session token's user is not yet known.
const sessionUnresolved = "session token, user not yet resolved"

// signedInIdentityHint is what the header shows for a signed-in credential
// that carries neither an email nor a display name. The account is the only
// identity once signed in, so the git user is never substituted; logging in
// again re-stamps the fields.
const signedInIdentityHint = `signed in — run "satelle login" to refresh identity`

// userIdentity is the one satelle user (sty_e698d914). Local-only (no
// credential for the configured hosted server) there is no sync or comms, so
// the user IS the repo's git user. Signed in, the online account is the user
// and the git user is repo configuration, never presented as the user.
//
// Under a session token the user is the token's principal, learned from the
// location registration and cached locally. Until it is known PrincipalID is
// empty, so Holder is empty and an engage is refused rather than passing the
// wrong-holder guard unnoticed; Actor is a non-empty `session:<fp8>` and Display
// says the user is not yet resolved — never blank, never "signed out".
type userIdentity struct {
	Email       string
	DisplayName string
	PrincipalID string
	Source      string
	// SessionFP is the one-way fingerprint of the session token (session only).
	SessionFP string
}

// SignedIn reports whether the user is the online account, or the user a
// session token acts as.
func (u userIdentity) SignedIn() bool {
	return u.Source == userSourceAccount || u.Source == userSourceSession
}

// IsSession reports whether the user is acting through a session token.
func (u userIdentity) IsSession() bool { return u.Source == userSourceSession }

// Unresolved reports a session token whose user is not yet known.
func (u userIdentity) Unresolved() bool { return u.IsSession() && u.PrincipalID == "" }

// Display is the name the header shows for the user.
func (u userIdentity) Display() string {
	if u.IsSession() {
		if u.Unresolved() {
			return sessionUnresolved + ` (run "satelle whoami")`
		}
		return "user " + u.PrincipalID + " (session token)"
	}
	if !u.SignedIn() {
		return u.Email
	}
	switch {
	case u.Email != "":
		return u.Email
	case u.DisplayName != "":
		return u.DisplayName
	}
	return signedInIdentityHint
}

// Actor is the person-valued attribution for a ledger row: the account
// PrincipalID when signed in, the git email when local-only. Empty when the
// source holds nothing.
func (u userIdentity) Actor() string {
	if u.Unresolved() {
		return "session:" + u.SessionFP[:min(8, len(u.SessionFP))]
	}
	if u.SignedIn() {
		return u.PrincipalID
	}
	return u.Email
}

// Holder is the engagement holder id: the account PrincipalID when signed in,
// empty when local-only (no holder offline).
func (u userIdentity) Holder() string {
	if u.SignedIn() {
		return u.PrincipalID
	}
	return ""
}

// resolveUser decides who the satelle user is for the repo's configured hosted
// server. A local file read plus at most one git call — never the network.
func resolveUser(cfg config.Config, repoRoot string) userIdentity {
	return resolveUserFor(config.ResolveHostedServer(cfg), repoRoot)
}

// resolveUserFor is resolveUser for an explicit hosted server. Signed in means
// a credential loads for that server; whether its identity fields are filled
// has no bearing on that.
func resolveUserFor(server, repoRoot string) userIdentity {
	if server != "" {
		if cred, err := hosted.DefaultStore().Load(server); err == nil {
			if cred.IsSession() {
				id, _ := hosted.SessionPrincipal(cred)
				return userIdentity{PrincipalID: id, Source: userSourceSession, SessionFP: hosted.SessionFingerprint(cred)}
			}
			return userIdentity{
				Email:       cred.Email,
				DisplayName: cred.DisplayName,
				PrincipalID: cred.PrincipalID,
				Source:      userSourceAccount,
			}
		}
	}
	return userIdentity{Email: gitConfigEmail(repoRoot), Source: userSourceGit}
}

// ensureSessionPrincipal is the engage guard (verb.SetEngageGuard). Under a
// session token whose user is not yet known it resolves it through the
// location registration — the one network call an engage under a session token
// adds — and refuses the engage, naming how to resolve it, when that fails.
// Anything else (no session token, user already known) passes.
func ensureSessionPrincipal(ctx context.Context, cfg config.Config, repoRoot string) error {
	server := config.ResolveHostedServer(cfg)
	if !resolveUserFor(server, repoRoot).Unresolved() {
		return nil
	}
	client := hosted.NewClient(server, hosted.DefaultStore(), nil)
	if _, err := hosted.ResolveSessionPrincipal(ctx, client, repoRoot, server); err != nil {
		return fmt.Errorf("%s — run \"satelle whoami\" (needs network to %s): %w", sessionUnresolved, server, err)
	}
	return nil
}

func gitConfigEmail(repoRoot string) string {
	cmd := exec.Command("git", "config", "--get", "user.email")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
