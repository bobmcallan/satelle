package config

// Hosted-server RESOLUTION (sty_53ccf845 / sty_a13d7c4a). The hosted server
// the CLI signs in to is a per-USER/machine setting (~/.satelle/config.toml
// [hosted] server, written by `satelle login`) and still wins. Repo connection
// settings live on [sync] (server / project / workspace) with defaults:
// server https://satelle.dev, project = this repo's directory name. A leftover
// satelle.toml [hosted] table is a read-only fallback until init/migrate
// copies leftover keys onto [sync] and drops the table (sty_5eb1bb8a).
// Tokens stay in the credstore (internal/hosted).

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

// SyncServer is [sync] server, else leftover [hosted] server (raw, un-defaulted).
func (c Config) SyncServer() string {
	if s := c.syncValue(syncServerKey); s != "" {
		return strings.TrimRight(s, "/")
	}
	return strings.TrimRight(strings.TrimSpace(c.Hosted.Server), "/")
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

// HostedServerFor: global login server, then repo SyncServer, then DefaultHostedServer.
func HostedServerFor(gc GlobalConfig, repo Config) string {
	if s := gc.Hosted.ResolveServer(); s != "" {
		return s
	}
	if s := repo.SyncServer(); s != "" {
		return s
	}
	return DefaultHostedServer
}

// ResolveHostedServer loads the global config and applies HostedServerFor.
// A malformed global degrades to repo SyncServer then the default.
func ResolveHostedServer(repo Config) string {
	gc, err := LoadGlobal()
	if err != nil {
		if s := repo.SyncServer(); s != "" {
			return s
		}
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
