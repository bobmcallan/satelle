// Package worktree is the mechanism behind `satelle story worktree`
// (sty_804c566b): opening a git worktree for a story and carrying into it the
// gitignored paths the repository declares a worktree needs.
//
// It decides nothing about a repo's convention. Which paths are carried, how
// branches are named and where trees live are the caller's input, declared by
// the operator in configuration; this package only runs git and links paths.
//
// A carried path is a symlink from the worktree to the main tree's path: one
// source, so a copy cannot drift and a secret is never duplicated. git sees a
// symlink as a file, so a directory-only ignore pattern (".tool/") does not
// match it; Carry therefore adds an anchored line for each carried path to the
// repository's common info/exclude — git has no per-worktree exclude file — and
// then proves, per path, that the worktree ignores it.
package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Entry statuses reported per declared path.
const (
	StatusCarried = "carried"
	StatusAlready = "already carried"
	StatusAbsent  = "not present in main tree — skipped"
	StatusExists  = "exists, not a link — left as is"
)

// Outcome is what happened to one declared path.
type Outcome struct {
	Entry  string `json:"entry"`
	Status string `json:"status"`
}

// Report lists every declared path, in declaration order.
type Report struct {
	Entries []Outcome `json:"entries"`
}

// Names returns the entries with the given status.
func (r Report) Names(status string) []string {
	var out []string
	for _, o := range r.Entries {
		if o.Status == status {
			out = append(out, o.Entry)
		}
	}
	return out
}

// excludeWriter records the carried paths in the common exclude file. A var so
// a test can prove the post-check catches a path the exclude step failed to
// cover.
var excludeWriter = AddExcludes

// git runs git in dir and returns its trimmed stdout. Ambient repository
// selectors are dropped so a caller running inside a hook cannot redirect it.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = cleanEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_PREFIX":
			continue
		}
		env = append(env, kv)
	}
	return env
}

// CommonDir returns the absolute, symlink-resolved git common dir of the tree
// at root — the same directory for a main tree and every linked worktree.
func CommonDir(ctx context.Context, root string) (string, error) {
	out, err := git(ctx, root, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(root, out)
	}
	if resolved, rerr := filepath.EvalSymlinks(out); rerr == nil {
		out = resolved
	}
	return filepath.Clean(out), nil
}

// TopLevel returns the symlink-resolved working-tree root containing dir.
func TopLevel(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if resolved, rerr := filepath.EvalSymlinks(out); rerr == nil {
		out = resolved
	}
	return filepath.Clean(out), nil
}

