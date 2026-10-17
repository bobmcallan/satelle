// skipcheck runs a `go test -json` command as its own child process and fails
// the run when the child fails or when any test or subtest skips without being
// on the reviewed allowlist (sty_53c3f311).
//
// Usage: skipcheck [-allow FILE] [-stall DURATION] -- go test -json [flags] [packages]
//
// It is a wrapper, not a pipe: make runs recipes under /bin/sh, which has no
// pipefail, so `go test -json | filter` would turn a failing run green.
// Here the child's exit status is read directly and wins. The test2json stream
// is echoed to stdout so CI logs still read like a test run.
//
// With -stall, a test that has started and shown no result for that long fails
// the run by name, and the child's whole process group is killed so no test
// binary is left behind (sty_3f2a5c78). It is off by default: it is a hang
// backstop beneath a package timeout, and the caller sets it per suite.
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
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
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
// Every record is also shown to watch (nil to not watch).
func Scan(r io.Reader, w io.Writer, allow Allowlist, watch *Watch) ([]Skip, error) {
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
				watch.observe(ev, time.Now())
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

// Stalled is a test still running past the stall deadline.
type Stalled struct {
	Package, Test string
	Running       time.Duration
}

// Watch tracks which tests are running, and since when, from the test2json
// stream. A test runs from its run (or cont) event until its pass, fail or skip,
// or until it pauses to wait for its parallel siblings. A subtest's events count
// as progress for its running parents, so a parent that runs many quick subtests
// in turn is not mistaken for a hang. A package's own terminal event closes every
// test it still has open: a crashed or timed-out package does not always.
type Watch struct {
	stall time.Duration

	mu      sync.Mutex
	live    map[key]time.Time
	stalled []Stalled
}

func newWatch(stall time.Duration) *Watch {
	return &Watch{stall: stall, live: map[key]time.Time{}}
}

// observe folds one record into the running set. It is safe on a nil Watch.
func (w *Watch) observe(ev event, now time.Time) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if ev.Test == "" {
		switch ev.Action {
		case "pass", "fail", "skip":
			for k := range w.live {
				if k.pkg == ev.Package {
					delete(w.live, k)
				}
			}
		}
		return
	}
	switch ev.Action {
	case "run", "cont":
		w.live[key{ev.Package, ev.Test}] = now
	case "pause", "pass", "fail", "skip":
		delete(w.live, key{ev.Package, ev.Test})
	}
	for i, c := range ev.Test {
		if c != '/' {
			continue
		}
		parent := key{ev.Package, ev.Test[:i]}
		if _, running := w.live[parent]; running {
			w.live[parent] = now
		}
	}
}

// expired lists the tests that have been running longer than the stall deadline
// at now, in package and test order.
func (w *Watch) expired(now time.Time) []Stalled {
	w.mu.Lock()
	defer w.mu.Unlock()
	var hung []Stalled
	for k, since := range w.live {
		if d := now.Sub(since); d > w.stall {
			hung = append(hung, Stalled{k.pkg, k.test, d})
		}
	}
	sort.Slice(hung, func(i, j int) bool {
		if hung[i].Package != hung[j].Package {
			return hung[i].Package < hung[j].Package
		}
		return hung[i].Test < hung[j].Test
	})
	return hung
}

// patrol checks the running set until stop closes. On the first stall it records
// what hung and calls kill once.
func (w *Watch) patrol(stop <-chan struct{}, kill func()) {
	period := min(max(w.stall/4, 10*time.Millisecond), time.Second)
	tick := time.NewTicker(period)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-tick.C:
			if hung := w.expired(now); len(hung) > 0 {
				w.mu.Lock()
				w.stalled = hung
				w.mu.Unlock()
				kill()
				return
			}
		}
	}
}

// hung returns what patrol recorded.
func (w *Watch) hung() []Stalled {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stalled
}

func reportStalled(w io.Writer, hung []Stalled, stall time.Duration) {
	fmt.Fprintf(w, "\nskipcheck: %d test(s) stalled, running more than %s with no result:\n", len(hung), stall)
	for _, h := range hung {
		fmt.Fprintf(w, "  %s %s: running %s\n", h.Package, h.Test, h.Running.Round(time.Millisecond))
	}
	fmt.Fprintln(w, "skipcheck: killed the test process group; a hung test fails the run by name.")
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
// failed, 2 for a usage or setup error, 1 for a stalled test or an unlisted skip,
// else 0.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("skipcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	allowPath := fs.String("allow", "", "allowed-skip list (`<package> <test> <reason>` per line)")
	stall := fs.Duration("stall", 0, "fail the run, and kill the child's process group, when a test runs this long with no result (0 = off)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cmdline := fs.Args()
	if len(cmdline) == 0 {
		fmt.Fprintln(stderr, "usage: skipcheck [-allow FILE] [-stall DURATION] -- go test -json [flags] [packages]")
		return 2
	}
	if *stall < 0 {
		fmt.Fprintln(stderr, "skipcheck: -stall must not be negative")
		return 2
	}
	allow, err := loadAllowlist(*allowPath)
	if err != nil {
		fmt.Fprintf(stderr, "skipcheck: %v\n", err)
		return 2
	}

	child := exec.Command(cmdline[0], cmdline[1:]...)
	ownGroup(child)
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

	// The child is in its own process group, so a terminal's Ctrl-C no longer
	// reaches it directly: pass the signal on to the group.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	finished := make(chan struct{})
	defer func() { signal.Stop(sigs); close(finished) }()
	go func() {
		for {
			select {
			case sig := <-sigs:
				signalGroup(child, sig)
			case <-finished:
				return
			}
		}
	}()

	var watch *Watch
	stopPatrol, patrolled := make(chan struct{}), make(chan struct{})
	if *stall > 0 {
		watch = newWatch(*stall)
		go func() {
			defer close(patrolled)
			watch.patrol(stopPatrol, func() { signalGroup(child, os.Kill) })
		}()
	} else {
		close(patrolled)
	}
	bad, scanErr := Scan(out, stdout, allow, watch)
	if scanErr != nil {
		fmt.Fprintf(stderr, "skipcheck: reading child output: %v\n", scanErr)
	}
	close(stopPatrol)
	<-patrolled
	waitErr := child.Wait()

	if len(bad) > 0 {
		report(stderr, bad)
	}
	var hung []Stalled
	if watch != nil {
		if hung = watch.hung(); len(hung) > 0 {
			reportStalled(stderr, hung, *stall)
		}
	}
	var ee *exec.ExitError
	switch {
	case len(hung) > 0:
		return 1
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
