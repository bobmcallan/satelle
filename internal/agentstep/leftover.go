package agentstep

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
)

// untrackedSnapshot lists repo-relative untracked paths under repoRoot. It is
// LANGUAGE-NEUTRAL and knows nothing about Go, tests, or any other ecosystem —
// what counts as a leftover is entirely config.LeftoverRule (sty_e7aaf8b1,
// satelle-story-architecture-review revision 2: no Go opinion in the binary).
func untrackedSnapshot(repoRoot string) (map[string]bool, error) {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			set[line] = true
		}
	}
	return set, nil
}

// leftoverMatch reports whether relpath is a leftover under rule: a glob match
// on the repo-relative path or its basename, or a content_regex match within
// max_bytes.
func leftoverMatch(repoRoot, relpath string, rule config.LeftoverRule) bool {
	base := filepath.Base(relpath)
	for _, pat := range rule.Patterns {
		if ok, _ := filepath.Match(pat, relpath); ok {
			return true
		}
		if ok, _ := filepath.Match(pat, base); ok {
			return true
		}
	}
	if strings.TrimSpace(rule.ContentRegex) == "" {
		return false
	}
	re, err := regexp.Compile(rule.ContentRegex)
	if err != nil {
		return false
	}
	full := filepath.Join(repoRoot, relpath)
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return false
	}
	if rule.MaxBytes > 0 && info.Size() > int64(rule.MaxBytes) {
		return false
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return false
	}
	return re.Match(data)
}

// SweepLeftovers moves (or, when rule.Action == "flag", merely reports) every
// untracked file created SINCE `before` that matches rule out of the working
// tree and into <scratch>/leftovers/. Pre-existing untracked files (present in
// `before`) are never touched — the sweep only ever claims what THIS session
// created. An empty rule (no patterns, no content_regex) is a no-op so a repo
// that configures nothing pays no git cost and moves nothing.
func SweepLeftovers(repoRoot, scratch string, before map[string]bool, rule config.LeftoverRule) ([]string, error) {
	if len(rule.Patterns) == 0 && strings.TrimSpace(rule.ContentRegex) == "" {
		return nil, nil
	}
	after, err := untrackedSnapshot(repoRoot)
	if err != nil {
		return nil, err
	}
	var matched []string
	for relpath := range after {
		if before[relpath] {
			continue
		}
		if leftoverMatch(repoRoot, relpath, rule) {
			matched = append(matched, relpath)
		}
	}
	sort.Strings(matched)
	if len(matched) == 0 {
		return nil, nil
	}
	if rule.ResolveAction() == config.LeftoverActionFlag {
		return matched, nil
	}
	for _, relpath := range matched {
		src := filepath.Join(repoRoot, relpath)
		dst := filepath.Join(scratch, "leftovers", relpath)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return matched, err
		}
		if err := os.Rename(src, dst); err != nil {
			if cerr := copyThenRemove(src, dst); cerr != nil {
				return matched, cerr
			}
		}
	}
	return matched, nil
}

// copyThenRemove is os.Rename's cross-device fallback.
func copyThenRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

// ledgerLeftovers records the leftovers sweep outcome, best-effort, so it is
// visible on the story's ledger the same way scratch retention is.
func (g *Engine) ledgerLeftovers(ctx context.Context, storyID, scratch, action string, files []string) {
	if len(files) == 0 {
		return
	}
	g.recordInvocation(ctx, storyID, map[string]any{
		"phase": "leftovers", "action": action, "files": files, "scratch_dir": scratch,
	})
}
