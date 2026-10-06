package cli

import (
	"os/exec"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// Where the satelle user comes from.
const (
	userSourceAccount = "account"
	userSourceGit     = "git"
)

// signedInIdentityHint is what the header shows for a signed-in credential
// that carries neither an email nor a display name. The account is the only
// identity once signed in, so the git user is never substituted; logging in
// again re-stamps the fields.
const signedInIdentityHint = `signed in — run "satelle login" to refresh identity`

// userIdentity is the one satelle user (sty_e698d914). Local-only (no
// credential for the configured hosted server) there is no sync or comms, so
// the user IS the repo's git user. Signed in, the online account is the user
// and the git user is repo configuration, never presented as the user.
type userIdentity struct {
	Email       string
	DisplayName string
	PrincipalID string
	Source      string
}

// SignedIn reports whether the user is the online account.
func (u userIdentity) SignedIn() bool { return u.Source == userSourceAccount }

// Display is the name the header shows for the user.
func (u userIdentity) Display() string {
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
		if cred, err := (hosted.FileStore{}).Load(server); err == nil {
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

func gitConfigEmail(repoRoot string) string {
	cmd := exec.Command("git", "config", "--get", "user.email")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
