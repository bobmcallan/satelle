//go:build integration

package tests

// Offline proof of the Claude cloud-session hook carrier (sty_82cffd60): the
// tracked .claude/settings.local.json runs scripts/claude-cloud-hook.sh, which
// is inert locally and, when CLAUDE_CODE_REMOTE=true, lets a cloud session edit
// and commit only on the dispatch branch claude/satelle-*. No network and no
// satelle binary is involved in the hook itself.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	cloudHookScript   = "scripts/claude-cloud-hook.sh"
	cloudHookSettings = ".claude/settings.local.json"
)

type cloudHookEnv struct {
	repo string
	home string
}

// newCloudHookEnv builds a temporary git clone holding only the tracked carrier
// files, on branch.
func newCloudHookEnv(t *testing.T, branch string) cloudHookEnv {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	e := cloudHookEnv{repo: t.TempDir(), home: t.TempDir()}
	for _, rel := range []string{cloudHookScript, cloudHookSettings} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read tracked %s: %v", rel, err)
		}
		dst := filepath.Join(e.repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", e.repo, "init", "-q", "-b", branch).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return e
}

// hook runs the script in mode with stdin, as Claude Code would run the tracked
// command. remote sets CLAUDE_CODE_REMOTE=true; otherwise it is removed.
func (e cloudHookEnv) hook(t *testing.T, remote bool, mode, stdin string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", filepath.Join(e.repo, filepath.FromSlash(cloudHookScript)), mode)
	cmd.Dir = e.repo
	cmd.Stdin = strings.NewReader(stdin)
	var env []string
	for _, kv := range isolatedEnv(t) {
		if strings.HasPrefix(kv, "CLAUDE_CODE_REMOTE=") || strings.HasPrefix(kv, "CLAUDE_PROJECT_DIR=") ||
			strings.HasPrefix(kv, "HOME=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HOME="+e.home, "CLAUDE_PROJECT_DIR="+e.repo)
	if remote {
		env = append(env, "CLAUDE_CODE_REMOTE=true")
	}
	cmd.Env = env
	out, err := cmd.Output() // stdout only: the hook protocol reads stdout
	return string(out), err
}

func (e cloudHookEnv) editPayload() string {
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": filepath.Join(e.repo, "scratch.txt")},
		"cwd":             e.repo,
	})
	return string(b)
}

func (e cloudHookEnv) bashPayload(command string) string {
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": command},
		"cwd":             e.repo,
	})
	return string(b)
}

