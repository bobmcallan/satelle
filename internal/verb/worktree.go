package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/worktree"
)

// Worktree wiring (sty_804c566b): the process-of-record config and the main
// tree's root reach the verb through package globals, the same shape as
// SetEngagementMode. Both come from the process plane, so a worktree reads the
// main tree's declaration (sty_ddbe2669), never a copy of its own.
var (
	worktreeCfg   config.WorktreeConfig
	worktreeRoot  string
	worktreeWired bool
)

// SetWorktreeConfig wires the repo's [worktree] declaration and the canonical
// (main) tree root worktrees are cut from and carried from.
func SetWorktreeConfig(cfg config.Config, mainRoot string) {
	worktreeCfg = cfg.Worktree
	worktreeRoot = mainRoot
	worktreeWired = true
}

// ClearWorktreeConfig resets the wiring (tests).
func ClearWorktreeConfig() {
	worktreeCfg, worktreeRoot, worktreeWired = config.WorktreeConfig{}, "", false
}

func init() {
	Register(&Verb{
		Name:        "story-worktree",
		Description: "Open a git worktree for a story, or bring an existing one up to the repo's worktree declaration",
		Invoke:      storyWorktree,
	})
}

type worktreeReq struct {
	ID       string `json:"id"`
	Base     string `json:"base,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Path     string `json:"path,omitempty"`
	Existing string `json:"existing,omitempty"`
}

// WorktreeResult is the story-worktree response.
type WorktreeResult struct {
	ID       string             `json:"id"`
	Path     string             `json:"path"`
	Branch   string             `json:"branch,omitempty"`
	Base     string             `json:"base,omitempty"`
	Existing bool               `json:"existing,omitempty"`
	Entries  []worktree.Outcome `json:"entries"`
	// Missing names declared paths the main tree does not have; they were
	// skipped, which is reported rather than failed.
	Missing []string `json:"missing,omitempty"`
}

var worktreeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// storyWorktree opens a worktree for a story the way satelle directs and
// carries the declared gitignored paths into it. What is carried, and how the
// branch and location are named, is the repo's [worktree] declaration — this
// verb holds no convention of its own.
func storyWorktree(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req worktreeReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if !worktreeWired || worktreeRoot == "" {
		return nil, errors.New("verb: worktree configuration not wired")
	}
	if !worktreeIDPattern.MatchString(req.ID) {
		return nil, fmt.Errorf("verb: worktree needs a story id (letters, digits, . _ -), got %q", req.ID)
	}
	main := worktreeRoot
	res := WorktreeResult{ID: req.ID}

	if req.Existing != "" {
		if req.Base != "" || req.Branch != "" || req.Path != "" {
			return nil, errors.New("verb: --existing applies the declaration to a worktree that already exists; it cannot be combined with --base, --branch or --path")
		}
		wt, err := existingWorktree(ctx, main, req.Existing)
		if err != nil {
			return nil, err
		}
		res.Path, res.Existing = wt, true
	} else {
		if strings.TrimSpace(req.Base) == "" {
			return nil, errors.New("verb: --base <ref> is required — name the ref the worktree is cut from (for a child of an epic, the epic's base branch; for a dependent, trunk or its dependency's branch — see satelle help worktree); satelle never defaults to HEAD")
		}
		branch, err := resolveTemplate("branch", req.Branch, worktreeCfg.Branch, req.ID)
		if err != nil {
			return nil, err
		}
		path, err := resolveTemplate("path", req.Path, worktreeCfg.Path, req.ID)
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(main, path)
		}
		path = filepath.Clean(path)
		if err := worktree.Open(ctx, main, path, branch, req.Base); err != nil {
			return nil, err
		}
		res.Path, res.Branch, res.Base = path, branch, req.Base
	}

	rep, err := worktree.Carry(ctx, main, res.Path, worktreeCfg.Include)
	res.Entries = rep.Entries
	res.Missing = rep.Names(worktree.StatusAbsent)
	if res.Entries == nil {
		res.Entries = []worktree.Outcome{}
	}
	if err != nil {
		if res.Existing {
			return nil, err
		}
		// A fresh open that could not carry is left in place, never removed
		// implicitly: the operator decides.
		return nil, fmt.Errorf("%w — worktree %s (branch %s) was created and left in place; remove it with `git worktree remove %s` and `git branch -D %s`, or fix the declaration and run `satelle story worktree %s --existing %s`",
			err, res.Path, res.Branch, res.Path, res.Branch, req.ID, res.Path)
	}
	return json.Marshal(res)
}

// resolveTemplate picks a flag value over the repo's template and substitutes
// the story id. With neither, the flag is required and the refusal names both.
func resolveTemplate(key, flag, tmpl, id string) (string, error) {
	v := strings.TrimSpace(flag)
	if v == "" {
		v = tmpl
	}
	if strings.TrimSpace(v) == "" {
		return "", fmt.Errorf("verb: --%s is required — this repo declares no [worktree] %s template", key, key)
	}
	return strings.ReplaceAll(v, config.WorktreeIDPlaceholder, id), nil
}

// existingWorktree validates path as a linked worktree of the main tree and
// returns its root. It never creates a branch or a tree.
func existingWorktree(ctx context.Context, main, path string) (string, error) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs // an operator-typed path is relative to where they typed it
	}
	top, err := worktree.TopLevel(ctx, path)
	if err != nil {
		return "", fmt.Errorf("verb: --existing %s is not a git working tree: %w", path, err)
	}
	if want, err := filepath.EvalSymlinks(path); err == nil && filepath.Clean(want) != top {
		return "", fmt.Errorf("verb: --existing %s is not the root of its working tree (%s)", path, top)
	}
	mainTop, err := worktree.TopLevel(ctx, main)
	if err != nil {
		return "", err
	}
	if top == mainTop {
		return "", fmt.Errorf("verb: --existing %s is the main tree itself; there is nothing to carry into it", path)
	}
	a, err := worktree.CommonDir(ctx, top)
	if err != nil {
		return "", err
	}
	b, err := worktree.CommonDir(ctx, mainTop)
	if err != nil {
		return "", err
	}
	if a != b {
		return "", fmt.Errorf("verb: --existing %s belongs to a different repository than %s — refused", path, main)
	}
	return top, nil
}
