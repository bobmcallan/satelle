//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// declareStoryHost points remote "edge" at a fake host that runs {command} under
// sh, and gives it storyCommand and a host.env entry read from the process
// environment.
func declareStoryHost(t *testing.T, repo, storyCommand string) {
	t.Helper()
	path := filepath.Join(repo, ".satelle", filepath.FromSlash(config.RemotesDir), "edge", "remote.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	run := `run = ["your-host-cli", "run", "{label}", "{command}"]`
	story := "# story_command = \"your-agent-cli {story}\"\n"
	s := string(b)
	if !strings.Contains(s, run) || !strings.Contains(s, story) {
		t.Fatalf("scaffold lacks the host.run or story_command lines:\n%s", s)
	}
	s = strings.Replace(s, run, `run = ["sh", "-c", "{command}"]`, 1)
	if storyCommand != "" {
		s = strings.Replace(s, story, "story_command = "+`"`+storyCommand+`"`+"\n\n[host.env]\nMARK_DIR = \"${SATELLE_TEST_MARK}\"\n", 1)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func handbackRows(t *testing.T, story string) []ledger.Entry {
	t.Helper()
	var out []ledger.Entry
	for _, e := range ledgerRows(t, ledger.KindRemoteHandback) {
		if e.StoryID == story {
			out = append(out, e)
		}
	}
	return out
}

// TestRemoteWakeRunsStoryOnFakeHost: a definition that declares
// host.story_command makes the wake run the rendered command through host.run on
// the fake host, under the expanded host.env, and re-read the story afterwards.
func TestRemoteWakeRunsStoryOnFakeHost(t *testing.T) {
	t.Run("declared", func(t *testing.T) {
		repo := remoteWithTriggers(t)
		mark := t.TempDir()
		t.Setenv("SATELLE_TEST_MARK", mark)
		declareStoryHost(t, repo, "touch $MARK_DIR/{story}")
		id := newStory(t, "in_progress")

		out, err := runRootIn(t, `{"trigger_id":"fire_1","remote":"edge"}`, "remote", "wake", "edge")
		if err != nil {
			t.Fatalf("wake: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(mark, id)); err != nil {
			t.Errorf("the story command did not run with the rendered story and the expanded host.env: %v\n%s", err, out)
		}
		if !strings.Contains(out, "handed off "+id) {
			t.Errorf("no hand-off line for %s:\n%s", id, out)
		}
		if !strings.Contains(out, "story "+id+" left at in_progress") {
			t.Errorf("the hand-back check did not report the story left at in_progress:\n%s", out)
		}
		if n := len(handbackRows(t, id)); n != 1 {
			t.Errorf("want one remote_handback row for %s, got %d", id, n)
		}
	})

	t.Run("no story command", func(t *testing.T) {
		repo := remoteWithTriggers(t)
		mark := t.TempDir()
		t.Setenv("SATELLE_TEST_MARK", mark)
		declareStoryHost(t, repo, "")
		id := newStory(t, "in_progress")

		out, err := runRootIn(t, `{"trigger_id":"fire_1","remote":"edge"}`, "remote", "wake", "edge")
		if err != nil {
			t.Fatalf("wake: %v\n%s", err, out)
		}
		if !strings.Contains(out, "handed off "+id) {
			t.Errorf("no hand-off line for %s:\n%s", id, out)
		}
		if entries, _ := os.ReadDir(mark); len(entries) != 0 {
			t.Errorf("a definition with no story command must run nothing, found %d marker(s)", len(entries))
		}
		if n := len(handbackRows(t, id)); n != 0 || strings.Contains(out, "left at") {
			t.Errorf("a record-only wake must not hand back (%d rows):\n%s", n, out)
		}
	})
}
