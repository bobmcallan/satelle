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
		{`echo hi | tee "internal/x.go"`, "/work/repo/internal/x.go"},
		{`echo hi | tee -a 'internal/x.go'`, "/work/repo/internal/x.go"},
		{"env tee internal/x.go", "/work/repo/internal/x.go"},
		{`true | env tee "README.md"`, "/work/repo/README.md"},
		{`sudo tee 'internal/x.go'`, "/work/repo/internal/x.go"},
		{`nohup tee -a "README.md"`, "/work/repo/README.md"},
		{`xargs tee "README.md"`, "/work/repo/README.md"},
		{`stdbuf -o0 tee "README.md"`, "/work/repo/README.md"},
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

// inlineAnchor is the session tree the inline-interpreter cases run against; the
// fake file checker knows these files under it, so a named script is found without
// touching the disk (sty_5fac62b9).
const inlineAnchor = "/work/repo"

// inlineHome is the HOME the inline test pins, so ~/.bashrc expands to a path the
// fake checker knows: the --rcfile case passes only because --rcfile takes its value.
const inlineHome = "/work/home"

func inlineFiles(files ...string) func(string) bool {
	known := map[string]bool{}
	for _, f := range files {
		if strings.HasPrefix(f, "/") {
			known[f] = true
			continue
		}
		known[inlineAnchor+"/"+f] = true
	}
	return func(p string) bool { return known[p] }
}

var inlineChecker = inlineFiles("tool.py", "build.js", "script.sh", "scripts/x.sh", "x.pl", "x.js",
	"x.rb", "x.py", "setup.js", "d.json", "a.go", "x.go", "f", "x", "script",
	inlineHome+"/.bashrc")

// AC1: an interpreter handed inline program text writes where the command line
// cannot show it, so the edit gate treats it as a tree write.
func TestBashMutatesTreeInlineInterpreter(t *testing.T) {
	t.Setenv("HOME", inlineHome)
	for _, c := range []string{
		"python3 - <<'PY'\nopen('a.go','w').write('x')\nPY",
		`python3 -c "open('a.go','w')"`,
		`cd internal && python3 -c "open('a.go','w')"`,
		`python3 -BcCODE`,
		`node -e "require('fs')"`,
		`perl -pi -e 's/a/b/' f`,
		`perl -i -lpe 's/a/b/' x.go`,
		`perl -0pe 's/a/b/' f`,
		`perl -Te 'print 1'`,
		`perl -de 'print 1'`,
		`perl -CSDAe 'print 1'`,
		`ruby -de 'puts 1'`,
		`ruby -We 'File.write("a.go","x")'`,
		`ruby -Ee 'File.write("a.go","x")'`,
		`bash -c "echo hi > a.go"`,
		"cat x | python3 -",
		"bash <<'SH'\nsed -i s/a/b/ x.go\nSH",
		"cat script | sh",
		"bash -s",
		"sh -",
		`bash -o pipefail -c "echo"`,
		`bash --rcfile ~/.bashrc -c "echo"`,
		`bash --login -c "echo"`,
		`bash --noprofile --norc -c "echo"`,
		`node -r ./setup.js -e "1"`,
		`node --no-warnings -e "1"`,
		`python3 -W ignore -c "1"`,
		`X=1 python3 -c "1"`,
		`env X=1 python3 -c "1"`,
		`node --eval "1"`,
		`python3.11 -c "1"`,
		// AC3 fail-closed limits.
		"bash -s arg <<'EOF'\nrm x.go\nEOF",
		"cat d | python3 missing.py",
		"cat s | bash --norc script.sh",
		"python3 -Wignore::c x.py",
	} {
		if !bashMutatesTreeWith(c, inlineAnchor, inlineChecker) {
			t.Errorf("bashMutatesTreeWith(%q) = false, want true", c)
		}
	}
}

// AC3: a named script file, a module, or a flag that is not a code flag leaves the
// program off the command line's account.
func TestBashMutatesTreeNamedScriptIsNotInline(t *testing.T) {
	for _, c := range []string{
		"python3 tool.py",
		"python3 -m pytest",
		"node build.js",
		"bash script.sh",
		"cat d.json | python3 tool.py",
		`cat d.json | python3 "tool.py"`,
		"python3 tool.py <<EOF\nrm x.go\nEOF",
		"bash -e script.sh",
		"bash -ex ./scripts/x.sh",
		"python3 -E tool.py",
		"perl -c x.pl",
		"node -c x.js",
		"perl -I/home/x/lib x.pl",
		"ruby -E UTF-8 x.rb",
		"python3 tool.py -c x",
		"python3",
		"node --version",
	} {
		if bashMutatesTreeWith(c, inlineAnchor, inlineChecker) {
			t.Errorf("bashMutatesTreeWith(%q) = true, want false", c)
		}
	}
	// With no checker no file exists: a piped script name fails closed.
	if !bashMutatesTreeWith("cat d.json | python3 tool.py", inlineAnchor, nil) {
		t.Error("a nil file checker must fail closed")
	}
}

// AC4: outside the tree an inline program is a write only when its text names the
// tree's root as a whole path token.
func TestBashMutatesTreeInlineInterpreterOutsideTree(t *testing.T) {
	for _, c := range []string{
		`cd /tmp && python3 -c "open('/work/repo/a.go','w')"`,
		"cd /tmp && cat <<'EOF' | python3 -\nopen('/work/repo/a.go','w')\nEOF",
	} {
		if !bashMutatesTreeWith(c, inlineAnchor, inlineChecker) {
			t.Errorf("bashMutatesTreeWith(%q) = false, want true", c)
		}
	}
	for _, c := range []string{
		`cd /tmp && python3 -c 'print(1)'`,
		`cd /tmp && python3 -c "open('/work/repo-other/x.go','w')"`,
	} {
		if bashMutatesTreeWith(c, inlineAnchor, inlineChecker) {
			t.Errorf("bashMutatesTreeWith(%q) = true, want false", c)
		}
	}
}
