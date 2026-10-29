package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bobmcallan/satelle/internal/config"
)

// teamWriteDown is what one write-down of the project tier into the tree did.
type teamWriteDown struct {
	// Written lists the data-dir-relative paths written (under dry-run, the
	// paths that would be).
	Written []string
	Backups []teamBackup
	Errors  []string
}

// teamBackup is a locally edited tree copy kept before it was replaced.
type teamBackup struct {
	Rel    string
	Path   string
	Notice string
}

// Warnings is one line per replaced local edit, naming the file and its backup,
// then one per error.
func (w teamWriteDown) Warnings() []string {
	var out []string
	for _, b := range w.Backups {
		line := fmt.Sprintf("team substrate: %s/%s was edited locally and replaced from the project tier; your copy is at %s", config.DefaultDataDir, b.Rel, b.Path)
		if b.Notice != "" {
			line += " (" + b.Notice + ")"
		}
		out = append(out, line)
	}
	for _, e := range w.Errors {
		out = append(out, "team substrate: "+e)
	}
	return out
}

// writeDownProjectTier brings the session's tree to the project tier
// (sty_4cc971f7): each team-owned file the tier holds is written into the
// tree's data dir where it is absent or differs. A differing copy that is not
// the one satelle last wrote is a local edit; it is backed up first, and a file
// whose backup fails is left as it is. A repo with no tier — unbound included —
// is untouched. Local override files and the settings file are never written,
// and files the tier does not hold are never touched or deleted. dryRun writes
// nothing and lists what would be written.
func writeDownProjectTier(process config.Config, processRoot, runtimeDir, repoRoot string, tree config.Config, dryRun bool) (teamWriteDown, error) {
	var res teamWriteDown
	tier, ok := config.LoadProjectTier(process, processRoot, runtimeDir)
	if !ok {
		return res, nil
	}
	if runtimeDir == "" {
		runtimeDir = filepath.Dir(tier.Dir)
	}
	dataDir := tree.ResolveDataDir(repoRoot)
	prev := config.ReadTeamWritten(runtimeDir)
	record := map[string]string{}
	opts := ResolveBackupOpts(tree, repoRoot)
	opts.BackupsDir = runtimeDir
	for _, rel := range tier.Stamp.Files {
		if !tier.Team.Owns(rel) || config.LocalOnlyPath(rel) || rel == config.ConfigName {
			continue
		}
		body, err := os.ReadFile(tier.Path(rel))
		if err != nil {
			continue
		}
		sum := teamFileSum(body)
		dest := filepath.Join(dataDir, filepath.FromSlash(rel))
		cur, err := os.ReadFile(dest)
		switch {
		case err == nil && bytes.Equal(cur, body):
			record[rel] = sum
			continue
		case err != nil && !os.IsNotExist(err):
			res.Errors = append(res.Errors, fmt.Sprintf("%s/%s not replaced: %v", config.DefaultDataDir, rel, err))
			carryTeamRecord(record, prev, rel)
			continue
		}
		if dryRun {
			res.Written = append(res.Written, rel)
			continue
		}
		if err == nil && teamFileSum(cur) != prev[rel] {
			b, berr := backupExistingFile(dataDir, BackupKindTeamReplaced, rel, cur, opts)
			if berr != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s/%s was edited locally and is kept as it is — its backup failed: %v", config.DefaultDataDir, rel, berr))
				carryTeamRecord(record, prev, rel)
				continue
			}
			notice := ""
			if opts.HostedServer != "" && opts.HostedProject != "" {
				notice = b.Notice
			}
			res.Backups = append(res.Backups, teamBackup{Rel: rel, Path: b.LocalPath, Notice: notice})
		}
		if werr := writeTeamFile(dest, body); werr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s/%s not replaced: %v", config.DefaultDataDir, rel, werr))
			carryTeamRecord(record, prev, rel)
			continue
		}
		record[rel] = sum
		res.Written = append(res.Written, rel)
	}
	if dryRun {
		return res, nil
	}
	return res, config.WriteTeamWritten(runtimeDir, record)
}

// carryTeamRecord keeps a path's previous record entry when this pass did not
// write it.
func carryTeamRecord(record, prev map[string]string, rel string) {
	if s, ok := prev[rel]; ok {
		record[rel] = s
	}
}

// writeTeamFile writes body to dest through a temp file in dest's dir and a
// rename, so a reader never sees a half-written file.
func writeTeamFile(dest string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), config.TeamFileMode(body)); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}

func teamFileSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