// denied reports whether out is a PreToolUse deny.
func denied(out string) bool {
	var v struct {
		Hook struct {
			Decision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		return false
	}
	return v.Hook.Decision == "deny"
}

func TestClaudeCloudHookInertLocally(t *testing.T) {
	e := newCloudHookEnv(t, "main")
	for _, c := range []struct{ mode, stdin string }{
		{"gate", e.editPayload()}, {"commitgate", e.bashPayload("git commit -m probe")},
	} {
		out, err := e.hook(t, false, c.mode, c.stdin)
		if err != nil || out != "" {
			t.Errorf("%s with CLAUDE_CODE_REMOTE unset: err=%v out=%q, want exit 0 and no output", c.mode, err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(e.repo, ".satelle")); !os.IsNotExist(err) {
		t.Errorf(".satelle/ created by a non-cloud run (stat err=%v)", err)
	}
}

// A cloud session on the dispatch branch may edit and commit; on any other
// branch both are denied.
func TestClaudeCloudHookBranchGate(t *testing.T) {
	for _, c := range []struct {
		branch  string
		allowed bool
	}{
		{"claude/satelle-sty_ab12cd34-0f1e2d3c", true},
		{"claude/other", false},
		{"claude/probe-sty_3b112554-1", false},
		{"main", false},
	} {
		e := newCloudHookEnv(t, c.branch)
		for name, in := range map[string]struct{ mode, stdin string }{
			"edit":   {"gate", e.editPayload()},
			"commit": {"commitgate", e.bashPayload("git commit -m done")},
			"push":   {"commitgate", e.bashPayload("git push origin " + c.branch)},
		} {
			out, err := e.hook(t, true, in.mode, in.stdin)
			if err != nil {
				t.Fatalf("%s on %s: %v\n%s", name, c.branch, err, out)
			}
			if got := denied(out); got == c.allowed {
				t.Errorf("%s on branch %q: denied=%v, want allowed=%v (out %q)", name, c.branch, got, c.allowed, out)
			}
		}
	}
}

// git's global options (-C <dir>, -c k=v, --no-pager, a quoted value) between
// "git" and the subcommand do not hide a commit or push: off the dispatch branch
// each is denied, on it each is allowed.
func TestClaudeCloudHookGitGlobalOptions(t *testing.T) {
	cmds := []string{
		"git -C /tmp/work commit -m x",
		"git -C /tmp/work push origin HEAD",
		"git -c user.name=x commit -m x",
		"git -c k=v push",
		"git -C /tmp/work -c k=v commit -m x",
		"git --no-pager commit -m x",
		"git --git-dir=/tmp/x/.git push",
		`git -C "/tmp/my dir" commit -m x`,
		"git -C '/tmp/my dir' push",
		"cd /tmp && git -C . commit -am x",
		"/usr/bin/git -C /tmp/work commit",
	}
	for _, c := range []struct {
		branch  string
		allowed bool
	}{
		{"claude/satelle-sty_ab12cd34-0f1e2d3c", true},
		{"claude/auto-named", false},
	} {
		e := newCloudHookEnv(t, c.branch)
		for _, cmd := range cmds {
			out, err := e.hook(t, true, "commitgate", e.bashPayload(cmd))
			if err != nil {
				t.Fatalf("%q on %s: %v\n%s", cmd, c.branch, err, out)
			}
			if got := denied(out); got == c.allowed {
				t.Errorf("%q on branch %q: denied=%v, want allowed=%v (out %q)", cmd, c.branch, got, c.allowed, out)
			}
		}
	}
}

// Off the dispatch branch only the edit tools and git commit/push are refused:
// everything a session needs to reach the branch and verify its work still runs.
func TestClaudeCloudHookAllowsOtherCommandsOffBranch(t *testing.T) {
	e := newCloudHookEnv(t, "claude/auto-named")
	for _, cmd := range []string{
		"git checkout -b claude/satelle-sty_ab12cd34-0f1e2d3c",
		"git status --short",
		"go test ./...",
		"git log --oneline",
		"git -C /tmp/work status",
		"git -c k=v log -1",
		"git commit-tree HEAD^{tree}",
	} {
		out, err := e.hook(t, true, "commitgate", e.bashPayload(cmd))
		if err != nil || out != "" {
			t.Errorf("%q off the dispatch branch: err=%v out=%q, want it left alone", cmd, err, out)
		}
	}
}

// hookMatchers maps each PreToolUse matcher in a settings file to the gate mode
// (the word before the harness name) its command runs.
func hookMatchers(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	got := map[string]string{}
	for _, g := range doc.Hooks["PreToolUse"] {
		for _, h := range g.Hooks {
			switch {
			case strings.Contains(h.Command, "commitgate"):
				got[g.Matcher] = "commitgate"
			case strings.Contains(h.Command, "gate"):
				got[g.Matcher] = "gate"
			}
		}
	}
	return got
}

func TestClaudeCloudHookTrackedSettingsConfined(t *testing.T) {
	e := newCloudHookEnv(t, "main")
	tracked := filepath.Join(e.repo, filepath.FromSlash(cloudHookSettings))

	b, err := os.ReadFile(tracked)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || top["hooks"] == nil {
		keys := make([]string, 0, len(top))
		for k := range top {
			keys = append(keys, k)
		}
		t.Fatalf("tracked %s must hold only a top-level hooks key, has %v", cloudHookSettings, keys)
	}
	var events map[string][]struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(top["hooks"], &events); err != nil {
		t.Fatal(err)
	}
	for ev, groups := range events {
		if ev != "PreToolUse" {
			t.Errorf("tracked settings wire hook event %q; only PreToolUse is allowed", ev)
		}
		for _, g := range groups {
			for _, h := range g.Hooks {
				if !strings.Contains(h.Command, "scripts/claude-cloud-hook.sh") {
					t.Errorf("%s entry runs %q, want scripts/claude-cloud-hook.sh", ev, h.Command)
				}
			}
		}
	}
	if len(events["PreToolUse"]) == 0 {
		t.Error("tracked settings have no PreToolUse entry")
	}

	// Matcher drift: what init scaffolds for claude must match the tracked matchers.
	mustRun(t, testBin, e.repo, "init", "--harness", "claude", "--no-workspace")
	want := hookMatchers(t, filepath.Join(e.repo, ".claude", "settings.json"))
	if len(want) == 0 {
		t.Fatal("init wrote no PreToolUse matchers into .claude/settings.json")
	}
	if got := hookMatchers(t, tracked); !reflect.DeepEqual(got, want) {
		t.Errorf("tracked PreToolUse matchers %v differ from `satelle init` %v", got, want)
	}
}

// The script carries no bootstrap and no instruction to open the network to the
// hosted service: a cloud performer needs neither.
func TestClaudeCloudHookHasNoBootstrap(t *testing.T) {
	e := newCloudHookEnv(t, "main")
	b, err := os.ReadFile(filepath.Join(e.repo, filepath.FromSlash(cloudHookScript)))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"satelle.dev", "satelle init", "install.sh", "allowlist"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("%s still mentions %q", cloudHookScript, banned)
		}
	}
}
