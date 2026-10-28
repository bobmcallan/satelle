package doctor

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

const driftedVersion = "2026.10.22-27dcf5f"

// The captured contracts a drift report must name, written out here rather than
// read from the table so a dropped contract fails instead of passing circularly.
var driftContracts = []string{"stop cap", "hook timeout handling", "envelope shapes", "ACP methods", "usage fields"}

// fullHelp lists every flag cursor's command templates use.
const fullHelp = "Options:\n  -p, --print\n  --output-format <format>\n  --mode <mode>\n  --trust\n  --model <model>\n"

// stubCLI installs an executable `name` first on PATH. It appends every
// invocation's argv to a log, answers --version with version (exit 1 when
// failVersion), --help with help, and answers an ACP initialize on `acp`.
func stubCLI(t *testing.T, name, version string, failVersion bool, help string) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	log = filepath.Join(t.TempDir(), "invocations")
	versionCmd := `echo "` + version + `"`
	if failVersion {
		versionCmd = `echo "boom" >&2; exit 1`
	}
	body := `#!/bin/sh
echo "$@" >> ` + log + `
case "$1" in
  --version) ` + versionCmd + ` ;;
  --help) printf '%s' '` + help + `' ;;
  acp) head -n 1 >/dev/null; echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}'; sleep 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, log
}

func invocations(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return ""
	}
	return string(b)
}

func driftFindings(fs health.Findings) health.Findings {
	var out health.Findings
	for _, f := range fs {
		if f.ID == health.IDLiveVersionDrift {
			out = append(out, f)
		}
	}
	return out
}

// assertDriftDetail checks a drift report names both builds, the recheck command
// and each captured contract with a non-empty basis.
func assertDriftDetail(t *testing.T, text, installed string) {
	t.Helper()
	captured := "2026.10.01-e373342"
	for _, want := range []string{installed, captured, "satelle agent recheck cursor"} {
		if !strings.Contains(text, want) {
			t.Errorf("drift report missing %q:\n%s", want, text)
		}
	}
	for _, c := range driftContracts {
		if !regexp.MustCompile(regexp.QuoteMeta(c) + ` \([^)]+\)`).MatchString(text) {
			t.Errorf("drift report names no basis for %q:\n%s", c, text)
		}
	}
}

// AC2/AC3: a cursor command binding whose --version differs from the captured
// build is a WARN naming both builds, the resolved binary and the contracts at risk.
func TestLiveVersionDriftCommandBinding(t *testing.T) {
	dir, _ := stubCLI(t, "cursor-agent", driftedVersion, false, fullHelp)
	g := grantFor("coder", "cursor-agent -p --trust --output-format json --model {model}", config.InterfaceCommand)
	fs := driftFindings(probeGrant(context.Background(), g, 5*time.Second))
	if len(fs) != 1 || fs[0].Severity != health.SeverityWarn {
		t.Fatalf("want one WARN %s, got %+v", health.IDLiveVersionDrift, fs)
	}
	assertDriftDetail(t, fs[0].Detail+" "+fs[0].Remediation, driftedVersion)
	if !strings.Contains(fs[0].Detail, filepath.Join(dir, "cursor-agent")) {
		t.Errorf("drift detail must name the resolved binary %s:\n%s", filepath.Join(dir, "cursor-agent"), fs[0].Detail)
	}
}

// AC2: the same on the ACP interface, beside the handshake result.
func TestLiveVersionDriftACPBinding(t *testing.T) {
	dir, _ := stubCLI(t, "cursor-agent", driftedVersion, false, fullHelp)
	g := grantFor("coder", "cursor-agent acp", config.InterfaceACP)
	all := probeGrant(context.Background(), g, 5*time.Second)
	if !hasID(all, health.IDLiveOK) {
		t.Errorf("the ACP handshake result must still be reported: %+v", all)
	}
	fs := driftFindings(all)
	if len(fs) != 1 || fs[0].Severity != health.SeverityWarn {
		t.Fatalf("want one WARN %s, got %+v", health.IDLiveVersionDrift, fs)
	}
	assertDriftDetail(t, fs[0].Detail, driftedVersion)
	if !strings.Contains(fs[0].Detail, filepath.Join(dir, "cursor-agent")) {
		t.Errorf("drift detail must name the resolved binary:\n%s", fs[0].Detail)
	}
}

const cursorReviewerAgents = healthyAgentsTOML + `
[probe]
role    = "reviewer"
command = "cursor-agent -p --trust --output-format json --model {model}"
tools   = "Read,Grep,Glob"
`

// AC2: ordinary doctor spawns nothing of cursor's; --live compares the build.
func TestDoctorVersionDriftOnlyUnderLive(t *testing.T) {
	_, log := stubCLI(t, "cursor-agent", driftedVersion, false, fullHelp)
	stubCLI(t, "claude", "2.1.288", false, "") // the baseline seats are claude bindings
	root := newFixtureRepo(t, fixtureOpts{agents: cursorReviewerAgents})
	opts := Opts{RepoRoot: root, DataDir: filepath.Join(root, ".satelle")}

	rep := Check(context.Background(), opts)
	if got := invocations(t, log); got != "" {
		t.Fatalf("ordinary doctor must never invoke the CLI, it ran: %q", got)
	}
	if len(driftFindings(rep.Findings)) != 0 {
		t.Errorf("ordinary doctor reports no version drift: %+v", driftFindings(rep.Findings))
	}

	opts.Live = true
	rep = Check(context.Background(), opts)
	if !strings.Contains(invocations(t, log), "--version") {
		t.Fatalf("--live must ask the CLI its version, log: %q", invocations(t, log))
	}
	var warns health.Findings
	for _, f := range driftFindings(rep.Findings) {
		if f.Severity == health.SeverityWarn {
			warns = append(warns, f)
		}
	}
	if len(warns) != 1 || !strings.Contains(warns[0].Detail, driftedVersion) {
		t.Fatalf("--live must WARN %s once for the cursor binding, got %+v", health.IDLiveVersionDrift, rep.Findings)
	}
}

// AC5: a matching build raises nothing, on either interface.
func TestLiveVersionMatchRaisesNothing(t *testing.T) {
	stubCLI(t, "cursor-agent", "2026.10.01-e373342", false, fullHelp)
	for _, g := range []agentvalidate.Grant{
		grantFor("c", "cursor-agent -p --output-format json", config.InterfaceCommand),
		grantFor("a", "cursor-agent acp", config.InterfaceACP),
	} {
		all := probeGrant(context.Background(), g, 5*time.Second)
		if len(driftFindings(all)) != 0 {
			t.Errorf("%s: matching build must raise no drift finding: %+v", g.Interface, all)
		}
		if !hasID(all, health.IDLiveOK) {
			t.Errorf("%s: the reachable finding must remain: %+v", g.Interface, all)
		}
	}
}

// AC5: an unreadable version is reported as unknown, never as a silent pass.
func TestLiveVersionUnknownIsReported(t *testing.T) {
	t.Run("empty output, command", func(t *testing.T) {
		stubCLI(t, "cursor-agent", "", false, fullHelp)
		fs := driftFindings(probeGrant(context.Background(), grantFor("c", "cursor-agent -p --output-format json", config.InterfaceCommand), 5*time.Second))
		if len(fs) != 1 || fs[0].Severity != health.SeverityWarn || !strings.Contains(fs[0].Detail, "version unknown") {
			t.Fatalf("want a WARN naming version unknown, got %+v", fs)
		}
	})
	t.Run("failing --version, ACP", func(t *testing.T) {
		stubCLI(t, "cursor-agent", "", true, fullHelp)
		fs := driftFindings(probeGrant(context.Background(), grantFor("a", "cursor-agent acp", config.InterfaceACP), 5*time.Second))
		if len(fs) != 1 || fs[0].Severity != health.SeverityWarn || !strings.Contains(fs[0].Detail, "version unknown") {
			t.Fatalf("an ACP binding must not end at the handshake alone, got %+v", fs)
		}
	})
	t.Run("failing --version, command", func(t *testing.T) {
		stubCLI(t, "cursor-agent", "", true, fullHelp)
		fs := probeGrant(context.Background(), grantFor("c", "cursor-agent -p --output-format json", config.InterfaceCommand), 5*time.Second)
		if len(fs) != 1 || fs[0].ID != health.IDLiveSpawn || !strings.Contains(fs[0].Detail, "version unknown") {
			t.Fatalf("the spawn failure must say the version is unknown, got %+v", fs)
		}
	})
}

// AC5: claude and grok have no captured version recorded; --live says so by name
// and spawns nothing of cursor's.
func TestLiveVersionNoCaptureIsReportedByName(t *testing.T) {
	_, cursorLog := stubCLI(t, "cursor-agent", driftedVersion, false, fullHelp)
	stubCLI(t, "claude", "2.1.288", false, "")
	stubCLI(t, "grok", "1.0.46", false, "")
	for _, tc := range []struct{ harness, command string }{
		{"claude", "claude -p {system}"},
		{"grok", "grok -p {system}"},
	} {
		all := probeGrant(context.Background(), grantFor(tc.harness, tc.command, config.InterfaceCommand), 5*time.Second)
		fs := driftFindings(all)
		want := "no captured version recorded for " + tc.harness
		if len(fs) != 1 || fs[0].Severity != health.SeverityInfo || !strings.Contains(fs[0].Detail, want) {
			t.Errorf("%s: want one INFO naming %q, got %+v", tc.harness, want, all)
		}
	}
	if got := invocations(t, cursorLog); got != "" {
		t.Errorf("a claude/grok-only run must not spawn cursor-agent, it ran: %q", got)
	}
}

// AC4/AC5: recheck reports every captured contract; a --help that lost a flag the
// templates use is changed (and Changed() is what turns the exit code non-zero).
func TestRecheckChangedWhenHelpOmitsOutputFormat(t *testing.T) {
	stubCLI(t, "cursor-agent", driftedVersion, false, "Options:\n  -p, --print\n  --mode <mode>\n  --trust\n  --model <model>\n")
	res := RecheckGrants(context.Background(), "cursor", nil, 5*time.Second)
	if !res.Changed() {
		t.Fatalf("a --help without --output-format must be changed: %+v", res)
	}
	var out strings.Builder
	res.Render(&out)
	text := out.String()
	for _, want := range []string{driftedVersion, "2026.10.01-e373342"} {
		if !strings.Contains(text, want) {
			t.Errorf("recheck header missing %q:\n%s", want, text)
		}
	}
	status := regexp.MustCompile(`(?m)^(holds|changed|unverifiable)\s+(\S.*?): `)
	got := map[string]string{}
	for _, m := range status.FindAllStringSubmatch(text, -1) {
		got[m[2]] = m[1]
	}
	if len(got) != len(driftContracts) {
		t.Errorf("want one status line per captured contract, got %v\n%s", got, text)
	}
	for _, c := range driftContracts {
		if got[c] == "" {
			t.Errorf("no status line for %q:\n%s", c, text)
		}
	}
	if got["envelope shapes"] != "changed" {
		t.Errorf("envelope shapes = %q, want changed", got["envelope shapes"])
	}
	for _, c := range []string{"stop cap", "hook timeout handling", "usage fields", "ACP methods"} {
		if got[c] != "unverifiable" {
			t.Errorf("%s = %q, want unverifiable without a session or ACP binding", c, got[c])
		}
	}
	if !strings.Contains(text, "testdata/cursor/20-cap-hooks.log") {
		t.Errorf("an unverifiable contract must name the fixture to re-capture:\n%s", text)
	}
}

func TestRecheckHoldsWhenFlagsPresentAndACPAnswers(t *testing.T) {
	stubCLI(t, "cursor-agent", "2026.10.01-e373342", false, fullHelp)
	grants := []agentvalidate.Grant{
		grantFor("c", "cursor-agent -p --output-format json", config.InterfaceCommand),
		grantFor("a", "cursor-agent acp", config.InterfaceACP),
	}
	res := RecheckGrants(context.Background(), "cursor", grants, 5*time.Second)
	if res.Changed() {
		t.Fatalf("nothing changed, got %+v", res)
	}
	byName := map[string]RecheckStatus{}
	for _, l := range res.Lines {
		byName[l.Contract] = l.Status
	}
	if byName["envelope shapes"] != RecheckHolds || byName["ACP methods"] != RecheckHolds {
		t.Errorf("envelope shapes and ACP methods must hold: %v", byName)
	}
	if byName["stop cap"] != RecheckUnverifiable {
		t.Errorf("stop cap needs a session and must stay unverifiable, got %v", byName["stop cap"])
	}
}

func TestRecheckChangedWhenACPHandshakeFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cursor-agent"), []byte("#!/bin/sh\ncase \"$1\" in --version) echo 1;; --help) printf '%s' '"+strings.ReplaceAll(fullHelp, "\n", " ")+"';; acp) head -n 1 >/dev/null; exit 0;; esac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	res := RecheckGrants(context.Background(), "cursor", []agentvalidate.Grant{grantFor("a", "cursor-agent acp", config.InterfaceACP)}, 5*time.Second)
	if !res.Changed() {
		t.Fatalf("an ACP peer that answers no initialize must be changed: %+v", res)
	}
}

// AC5: an adapter with no captured version says so and re-checks nothing.
func TestRecheckNoCapture(t *testing.T) {
	res := RecheckGrants(context.Background(), "claude", nil, time.Second)
	if res.HasCapture || res.Changed() {
		t.Fatalf("claude has no capture: %+v", res)
	}
	var out strings.Builder
	res.Render(&out)
	if !strings.Contains(out.String(), "no captured version recorded for claude") {
		t.Errorf("render = %q", out.String())
	}
}

// AC5: a missing binary leaves every contract unverifiable and the installed
// version unknown — not changed, not holding.
func TestRecheckMissingBinaryIsUnverifiable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	res := RecheckGrants(context.Background(), "cursor", nil, 2*time.Second)
	if res.Changed() || res.Installed != "unknown" {
		t.Fatalf("a missing CLI is unknown, not changed: %+v", res)
	}
	for _, l := range res.Lines {
		if l.Status != RecheckUnverifiable {
			t.Errorf("%s = %s, want unverifiable", l.Contract, l.Status)
		}
	}
}
