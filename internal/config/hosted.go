package config

// Hosted-server RESOLUTION (sty_53ccf845). The hosted server the CLI and web UI
// sign in to is a per-USER/machine setting, held in the global config
// (~/.satelle/config.toml [hosted] server, written by `satelle login`). A repo's
// committed satelle.toml [hosted] server is kept only as a READ-ONLY backward-
// compatibility fallback for repos bound before the global model — it is never
// written any more. This file owns the one precedence rule every consumer routes
// through, so a single login is reflected uniformly across every project. The
// OAuth tokens are secrets and live in the per-user credential store outside any
// config (internal/hosted), never here.

import "strings"

// HostedServerFor applies the precedence rule: the global hosted server wins;
// the repo's committed [hosted] server is the read-only fallback for a repo that
// predates the global binding. Both are normalized so a "https://h/" value can
// never mismatch the credential-store key "https://h". Returns "" when neither
// is set.
func HostedServerFor(gc GlobalConfig, repo Config) string {
	if s := gc.Hosted.ResolveServer(); s != "" {
		return s
	}
	return strings.TrimRight(strings.TrimSpace(repo.Hosted.Server), "/")
}

// ResolveHostedServer is the convenience resolver for callers that hold only the
// repo config: it loads the global config (a malformed global degrades to the
// repo fallback so a render or read command never fails on it) and applies the
// precedence rule. The login WRITE path calls LoadGlobal directly so it can still
// surface a malformed-global error.
func ResolveHostedServer(repo Config) string {
	gc, err := LoadGlobal()
	if err != nil {
		return strings.TrimRight(strings.TrimSpace(repo.Hosted.Server), "/")
	}
	return HostedServerFor(gc, repo)
}
