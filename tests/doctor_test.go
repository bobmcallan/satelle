//go:build integration

package tests

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorHealthyRepo drives the real binary: a freshly initialised repo is
// healthy, exits 0, and shows each binding's effective value with its source.
func TestDoctorHealthyRepo(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "doctor")
	for _, want := range []string{"HEALTHY", "Agent grants (effective value → source)", "(repo)", "PASS  no problems found"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
	// Lifecycle hooks are surfaced, since they fire outside the status graph.
	if !strings.Contains(out, "Lifecycle hooks") || !strings.Contains(out, "create_review") {
		t.Errorf("doctor should surface lifecycle hooks:\n%s", out)
	}
}

// TestDoctorUnhealthyRepoExitsNonZeroWithIdentifiers pins the failure path and
// the identifier contract through the real binary.
func TestDoctorUnhealthyRepoExitsNonZeroWithIdentifiers(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "reindex")

	// A command whose first token is a placeholder can never run, in any
	// environment — that is an error, unlike a merely absent binary.
	writeFile(t, filepath.Join(repo, ".satelle", "agents.toml"),
		"[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n"+
			"[reviewer]\nrole = \"reviewer\"\ncommand = \"{system} --disallowedTools Write\"\ntools = \"Read,Grep,Glob\"\n")

	out, err := run(t, testBin, repo, "doctor")
	if err == nil {
		t.Fatalf("an unhealthy repo must exit non-zero:\n%s", out)
	}
	for _, want := range []string{"UNHEALTHY", "[binary.malformed]", "fix:"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}

	// An absent binary is reported but ADVISORY: a repo is legitimately set up
	// before its agent CLI is installed, so doctor must not refuse it.
	writeFile(t, filepath.Join(repo, ".satelle", "agents.toml"),
		"[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n"+
			"[reviewer]\nrole = \"reviewer\"\ncommand = \"definitely-not-installed --disallowedTools Write {system}\"\ntools = \"Read,Grep,Glob\"\n")
	advisory := mustRun(t, testBin, repo, "doctor")
	if !strings.Contains(advisory, "WARN  [binary.missing]") {
		t.Errorf("an absent binary must be a warning, not a failure:\n%s", advisory)
	}
}

// TestDoctorAndEngagementAgree is AC5: the identifier an engagement refusal
// carries is the SAME one doctor reports for that repo. Asserted by comparing
// the two outputs rather than by reading either in isolation, so they cannot
// drift into two vocabularies.
func TestDoctorAndEngagementAgree(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// The route is authored FIRST so the story is stamped with it: a step
	// allocating a binding that does not exist. Engagement must refuse, and
	// doctor must name the same defect.
	writeSpineFixture(t, repo, "", "", "",
		"plan|ghost-agent|plan||",
		"done||||")
	mustRun(t, testBin, repo, "reindex")
	mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "Add a widget", "--body", "Render a widget on the dashboard", "--acceptance", "1. the widget renders")

	doctorOut, derr := run(t, testBin, repo, "doctor")
	if derr == nil {
		t.Fatalf("doctor must report the broken allocation:\n%s", doctorOut)
	}
	if !strings.Contains(doctorOut, "[node.alloc]") {
		t.Fatalf("doctor should carry the node.alloc identifier:\n%s", doctorOut)
	}

	id := storyID(t, repo)
	engageOut, eerr := run(t, testBin, repo, "story", "set", id, "--status", "plan")
	if eerr == nil {
		t.Fatalf("engagement must fail closed on a broken allocation:\n%s", engageOut)
	}
	if !strings.Contains(engageOut, "node.alloc") {
		t.Errorf("the engage refusal must carry the SAME identifier doctor prints:\n%s", engageOut)
	}
	if !strings.Contains(engageOut, "satelle doctor") {
		t.Errorf("the refusal should point at doctor:\n%s", engageOut)
	}
}

// TestDoctorAllReportsEveryRegisteredRepo pins AC2 through the real binary, and
// that one bad repo never hides a good one.
func TestDoctorAllReportsEveryRegisteredRepo(t *testing.T) {
	good := t.TempDir()
	mustRun(t, testBin, good, "init")
	mustRun(t, testBin, good, "reindex")

	bad := t.TempDir()
	mustRun(t, testBin, bad, "init")
	writeFile(t, filepath.Join(bad, ".satelle", "agents.toml"), "[reviewer\nnot toml")

	mustRun(t, testBin, good, "workspace", "add", bad)

	out, err := run(t, testBin, good, "doctor", "--all")
	if err == nil {
		t.Fatalf("a sweep containing an unhealthy repo must exit non-zero:\n%s", out)
	}
	if !strings.Contains(out, "HEALTHY "+good) {
		t.Errorf("the healthy repo must still be reported healthy:\n%s", out)
	}
	if !strings.Contains(out, "UNHEALTHY "+bad) {
		t.Errorf("the broken repo must be reported:\n%s", out)
	}
	if !strings.Contains(out, "healthy,") {
		t.Errorf("the sweep must print a summary tally:\n%s", out)
	}
}

