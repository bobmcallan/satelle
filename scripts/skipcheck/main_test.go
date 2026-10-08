package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pkg = "example.com/m/p"

func ev(action, test, output string) string {
	var b strings.Builder
	b.WriteString(`{"Action":"` + action + `","Package":"` + pkg + `"`)
	if test != "" {
		b.WriteString(`,"Test":"` + test + `"`)
	}
	if output != "" {
		b.WriteString(`,"Output":"` + output + `"`)
	}
	b.WriteString("}\n")
	return b.String()
}

// skipStream is one test that skips, with the output go test prints for it.
func skipStream(test string) string {
	return ev("run", test, "") +
		ev("output", test, "=== RUN   "+test+`\n`) +
		ev("output", test, "    x_test.go:9: git not on PATH\\n") +
		ev("output", test, "--- SKIP: "+test+` (0.00s)\n`) +
		ev("skip", test, "")
}

// runWith writes the stream to a fixture, runs skipcheck over a fake child that
// prints it and exits with code, and returns (exit, stdout, stderr).
func runWith(t *testing.T, stream string, code string, allowBody *string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	fixture := filepath.Join(dir, "events.json")
	if err := os.WriteFile(fixture, []byte(stream), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{}
	if allowBody != nil {
		allow := filepath.Join(dir, "allow.txt")
		if err := os.WriteFile(allow, []byte(*allowBody), 0o644); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-allow", allow)
	}
	args = append(args, "--", "sh", "-c", `cat "$1"; exit "$2"`, "sh", fixture, code)
	var out, errb bytes.Buffer
	got := run(args, &out, &errb)
	return got, out.String(), errb.String()
}

func ptr(s string) *string { return &s }

func TestFailingChildFailsTheRun(t *testing.T) {
	stream := ev("run", "TestA", "") + ev("fail", "TestA", "") + ev("fail", "", "")
	got, _, _ := runWith(t, stream, "1", ptr(""))
	if got != 1 {
		t.Fatalf("exit = %d, want the child's 1", got)
	}
	// The child's own status is kept, whatever it is.
	if got, _, _ := runWith(t, stream, "3", ptr("")); got != 3 {
		t.Fatalf("exit = %d, want the child's 3", got)
	}
}

func TestUnlistedSkipFailsAndNamesIt(t *testing.T) {
	got, stdout, stderr := runWith(t, skipStream("TestGit"), "0", ptr(""))
	if got == 0 {
		t.Fatal("exit 0, want non-zero for an unlisted skip")
	}
	for _, want := range []string{pkg, "TestGit", "git not on PATH"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("report is missing %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(stdout, "--- SKIP: TestGit") {
		t.Errorf("the test output was not echoed:\n%s", stdout)
	}
}

func TestListedSkipPasses(t *testing.T) {
	allow := ptr("# reviewed\n\n" + pkg + " TestGit git is absent on the runner\n")
	if got, _, stderr := runWith(t, skipStream("TestGit"), "0", allow); got != 0 {
		t.Fatalf("exit = %d, want 0 for a listed skip:\n%s", got, stderr)
	}
}

func TestSubtestSkipNeedsItsOwnEntry(t *testing.T) {
	stream := skipStream("TestTable/case_one")
	if got, _, _ := runWith(t, stream, "0", ptr(pkg+" TestTable parent only\n")); got == 0 {
		t.Fatal("a parent entry must not allow a subtest skip")
	}
	if got, _, _ := runWith(t, stream, "0", ptr(pkg+" TestTable/case_one needs a tty\n")); got != 0 {
		t.Fatal("an exact subtest entry must allow the subtest skip")
	}
}

func TestPackageLevelSkipIsIgnored(t *testing.T) {
	stream := ev("output", "", "?   \\texample.com/m/p\\t[no test files]\\n") + ev("skip", "", "")
	if got, _, stderr := runWith(t, stream, "0", ptr("")); got != 0 {
		t.Fatalf("exit = %d, want 0 for a package with no test files:\n%s", got, stderr)
	}
}

func TestRepeatedSkipIsReportedOnce(t *testing.T) {
	stream := skipStream("TestGit") + skipStream("TestGit") + skipStream("TestGit")
	_, _, stderr := runWith(t, stream, "0", ptr(""))
	if n := strings.Count(stderr, " TestGit:"); n != 1 {
		t.Fatalf("TestGit reported %d times, want once:\n%s", n, stderr)
	}
}

func TestPassingRunWithNoSkipsPasses(t *testing.T) {
	stream := ev("run", "TestA", "") + ev("pass", "TestA", "") + ev("pass", "", "")
	if got, _, stderr := runWith(t, stream, "0", ptr("")); got != 0 {
		t.Fatalf("exit = %d, want 0:\n%s", got, stderr)
	}
}

func TestNonJSONLinesPassThrough(t *testing.T) {
	_, stdout, _ := runWith(t, "# example.com/m/p\nx.go:1:1: undefined: y\n", "1", ptr(""))
	if !strings.Contains(stdout, "undefined: y") {
		t.Fatalf("build error was not passed through:\n%s", stdout)
	}
}

func TestMalformedAllowlistFailsBeforeRunningTheChild(t *testing.T) {
	for name, body := range map[string]string{
		"no reason":    pkg + " TestGit\n",
		"one field":    "TestGit\n",
		"duplicate":    pkg + " TestGit a\n" + pkg + " TestGit b\n",
		"second entry": pkg + " TestA fine\n" + pkg + " TestB\n",
	} {
		t.Run(name, func(t *testing.T) {
			// A passing, skip-free stream: only the allowlist can fail this run.
			stream := ev("run", "TestA", "") + ev("pass", "TestA", "")
			got, stdout, stderr := runWith(t, stream, "0", ptr(body))
			if got == 0 {
				t.Fatalf("exit 0, want non-zero for a malformed allowlist (%s)", name)
			}
			if stdout != "" {
				t.Errorf("the child ran despite a bad allowlist:\n%s", stdout)
			}
			if !strings.Contains(stderr, "line ") {
				t.Errorf("error does not name the line:\n%s", stderr)
			}
		})
	}
}

func TestMissingAllowlistFileFails(t *testing.T) {
	var out, errb bytes.Buffer
	args := []string{"-allow", filepath.Join(t.TempDir(), "absent.txt"), "--", "true"}
	if got := run(args, &out, &errb); got == 0 {
		t.Fatal("exit 0, want non-zero for a missing allowlist file")
	}
}

func TestNoCommandIsAUsageError(t *testing.T) {
	var out, errb bytes.Buffer
	if got := run([]string{"-allow", ""}, &out, &errb); got != 2 {
		t.Fatalf("exit = %d, want 2", got)
	}
}

func TestParseAllowlistKeepsTheReason(t *testing.T) {
	allow, err := ParseAllowlist(strings.NewReader("# c\n" + pkg + " TestX/sub needs the real  network\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := allow[key{pkg, "TestX/sub"}]; got != "needs the real network" {
		t.Fatalf("reason = %q", got)
	}
}

// TestScratchProbe is an inert-by-default hook for the negative proofs of
// sty_53c3f311 (a skip that is not allowed; a failure the checker must carry).
// It does nothing unless SATELLE_TEST_PROBE_SKIPCHECK is set (hermetic.sh
// forwards SATELLE_TEST_PROBE_*), so it is inert by default.
func TestScratchProbe(t *testing.T) {
	switch os.Getenv("SATELLE_TEST_PROBE_SKIPCHECK") {
	case "skip":
		t.Skip("scratch skip, not on the allowlist")
	case "fail":
		t.Fatal("scratch failure")
	}
}
