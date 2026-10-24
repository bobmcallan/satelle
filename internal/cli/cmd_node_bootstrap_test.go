//go:build !windows

package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/remotenode/devtest"
)

// bootstrapLine matches the bootstrap line of the satelle-server fixture.
var bootstrapLine = regexp.MustCompile(`(?m)^bootstrap = .*\n`)

// setBootstrap makes the definition the rig serves declare argvs as its [host]
// bootstrap; nil declares none.
func (r *nodeRig) setBootstrap(argvs [][]string) {
	r.t.Helper()
	cur := bootstrapLine.ReplaceAllString(r.bundle["remote.toml"], "")
	if len(argvs) > 0 {
		var parts []string
		for _, argv := range argvs {
			var quoted []string
			for _, a := range argv {
				quoted = append(quoted, strconv.Quote(a))
			}
			parts = append(parts, "["+strings.Join(quoted, ", ")+"]")
		}
		if !strings.Contains(cur, "[host]\n") {
			r.t.Fatal("the fixture no longer has a [host] table")
		}
		cur = strings.Replace(cur, "[host]\n", "[host]\nbootstrap = ["+strings.Join(parts, ", ")+"]\n", 1)
	}
	r.bundle["remote.toml"] = cur
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(path)
	return string(b)
}

// The definition's bootstrap commands run once each, in order, in the node root,
// after the definition is installed and before the node reads its run, joins the
// relay or reports ready; the node then works as it does with no bootstrap.
func TestNodeJoinRunsTheHostBootstrapInOrderBeforeTheRun(t *testing.T) {
	r := newNodeRig(t)
	scratch := t.TempDir()
	order, started, release := filepath.Join(scratch, "order"), filepath.Join(scratch, "started"), filepath.Join(scratch, "release")
	r.setBootstrap([][]string{
		// The definition is installed already, and the command runs in the node root.
		{"sh", "-c", "test -f .satelle/workflows/step.toml && echo one >> " + order + " && : > " + started +
			" && while [ ! -e " + release + " ]; do sleep 0.02; done"},
		{"sh", "-c", "echo two >> " + order},
	})
	fakes := make(chan *devtest.Node, 1)
	r.fakeSetup = func(f *devtest.Node) { fakes <- f }

	type result struct{ err error }
	opened := make(chan result, 1)
	go func() {
		_, _, _, _, err := r.tryOpen(t)
		opened <- result{err}
	}()
	fake := <-fakes
	deadline := time.Now().Add(30 * time.Second)
	for !fileExists(started) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !fileExists(started) {
		t.Fatalf("the first bootstrap command never ran\nnode log:\n%s", r.log)
	}
	// While the first command runs the node has done nothing but fetch its definition.
	if got := fake.Count(http.MethodGet, "/definition"); got != 1 {
		t.Errorf("definition fetches = %d, want 1", got)
	}
	for _, tail := range []string{"/run", "/status", "/end"} {
		for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodPost} {
			if n := fake.Count(m, tail); n != 0 {
				t.Errorf("%s %s called %d times before the bootstrap finished", m, tail, n)
			}
		}
	}
	if got := readFile(t, order); got != "one\n" {
		t.Errorf("order = %q while the first command runs, want only it", got)
	}
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if res := <-opened; res.err != nil {
		t.Fatalf("Attach: %v\nnode log:\n%s", res.err, r.log)
	}
	if got := readFile(t, order); got != "one\ntwo\n" {
		t.Errorf("order = %q, want each command once, in order", got)
	}
	if n := fake.Count(http.MethodGet, "/definition"); n != 1 {
		t.Errorf("definition fetches = %d, want 1", n)
	}
}

// A failing second command fails the start: the node reports its index and reason
// (and nothing of its output or argv) as a concluded, rejected status, works no
// run, and ends once.
func TestNodeJoinFailingBootstrapFailsTheStartAndReportsIt(t *testing.T) {
	r := newNodeRig(t)
	scratch := t.TempDir()
	order := filepath.Join(scratch, "order")
	r.setBootstrap([][]string{
		{"sh", "-c", "echo one >> " + order},
		{"sh", "-c", "echo OUTPUT-SENTINEL; echo two >> " + order + "; exit 3 # ARGV-SENTINEL"},
		{"sh", "-c", "echo three >> " + order},
	})
	fake := r.serve("nod_a", "node-token", r.encode())
	jerr := r.joinNow(r.relay.URL(), "nod_a", "node-token")
	if jerr == nil || !strings.Contains(jerr.Error(), "bootstrap command 2: exit status 3") {
		t.Errorf("join ended with %v, want the failing command's index and reason", jerr)
	}
	if got := readFile(t, order); got != "one\ntwo\n" {
		t.Errorf("order = %q, want the commands up to the failing one", got)
	}

	st := fake.Statuses()
	if len(st) != 1 || st[0]["run_state"] != "concluded" || st[0]["outcome"] != "reject" ||
		!strings.Contains(st[0]["findings"], "bootstrap command 2: exit status 3") {
		t.Fatalf("statuses = %+v, want one concluded reject carrying the failing command", st)
	}
	for k, v := range st[0] {
		if strings.Contains(v, "SENTINEL") || strings.Contains(v, devtest.SecretValue) {
			t.Errorf("status %s = %q holds output, argv or a secret", k, v)
		}
	}
	if strings.Contains(st[0]["findings"], "sh") && strings.Contains(st[0]["findings"], "echo") {
		t.Errorf("findings = %q names the command", st[0]["findings"])
	}
	// No run was worked: the relay was never joined, no workstate, no channel key.
	if r.relay.Attempts() != 0 {
		t.Error("the node joined the relay after a failed bootstrap")
	}
	if n := len(fake.Pushed()); n != 0 {
		t.Errorf("workstate pushes = %d, want none", n)
	}
	if fake.ChannelKey() != nil {
		t.Error("the node published a channel key after a failed bootstrap")
	}
	if n := fake.Count(http.MethodPost, "/end"); n != 1 {
		t.Errorf("end calls = %d, want 1", n)
	}
	if strings.Contains(r.log.String(), devtest.SecretValue) {
		t.Error("the secret value is in the node's log")
	}
}

// A definition with no bootstrap starts as it always has.
func TestNodeJoinWithNoBootstrapStartsUnchanged(t *testing.T) {
	r := newNodeRig(t)
	r.setBootstrap(nil)
	if strings.Contains(r.bundle["remote.toml"], "bootstrap") {
		t.Fatal("the definition still declares a bootstrap")
	}
	run, fake, joined, _ := r.open(t)
	if run.Report.Error != "" {
		t.Errorf("report = %+v", run.Report)
	}
	if err := run.End(); err != nil {
		t.Errorf("End: %v", err)
	}
	if err := waitJoin(t, joined); err != nil {
		t.Errorf("node join ended with %v\nnode log:\n%s", err, r.log)
	}
	if n := len(fake.Statuses()); n != 0 {
		t.Errorf("statuses = %d, want none from a start that did not fail", n)
	}
	if n := fake.Count(http.MethodPost, "/end"); n != 1 {
		t.Errorf("end calls = %d, want 1", n)
	}
}
