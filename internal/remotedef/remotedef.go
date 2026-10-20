// Package remotedef reads the part of a remote definition that a node needs: the
// secrets the remote declares, the repos it fetches and the egress it is allowed.
// A node receives the definition from satelle.dev, in the bundle it fetches with
// its own node token, and Parse is the only reader of its remote.toml.
//
// The format and its validation are satelle-server's internal/remotedef: the
// version a node receives was validated there, so Parse decodes only what the
// node uses and tolerates every other key, including any the server adds later.
// It runs nothing, opens nothing and holds no credential.
package remotedef

import (
	"net/url"
	"path"
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
	Key       string `toml:"key"`
	Scope     string `toml:"scope"`
	From      any    `toml:"from"`
	Workspace string `toml:"workspace"`
}

// Repo is one repository the node fetches, read-only: an https URL and the ref
// (branch, tag or commit) to check out.
type Repo struct {
	URL string `toml:"url"`
	Ref string `toml:"ref"`
}

// Network is the node's egress beyond what the definition already implies.
type Network struct {
	Allow []string `toml:"allow"`
}

// Definition is the part of remote.toml a node reads.
type Definition struct {
	Secret  []SecretDecl `toml:"secret"`
	Repos   []Repo       `toml:"repo"`
	Network Network      `toml:"network"`
}

// Allow is the egress allowlist the definition declares.
func (d Definition) Allow() []string { return d.Network.Allow }

// RepoName is the directory a repo is cloned into: the last element of its URL
// path without a .git suffix.
func RepoName(repoURL string) string {
	u, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(path.Base(strings.TrimRight(u.Path, "/")), ".git")
}
