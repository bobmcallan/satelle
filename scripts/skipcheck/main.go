// skipcheck runs a `go test -json` command as its own child process and fails
// the run when the child fails or when any test or subtest skips without being
// on the reviewed allowlist (sty_53c3f311).
//
// Usage: skipcheck [-allow FILE] -- go test -json [flags] [packages]
//
// It is a wrapper, not a pipe: make runs recipes under /bin/sh, which has no
// pipefail, so `go test -json | filter` would turn a failing run green.
// Here the child's exit status is read directly and wins. The test2json stream
// is echoed to stdout so CI logs still read like a test run.
//
// The allowlist decides which skips are acceptable; this program only reads it.
// An entry is one line, `<package> <TestName[/subtest]> <reason>`. The reason is
// required. Blank lines and lines starting with # are ignored. Matching is exact
// on package and test name: no globs and no skip text, because skip messages
// share no pattern.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// event is the subset of a test2json record the check reads.
type event struct {
	Action  string
	Package string
	Test    string
	Output  string
}

// key identifies a test within a package.
type key struct{ pkg, test string }

// Skip is an unlisted skip: the test and the output it printed.
type Skip struct {
	Package, Test, Output string
}

// Allowlist maps an allowed skip to its reason.
type Allowlist map[key]string

// ParseAllowlist reads the allowlist format. A malformed line is an error that
// names its line number; an entry without a reason is malformed.
func ParseAllowlist(r io.Reader) (Allowlist, error) {
	allow := Allowlist{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 {
			return nil, fmt.Errorf("allowlist line %d: want `<package> <TestName[/subtest]> <reason>`, got %q (a reason is required)", n, line)
		}
		k := key{f[0], f[1]}
		if _, dup := allow[k]; dup {
			return nil, fmt.Errorf("allowlist line %d: duplicate entry for %s %s", n, k.pkg, k.test)
		}
		allow[k] = strings.Join(f[2:], " ")
	}
	return allow, sc.Err()
}

func loadAllowlist(path string) (Allowlist, error) {
	if path == "" {
		return Allowlist{}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	allow, err := ParseAllowlist(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return allow, nil
}

// Scan reads a test2json stream, echoes each output line to w, and returns the
// skips that are not allowed. A skip repeated by -count is reported once.
// Package-level skips (no Test, e.g. "no test files") are ignored. A line that
// is not a test2json record, such as a build error, is passed through as-is.
func Scan(r io.Reader, w io.Writer, allow Allowlist) ([]Skip, error) {
	var (
		bad  []Skip
		seen = map[key]bool{}
		buf  = map[key][]string{}
		rd   = bufio.NewReader(r)
	)
	for {
		line, err := rd.ReadString('\n')
		if line != "" {
			var ev event
			if json.Unmarshal([]byte(line), &ev) != nil || ev.Action == "" {
				io.WriteString(w, line)
			} else {
				io.WriteString(w, ev.Output)
				if ev.Test != "" {
					k := key{ev.Package, ev.Test}
					switch ev.Action {
					case "output":
						buf[k] = append(buf[k], ev.Output)
					case "skip":
						if _, ok := allow[k]; !ok && !seen[k] {
							seen[k] = true
							bad = append(bad, Skip{ev.Package, ev.Test, skipOutput(buf[k])})
						}
						delete(buf, k)
					case "pass", "fail":
						delete(buf, k)
					}
				}
			}
		}
		if err == io.EOF {
			return bad, nil
		}
		if err != nil {
			return bad, err
		}
	}
}

// skipOutput keeps the lines a skip printed, dropping the === RUN chatter.
func skipOutput(lines []string) string {
	var out []string
	for _, l := range strings.Split(strings.Join(lines, ""), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "=== ") {
			out = append(out, l)
		}
	}
	return strings.Join(out, " | ")
}

func report(w io.Writer, bad []Skip) {
	sort.Slice(bad, func(i, j int) bool {
		if bad[i].Package != bad[j].Package {
			return bad[i].Package < bad[j].Package
		}
		return bad[i].Test < bad[j].Test
	})
	fmt.Fprintf(w, "\nskipcheck: %d skip(s) not on the allowlist:\n", len(bad))
	for _, s := range bad {
		fmt.Fprintf(w, "  %s %s: %s\n", s.Package, s.Test, s.Output)
	}
	fmt.Fprintln(w, "skipcheck: fix the skip, or add `<package> <test> <reason>` to the allowlist after review.")
}

// run returns the process exit status: the child's non-zero status when it
// failed, 2 for a usage or setup error, 1 for an unlisted skip, else 0.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("skipcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	allowPath := fs.String("allow", "", "allowed-skip list (`<package> <test> <reason>` per line)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cmdline := fs.Args()
	if len(cmdline) == 0 {
		fmt.Fprintln(stderr, "usage: skipcheck [-allow FILE] -- go test -json [flags] [packages]")
		return 2
	}
	allow, err := loadAllowlist(*allowPath)
	if err != nil {
		fmt.Fprintf(stderr, "skipcheck: %v\n", err)
		return 2
	}

	child := exec.Command(cmdline[0], cmdline[1:]...)
	child.Stdin = os.Stdin
	child.Stderr = stderr
	out, err := child.StdoutPipe()
	if err != nil {
		fmt.Fprintf(stderr, "skipcheck: %v\n", err)
		return 2
	}
	if err := child.Start(); err != nil {
		fmt.Fprintf(stderr, "skipcheck: %v\n", err)
		return 2
	}
	bad, scanErr := Scan(out, stdout, allow)
	if scanErr != nil {
		fmt.Fprintf(stderr, "skipcheck: reading child output: %v\n", scanErr)
	}
	waitErr := child.Wait()

	if len(bad) > 0 {
		report(stderr, bad)
	}
	var ee *exec.ExitError
	switch {
	case errors.As(waitErr, &ee):
		if code := ee.ExitCode(); code > 0 {
			return code
		}
		return 1 // killed by a signal
	case waitErr != nil, scanErr != nil:
		return 2
	case len(bad) > 0:
		return 1
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
