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

// The cd/env-assignment walk is shared with gitCommandDir (sty_3a9b06fe): these
// pin what it hands the containment classifier — a redirect on a cd or env-only
// segment resolves against the cwd BEFORE that segment's own cd, and a later
// segment sees the updated cwd, with env assignments stripped before sed's args.
func TestBashMutationTargetsSegmentWalk(t *testing.T) {
	anchor := "/work/repo"
	cases := []struct {
		name, command string
		home, foreign []string
	}{
		{"redirect on a cd segment uses the pre-cd cwd", "cd sub > out.txt", []string{"/work/repo/out.txt"}, nil},
		{"redirect on a second cd uses the first cd's cwd", "cd /other/x; cd y > o.txt", nil, []string{"/other/x/o.txt"}},
		{"redirect on an env-only segment", "FOO=1 > f", []string{"/work/repo/f"}, nil},
		{"sed path resolves under the updated cwd", "cd other && sed -i 's/a/b/' f", []string{"/work/repo/other/f"}, nil},
		{"sed after env assignment keeps its slot", "FOO=1 sed -i 's/a/b/' f", []string{"/work/repo/f"}, nil},
		{"cd alone is no target", "cd /other && ls", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, foreign := bashMutationTargets(tc.command, anchor)
			if strings.Join(home, ",") != strings.Join(tc.home, ",") || strings.Join(foreign, ",") != strings.Join(tc.foreign, ",") {
				t.Fatalf("home=%v foreign=%v, want home=%v foreign=%v", home, foreign, tc.home, tc.foreign)
			}
		})
	}
}

