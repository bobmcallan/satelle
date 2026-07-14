// substrate_backup.go — shared pre-mutation backup for authored substrate
// (sty_873a5380). Local floor always under .satelle/backups/; optional personal
// hosted push when configured+authenticated; advisory (never a gate) when no
// hosted channel and the operator has not opted local-only.
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// BackupKind names the backup sub-tree (diverged / pre-mutation / restore / …).
// Layout: dataDir/backups/<kind>/<relPath> — deterministic, clock-free for
// single-file kinds so re-runs overwrite the same slot (idempotent).
type BackupKind string

const (
	BackupKindDiverged    BackupKind = "diverged"
	BackupKindPreMutation BackupKind = "pre-mutation"
	BackupKindRestore     BackupKind = "restore"
)

// BackupResult describes where a pre-mutation copy landed.
type BackupResult struct {
	LocalPath string // absolute path under dataDir/backups/…
	// Notice is a single line for command output (hosted destination, degrade
	// notice, or the online-channel advisory). Empty when nothing to print.
	Notice string
}

// BackupOpts tunes the online/local policy for one backup call.
type BackupOpts struct {
	// LocalOnly suppresses the "enable online backup" advisory (operator
	// opted out via [backup] local_only = true).
	LocalOnly bool
	// HostedServer/Project when non-empty attempt a personal hosted push of
	// the pre-image. Failures degrade to local-only with a non-fatal notice.
	HostedServer  string
	HostedProject string
	// Now, if non-zero, stamps time-based roots (restore batch). Zero uses
	// time.Now() when a timestamped root is needed.
	Now time.Time
	// HostedPush is an optional override for tests (nil → real hosted client).
	HostedPush func(ctx context.Context, relPath string, body []byte) (dest string, err error)
}

// ResolveBackupOpts reads the backup policy from cfg (and defaults). Safe with
// a zero config.
func ResolveBackupOpts(cfg config.Config) BackupOpts {
	return BackupOpts{
		LocalOnly:     cfg.Backup.LocalOnly,
		HostedServer:  strings.TrimSpace(cfg.Hosted.Server),
		HostedProject: strings.TrimSpace(cfg.Hosted.Project),
	}
}

// backupExistingFile writes a local copy of body at
// dataDir/backups/<kind>/<relPath>, then optionally pushes to the personal
// hosted service. Never blocks a heal path: hosted/auth failures degrade to
// local-only with a notice. relPath is the kind-relative slash path.
func backupExistingFile(dataDir string, kind BackupKind, relPath string, body []byte, opts BackupOpts) (BackupResult, error) {
	if body == nil {
		body = []byte{}
	}
	local := filepath.Join(dataDir, "backups", string(kind), filepath.FromSlash(relPath))
	if err := writeEmbedded(local, string(body)); err != nil {
		return BackupResult{}, fmt.Errorf("backup %s: %w", relPath, err)
	}
	res := BackupResult{LocalPath: local}

	// Online-first when a server+project are configured.
	if opts.HostedServer != "" && opts.HostedProject != "" {
		dest, herr := pushHostedBackup(opts, relPath, body)
		if herr != nil {
			res.Notice = fmt.Sprintf("backup: hosted unavailable (%v) — kept local copy at %s", herr, local)
		} else {
			res.Notice = fmt.Sprintf("backup: local %s; hosted %s", local, dest)
		}
		return res, nil
	}

	// No hosted channel: advisory unless operator opted local-only.
	if !opts.LocalOnly {
		res.Notice = "backup: local only at " + local +
			" — online/personal backup is available via `satelle login` + [hosted] server/project; set [backup] local_only = true to suppress this advisory"
	}
	return res, nil
}

// pushHostedBackup sends the pre-image to the personal hosted documents partition
// under backups/<relPath>. Soft-fail on auth/offline.
func pushHostedBackup(opts BackupOpts, relPath string, body []byte) (string, error) {
	if opts.HostedPush != nil {
		return opts.HostedPush(context.Background(), relPath, body)
	}
	server := opts.HostedServer
	store := hosted.FileStore{}
	cred, err := store.Load(server)
	if err != nil {
		return "", err
	}
	_ = cred
	client := hosted.NewClient(server, store, nil)
	// Prefer the personal workspace; fall back to ActiveWorkspaceID("").
	wsID, err := client.ActiveWorkspaceID(context.Background(), "")
	if err != nil {
		wss, werr := client.Workspaces(context.Background())
		if werr != nil {
			return "", err
		}
		for _, w := range wss {
			if w.Kind == "personal" {
				wsID = w.ID
				break
			}
		}
		if wsID == "" && len(wss) > 0 {
			wsID = wss[0].ID
		}
	}
	if wsID == "" {
		return "", fmt.Errorf("no hosted workspace")
	}
	path := "backups/" + strings.TrimPrefix(filepath.ToSlash(relPath), "/")
	if _, err := client.PushDocumentFile(context.Background(), wsID, opts.HostedProject, path, body); err != nil {
		return "", err
	}
	return server + " documents/" + path + " (project " + opts.HostedProject + ")", nil
}

// backupExistingPath reads path and backs it up under kind/relPath when the
// file exists. No-op (nil error, empty result) when the file is absent —
// creates do not need a pre-image.
func backupExistingPath(dataDir string, kind BackupKind, relPath, absPath string, opts BackupOpts) (BackupResult, error) {
	cur, err := os.ReadFile(absPath)
	if os.IsNotExist(err) {
		return BackupResult{}, nil
	}
	if err != nil {
		return BackupResult{}, fmt.Errorf("backup read %s: %w", absPath, err)
	}
	return backupExistingFile(dataDir, kind, relPath, cur, opts)
}

// backupPolicyNotice returns the advisory / online-status line for a command
// that already performed a local backup at localRoot (e.g. rebase's dir move).
// Empty when local_only and no hosted channel.
func backupPolicyNotice(opts BackupOpts, localRoot string) string {
	if opts.HostedServer != "" && opts.HostedProject != "" {
		// Directory-level hosted push is out of scope for a single wipe; surface
		// local root and that hosted is configured for file-level backups.
		return "backup: local " + localRoot + " (hosted channel configured — file-level pre-mutation backups also go online)"
	}
	if opts.LocalOnly {
		return ""
	}
	return "backup: local only at " + localRoot +
		" — online/personal backup is available via `satelle login` + [hosted] server/project; set [backup] local_only = true to suppress this advisory"
}
