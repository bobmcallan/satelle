package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestIsGitCommitOrPush(t *testing.T) {
	yes := []string{
		"git commit -m x",
		"cd /r && git push origin main",
		"git -C . commit -m x",
		"git -c user.email=x push",
		"/usr/bin/git commit -m ok",
		"git --no-pager commit -m x",
		"git --git-dir=.git commit -m x",
	}
	no := []string{
		"ls",
		"git status",
		"git diff",
		"git config --get commit.template",
		`echo "git commit is a phrase"`,
		`satelle story create --title "git commit" --body "git push later" --acceptance "1. a"`,
		`./satelle story set sty_x --status plan --body "mentions git commit and git push"`,
	}
	for _, c := range yes {
		if !isGitCommitOrPush(c) {
			t.Errorf("isGitCommitOrPush(%q) = false, want true", c)
		}
	}
	for _, c := range no {
		if isGitCommitOrPush(c) {
			t.Errorf("isGitCommitOrPush(%q) = true, want false", c)
		}
	}
}

func TestOutsideAnchorTargets(t *testing.T) {
	anchor := "/home/u/home-repo"
	other := "/home/u/other-repo"

	cases := []struct {
		name    string
		cmd     string
		wantAny string // substring that must appear in some escaped path; empty = want none
	}{
		{
			name:    "cd elsewhere then rm",
			cmd:     "cd " + other + " && rm file.go",
			wantAny: filepath.Join(other, "file.go"),
		},
		{
			name:    "git -C abs other commit",
			cmd:     "git -C " + other + " commit -m x",
			wantAny: other,
		},
		{
			name:    "redirect to sibling",
			cmd:     "echo hi > " + other + "/out.txt",
			wantAny: filepath.Join(other, "out.txt"),
		},
		{
			name:    "rm absolute other",
			cmd:     "rm " + other + "/f",
			wantAny: filepath.Join(other, "f"),
		},
		{
			name:    "in-home rm allowed (no escape)",
			cmd:     "rm internal/x.go",
			wantAny: "",
		},
		{
			name:    "story create after cd other allowed",
			cmd:     "cd " + other + " && satelle story create --title t --body b --acceptance '1. a'",
			wantAny: "",
		},
		{
			name:    "story create plain allowed",
			cmd:     "satelle story create --title t --body b --acceptance '1. a'",
			wantAny: "",
		},
		{
			name:    "git commit in home no escape",
			cmd:     "git commit -m x",
			wantAny: "",
		},
		{
			name:    "tee to other",
			cmd:     "echo x | tee " + other + "/log",
			wantAny: filepath.Join(other, "log"),
		},
		{
			name:    "redirect to /dev/null is benign",
			cmd:     "ls 2>/dev/null; echo hi >/dev/null",
			wantAny: "",
		},
		{
			name:    "spaced redirect to /dev/null",
			cmd:     "cmd > /dev/null 2>&1",
			wantAny: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := outsideAnchorTargets(tc.cmd, anchor)
			if tc.wantAny == "" {
				if len(got) != 0 {
					t.Fatalf("want no targets, got %v", got)
				}
				return
			}
			found := false
			for _, p := range got {
				if p == tc.wantAny || strings.HasPrefix(p, tc.wantAny) || strings.Contains(p, tc.wantAny) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("want a target containing %q, got %v", tc.wantAny, got)
			}
		})
	}
}

func TestTokenizeBashQuotedOpaque(t *testing.T) {
	// Prose in quotes must not yield word tokens "git" / "commit".
	toks := tokenizeBash(`echo "please git commit and git push"`)
	for _, tok := range toks {
		if tok.Kind == "word" && (tok.Value == "git" || tok.Value == "commit" || tok.Value == "push") {
			t.Errorf("quoted prose leaked word token %q", tok.Value)
		}
	}
	if isGitCommitOrPush(`echo "please git commit and git push"`) {
		t.Error("quoted prose must not match as git commit/push")
	}
}

func TestSegmentIsStoryEngage(t *testing.T) {
	if !segmentIsStoryEngage([]string{"satelle", "story", "set", "sty_x", "--status", "plan"}) {
		t.Error("plain engage should match")
	}
	if segmentIsStoryEngage([]string{"satelle", "story", "set", "sty_x", "--title", "t"}) {
		t.Error("set without --status is not engage")
	}
	if segmentIsStoryEngage([]string{"satelle", "story", "create", "--title", "t"}) {
		t.Error("create is not engage")
	}
}
