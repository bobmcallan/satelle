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

func TestMutationTargets(t *testing.T) {
	// Pure candidate extractor: paths outside anchor surface even when they
	// are non-repo (foreignTreeTarget decides). /dev/null is a candidate here;
	// the FS filter allows it because it has no .git ancestor.
	anchor := "/home/u/home-repo"
	other := "/home/u/other-repo"

	cases := []struct {
		name    string
		cmd     string
		wantAny string // substring that must appear in some candidate; empty = want none
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
			name:    "in-home rm no candidate",
			cmd:     "rm internal/x.go",
			wantAny: "",
		},
		{
			name:    "story create after cd other no candidate",
			cmd:     "cd " + other + " && satelle story create --title t --body b --acceptance '1. a'",
			wantAny: "",
		},
		{
			name:    "story create plain no candidate",
			cmd:     "satelle story create --title t --body b --acceptance '1. a'",
			wantAny: "",
		},
		{
			name:    "git commit in home no candidate",
			cmd:     "git commit -m x",
			wantAny: "",
		},
		{
			name:    "tee to other",
			cmd:     "echo x | tee " + other + "/log",
			wantAny: filepath.Join(other, "log"),
		},
		{
			name:    "redirect to /dev/null is still a candidate (filter allows)",
			cmd:     "ls 2>/dev/null; echo hi >/dev/null",
			wantAny: "/dev/null",
		},
		{
			name:    "cp source outside dest home — only dest (in-home → no candidate)",
			cmd:     "cp " + other + "/src.go ./dst.go",
			wantAny: "",
		},
		{
			name:    "mv dest outside — only dest",
			cmd:     "mv a/f " + other + "/g",
			wantAny: filepath.Join(other, "g"),
		},
		{
			name:    "rsync dest home — sources ignored",
			cmd:     "rsync -a " + other + "/ ./here/",
			wantAny: "",
		},
		{
			name:    "cp -t outside dir",
			cmd:     "cp -t " + other + "/dir a b",
			wantAny: filepath.Join(other, "dir"),
		},
		// sty_74c0556f: fd-duplication must not become a mutation target under a foreign cwd.
		{
			name:    "story list 2>&1 after cd other — no candidate (regression)",
			cmd:     "cd " + other + " && satelle story list 2>&1",
			wantAny: "",
		},
		{
			name:    "story create 2>&1 after cd other — no candidate",
			cmd:     "cd " + other + " && satelle story create --title x 2>&1",
			wantAny: "",
		},
		{
			name:    "story list >&2 after cd other — no candidate",
			cmd:     "cd " + other + " && satelle story list >&2",
			wantAny: "",
		},
		{
			name:    "story list 1>&2 after cd other — no candidate",
			cmd:     "cd " + other + " && satelle story list 1>&2",
			wantAny: "",
		},
		{
			name:    "story list 2>&1 | head after cd other — no candidate",
			cmd:     "cd " + other + " && satelle story list 2>&1 | head",
			wantAny: "",
		},
		{
			name:    "story list 2>&- after cd other — close-fd, no candidate",
			cmd:     "cd " + other + " && satelle story list 2>&-",
			wantAny: "",
		},
		{
			name:    "story list 2>&1- after cd other — fd-move, no candidate",
			cmd:     "cd " + other + " && satelle story list 2>&1-",
			wantAny: "",
		},
		{
			name:    "glued 2>err.log after cd other",
			cmd:     "cd " + other + " && echo x 2>err.log",
			wantAny: filepath.Join(other, "err.log"),
		},
		// Real file redirects into a foreign tree stay candidates.
		{
			name:    "echo redirect file after cd other",
			cmd:     "cd " + other + " && echo x > f.txt",
			wantAny: filepath.Join(other, "f.txt"),
		},
		{
			name:    "2> err.log after cd other",
			cmd:     "cd " + other + " && echo x 2> err.log",
			wantAny: filepath.Join(other, "err.log"),
		},
		{
			name:    "&> out.log after cd other",
			cmd:     "cd " + other + " && echo x &> out.log",
			wantAny: filepath.Join(other, "out.log"),
		},
		{
			name:    "csh-style >& file after cd other",
			cmd:     "cd " + other + " && echo x >& out.log",
			wantAny: filepath.Join(other, "out.log"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mutationTargets(tc.cmd, anchor)
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

func TestBashMutationTargetsClassifiesInHome(t *testing.T) {
	anchor := "/work/repo"
	cases := []struct {
		command string
		want    string
	}{
		{"sed -i s/a/b/ internal/x.go", "/work/repo/internal/x.go"},
		{"echo hi > internal/x.go", "/work/repo/internal/x.go"},
		{"echo hi | tee internal/x.go", "/work/repo/internal/x.go"},
		{"cp source.go internal/x.go", "/work/repo/internal/x.go"},
		{"mv source.go internal/x.go", "/work/repo/internal/x.go"},
		{"rm internal/x.go", "/work/repo/internal/x.go"},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			home, foreign := bashMutationTargets(tc.command, anchor)
			if len(foreign) != 0 {
				t.Fatalf("foreign = %v, want none", foreign)
			}
			found := false
			for _, p := range home {
				if p == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("inHome = %v, want %q", home, tc.want)
			}
		})
	}
	for _, command := range []string{
		"git status",
		"rg TODO internal",
		`satelle story attach sty_x --name note --type note --body "later rm -rf internal"`,
		`echo "rm internal/x.go"`,
	} {
		home, _ := bashMutationTargets(command, anchor)
		if len(home) != 0 {
			t.Errorf("read/prose command %q classified as mutation: %v", command, home)
		}
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
