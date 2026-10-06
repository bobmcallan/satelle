//go:build integration

package tests

// Offline proof of the Claude cloud-session hook carrier (sty_3b112554): the
// tracked .claude/settings.local.json runs scripts/claude-cloud-hook.sh, which
// is inert locally and, when CLAUDE_CODE_REMOTE=true, bootstraps satelle and
// delegates the edit and commit gates. No network: a built satelle is put on
// PATH so the script's install step is skipped.

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
	bin  string
}

// newCloudHookEnv builds a temporary git clone holding only the tracked carrier
// files, a temporary HOME, and a bin dir exposing the built satelle as `satelle`.
func newCloudHookEnv(t *testing.T) cloudHookEnv {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	e := cloudHookEnv{repo: t.TempDir(), home: t.TempDir(), bin: t.TempDir()}
	if err := os.Symlink(testBin, filepath.Join(e.bin, "satelle")); err != nil {
		t.Fatal(err)
	}
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
	if out, err := exec.Command("git", "-C", e.repo, "init", "-q").CombinedOutput(); err != nil {
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
			strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "PATH=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HOME="+e.home,
		"PATH="+e.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CLAUDE_PROJECT_DIR="+e.repo,
	)
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

func (e cloudHookEnv) commitPayload() string {
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "git commit -m probe"},
		"cwd":             e.repo,
	})
	return string(b)
}

// denyReason returns the PreToolUse permissionDecisionReason when out is a deny.
func denyReason(out string) (string, bool) {
	var v struct {
		Hook struct {
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		return "", false
	}
	return v.Hook.Reason, v.Hook.Decision == "deny"
}

func TestClaudeCloudHookInertLocally(t *testing.T) {
	e := newCloudHookEnv(t)
	for _, c := range []struct{ mode, stdin string }{
		{"session", ""}, {"gate", e.editPayload()}, {"commitgate", e.commitPayload()},
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

func TestClaudeCloudHookBootstrapAndGates(t *testing.T) {
	e := newCloudHookEnv(t)

	out, err := e.hook(t, true, "session", "")
	if err != nil {
		t.Fatalf("session hook: %v\n%s", err, out)
	}
	for _, rel := range []string{".satelle", ".claude/settings.json", ".satelle/hooks/satelle-hook.sh"} {
		if _, err := os.Stat(filepath.Join(e.repo, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("after cloud SessionStart %s missing: %v\nhook output:\n%s", rel, err, out)
		}
	}
	if !strings.Contains(out, "Always-resident principles") {
		t.Errorf("SessionStart did not emit `satelle hook context`:\n%s", out)
	}

	for name, c := range map[string]struct{ mode, stdin string }{
		"edit":   {"gate", e.editPayload()},
		"commit": {"commitgate", e.commitPayload()},
	} {
		out, err := e.hook(t, true, c.mode, c.stdin)
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		if reason, ok := denyReason(out); !ok {
			t.Errorf("%s with no engaged story was not denied: %q", name, out)
		} else if strings.Contains(reason, "bootstrap absent") {
			t.Errorf("%s denied by the missing-hook fallback, not satelle's gate: %q", name, reason)
		}
	}

	// Fail closed: with the delegated hook gone the script itself denies.
	if err := os.Remove(filepath.Join(e.repo, ".satelle", "hooks", "satelle-hook.sh")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ mode, stdin string }{
		{"gate", e.editPayload()}, {"commitgate", e.commitPayload()},
	} {
		out, err := e.hook(t, true, c.mode, c.stdin)
		if err != nil {
			t.Fatalf("%s without hook: %v\n%s", c.mode, err, out)
		}
		if reason, ok := denyReason(out); !ok || !strings.Contains(reason, "bootstrap absent") {
			t.Errorf("%s without .satelle/hooks/satelle-hook.sh did not fail closed: %q", c.mode, out)
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
	e := newCloudHookEnv(t)
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
		if ev != "SessionStart" && ev != "PreToolUse" {
			t.Errorf("tracked settings wire hook event %q; only SessionStart and PreToolUse are allowed", ev)
		}
		for _, g := range groups {
			for _, h := range g.Hooks {
				if !strings.Contains(h.Command, "scripts/claude-cloud-hook.sh") {
					t.Errorf("%s entry runs %q, want scripts/claude-cloud-hook.sh", ev, h.Command)
				}
			}
		}
	}
	for _, ev := range []string{"SessionStart", "PreToolUse"} {
		if len(events[ev]) == 0 {
			t.Errorf("tracked settings have no %s entry", ev)
		}
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