// Open creates a worktree at path on a NEW branch cut from base. base must
// resolve to a commit and is never defaulted; an existing branch is refused and
// never reused or reset. Nothing is created unless every check passes.
func Open(ctx context.Context, mainRoot, path, branch, base string) error {
	if strings.TrimSpace(base) == "" {
		return errors.New("worktree: a base ref is required")
	}
	if _, err := git(ctx, mainRoot, "rev-parse", "--verify", "--quiet", base+"^{commit}"); err != nil {
		return fmt.Errorf("worktree: base %q does not resolve to a commit in %s", base, mainRoot)
	}
	if _, err := git(ctx, mainRoot, "check-ref-format", "refs/heads/"+branch); err != nil {
		return fmt.Errorf("worktree: branch %q is not a valid branch name", branch)
	}
	if _, err := git(ctx, mainRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		return fmt.Errorf("worktree: branch %q already exists — refusing to reuse or reset it", branch)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("worktree: %s already exists — refusing to open a worktree over it", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("worktree: create parent of %s: %w", path, err)
	}
	if _, err := git(ctx, mainRoot, "worktree", "add", "-b", branch, path, base); err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	return nil
}

// Carry links each declared path from the main tree into the worktree and
// returns what happened to every one. include entries are repo-relative paths
// already validated by configuration; a path that is not local is refused here
// too. The returned error is non-nil when a path was refused or could not be
// proven ignored in the worktree; the Report still describes every entry.
func Carry(ctx context.Context, mainRoot, wtRoot string, include []string) (Report, error) {
	var (
		rep     Report
		linked  []string
		refused []string
	)
	for _, raw := range include {
		entry := filepath.Clean(raw)
		if !filepath.IsLocal(entry) {
			refused = append(refused, fmt.Sprintf("worktree include %q: not a path inside the repository — refused", raw))
			continue
		}
		src, dst := filepath.Join(mainRoot, entry), filepath.Join(wtRoot, entry)
		if _, err := os.Lstat(src); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return rep, fmt.Errorf("worktree include %q: %w", entry, err)
			}
			rep.Entries = append(rep.Entries, Outcome{entry, StatusAbsent})
			continue
		}
		// Only a path git already ignores in the main tree is carried: a tracked
		// or unignored path is the repository's content, not local wiring.
		ignored, err := checkIgnore(ctx, mainRoot, entry)
		if err != nil {
			return rep, fmt.Errorf("worktree include %q: %w", entry, err)
		}
		if !ignored {
			refused = append(refused, fmt.Sprintf("worktree include %q: not gitignored in main tree — refused", entry))
			continue
		}
		if fi, err := os.Lstat(dst); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 && sameTarget(dst, src) {
				rep.Entries = append(rep.Entries, Outcome{entry, StatusAlready})
				linked = append(linked, entry)
			} else {
				rep.Entries = append(rep.Entries, Outcome{entry, StatusExists})
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return rep, fmt.Errorf("worktree include %q: %w", entry, err)
		}
		if err := os.Symlink(src, dst); err != nil {
			return rep, fmt.Errorf("worktree include %q: cannot link into the worktree (%w) — not copied", entry, err)
		}
		rep.Entries = append(rep.Entries, Outcome{entry, StatusCarried})
		linked = append(linked, entry)
	}
	if len(linked) > 0 {
		if err := excludeWriter(ctx, mainRoot, linked); err != nil {
			return rep, err
		}
		notIgnored, err := VerifyIgnored(ctx, wtRoot, linked)
		if err != nil {
			return rep, err
		}
		for _, e := range notIgnored {
			refused = append(refused, fmt.Sprintf("worktree include %q: carried but not gitignored in the worktree", e))
		}
	}
	if len(refused) > 0 {
		return rep, errors.New(strings.Join(refused, "; "))
	}
	return rep, nil
}

// sameTarget reports whether the link at dst resolves to src.
func sameTarget(dst, src string) bool {
	a, err1 := filepath.EvalSymlinks(dst)
	b, err2 := filepath.EvalSymlinks(src)
	return err1 == nil && err2 == nil && a == b
}

// checkIgnore reports whether git ignores entry in the tree at root.
func checkIgnore(ctx context.Context, root, entry string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-q", "--", entry)
	cmd.Env = cleanEnv()
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git check-ignore: %w", err)
}

// VerifyIgnored returns the entries the tree at root does not ignore.
func VerifyIgnored(ctx context.Context, root string, entries []string) ([]string, error) {
	var out []string
	for _, e := range entries {
		ok, err := checkIgnore(ctx, root, e)
		if err != nil {
			return nil, err
		}
		if !ok {
			out = append(out, e)
		}
	}
	return out, nil
}

const excludeHeader = "# satelle worktree include"

// AddExcludes appends an anchored line for each entry to the repository's
// common info/exclude, inside a satelle-owned block, skipping lines already
// present. It never edits .gitignore or any line the operator wrote.
func AddExcludes(ctx context.Context, mainRoot string, entries []string) error {
	common, err := CommonDir(ctx, mainRoot)
	if err != nil {
		return err
	}
	file := filepath.Join(common, "info", "exclude")
	existing, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("worktree: read %s: %w", file, err)
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(existing), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var add []string
	for _, e := range entries {
		line := "/" + filepath.ToSlash(filepath.Clean(e))
		if !have[line] {
			add = append(add, line)
			have[line] = true
		}
	}
	if len(add) == 0 {
		return nil
	}
	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		b.WriteString("\n")
	}
	if !have[excludeHeader] {
		b.WriteString(excludeHeader + "\n")
	}
	b.WriteString(strings.Join(add, "\n") + "\n")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("worktree: write %s: %w", file, err)
	}
	return nil
}
