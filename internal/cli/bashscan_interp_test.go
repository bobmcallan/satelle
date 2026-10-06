package cli

// sty_dc77e118 — the classifier pieces the Bash substrate lock rests on: the
// heredoc tokenizer keeps what follows the opener, interpreter commands are
// recognised, and the lock-root scan matches whole tokens only.

import (
	"reflect"
	"strings"
	"testing"
)

func TestTokenizeBashHeredocOpenerRestAndNextLine(t *testing.T) {
	toks := tokenizeBash("cat <<'EOF' > out.md\nbody > not-a-target\nEOF\nrm x")
	segs := segmentWords(toks)
	if len(segs) != 2 {
		t.Fatalf("want two segments (the heredoc command, then rm), got %d: %+v", len(segs), segs)
	}
	if got := wordsOnly(segs[0]); !reflect.DeepEqual(got, []string{"cat", ">", "out.md"}) {
		t.Errorf("heredoc segment words = %v, want the opener-line redirect kept and the body dropped", got)
	}
	if got := wordsOnly(segs[1]); !reflect.DeepEqual(got, []string{"rm", "x"}) {
		t.Errorf("segment after the heredoc = %v", got)
	}
	var raw string
	for _, tk := range segs[0] {
		if tk.Value == "HEREDOC" {
			raw = tk.Raw
		}
	}
	if raw == "" || !strings.Contains(raw, "body > not-a-target") {
		t.Errorf("the heredoc token must keep its body in Raw, got %q", raw)
	}
}

func TestBashMutationTargetsSeesHeredocOpenerRedirect(t *testing.T) {
	in, _ := bashMutationTargets("cat <<'EOF' > .satelle/skills/x.md\nbody\nEOF", "/repo")
	if !reflect.DeepEqual(in, []string{"/repo/.satelle/skills/x.md"}) {
		t.Errorf("targets = %v", in)
	}
	in, _ = bashMutationTargets("cat <<'EOF'\nbody > .satelle/body.md\nEOF", "/repo")
	if len(in) != 0 {
		t.Errorf("a heredoc body is data, not a redirect: %v", in)
	}
}

func TestIsInterpreterCommand(t *testing.T) {
	yes := [][]string{
		{"python", "x.py"}, {"python3", "-c", "x"}, {"python3.11", "-"}, {"/usr/bin/python3", "-"},
		{"node", "-e", "x"}, {"nodejs", "x"}, {"perl", "-e", "x"}, {"ruby", "-e", "x"},
		{"bash", "-c"}, {"sh", "-c"}, {"zsh", "-c"}, {"bash", "-lc"}, {"bash", "-ec"},
		{"env", "python3", "-"}, {"env", "-i", "FOO=1", "python3", "-"},
	}
	no := [][]string{
		nil, {"ls"}, {"cat", "python"}, {"bash", "script.sh"}, {"sh", "--version"},
		{"pythonic"}, {"git", "commit"}, {"env"}, {"env", "FOO=1"}, {"rm", "x"},
	}
	for _, w := range yes {
		if !isInterpreterCommand(w) {
			t.Errorf("isInterpreterCommand(%v) = false, want true", w)
		}
	}
	for _, w := range no {
		if isInterpreterCommand(w) {
			t.Errorf("isInterpreterCommand(%v) = true, want false", w)
		}
	}
}

func TestLockPathRefsMatchesWholeTokensOnly(t *testing.T) {
	lock := []string{".satelle/"}
	cases := []struct {
		name, text string
		want       []string
	}{
		{"prefix path", `open('.satelle/documents/x.md','w')`, []string{".satelle/documents/x.md"}},
		{"bare token in quotes", `os.path.join('.satelle','documents')`, []string{".satelle"}},
		{"Path division", `Path('.satelle')/'documents'`, []string{".satelle"}},
		{"assignment", `D=.satelle; echo x > $D/x`, []string{".satelle"}},
		{"absolute", `open('/repo/.satelle/x','w')`, []string{"/repo/.satelle/x"}},
		{"relative with dot", `open('./.satelle/x')`, []string{"./.satelle/x"}},
		{"home form", `open('~/.satelle/x')`, []string{"~/.satelle/x"}},
		{"another name after", `print('.satellerc')`, nil},
		{"another name before", `print('my.satelle')`, nil},
		{"dashed name", `print('.satelle-x')`, nil},
		{"fragments never spell it", `open('.sat'+'elle/x')`, nil},
		{"two mentions", `a('.satelle/x'); b('.satelle/y')`, []string{".satelle/x", ".satelle/y"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lockPathRefs(tc.text, lock); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("lockPathRefs(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
	if got := lockPathRefs(`open('.satelle/x')`, nil); got != nil {
		t.Errorf("no lock paths, no refs: %v", got)
	}
	if got := lockPathRefs(`open('policy/x')`, []string{"policy/"}); !reflect.DeepEqual(got, []string{"policy/x"}) {
		t.Errorf("an operator prefix is scanned the same way: %v", got)
	}
}

func TestInterpreterPathRefs(t *testing.T) {
	lock := []string{".satelle/"}
	cases := []struct {
		name, cmd string
		want      []string
	}{
		{"python -c", `python3 -c "open('.satelle/x','w')"`, []string{"/repo/.satelle/x"}},
		{"heredoc body", "python3 - <<'EOF'\nopen('.satelle/x','w')\nEOF", []string{"/repo/.satelle/x"}},
		{"bash -c body", `bash -c 'echo x > .satelle/x'`, []string{"/repo/.satelle/x"}},
		{"bare root", `python3 -c "import os; os.path.join('.satelle','a')"`, []string{"/repo/.satelle"}},
		{"after cd resolves against both", `cd /elsewhere && python3 -c "open('.satelle/x')"`, []string{"/elsewhere/.satelle/x", "/repo/.satelle/x"}},
		{"piped into the interpreter", `echo "open('.satelle/x')" | python3`, []string{"/repo/.satelle/x"}},
		{"assignment word is not an interpreter ref", `D=.satelle; echo x > $D/x`, nil},
		{"assignment joins refs when an interpreter runs", `D=.satelle; python3 -c "open(os.environ['D'])"`, []string{"/repo/.satelle"}},
		{"not an interpreter", `cat .satelle/x`, nil},
		{"not piped into one", `echo ".satelle/x"; python3 -c "print(1)"`, nil},
		{"script file not named", `python3 tool.py`, nil},
		{"assembled", `python3 -c "open('.sat'+'elle/x')"`, nil},
		{"other name", `python3 -c "print('.satellerc')"`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := interpreterPathRefs(tc.cmd, "/repo", lock); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("interpreterPathRefs(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
	if got, asg := interpreterPathRefs(`python3 -c "open('.satelle/x')"`, "/repo", nil); got != nil || asg != nil {
		t.Errorf("an empty lock list locks nothing: %v %v", got, asg)
	}
	if _, asg := interpreterPathRefs(`D=.satelle; ls $D`, "/repo", lock); !reflect.DeepEqual(asg, []string{"/repo/.satelle"}) {
		t.Errorf("assignment words are returned apart: %v", asg)
	}
}

func TestSplitSegmentsRecordsPipes(t *testing.T) {
	segs := splitSegments(tokenizeBash("a | b && c; d |\n e"))
	var got []bool
	for _, s := range segs {
		got = append(got, s.Piped)
	}
	if want := []bool{false, true, false, false, true}; !reflect.DeepEqual(got, want) {
		t.Errorf("piped = %v, want %v", got, want)
	}
}
