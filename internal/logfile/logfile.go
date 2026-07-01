// Package logfile is the single, shared rotating-append writer for satelle's flat
// evidence logs under .satelle/logs (the operation log and the reviewer log). It
// bounds their growth — roll by UTC day and by max size, keep at most N rotated
// files — so a long-running repo does not accumulate an unbounded log (sty_a67e6e8c).
//
// It is deliberately stateless and best-effort, matching how the evidence logs are
// written: each Append opens, rotates-if-needed, writes one line, and closes. A
// logging error never breaks the caller (the caller may ignore the returned error).
package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Config bounds a rotating log file. A non-positive limit disables that bound.
type Config struct {
	MaxSizeBytes int64 // roll before a write that would exceed this size
	MaxFiles     int   // keep at most this many ROTATED files (the active file is extra)
}

// DefaultConfig is the fallback when config resolves to nothing sensible: a 5 MiB
// size cap and 7 kept rotations (plus daily rolling, which is always on).
var DefaultConfig = Config{MaxSizeBytes: 5 << 20, MaxFiles: 7}

// Append writes line (a trailing newline is added) to path, ROLLING the file over
// first when it has aged into a new UTC day since its last write or when the write
// would push it past cfg.MaxSizeBytes. A roll renames path to
// <base>-<YYYYMMDD-HHMMSS>.log (a numeric suffix breaks a same-second tie), then
// prunes the oldest rotations beyond cfg.MaxFiles. Best-effort: a mkdir/open error
// is returned but a caller may ignore it.
func Append(now time.Time, path string, cfg Config, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		if shouldRoll(info, now, cfg, len(line)+1) {
			rotate(now, path)
			prune(path, cfg.MaxFiles)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintln(f, line)
	return err
}

// shouldRoll reports whether the active file must roll before the next write: a new
// UTC day since its last write (daily rolling, always on), or a size bound that the
// pending write of addBytes would breach.
func shouldRoll(info os.FileInfo, now time.Time, cfg Config, addBytes int) bool {
	if info.ModTime().UTC().Format("2006-01-02") != now.UTC().Format("2006-01-02") {
		return true
	}
	if cfg.MaxSizeBytes > 0 && info.Size()+int64(addBytes) > cfg.MaxSizeBytes {
		return true
	}
	return false
}

// rotate renames path aside to a timestamped sibling; a same-second collision gets
// a numeric suffix so no rotation is clobbered. Best-effort: a rename failure leaves
// the active file in place (the next write simply appends to it).
func rotate(now time.Time, path string) {
	base := strings.TrimSuffix(path, filepath.Ext(path))
	ext := filepath.Ext(path)
	stamp := now.UTC().Format("20060102-150405")
	dest := base + "-" + stamp + ext
	for n := 1; ; n++ {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			break
		}
		dest = fmt.Sprintf("%s-%s.%d%s", base, stamp, n, ext)
	}
	_ = os.Rename(path, dest)
}

// prune deletes the oldest rotated siblings so at most maxFiles remain. maxFiles<=0
// keeps everything. Rotations are matched by the <base>-*<ext> glob and ordered by
// mtime (newest kept).
func prune(path string, maxFiles int) {
	if maxFiles <= 0 {
		return
	}
	base := strings.TrimSuffix(path, filepath.Ext(path))
	ext := filepath.Ext(path)
	matches, err := filepath.Glob(base + "-*" + ext)
	if err != nil || len(matches) <= maxFiles {
		return
	}
	sort.Slice(matches, func(i, j int) bool {
		fi, ei := os.Stat(matches[i])
		fj, ej := os.Stat(matches[j])
		if ei != nil || ej != nil {
			return matches[i] < matches[j]
		}
		return fi.ModTime().After(fj.ModTime()) // newest first
	})
	for _, old := range matches[maxFiles:] {
		_ = os.Remove(old)
	}
}
