// substrate_backup.go — shared pre-mutation backup for authored substrate
// (sty_873a5380). Local floor always under the runtime plane's backups/ tree
// (~/.satelle/<repo-key>/backups/ when home-keyed — sty_4660bbe1); optional
// personal hosted push when configured+authenticated; advisory (never a gate)
// when no hosted channel and the operator has not opted local-only.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// errLocalOnlyBackup is returned by pushHostedBackup when the path is a
// .local file that must never leave the machine. Callers treat it as a
// deliberate withhold (local backup still lands), not a transport failure.
var errLocalOnlyBackup = errors.New("hosted push withheld — .local never leaves this machine")

// BackupKind names the backup sub-tree (diverged / pre-mutation / restore / …).
// Layout: <runtimeDir>/backups/<kind>/<relPath> — deterministic, clock-free for
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
	// BackupsDir is the parent of the backups/ tree (the runtime plane —
	// sty_4660bbe1). When empty, backupExistingFile uses its dataDir argument
	// as that parent (tests / single-tree fixtures).
	BackupsDir string
}

// ResolveBackupOpts reads the backup policy from cfg (and defaults). Safe with
// a zero config. Hosted documents push is OPT-IN via [backup] hosted = true
// (sty_84f14ace); when false, HostedServer/HostedProject stay empty so the
// existing server+project gate on the push path stays quiet. Hosted server URL
// uses global→repo precedence (config.ResolveHostedServer) when opted in.
func ResolveBackupOpts(cfg config.Config) BackupOpts {
	opts := BackupOpts{LocalOnly: cfg.Backup.LocalOnly}
	if cfg.Backup.Hosted {
		opts.HostedServer = config.ResolveHostedServer(cfg)
		opts.HostedProject = strings.TrimSpace(cfg.Hosted.Project)
	}
	return opts
}

// backupRoot is the parent of backups/<kind>/… — RuntimeDir when set on opts
// (sty_4660bbe1), else the dataDir argument (tests that share one tree).
func backupRoot(dataDir string, opts BackupOpts) string {
	if d := strings.TrimSpace(opts.BackupsDir); d != "" {
		return d
	}
	return dataDir
}

// backupExistingFile writes a local copy of body at
// <runtime|dataDir>/backups/<kind>/<relPath>, then optionally pushes to the
// personal hosted service. Never blocks a heal path: hosted/auth failures
// degrade to local-only with a notice. relPath is the kind-relative slash path.
func backupExistingFile(dataDir string, kind BackupKind, relPath string, body []byte, opts BackupOpts) (BackupResult, error) {
	if body == nil {
		body = []byte{}
	}
	local := filepath.Join(backupRoot(dataDir, opts), "backups", string(kind), filepath.FromSlash(relPath))
	if err := writeEmbedded(local, string(body)); err != nil {
		return BackupResult{}, fmt.Errorf("backup %s: %w", relPath, err)
	}
	res := BackupResult{LocalPath: local}

	// Online-first when a server+project are configured.
	if opts.HostedServer != "" && opts.HostedProject != "" {
		dest, herr := pushHostedBackup(opts, relPath, body)
		if errors.Is(herr, errLocalOnlyBackup) {
			res.Notice = fmt.Sprintf("backup: local %s (hosted push withheld — .local never leaves this machine)", local)
		} else if herr != nil {
			res.Notice = fmt.Sprintf("backup: hosted unavailable (%v) — kept local copy at %s", herr, local)
		} else {
			res.Notice = fmt.Sprintf("backup: local %s; hosted %s", local, dest)
		}
		return res, nil
	}

	// No hosted channel: advisory unless operator opted local-only.
	if !opts.LocalOnly {
		res.Notice = "backup: local only at " + local +
			" — online/personal backup is opt-in via [backup] hosted = true (requires satelle login + [hosted] project); set [backup] local_only = true to suppress this advisory"
	}
	return res, nil
}

// pushHostedBackup sends the pre-image to the personal hosted documents partition
// under backups/<relPath>. Soft-fail on auth/offline. Paths matching
// config.LocalOnlyPath are refused so a hosted backup never becomes an
// exfiltration path for satelle.local.toml or other .local secrets.
func pushHostedBackup(opts BackupOpts, relPath string, body []byte) (string, error) {
	if config.LocalOnlyPath(relPath) {
		return "", errLocalOnlyBackup
	}
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
	path := "backups/" + strings.TrimPrefix(filepath.ToSlash(relPath), "/")
	if _, err := client.PushDocumentFile(context.Background(), opts.HostedProject, path, body); err != nil {
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
// Empty when local_only, or when a hosted push channel is already active.
func backupPolicyNotice(opts BackupOpts, localRoot string) string {
	if opts.HostedServer != "" && opts.HostedProject != "" {
		return "backup: local " + localRoot + " (hosted channel configured — pushing pre-images next)"
	}
	if opts.LocalOnly {
		return ""
	}
	return "backup: local only at " + localRoot +
		" — online/personal backup is opt-in via [backup] hosted = true (requires satelle login + [hosted] project); set [backup] local_only = true to suppress this advisory"
}

// pushBackupTreeHosted walks a local backup tree (e.g. rebase's timestamped
// dir) and best-effort pushes each regular file to the personal hosted
// documents partition under backups/<rel-from-tree-root>. Returns count pushed
// and a summary notice (empty when no hosted channel or nothing to push).
// .local files are withheld (counted separately) and never uploaded.
func pushBackupTreeHosted(localRoot string, opts BackupOpts) (int, string) {
	if opts.HostedServer == "" || opts.HostedProject == "" {
		return 0, ""
	}
	var n, withheld int
	var firstErr error
	_ = filepath.Walk(localRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(localRoot, path)
		if rerr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if config.LocalOnlyPath(relSlash) {
			withheld++
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			if firstErr == nil {
				firstErr = rerr
			}
			return nil
		}
		// Reuse the same hosted path scheme as file-level backups.
		dest, perr := pushHostedBackup(opts, relSlash, body)
		if perr != nil {
			if errors.Is(perr, errLocalOnlyBackup) {
				withheld++
				return nil
			}
			if firstErr == nil {
				firstErr = perr
			}
			return nil
		}
		_ = dest
		n++
		return nil
	})
	if n == 0 && withheld == 0 && firstErr != nil {
		return 0, fmt.Sprintf("backup: hosted unavailable (%v) — kept local tree at %s", firstErr, localRoot)
	}
	if n == 0 && withheld == 0 {
		return 0, ""
	}
	if withheld > 0 {
		return n, fmt.Sprintf("backup: pushed %d file(s), %d withheld (.local) to hosted project %s (local %s)", n, withheld, opts.HostedProject, localRoot)
	}
	return n, fmt.Sprintf("backup: pushed %d file(s) to hosted project %s (local %s)", n, opts.HostedProject, localRoot)
}
