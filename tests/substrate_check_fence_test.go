//go:build integration

package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/structure"
)

// fenceCase is one golden (stdin, wantExit, wantStdoutSub) for a coded-check skill.
type fenceCase struct {
	name  string
	setup func(t *testing.T, repo string) // optional prep (git, files)
	stdin string                          // if empty, built from sid via defaultStoryPayload
	sid   string                          // story id for payload + commits
	// pathPrefix is prepended to PATH so a case can shim a binary the check
	// shells out to (e.g. a `satelle story diff` stub for the recorded channel).
	// Relative to the case's repo dir.
	pathPrefix string
	wantExit   int
	wantStdout string
}

// fenceFixtures maps every embedded skill that carries a ```check fence to its
// golden table. Discovery (TestEveryCheckFenceHasFixtures) fails if a skill has
// a fence but no entry here (sty_6830e78e AC3).
var fenceFixtures = map[string][]fenceCase{
	"satelle-estimate-actual-review": {
		{
			name:       "accept enter in_progress with estimate tags",
			stdin:      `{"story":{"id":"sty_est00001","tags":["estimate-minutes:10","estimate-tokens:1000"]},"from":"backlog","to":"in_progress"}`,
			wantExit:   0,
			wantStdout: "",
		},
		{
			name:       "reject enter in_progress without estimate",
			stdin:      `{"story":{"id":"sty_est00002","tags":[]},"from":"backlog","to":"in_progress"}`,
			wantExit:   1,
			wantStdout: "no plan estimate",
		},
		{
			name:       "accept enter done with actual tags",
			stdin:      `{"story":{"id":"sty_est00003","tags":["actual-minutes:10","actual-tokens:900"]},"from":"release","to":"done"}`,
			wantExit:   0,
			wantStdout: "",
		},
		{
			name:       "reject enter done without actual",
			stdin:      `{"story":{"id":"sty_est00004","tags":["estimate-minutes:10"]},"from":"release","to":"done"}`,
			wantExit:   1,
			wantStdout: "no actual recorded",
		},
		{
			name:     "untargeted edge (cancelled) is n/a accept",
			stdin:    `{"story":{"id":"sty_est00005","tags":[]},"from":"backlog","to":"cancelled"}`,
			wantExit: 0,
		},
	},
	"satelle-task-validate-before-review": {
		{
			name: "accept when parent task header exists",
			setup: func(t *testing.T, repo string) {
				mustWrite(t, filepath.Join(repo, ".satelle", "tasks", "tsk_fixture1.md"),
					"---\nid: tsk_fixture1\ntype: task\nstatus: done\n---\n\n# Fixture task\n")
			},
			stdin:    `{"story":{"id":"exe_aaa11111","parent_id":"tsk_fixture1","kind":"execution"},"from":"backlog","to":"in_progress"}`,
			wantExit: 0,
		},
		{
			name:       "reject when parent_id missing",
			stdin:      `{"story":{"id":"exe_bbb22222","kind":"execution"},"from":"backlog","to":"in_progress"}`,
			wantExit:   1,
			wantStdout: "no parent task",
		},
		{
			name:       "reject when parent file missing",
			stdin:      `{"story":{"id":"exe_ccc33333","parent_id":"tsk_missing1","kind":"execution"},"from":"backlog","to":"in_progress"}`,
			wantExit:   1,
			wantStdout: "does not exist",
		},
		{
			name: "reject missing frontmatter",
			setup: func(t *testing.T, repo string) {
				mustWrite(t, filepath.Join(repo, ".satelle", "tasks", "tsk_nofm.md"), "# No frontmatter\n")
			},
			stdin:      `{"story":{"id":"exe_eee55555","parent_id":"tsk_nofm","kind":"execution"},"from":"backlog","to":"in_progress"}`,
			wantExit:   1,
			wantStdout: "frontmatter",
		},
		{
			name: "reject missing id field",
			setup: func(t *testing.T, repo string) {
				mustWrite(t, filepath.Join(repo, ".satelle", "tasks", "tsk_noid.md"),
					"---\ntype: task\nstatus: done\n---\n\n# No id\n")
			},
			stdin:      `{"story":{"id":"exe_fff66666","parent_id":"tsk_noid","kind":"execution"},"from":"backlog","to":"in_progress"}`,
			wantExit:   1,
			wantStdout: "missing id",
		},
		{
			name: "reject wrong type",
			setup: func(t *testing.T, repo string) {
				mustWrite(t, filepath.Join(repo, ".satelle", "tasks", "tsk_badtype.md"),
					"---\nid: tsk_badtype\ntype: story\nstatus: done\n---\n\n# Bad type\n")
			},
			stdin:      `{"story":{"id":"exe_ggg77777","parent_id":"tsk_badtype","kind":"execution"},"from":"backlog","to":"in_progress"}`,
			wantExit:   1,
			wantStdout: "type: task",
		},
		{
			name: "reject missing status",
			setup: func(t *testing.T, repo string) {
				mustWrite(t, filepath.Join(repo, ".satelle", "tasks", "tsk_nostatus.md"),
					"---\nid: tsk_nostatus\ntype: task\n---\n\n# No status\n")
			},
			stdin:      `{"story":{"id":"exe_hhh88888","parent_id":"tsk_nostatus","kind":"execution"},"from":"backlog","to":"in_progress"}`,
			wantExit:   1,
			wantStdout: "missing status",
		},
		{
			name: "reject missing title heading",
			setup: func(t *testing.T, repo string) {
				mustWrite(t, filepath.Join(repo, ".satelle", "tasks", "tsk_notitle.md"),
					"---\nid: tsk_notitle\ntype: task\nstatus: done\n---\n\nNo heading here.\n")
			},
			stdin:      `{"story":{"id":"exe_iii99999","parent_id":"tsk_notitle","kind":"execution"},"from":"backlog","to":"in_progress"}`,
			wantExit:   1,
			wantStdout: "Title",
		},
		{
			name:     "non-in_progress edge is n/a accept",
			stdin:    `{"story":{"id":"exe_ddd44444"},"from":"in_progress","to":"done"}`,
			wantExit: 0,
		},
	},
	"satelle-route-drift-check": {
		{
			// The common path: no route_drift block in the payload, so there is
			// nothing to judge and the gate must not cost a rejection.
			name:     "accepts a payload with no drift block",
			stdin:    `{"story":{"id":"sty_nodrift1"},"from":"plan","to":"in_progress"}`,
			wantExit: 0,
		},
		{
			name:       "rejects a drifted payload naming both lanes",
			stdin:      `{"story":{"id":"sty_drift001"},"from":"in_progress","to":"done","route_drift":{"item":"sty_drift001","category":"docs","status":"in_progress","walked":["backlog","plan","in_progress"],"derived":["backlog","in_progress","done"],"off_route":["plan"],"status_on_route":true}}`,
			wantExit:   1,
			wantStdout: "route drift",
		},
	},
	"satelle-docs-only-check": {
		{
			name: "accepts a markdown-only slice",
			sid:  "sty_d0c11111",
			setup: func(t *testing.T, repo string) {
				gitInit(t, repo)
				mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
				gitCommitAll(t, repo, "baseline")
				mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n\n## Retry policy\n")
				mustWrite(t, filepath.Join(repo, "docs", "guide.md"), "# Guide\n")
				gitCommitAll(t, repo, "document the retry policy (sty_d0c11111)")
			},
			wantExit:   0,
			wantStdout: "docs-only slice confirmed",
		},
		{
			name: "rejects a non-doc path, naming the offender",
			sid:  "sty_d0c22222",
			setup: func(t *testing.T, repo string) {
				gitInit(t, repo)
				mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
				gitCommitAll(t, repo, "baseline")
				mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n\n## Config\n")
				mustWrite(t, filepath.Join(repo, "cmd", "foo.go"), "package main\n")
				gitCommitAll(t, repo, "docs plus code (sty_d0c22222)")
			},
			wantExit:   1,
			wantStdout: "cmd/foo.go",
		},
		{
			// The lane's whole file-type scope is the doc_paths pattern: a
			// non-markdown doc form is out until a repo widens it by overriding
			// this skill — the pattern is configuration, not a Go branch.
			name: "rejects a non-markdown doc form under the shipped default",
			sid:  "sty_d0c33333",
			setup: func(t *testing.T, repo string) {
				gitInit(t, repo)
				mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
				gitCommitAll(t, repo, "baseline")
				mustWrite(t, filepath.Join(repo, "docs", "guide.rst"), "Guide\n=====\n")
				gitCommitAll(t, repo, "rst guide (sty_d0c33333)")
			},
			wantExit:   1,
			wantStdout: "docs/guide.rst",
		},
		{
			name: "rejects an empty change set",
			sid:  "sty_d0c44444",
			setup: func(t *testing.T, repo string) {
				gitInit(t, repo)
				mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
				gitCommitAll(t, repo, "baseline")
			},
			wantExit:   1,
			wantStdout: "no change set found",
		},
	},
	"satelle-substrate-only-check": {
		{
			name: "accepts managed footprint",
			sid:  "sty_aaa11111",
			setup: func(t *testing.T, repo string) {
				gitInit(t, repo)
				mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
				gitCommitAll(t, repo, "baseline")
				mustWrite(t, filepath.Join(repo, ".gitignore"), "# satelle managed\n.satelle/data/\n")
				mustWrite(t, filepath.Join(repo, ".claude", "settings.json"), "{}\n")
				mustWrite(t, filepath.Join(repo, ".grok", "hooks", "satelle.json"), "{}\n")
				mustWrite(t, filepath.Join(repo, ".satelle", "x.md"), "# substrate\n")
				gitCommitAll(t, repo, "init footprint (sty_aaa11111)")
			},
			wantExit:   0,
			wantStdout: "substrate-only slice confirmed",
		},
		{
			// sty_30d3bd99 AC2: a prune slice is deletions of git-ignored
			// .satelle/ files, so git sees nothing and there is no commit — the
			// recorded channel is the ONLY evidence. The gate must accept it
			// rather than reporting "no change set found".
			name:       "accepts a deletion-only prune slice from the recorded channel",
			sid:        "sty_44be0001",
			pathPrefix: "bin",
			setup: func(t *testing.T, repo string) {
				gitInit(t, repo)
				mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
				gitCommitAll(t, repo, "baseline")
				// The removed seeds are gone from disk and were never tracked.
				mustWrite(t, filepath.Join(repo, "bin", "satelle"), `#!/usr/bin/env bash
for a in "$@"; do
  if [ "$a" = "--recorded" ]; then
    echo '{"files":[".satelle/principles/satelle-agent-goals.md",".satelle/principles/satelle-agent-model.md"]}'
    exit 0
  fi
done
echo '{"files":[]}'
`)
				if err := os.Chmod(filepath.Join(repo, "bin", "satelle"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			wantExit:   0,
			wantStdout: "substrate-only slice confirmed",
		},
		{
			name: "rejects code slice naming offenders",
			sid:  "sty_bbb22222",
			setup: func(t *testing.T, repo string) {
				gitInit(t, repo)
				mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
				gitCommitAll(t, repo, "baseline")
				mustWrite(t, filepath.Join(repo, "cmd", "foo.go"), "package main\n")
				mustWrite(t, filepath.Join(repo, "Makefile"), "all:\n")
				gitCommitAll(t, repo, "code change (sty_bbb22222)")
			},
			wantExit:   1,
			wantStdout: "cmd/foo.go",
		},
		{
			name: "honors edit_exempt_paths extension",
			sid:  "sty_ccc33333",
			setup: func(t *testing.T, repo string) {
				gitInit(t, repo)
				mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
				mustWrite(t, filepath.Join(repo, ".satelle", "satelle.toml"),
					"[gate]\nedit_exempt_paths = [\"build/\"]\n")
				gitCommitAll(t, repo, "baseline with exempt config")
				mustWrite(t, filepath.Join(repo, "build", "x.txt"), "artifact\n")
				gitCommitAll(t, repo, "build artifact (sty_ccc33333)")
			},
			wantExit:   0,
			wantStdout: "substrate-only slice confirmed",
		},
		{
			name: "fresh-init default exempts gitignore slice",
			sid:  "sty_ddd44444",
			setup: func(t *testing.T, repo string) {
				gitInit(t, repo)
				mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
				mustWrite(t, filepath.Join(repo, ".satelle", "satelle.toml"),
					"[gate]\nedit_exempt_paths = [\".satelle/\", \".gitignore\"]\n")
				gitCommitAll(t, repo, "baseline with fresh-init exempt")
				mustWrite(t, filepath.Join(repo, ".gitignore"), "# managed\n.satelle/local.toml\n")
				gitCommitAll(t, repo, "gitignore converge (sty_ddd44444)")
			},
			wantExit:   0,
			wantStdout: "substrate-only slice confirmed",
		},
		{
			// AC3: empty commit alone is not evidence of a substrate change.
			name: "rejects empty commit as sole evidence",
			sid:  "sty_eee55555",
			setup: func(t *testing.T, repo string) {
				gitInit(t, repo)
				mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
				gitCommitAll(t, repo, "baseline")
				cmd := exec.Command("git", "commit", "--allow-empty", "-q", "-m", "empty (sty_eee55555)")
				cmd.Dir = repo
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("empty commit: %v\n%s", err, out)
				}
			},
			wantExit:   1,
			wantStdout: "no change set found",
		},
	},
}

// TestEveryCheckFenceHasFixtures discovers embedded skills with a ```check fence
// and requires each to have a fixture table covering accept + reject.
func TestEveryCheckFenceHasFixtures(t *testing.T) {
	var missing []string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != "skills" {
			continue
		}
		if structure.CheckFence(d.Body) == "" {
			continue
		}
		cases, ok := fenceFixtures[d.Name]
		if !ok || len(cases) == 0 {
			missing = append(missing, d.Name)
			continue
		}
		hasAccept, hasReject := false, false
		for _, c := range cases {
			if c.wantExit == 0 {
				hasAccept = true
			} else {
				hasReject = true
			}
		}
		if !hasAccept || !hasReject {
			t.Errorf("skill %s fixtures must cover at least one accept and one reject", d.Name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("embedded skills with ```check but no fenceFixtures entry: %v", missing)
	}
	// reverse: no fixture for a skill that lost its fence
	for name := range fenceFixtures {
		found := false
		for _, d := range config.EmbeddedDefaults() {
			if d.Kind == "skills" && d.Name == name && structure.CheckFence(d.Body) != "" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("fenceFixtures has %s but no embedded skill carries a ```check fence", name)
		}
	}
}

// TestCheckFenceGoldenTables drives each fixture through the shipped fence.
func TestCheckFenceGoldenTables(t *testing.T) {
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != "skills" {
			continue
		}
		script := structure.CheckFence(d.Body)
		if script == "" {
			continue
		}
		cases, ok := fenceFixtures[d.Name]
		if !ok {
			continue // discovery test already fails
		}
		t.Run(d.Name, func(t *testing.T) {
			for _, tc := range cases {
				tc := tc
				t.Run(tc.name, func(t *testing.T) {
					repo := t.TempDir()
					if tc.setup != nil {
						tc.setup(t, repo)
					}
					scriptPath := filepath.Join(t.TempDir(), "check.sh")
					if err := os.WriteFile(scriptPath, []byte(script+"\n"), 0o755); err != nil {
						t.Fatal(err)
					}
					stdin := tc.stdin
					if stdin == "" {
						sid := tc.sid
						if sid == "" {
							sid = "sty_eee55555"
						}
						stdin = `{"story":{"id":"` + sid + `"},"from":"in_progress","to":"done"}`
					}
					out, exit := runFenceScript(t, scriptPath, repo, stdin, tc.pathPrefix)
					if exit != tc.wantExit {
						t.Fatalf("exit=%d want %d\nout:\n%s", exit, tc.wantExit, out)
					}
					if tc.wantStdout != "" && !strings.Contains(out, tc.wantStdout) {
						t.Errorf("stdout missing %q:\n%s", tc.wantStdout, out)
					}
				})
			}
		})
	}
}

func runFenceScript(t *testing.T, scriptPath, repo, stdin, pathPrefix string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(stdin)
	if pathPrefix != "" {
		cmd.Env = append(os.Environ(),
			"PATH="+filepath.Join(repo, pathPrefix)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			t.Fatalf("run fence: %v\n%s", err, buf.String())
		}
	}
	return buf.String(), exit
}
