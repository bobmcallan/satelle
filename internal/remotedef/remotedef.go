// Package remotedef holds the part of a remote definition that a node still
// reads: the secrets the remote declares and how it proves itself to satelle.dev
// when it reads one. It runs nothing, opens nothing and holds no credential.
//
// Remotes are defined, validated, published, secreted, launched and run through
// satelle-server; the CLI keeps no remote control plane, so nothing here loads or
// checks a definition file.
package remotedef

import (
	"encoding/json"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// The two scopes a secret is stored under on satelle.dev.
const (
	ScopePersonal = "personal"
	ScopeTeam     = "team"
)

// FromEnv is the value of a secret's From that reads its key from the environment.
const FromEnv = "env"

// SecretDecl is one secret a remote reads from satelle.dev. Key is the name it is
// stored and read under; Scope is ScopePersonal or ScopeTeam; From says where its
// value comes from (FromEnv, or an argv that prints it); Workspace is the team
// workspace id, for team scope only.
type SecretDecl struct {
	Key       string
	Scope     string
	From      any
	Workspace string
}

// OIDCIdentity is a platform workload identity: the remote proves itself with a
// short-lived token the platform issues instead of a key. Token is the argv that
// prints one for an audience, with PlaceholderAud standing for it.
type OIDCIdentity struct {
	Issuer  string
	Subject string
	Token   []string
}

// PlaceholderAud is the argv token an identity's token command carries where the
// audience goes.
const PlaceholderAud = "{aud}"

// Identity is how the remote proves itself to satelle.dev when it reads a secret:
// Key names the environment variable that holds its Ed25519 private key, or OIDC
// binds a platform workload identity. Neither holds a key.
type Identity struct {
	Key  string
	OIDC *OIDCIdentity
}

// Definition is the secrets and identity of one remote.
type Definition struct {
	Secret   []SecretDecl
	Identity Identity
}

// IdentityRef names how the remote proves itself, in the form a node reads it
// back from its environment: "key:<ENV>" for a key held in the host's secret
// store, "oidc:<token argv as a JSON array>" for a platform workload identity,
// "" for neither. It holds a variable name and a command, never a key.
func (d Definition) IdentityRef() string {
	switch {
	case d.Identity.OIDC != nil:
		argv, _ := json.Marshal(d.Identity.OIDC.Token)
		return "oidc:" + string(argv)
	case d.Identity.Key != "":
		return "key:" + d.Identity.Key
	}
	return ""
}

// TokenArgv is the identity's token command with PlaceholderAud filled. Each
// argument is replaced in one pass, so an audience that itself contains the
// placeholder is not expanded again.
func (d Definition) TokenArgv(aud string) []string {
	if d.Identity.OIDC == nil {
		return nil
	}
	r := strings.NewReplacer(PlaceholderAud, aud)
	out := make([]string, len(d.Identity.OIDC.Token))
	for i, a := range d.Identity.OIDC.Token {
		out[i] = r.Replace(a)
	}
	return out
}

var remoteIDPattern = regexp.MustCompile(`^rmt_[A-Za-z0-9_-]+$`)

// ValidRemoteID reports whether id has the shape of a satelle.dev remote id.
func ValidRemoteID(id string) bool { return remoteIDPattern.MatchString(id) }

// RepoName is the directory a repo is cloned into: the last element of its URL
// path without a .git suffix.
func RepoName(repoURL string) string {
	u, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(path.Base(strings.TrimRight(u.Path, "/")), ".git")
}
