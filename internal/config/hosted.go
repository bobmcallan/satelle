package config

// Hosted-server RESOLUTION (sty_34037275). The hosted origin is a machine
// setting only: ~/.satelle/config.toml [hosted] server, written by
// `satelle login` / `satelle settings --global server`. Unset →
// DefaultHostedServer. Repo [sync] server is a leftover stray (never
// resolved). [sync] project / workspace stay per-repo. Tokens stay in the
// credstore (internal/hosted).

import (
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultHostedServer is the zero-config hosted origin.
const DefaultHostedServer = "https://satelle.dev"

const (
	syncServerKey    = "server"
	syncProjectKey   = "project"
	syncWorkspaceKey = "workspace"
)

func (c Config) syncValue(key string) string {
	if c.Sync == nil {
		return ""
	}
	return strings.TrimSpace(c.Sync[key])
}

// SyncProject is [sync] project, else leftover [hosted] project (raw, un-defaulted).
func (c Config) SyncProject() string {
	if s := c.syncValue(syncProjectKey); s != "" {
		return s
	}
	return strings.TrimSpace(c.Hosted.Project)
}

// SyncWorkspace is [sync] workspace, else leftover [hosted] workspace.
func (c Config) SyncWorkspace() string {
	if s := c.syncValue(syncWorkspaceKey); s != "" {
		return s
	}
	return strings.TrimSpace(c.Hosted.Workspace)
}

// HostedServerFor is machine [hosted] server, else DefaultHostedServer.
// repo is unused (signature kept so call sites stay unchanged).
func HostedServerFor(gc GlobalConfig, repo Config) string {
	_ = repo
	if s := gc.Hosted.ResolveServer(); s != "" {
		return s
	}
	return DefaultHostedServer
}

// ResolveHostedServer loads the global config and applies HostedServerFor.
// A malformed or missing global degrades to DefaultHostedServer.
func ResolveHostedServer(repo Config) string {
	gc, err := LoadGlobal()
	if err != nil {
		return DefaultHostedServer
	}
	return HostedServerFor(gc, repo)
}

// Slugify is the one project-slug rule: lowercase, non [a-z0-9-] become '-',
// runs of '-' collapse, leading/trailing '-' stripped.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// ResolveBoundProject is [sync]/leftover [hosted] project, else Slugify of
// the repo directory name. Empty repoRoot yields "".
func ResolveBoundProject(c Config, repoRoot string) string {
	if s := c.SyncProject(); s != "" {
		return s
	}
	root := strings.TrimSpace(repoRoot)
	if root == "" || root == "." || root == string(filepath.Separator) {
		return ""
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	base := filepath.Base(abs)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	return Slugify(base)
}

// BoundProjectEdit is the single write of the repo project binding.
func BoundProjectEdit(slug string) KeyEdit {
	return KeyEdit{Section: "sync", Key: syncProjectKey, Value: strconv.Quote(Slugify(slug))}
}

// BoundWorkspaceEdit is the single write of the active-workspace overlay.
func BoundWorkspaceEdit(name string) KeyEdit {
	return KeyEdit{Section: "sync", Key: syncWorkspaceKey, Value: strconv.Quote(strings.TrimSpace(name))}
}