func TestGitCommandDir(t *testing.T) {
	const base = "/base/tree"
	cases := []struct {
		command, want string
	}{
		{"git commit -m x", ""},
		{"FOO=1 git push", ""},
		{"git status", ""},
		{"cd /a && ls", ""},
		{"cd /a && git commit -m x", "/a"},
		{"cd sub && git push", "/base/tree/sub"},
		{"cd /a; cd b && git push", "/a/b"},
		{"cd /a && FOO=1 git commit -m x", "/a"},
		{"git -C /a commit -m x", "/a"},
		{"git -C rel commit -m x", "/base/tree/rel"},
		{"cd /a && git -C ../x commit -m x", "/x"},
		{"git -C /a -C b push", "/a/b"},
		// -C after the subcommand is commit's own option (reuse message), not a dir.
		{"git commit -C HEAD", ""},
		// A later cd does not move a command that already ran.
		{"git commit -m x && cd /a", ""},
		// Only the first commit/push is considered.
		{"cd /a && git commit -m x && cd /b && git push", "/a"},
		{`echo "cd /a && git commit"`, ""},
	}
	for _, tc := range cases {
		if got := gitCommandDir(tc.command, base); got != tc.want {
			t.Errorf("gitCommandDir(%q) = %q, want %q", tc.command, got, tc.want)
		}
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

// sty_bc78617c AC1: sed is a read unless run in place; the script is never a
// target.
func TestSedTargetsOnlyWhenInPlace(t *testing.T) {
	anchor := "/work/repo"
	cases := []struct {
		command string
		want    []string // in-home targets; nil = none
	}{
		{"sed -n 332,463p internal/web/web.go", nil},
		{"sed -n 1,5p f", nil},
		{"sed 's/a/b/' f", nil},
		{"sed -e s/a/b/ f", nil},
		{"sed -ne 1p f", nil},
		{"sed --expression=s/a/b/ f", nil},
		{"sed --expression s/a/b/ f", nil},
		{"sed -f script.sed f", nil},
		{"sed -E -s 's/a/b/' f g", nil},
		{"sed -i s/a/b/ f", []string{"/work/repo/f"}},
		{"sed -i.bak s/a/b/ f", []string{"/work/repo/f"}},
		{"sed -ni 1p f", []string{"/work/repo/f"}},
		{"sed -in 1p f", []string{"/work/repo/f"}},
		{"sed --in-place s/a/b/ f", []string{"/work/repo/f"}},
		{"sed --in-place=.bak s/a/b/ f", []string{"/work/repo/f"}},
		{"sed -i -e s/a/b/ f g", []string{"/work/repo/f", "/work/repo/g"}},
		{"sed -i -f script.sed f", []string{"/work/repo/f"}},
		{"sed -i -- s/a/b/ f", []string{"/work/repo/f"}},
		// Quoted script / -e / -f values occupy their slot; the file is the target.
		{"sed -i 's/a/b/' internal/x.go", []string{"/work/repo/internal/x.go"}},
		{`sed -i "s/a/b/" internal/x.go`, []string{"/work/repo/internal/x.go"}},
		{"sed -i -e 's/a/b/' internal/x.go", []string{"/work/repo/internal/x.go"}},
		{`sed -i -e "s/a/b/" internal/x.go`, []string{"/work/repo/internal/x.go"}},
		{"sed -i --expression 's/a/b/' internal/x.go", []string{"/work/repo/internal/x.go"}},
		{"sed -i -f 'my script.sed' internal/x.go", []string{"/work/repo/internal/x.go"}},
		{"sed -i.bak 's/a/b/' f g", []string{"/work/repo/f", "/work/repo/g"}},
		{"sed -i 's/a/b/' 'internal/x.go'", []string{"/work/repo/internal/x.go"}},
		{"FOO=1 sed -i 's/a/b/' f", []string{"/work/repo/f"}},
		{"sed -n '1,5p' f", nil},
		{`sed -e "s/a/b/" f`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			home, foreign := bashMutationTargets(tc.command, anchor)
			if len(foreign) != 0 {
				t.Fatalf("foreign = %v, want none", foreign)
			}
			if strings.Join(home, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("inHome = %v, want %v", home, tc.want)
			}
			if got, want := bashMutatesTree(tc.command, anchor), len(tc.want) > 0; got != want {
				t.Fatalf("bashMutatesTree = %v, want %v", got, want)
			}
		})
	}
}

// sty_bc78617c AC2/AC3: writes confined to the temp dir or /tmp are not tree
// mutations (a leading ~ or $VAR the hook's environment expands counts); an
// unresolvable $ or ~ word stays in-home; real tree writes are still targets.
func TestTempWriteTargetsAreNotTreeMutations(t *testing.T) {
	anchor := "/work/repo"
	scratch := "/tmp/claude-1000/p/s/scratchpad"
	t.Setenv("TMPDIR", "/tmp/custom")
	t.Setenv("SCRATCH", scratch)
	t.Setenv("HOME", "/tmp/fakehome")
	t.Setenv("RELVAR", "relative/dir")
	t.Setenv("EMPTY_FOR_TEST", "")

	notMutating := []string{
		"mkdir -p " + scratch + "/octop; curl -sfL https://x/y -o " + scratch + "/octop/f",
		"mkdir -p /tmp/x && touch /tmp/x/f",
		"echo hi > /tmp/x/out",
		"mkdir -p $TMPDIR/x",
		"mkdir -p ${TMPDIR}/x",
		"mkdir -p $SCRATCH/octop",
		"touch $SCRATCH/octop/f",
		"mkdir -p ~/octop",
		"echo hi > $TMPDIR/out",
		"cp internal/x.go $SCRATCH/copy.go",
		"cd $SCRATCH && touch f",
	}
	for _, c := range notMutating {
		if bashMutatesTree(c, anchor) {
			t.Errorf("bashMutatesTree(%q) = true, want false", c)
		}
	}

	mutating := []string{
		"sed -i s/a/b/ internal/x.go",
		"rm internal/x.go",
		"echo x > internal/x.go",
		"mkdir -p internal/newdir",
		"mkdir -p $EMPTY_FOR_TEST/x",
		"mkdir -p $NO_SUCH_VAR_FOR_TEST/x",
		"mkdir -p ${NO_SUCH_VAR_FOR_TEST}/x",
		"mkdir -p ~someone/x",
		"mkdir -p $RELVAR/x",
		"mkdir -p $SCRATCHsuffix/x",
		"mkdir -p $(mktemp -d)/x",
	}
	for _, c := range mutating {
		if !bashMutatesTree(c, anchor) {
			t.Errorf("bashMutatesTree(%q) = false, want true", c)
		}
	}

	// A var that expands into the tree is a tree target.
	t.Setenv("INTREE", anchor+"/internal")
	if !bashMutatesTree("rm $INTREE/x.go", anchor) {
		t.Error("var expanding into the anchor must stay a tree mutation")
	}
}