// TestDoctorJSONPayload pins AC8's machine-readable contract end to end.
func TestDoctorJSONPayload(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "doctor", "--json")
	var payload struct {
		Repos []struct {
			Repo     string `json:"repo"`
			OK       bool   `json:"ok"`
			Findings []struct {
				ID       string `json:"id"`
				Severity string `json:"severity"`
			} `json:"findings"`
			Grants []struct {
				Name    string            `json:"Name"`
				Sources map[string]string `json:"Sources"`
			} `json:"grants"`
		} `json:"repos"`
		Summary struct {
			Healthy   int `json:"healthy"`
			Unhealthy int `json:"unhealthy"`
		} `json:"summary"`
		ExitCode int `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("payload must unmarshal: %v\n%s", err, out)
	}
	if len(payload.Repos) != 1 || !payload.Repos[0].OK {
		t.Fatalf("want one healthy repo: %+v", payload.Repos)
	}
	if payload.ExitCode != 0 || payload.Summary.Healthy != 1 {
		t.Errorf("summary/exit mismatch: %+v exit=%d", payload.Summary, payload.ExitCode)
	}
	if len(payload.Repos[0].Grants) == 0 || len(payload.Repos[0].Grants[0].Sources) == 0 {
		t.Errorf("grants with per-field sources must be in the payload: %+v", payload.Repos[0].Grants)
	}
}

// TestDoctorSecretsNeverPrinted pins AC6's containment through the real binary,
// in both text and JSON, for a value that genuinely resolves.
func TestDoctorSecretsNeverPrinted(t *testing.T) {
	const secret = "sk-integration-must-not-print"
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
		fmt.Sprintf("[vars]\nDOCTOR_SECRET = %q\n", secret))
	writeFile(t, filepath.Join(repo, ".satelle", "agents.toml"),
		"[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n"+
			"[reviewer]\nrole = \"reviewer\"\ncommand = \"claude -p --disallowedTools Write,Edit --append-system-prompt {system}\"\ntools = \"Read,Grep,Glob\"\n"+
			"env = { ANTHROPIC_AUTH_TOKEN = \"${DOCTOR_SECRET}\" }\n")
	mustRun(t, testBin, repo, "reindex")

	for _, args := range [][]string{{"doctor"}, {"doctor", "--json"}} {
		out, _ := run(t, testBin, repo, args...)
		if strings.Contains(out, secret) {
			t.Fatalf("`satelle %s` leaked an env value:\n%s", strings.Join(args, " "), out)
		}
		if !strings.Contains(out, "ANTHROPIC_AUTH_TOKEN") {
			t.Errorf("`satelle %s` should name the env KEY:\n%s", strings.Join(args, " "), out)
		}
	}
}

// TestDoctorHelpDocumentsTheContract pins AC8's help: severity, exit codes, the
// live side effect, and the two configuration layers.
func TestDoctorHelpDocumentsTheContract(t *testing.T) {
	dir := t.TempDir()
	for _, out := range []string{
		mustRun(t, testBin, dir, "help", "doctor"),
		mustRun(t, testBin, dir, "doctor", "--help"),
	} {
		for _, want := range []string{
			"FAIL", "WARN", "INFO",
			"0", "1", "2",
			"no paid",
			"repo workflow POLICY", "machine-wide EXECUTION",
		} {
			if !strings.Contains(out, want) && !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
				t.Errorf("doctor documentation missing %q:\n%s", want, out)
			}
		}
	}
	if list := mustRun(t, testBin, dir, "help"); !strings.Contains(list, "doctor") {
		t.Errorf("`satelle help` should list the doctor topic:\n%s", list)
	}
}

// TestInitAndDoctorAgree is AC4: init prints doctor's findings, so a defect that
// makes doctor unhealthy also fails init — with the same identifier.
func TestInitAndDoctorAgree(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "broken.md"), "---\nname: broken\n---\n# broken\n")

	initOut, ierr := run(t, testBin, repo, "init")
	if ierr == nil {
		t.Fatalf("init must fail on broken substrate:\n%s", initOut)
	}
	if !strings.Contains(initOut, "[workflow.structure]") {
		t.Errorf("init must print doctor's finding identifiers:\n%s", initOut)
	}
	doctorOut, derr := run(t, testBin, repo, "doctor")
	if derr == nil || !strings.Contains(doctorOut, "[workflow.structure]") {
		t.Errorf("doctor must report the same defect:\n%s", doctorOut)
	}
}

// TestServiceStatusReportsRegisteredRepoHealth is AC4's second half: an
// unhealthy registered repo is visible from the service surface rather than
// looking identical to a ready one.
func TestServiceStatusReportsRegisteredRepoHealth(t *testing.T) {
	good := t.TempDir()
	mustRun(t, testBin, good, "init")
	bad := t.TempDir()
	mustRun(t, testBin, bad, "init")
	writeFile(t, filepath.Join(bad, ".satelle", "agents.toml"), "[reviewer\nnot toml")
	mustRun(t, testBin, good, "workspace", "add", bad)

	out, _ := run(t, testBin, good, "service", "status")
	if !strings.Contains(out, "repos:") {
		t.Skipf("service status short-circuited on this host (no systemctl):\n%s", out)
	}
	if !strings.Contains(out, "UNHEALTHY "+bad) {
		t.Errorf("service status must name the unhealthy registered repo:\n%s", out)
	}
	if !strings.Contains(out, "satelle doctor --all") {
		t.Errorf("service status should point at doctor for the detail:\n%s", out)
	}
}

// storyID returns the id of the first story in a repo.
func storyID(t *testing.T, repo string) string {
	t.Helper()
	var rows []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(mustRun(t, testBin, repo, "story", "list")), &rows); err != nil {
		t.Fatalf("story list: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no stories")
	}
	return rows[0].ID
}
