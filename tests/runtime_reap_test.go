//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// reposLine parses the `repos:   N registered — H healthy, U unhealthy` line
// `satelle service status` prints, and returns (registered, healthy, unhealthy).
var reposLine = regexp.MustCompile(`repos:\s+(\d+) registered — (\d+) healthy, (\d+) unhealthy`)

func parseRepos(t *testing.T, out string) (registered, healthy, unhealthy int) {
	t.Helper()
	m := reposLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("could not find the repos health line in:\n%s", out)
	}
	registered, _ = strconv.Atoi(m[1])
	healthy, _ = strconv.Atoi(m[2])
	unhealthy, _ = strconv.Atoi(m[3])
	return
}

// TestRuntimeReapDropsUnhealthyCount (sty_bd8af0b6 AC5): registry entries for
// deleted repos count as UNHEALTHY (repo.unreadable) forever, so the health
// headline an operator reads to decide whether anything needs attention is
// mostly tombstones. After a reap the count must drop by exactly the number of
// cleared entries, with no live repo changing state.
func TestRuntimeReapDropsUnhealthyCount(t *testing.T) {
	home := t.TempDir()
	env := append(os.Environ(), "SATELLE_HOME="+home, "SATELLE_SERVER_ENDPOINT=none")

	runIn := func(dir string, args ...string) (string, error) {
		cmd := exec.Command(testBin, args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// One real, initialised repo — it registers itself.
	live := t.TempDir()
	if out, err := runIn(live, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	// Three repos that are initialised, registered, and then deleted — exactly
	// the tombstones the story observed.
	const tombstones = 3
	var deleted []string
	for i := 0; i < tombstones; i++ {
		dir := t.TempDir()
		if out, err := runIn(dir, "init"); err != nil {
			t.Fatalf("init tombstone %d: %v\n%s", i, err, out)
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		deleted = append(deleted, dir)
	}

	before, err := runIn(live, "service", "status")
	if err != nil && !strings.Contains(before, "repos:") {
		t.Fatalf("service status: %v\n%s", err, before)
	}
	regBefore, healthyBefore, unhealthyBefore := parseRepos(t, before)
	if unhealthyBefore < tombstones {
		t.Fatalf("want at least %d unhealthy tombstones, got %d in:\n%s", tombstones, unhealthyBefore, before)
	}

	// Dry run first: it must change nothing.
	dry, err := runIn(live, "runtime", "reap")
	if err != nil {
		t.Fatalf("runtime reap (dry): %v\n%s", err, dry)
	}
	for _, d := range deleted {
		if !strings.Contains(dry, d) {
			t.Errorf("dry run should report the deleted repo %s:\n%s", d, dry)
		}
	}
	midStatus, _ := runIn(live, "service", "status")
	if _, _, u := parseRepos(t, midStatus); u != unhealthyBefore {
		t.Errorf("dry run must not change the health count: %d → %d", unhealthyBefore, u)
	}

	// Act.
	act, err := runIn(live, "runtime", "reap", "--yes")
	if err != nil {
		t.Fatalf("runtime reap --yes: %v\n%s", err, act)
	}

	after, err := runIn(live, "service", "status")
	if err != nil && !strings.Contains(after, "repos:") {
		t.Fatalf("service status after: %v\n%s", err, after)
	}
	regAfter, healthyAfter, unhealthyAfter := parseRepos(t, after)

	if want := unhealthyBefore - tombstones; unhealthyAfter != want {
		t.Errorf("unhealthy should drop by exactly %d: %d → %d (want %d)\n%s",
			tombstones, unhealthyBefore, unhealthyAfter, want, after)
	}
	if healthyAfter != healthyBefore {
		t.Errorf("no live repo may change state: healthy %d → %d", healthyBefore, healthyAfter)
	}
	if want := regBefore - tombstones; regAfter != want {
		t.Errorf("registered should drop by %d: %d → %d (want %d)", tombstones, regBefore, regAfter, want)
	}
	if !strings.Contains(after, live) {
		t.Errorf("the live repo must still be registered:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(live, ".satelle")); err != nil {
		t.Errorf("the live repo must be untouched on disk: %v", err)
	}
}
