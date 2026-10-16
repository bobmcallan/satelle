package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// countGitSpawns puts a `git` shim first on PATH that logs each invocation's
// arguments and then runs the real git, and returns a func reporting the logged
// invocations. It counts spawns without a seam in non-test code.
func countGitSpawns(t *testing.T) func() []string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "spawns.log")
	shim := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		b, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

// AC5: one hook call loads the process config once. Loading it resolves the
// canonical repo root through `git rev-parse --git-common-dir`, so rule
// evaluation must share the containment check's load rather than make its own:
// the spawn count is one with rules authored, and one with none. SATELLE_CONFIG
// is cleared because, when set, the load never resolves a root and spawns
// nothing, which would make the count vacuous.
//
// That includes a matched command: one that reads the seat or writes the ledger
// opens the store, and the store opens from the same load and resolves its
// runtime dir from the same memoised identity, so it starts no further git. Each
// matched case also checks the ledger row, so the count is not vacuous.
func TestCommitgateLoadsConfigOncePerCall(t *testing.T) {
	const denyEverywhere = "\n[[gate.command_rule]]\npattern = \"orbctl nuke\"\naction = \"deny\"\nmessage = \"never\"\n"
	for _, tc := range []struct {
		name, rules, command string
		seat                 stopcheckSeatState
		rows                 int // command_rule ledger rows the call must write
	}{
		{"no rules", "", "ls", seatMine, 0},
		{"rules, no match", exampleRules(t), "ls", seatMine, 0},
		{"rules, deny match without allow list (no seat read, no row)", denyEverywhere, "orbctl nuke", seatMine, 0},
		{"allowed match, story at an allowed status", exampleRules(t), "orbctl apply -f x", seatMine, 1},
		{"warned match, story engaged", exampleRules(t), "orbctl scale web 2", seatMine, 1},
		{"warned match, no story engaged", exampleRules(t), "orbctl scale web 2", seatNone, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := ruleRepo(t, tc.seat, tc.rules)
			if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git init: %v\n%s", err, out)
			}
			t.Setenv("SATELLE_CONFIG", "")
			spawns := countGitSpawns(t)
			_, _ = commitgateOn(t, "claude", tc.command) // a deny is an error; the count is what is judged
			var loads []string
			for _, s := range spawns() {
				if strings.Contains(s, "--git-common-dir") {
					loads = append(loads, s)
				}
			}
			if len(loads) != 1 {
				t.Errorf("%q started %d `git rev-parse --git-common-dir` (want 1, one config load): %q",
					tc.command, len(loads), loads)
			}
			if got := len(ledgerRows(t, ledger.KindCommandRule)); got != tc.rows {
				t.Errorf("%q wrote %d command_rule ledger rows, want %d", tc.command, got, tc.rows)
			}
		})
	}
}

// AC5, every git process: against the spawns the same fixture makes with a
// command no rule matches (the no-rule baseline), a matched command that reads
// the seat starts no git process the baseline did not, and none more often than
// the baseline made it. That includes the session worktree (unstamped,
// unmatched and multi-seat sessions all ask for it): it comes from the one
// `--git-common-dir` the call already made, not from its own `--show-toplevel`.
func TestCommitgateMatchedPathSpawnsAgainstNoRuleBaseline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		seat    stopcheckSeatState
		session string // "" = an unstamped session
		command string
	}{
		{"stamped, mine, allowed", seatMine, thisSession, "orbctl apply -f x"},
		{"stamped, mine, warned", seatMine, thisSession, "orbctl scale web 2"},
		{"stamped, no seat, warned", seatNone, thisSession, "orbctl scale web 2"},
		{"stamped, sibling's seat (unmatched), warned", seatOther, thisSession, "orbctl scale web 2"},
		{"stamped, sibling's seat (unmatched), denied", seatOther, thisSession, "orbctl apply -f x"},
		{"unstamped, mine, allowed", seatMine, "", "orbctl apply -f x"},
		{"unstamped, mine, warned", seatMine, "", "orbctl scale web 2"},
		{"unstamped, no seat, denied", seatNone, "", "orbctl apply -f x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := ruleRepo(t, tc.seat, exampleRules(t))
			if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git init: %v\n%s", err, out)
			}
			t.Setenv("SATELLE_CONFIG", "")
			t.Setenv(config.SessionEnv, tc.session)
			spawns := countGitSpawns(t)
			_, _ = commitgateOn(t, "claude", "ls")
			baseline := spawns()
			_, _ = commitgateOn(t, "claude", tc.command)
			matched := spawns()[len(baseline):]

			counts := map[string]int{}
			for _, s := range matched {
				counts[s]++
			}
			for _, s := range baseline {
				counts[s]--
			}
			for s, n := range counts {
				if n > 0 {
					t.Errorf("%q started %d more `git %s` than the no-rule baseline\nbaseline: %q\nmatched:  %q", tc.command, n, s, baseline, matched)
				}
			}
		})
	}
}
