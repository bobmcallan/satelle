package cli

import (
	"reflect"
	"testing"
)

// segOf tokenizes one command line into its single segment.
func segOf(t *testing.T, cmd string) []bashTok {
	t.Helper()
	segs := segmentWords(tokenizeBash(cmd))
	if len(segs) != 1 {
		t.Fatalf("%q: want one segment, got %d", cmd, len(segs))
	}
	return segs[0]
}

// TestRedirectTargetsQuoted (sty_e642df17): a quoted word after a redirect
// operator is that redirect's target, operators are never targets, and a
// descriptor duplication names no file.
func TestRedirectTargetsQuoted(t *testing.T) {
	const cwd = "/repo"
	cases := []struct {
		cmd  string
		want []string
	}{
		// The observed shape: the quoted target was dropped and `2>` taken instead.
		{`make test > "/tmp/x/maketest.log" 2>&1`, []string{"/tmp/x/maketest.log"}},
		{`cmd > "/tmp/x.log"`, []string{"/tmp/x.log"}},
		{`cmd > '/tmp/x.log'`, []string{"/tmp/x.log"}},
		{`cmd >"/tmp/x.log" 2>>"/tmp/err.log"`, []string{"/tmp/x.log", "/tmp/err.log"}},
		{`cmd &> /tmp/x.log`, []string{"/tmp/x.log"}},
		{`cmd &>> "/tmp/x.log"`, []string{"/tmp/x.log"}},
		{`cmd > /tmp/x.log 2>&1`, []string{"/tmp/x.log"}},
		{`cmd > "rel/out.log"`, []string{"/repo/rel/out.log"}},
		{`cat >> '/tmp/x/out.md' <<'EOF'` + "\nbody\nEOF", []string{"/tmp/x/out.md"}},
		// Unresolvable targets stay conservatively in-home.
		{`echo x > "$UNSET_VAR_FOR_TEST/x.log"`, []string{"/repo/$UNSET_VAR_FOR_TEST/x.log"}},
		{`echo x > "$(pwd)/README.md"`, []string{"/repo/$(pwd)/README.md"}},
		// Operators and descriptor duplications are never targets.
		{`cmd 2>&1`, nil},
		{`cmd >&-`, nil},
		{`cmd 2> &1`, nil},
		{`cmd 2>`, nil},
		{`cmd > 2>`, nil},
		{`cmd >> 2>>`, nil},
		{`cmd > &>>`, nil},
		{`cmd > <<'EOF'` + "\nbody\nEOF", nil},
		{`cmd 2>/dev/null`, []string{"/dev/null"}},
	}
	for _, c := range cases {
		got := redirectTargets(segOf(t, c.cmd), cwd)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("redirectTargets(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestIsRedirectOp(t *testing.T) {
	for _, w := range []string{">", ">>", ">|", ">&", "1>", "2>", "2>>", "&>", "&>>", "10>"} {
		if !isRedirectOp(w) {
			t.Errorf("isRedirectOp(%q) = false, want true", w)
		}
	}
	for _, w := range []string{"", ">file", "2>file", "&", "2", "a>", "<", ">>>"} {
		if isRedirectOp(w) {
			t.Errorf("isRedirectOp(%q) = true, want false", w)
		}
	}
}

// TestQuotedTeeSameVerdictInEverySlot (sty_e79be13a): a path gets one verdict as
// a tee destination (command word or behind a wrapper), a redirect target and a
// mutation-verb argument, quoted or bare, in or out of the repo.
func TestQuotedTeeSameVerdictInEverySlot(t *testing.T) {
	const anchor = "/work/repo"
	for _, p := range []string{"README.md", "internal/x.go", "/tmp/x/out.log"} {
		for _, q := range []string{p, `"` + p + `"`, `'` + p + `'`} {
			var want []string
			for _, c := range []string{"echo x | tee " + q, "echo x | env tee " + q, "echo x > " + q, "rm " + q} {
				home, foreign := bashMutationTargets(c, anchor)
				got := append(append([]string{}, home...), foreign...)
				if len(got) != 1 {
					t.Fatalf("%q: targets %v, want one", c, got)
				}
				if want == nil {
					want = got
					continue
				}
				if got[0] != want[0] {
					t.Errorf("%q: target %q, want %q", c, got[0], want[0])
				}
			}
		}
	}
	got := redirectTargets(segOf(t, `tee "f" > /tmp/o`), "/work/repo")
	if len(got) != 2 || got[0] != "/work/repo/f" || got[1] != "/tmp/o" {
		t.Errorf(`tee "f" > /tmp/o: targets %q, want [/work/repo/f /tmp/o]`, got)
	}
}

// TestQuotedPathSameVerdictInEverySlot (sty_e642df17): one quoted path resolves
// to the same value as a redirect target, a mutation-verb argument and a sed -i
// path, single- or double-quoted.
func TestQuotedPathSameVerdictInEverySlot(t *testing.T) {
	const cwd = "/repo"
	for _, q := range []string{`"/tmp/x/f.log"`, `'/tmp/x/f.log'`, `"rel/f.log"`, `'rel/f.log'`, `"$UNSET_VAR_FOR_TEST/f"`} {
		red := redirectTargets(segOf(t, "echo > "+q), cwd)
		rm := mutationPathArgs(commandArgWords(segOf(t, "rm "+q)), cwd)
		sed := sedPathArgs(segOf(t, "sed -i 's/a/b/' "+q), cwd)
		if len(red) != 1 || len(rm) != 1 || len(sed) != 1 {
			t.Fatalf("%s: redirect=%q rm=%q sed=%q, want one each", q, red, rm, sed)
		}
		if red[0] != rm[0] || rm[0] != sed[0] {
			t.Errorf("%s: redirect=%q rm=%q sed=%q differ", q, red, rm, sed)
		}
	}
}
