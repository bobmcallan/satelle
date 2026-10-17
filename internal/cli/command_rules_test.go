package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/help"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

// Repo-authored agent command rules (sty_a74551d6). Every rule here is authored
// by the test: satelle ships none. The example tool is the fictional orbctl in
// testdata/command_rules/example.toml.

const ruleGateBase = "[review]\ngate_create = false\n\n[gate]\nedit_exempt_paths = [\".satelle/\"]\n"

// ruleRepo is a governed repo with the given seat and the given rules appended
// to its [gate] table.
func ruleRepo(t *testing.T, seat stopcheckSeatState, rules string) (repo, storyID string) {
	t.Helper()
	repo, storyID = stopcheckRepo(t, seat)
	writeRuleConfig(t, repo, rules)
	return repo, storyID
}

func writeRuleConfig(t *testing.T, repo, rules string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte(ruleGateBase+rules), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exampleRules(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0) // the test may have changed directory
	b, err := os.ReadFile(filepath.Join(filepath.Dir(here), "testdata", "command_rules", "example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return "\n" + string(b)
}

func ruleBashEvent(command string) string {
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": "Bash",
		"tool_input": map[string]any{"command": command},
	})
	return string(b)
}

func commitgateOn(t *testing.T, harness, command string) (string, error) {
	t.Helper()
	return runRootIn(t, ruleBashEvent(command), "hook", "commitgate", "--harness", harness)
}

func ruleDenyReason(t *testing.T, out string) string {
	t.Helper()
	var doc claudePreToolUseDenyOut
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &doc); err != nil {
		t.Fatalf("not a single deny document: %v\n%s", err, out)
	}
	if doc.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("not a deny: %s", out)
	}
	return doc.HookSpecificOutput.PermissionDecisionReason
}

func setStoryStatus(t *testing.T, id, status string) {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Stories.SetStatus(context.Background(), id, status, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

// AC1: nothing is denied or warned with no rules authored.
func TestCommandRulesNoDefault(t *testing.T) {
	for _, seat := range []stopcheckSeatState{seatNone, seatMine} {
		t.Run(string(seat), func(t *testing.T) {
			ruleRepo(t, seat, "")
			for _, cmd := range []string{"rm -rf /", "kubectl delete x", "gh pr merge 1", "orbctl apply -f x", "ls"} {
				out, err := commitgateOn(t, "claude", cmd)
				if err != nil || strings.TrimSpace(out) != "" {
					t.Errorf("%q with no rules: err=%v out=%q", cmd, err, out)
				}
			}
		})
	}
	if idx, ok := matchCommandRule(nil, "rm -rf /"); ok || idx != 0 {
		t.Errorf("no rules matched a command")
	}
}

func TestCommandRulesScaffoldAndEmbeddedShipNone(t *testing.T) {
	for _, line := range strings.Split(scaffoldToml, "\n") {
		if strings.Contains(line, "command_rule") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Errorf("the scaffold must mention command_rule only in comments: %q", line)
		}
	}
	var cfg config.Config
	if _, err := toml.Decode(scaffoldToml, &cfg); err != nil {
		t.Fatal(err)
	}
	if n := len(cfg.Gate.CommandRules); n != 0 {
		t.Errorf("scaffold declares %d rules", n)
	}
	if n := len((config.Config{}).Gate.CommandRules); n != 0 {
		t.Errorf("zero config declares %d rules", n)
	}
}

// AC3 / AC4: the example rule set — read-only commands pass in every status,
// mutating ones are denied unless the story is engaged at in_progress, scale
// warns.
func TestCommandRulesExampleSet(t *testing.T) {
	readOnly := []string{"orbctl get pods", "orbctl list", "orbctl describe node-1", "ls", "echo orbctl apply"}
	mutating := []string{"orbctl apply -f x.yaml", "orbctl delete pod/x", "cd /tmp && orbctl apply -f x", "FOO=1 orbctl delete y", "orbctl 'apply' -f x"}

	t.Run("unengaged", func(t *testing.T) {
		ruleRepo(t, seatNone, exampleRules(t))
		for _, c := range readOnly {
			if out, err := commitgateOn(t, "claude", c); err != nil || out != "" {
				t.Errorf("%q must pass: %v %q", c, err, out)
			}
		}
		for _, c := range mutating {
			out, err := commitgateOn(t, "claude", c)
			if err == nil {
				t.Errorf("%q must be denied with no story engaged", c)
				continue
			}
			reason := ruleDenyReason(t, out)
			if !strings.Contains(reason, "changes shared state") && !strings.Contains(reason, "removes shared state") {
				t.Errorf("%q: the rule's message is missing: %s", c, reason)
			}
			if !strings.Contains(reason, "in_progress") || !strings.Contains(reason, "no story is engaged") {
				t.Errorf("%q: the refusal must say what to engage: %s", c, reason)
			}
		}
	})
	t.Run("engaged at in_progress", func(t *testing.T) {
		ruleRepo(t, seatMine, exampleRules(t))
		for _, c := range append(append([]string{}, readOnly...), mutating...) {
			if out, err := commitgateOn(t, "claude", c); err != nil || out != "" {
				t.Errorf("%q must pass at in_progress: %v %q", c, err, out)
			}
		}
	})
	t.Run("engaged elsewhere", func(t *testing.T) {
		_, id := ruleRepo(t, seatMine, exampleRules(t))
		setStoryStatus(t, id, "plan")
		for _, c := range readOnly {
			if out, err := commitgateOn(t, "claude", c); err != nil || out != "" {
				t.Errorf("%q must pass at plan: %v %q", c, err, out)
			}
		}
		out, err := commitgateOn(t, "claude", "orbctl apply -f x")
		if err == nil || !strings.Contains(ruleDenyReason(t, out), `"plan"`) {
			t.Errorf("an engaged story at plan must be refused and named: %v %s", err, out)
		}
	})
	t.Run("scale warns", func(t *testing.T) {
		ruleRepo(t, seatMine, exampleRules(t))
		out, err := commitgateOn(t, "claude", "orbctl scale deploy/x 3")
		if err != nil {
			t.Fatalf("a warned command runs: %v\n%s", err, out)
		}
		var doc agentcli.HookContextOut
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &doc); jerr != nil {
			t.Fatalf("a warn is one additionalContext document: %v\n%s", jerr, out)
		}
		if !strings.Contains(doc.HookSpecificOutput.AdditionalContext, "orbctl scale changes capacity") ||
			doc.HookSpecificOutput.PermissionDecision != "" || doc.HookSpecificOutput.HookEventName != "PreToolUse" {
			t.Errorf("warn = %+v", doc.HookSpecificOutput)
		}
	})
}

// A deny after a warn leaves one answer on stdout, not two.
func TestCommandRulesWarnThenLaterDenyEmitsOneDocument(t *testing.T) {
	_, _ = ruleRepo(t, seatNone, `
[[gate.command_rule]]
pattern = "git push"
action = "warn"
message = "pushing"

[gate.command_allow]
push = ["release"]
`)
	out, err := commitgateOn(t, "claude", "git push origin main")
	if err == nil {
		t.Fatalf("an unengaged push is refused:\n%s", out)
	}
	ruleDenyReason(t, out) // exactly one JSON document, and it is the deny
}

// AC9: a deny with no allow list refuses in every status, release included,
// and never reads the seat.
func TestCommandRuleDenyWithNoAllowRefusesEverywhere(t *testing.T) {
	const rules = `
[[gate.command_rule]]
pattern = "orbctl nuke"
action = "deny"
message = "orbctl nuke is never run by an agent"
`
	statuses := []string{"backlog", "plan", "in_progress", "integration", "release", "done", "blocked"}
	_, id := ruleRepo(t, seatMine, rules)
	for _, st := range statuses {
		setStoryStatus(t, id, st)
		out, err := commitgateOn(t, "claude", "orbctl nuke --all")
		if err == nil {
			t.Fatalf("status %s: a deny with no allow list must refuse:\n%s", st, out)
		}
		if reason := ruleDenyReason(t, out); !strings.Contains(reason, "never run by an agent") || !strings.Contains(reason, "every status") {
			t.Errorf("status %s: reason = %s", st, reason)
		}
	}
	t.Run("unengaged", func(t *testing.T) {
		ruleRepo(t, seatNone, rules)
		if _, err := commitgateOn(t, "claude", "orbctl nuke"); err == nil {
			t.Fatal("unengaged: must refuse")
		}
	})
	t.Run("no seat read", func(t *testing.T) {
		d := evalCommandRules(mustRules(t, rules), "orbctl nuke", func() (seatInfo, bool, error) {
			t.Fatal("a deny with no allow list must not read the seat")
			return seatInfo{}, false, nil
		})
		if d.Decision != commandRuleDenied {
			t.Fatalf("decision = %+v", d)
		}
	})
}

func mustRules(t *testing.T, tomlRules string) []config.CommandRule {
	t.Helper()
	var cfg config.Config
	if _, err := toml.Decode(tomlRules, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.Gate.CommandRules
}

// The allow list takes a step name as well as a status.
func TestCommandRuleAllowMatchesStatusOrStepName(t *testing.T) {
	rules := mustRules(t, `
[[gate.command_rule]]
pattern = "orbctl apply"
action = "deny"
allow = ["Coded", "release"]
message = "m"
`)
	deny := func(info seatInfo) bool {
		d := evalCommandRules(rules, "orbctl apply", func() (seatInfo, bool, error) { return info, true, nil })
		return d.Decision == commandRuleDenied
	}
	if deny(seatInfo{ItemID: "sty_1", StoryStatus: "in_progress", State: "coded"}) {
		t.Error("the seat's step name is in the allow list (case-insensitive)")
	}
	if deny(seatInfo{ItemID: "sty_1", StoryStatus: "release", State: "released"}) {
		t.Error("the story status is in the allow list")
	}
	if !deny(seatInfo{ItemID: "sty_1", StoryStatus: "plan", State: "readied"}) {
		t.Error("neither name is listed")
	}
}

func TestCommandRulesMatching(t *testing.T) {
	rules := mustRules(t, `
[[gate.command_rule]]
pattern = "orbctl scale * --force"
action = "deny"
message = "m"

[[gate.command_rule]]
pattern = "orbctl apply"
action = "warn"
message = "m"
`)
	cases := []struct {
		cmd  string
		want int // -1 none
	}{
		{"orbctl apply", 1},
		{"orbctl apply -f x", 1},
		{"orbctl applyx", -1},
		{"orbctl", -1},
		{"orbctl scale web --force", 0},
		{"orbctl scale --force", -1},
		{"orbctl scale web", -1},
		{"echo hi; orbctl apply", 1},
		{"echo hi | orbctl apply", 1},
		{"A=1 B=2 orbctl apply", 1},
		{`orbctl "apply"`, 1},
		{"echo orbctl apply", -1},
		{"cat <<EOF\norbctl apply\nEOF", -1},
	}
	for _, tc := range cases {
		got, ok := matchCommandRule(rules, tc.cmd)
		if tc.want < 0 {
			if ok {
				t.Errorf("%q matched rule %d", tc.cmd, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("%q = (%d,%v), want rule %d", tc.cmd, got, ok, tc.want)
		}
	}
	// First match in file order wins, whatever the command order.
	both := mustRules(t, `
[[gate.command_rule]]
pattern = "orbctl"
action = "warn"
message = "first"

[[gate.command_rule]]
pattern = "orbctl apply"
action = "deny"
message = "second"
`)
	if idx, _ := matchCommandRule(both, "orbctl apply"); idx != 0 {
		t.Errorf("first rule must win, got %d", idx)
	}
}

// The example's names stay out of the binary: AC4.
func TestExampleNamesAbsentFromProductionGo(t *testing.T) {
	banned := []string{"orbctl", "fetchy", "api.example.test"}
	root := filepath.Join("..", "..")
	var scanned int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "vendor", "node_modules", ".satelle":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		scanned++
		for _, word := range banned {
			if strings.Contains(string(b), word) {
				t.Errorf("%s names the example tool %q", path, word)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 50 {
		t.Fatalf("scanned only %d Go files — the walk is not reaching the tree", scanned)
	}
}

// AC5: pure in-process matching.
func TestCommandRulesImports(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "command_rules.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		switch p := strings.Trim(imp.Path.Value, `"`); {
		case p == "os/exec", p == "net", strings.HasPrefix(p, "net/"):
			t.Errorf("command_rules.go imports %s: rule evaluation must spawn nothing and call no network", p)
		}
	}
}

func BenchmarkCommandRules(b *testing.B) {
	var rules []config.CommandRule
	for i := 0; i < 100; i++ {
		rules = append(rules, config.CommandRule{Pattern: "tool" + string(rune('a'+i%26)) + " sub * --flag", Action: "deny", Message: "m"})
	}
	const command = "cd /srv/app && git status --short | grep -v x; ls -la internal/cli"
	seat := func() (seatInfo, bool, error) { return seatInfo{}, false, nil }
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		evalCommandRules(rules, command, seat)
	}
}

// The one measurement and verdict behind every AC5 relative-cost proof. Rule
// evaluation is judged against the tokenizer pass the gate already makes for the
// same command, on the same host in the same window: a ratio, never a clock.
//
// A shared CI runner preempts a timed loop and only ever adds time, so the verdict
// compares the median eval sample to the median scan sample. The minimum of each
// side is a single extreme sample: one unusually quick scan inflates the ratio
// (run 38988617675 divided by 1911ns against a 2478ns median and read 10.01x for a
// healthy 8.05x). The median of the interleaved samples ignores up to two quick
// or preempted outliers on each side independently, so a one-sided preemption
// cannot move the verdict. It needs an odd relativeCostSamples.
//
// The bound is set from measured ratios. Healthy medians are about 3x locally and
// 8.05x on the CI runner (run 38988617675), so 20x leaves 2.5x over CI. A
// regression, re-tokenising once per rule, costs about 115x locally (56x to
// 196x over 36 runs), so 20x sits 5x below it.
const (
	relativeCostBound   = 20.0
	relativeCostSamples = 5
	relativeCostIters   = 2000
)

var errScanUnmeasurable = errors.New("scan baseline too fast to measure")

// relativeCost times eval and scan over relativeCostSamples interleaved pairs of
// relativeCostIters calls each and returns the per-call nanoseconds of every
// sample. A fixed iteration count, unlike testing.Benchmark, bounds the wall time.
func relativeCost(eval, scan func()) (evalNs, scanNs []int64) {
	return relativeCostN(eval, scan, relativeCostIters)
}

func relativeCostN(eval, scan func(), iters int) (evalNs, scanNs []int64) {
	timed := func(f func()) int64 {
		start := time.Now()
		for i := 0; i < iters; i++ {
			f()
		}
		return time.Since(start).Nanoseconds() / int64(iters)
	}
	eval()
	scan()
	for s := 0; s < relativeCostSamples; s++ {
		runtime.GC()
		evalNs = append(evalNs, timed(eval))
		scanNs = append(scanNs, timed(scan))
	}
	return evalNs, scanNs
}

// median is the middle element of the sorted samples; the count is odd.
func median(ns []int64) int64 {
	return slices.Sorted(slices.Values(ns))[len(ns)/2]
}

// relativeCostVerdict is the ratio of the median eval sample to the median scan
// sample. It errors when that ratio exceeds bound, and returns errScanUnmeasurable
// when the median scan sample is zero.
func relativeCostVerdict(evalNs, scanNs []int64, bound float64) (float64, error) {
	medEval, medScan := median(evalNs), median(scanNs)
	if medScan <= 0 {
		return 0, errScanUnmeasurable
	}
	ratio := float64(medEval) / float64(medScan)
	if ratio > bound {
		return ratio, fmt.Errorf("rules cost %.1fx the tokenizer pass by median, over the %.0fx bound (eval %dns, scan %dns)", ratio, bound, medEval, medScan)
	}
	return ratio, nil
}

// checkRelativeCost measures eval against scan and fails t when the verdict does.
func checkRelativeCost(t *testing.T, eval, scan func()) {
	t.Helper()
	evalNs, scanNs := relativeCost(eval, scan)
	ratio, err := relativeCostVerdict(evalNs, scanNs, relativeCostBound)
	t.Logf("median ratio %.2f (eval %v ns, scan %v ns)", ratio, evalNs, scanNs)
	if errors.Is(err, errScanUnmeasurable) {
		t.Skip(err.Error())
	}
	if err != nil {
		t.Error(err)
	}
}

// AC5: evaluation costs no more than a small multiple of the tokenizer pass the
// gate already makes for the same command. A relative bound, never a clock.
func TestCommandRulesRelativeCost(t *testing.T) {
	var rules []config.CommandRule
	for i := 0; i < 100; i++ {
		rules = append(rules, config.CommandRule{Pattern: "tool" + string(rune('a'+i%26)) + " sub * --flag", Action: "deny", Message: "m"})
	}
	const command = "cd /srv/app && git status --short | grep -v x; ls -la internal/cli"
	seat := func() (seatInfo, bool, error) { return seatInfo{}, false, nil }
	checkRelativeCost(t,
		func() { evalCommandRules(rules, command, seat) },
		func() { gitSubcommands(command) })
}

// A pass seen in CI at 5.4x (eval 16856ns, scan 3138ns), the samples of CI run
// 38988617675 (10.01x by minimums, 8.05x by medians) and a one-sided preemption
// of either side are scheduler noise, not a regression.
func TestRelativeCostToleratesNoise(t *testing.T) {
	cases := map[string]struct{ eval, scan []int64 }{
		"one outlier pair among healthy ones": {
			[]int64{13000, 16856, 12000, 14000, 12500},
			[]int64{3100, 3138, 3000, 3200, 3050},
		},
		"every pair at the CI ratio": {
			[]int64{16856, 16856, 16856, 16856, 16856},
			[]int64{3138, 3138, 3138, 3138, 3138},
		},
		"CI run 38988617675": {
			[]int64{19127, 20870, 22192, 19951, 19648},
			[]int64{2478, 1911, 3130, 2861, 2096},
		},
		"eval preempted in one sample, scan in another": {
			[]int64{60000, 13000, 13500, 12800, 13200},
			[]int64{3100, 15000, 3050, 3000, 3150},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if ratio, err := relativeCostVerdict(c.eval, c.scan, relativeCostBound); err != nil {
				t.Errorf("noise failed the check at %.1fx: %v", ratio, err)
			}
		})
	}
}

// A real regression still fails: rule evaluation made an order of magnitude
// slower, here by re-tokenising once per rule.
func TestRelativeCostCatchesRegression(t *testing.T) {
	var rules []config.CommandRule
	for i := 0; i < 100; i++ {
		rules = append(rules, config.CommandRule{Pattern: "tool" + string(rune('a'+i%26)) + " sub * --flag", Action: "deny", Message: "m"})
	}
	const command = "cd /srv/app && git status --short | grep -v x; ls -la internal/cli"
	seat := func() (seatInfo, bool, error) { return seatInfo{}, false, nil }
	slow := func() {
		for range rules {
			gitSubcommands(command)
		}
		evalCommandRules(rules, command, seat)
	}
	evalNs, scanNs := relativeCostN(slow, func() { gitSubcommands(command) }, 100)
	ratio, err := relativeCostVerdict(evalNs, scanNs, relativeCostBound)
	t.Logf("re-tokenising per rule: median ratio %.1f", ratio)
	if err == nil || errors.Is(err, errScanUnmeasurable) {
		t.Errorf("re-tokenising per rule passed the check (ratio %.1f, err %v)", ratio, err)
	}

	pureEval := []int64{120000, 124000, 122000, 126000, 121000}
	pureScan := []int64{3000, 3100, 3050, 3000, 3200}
	if _, err := relativeCostVerdict(pureEval, pureScan, relativeCostBound); err == nil {
		t.Error("a 40x ratio passed the check")
	}
}

// AC5: no seat is read with no rules, or when no rule matches.
func TestCommandRulesReadNoSeatUnlessMatched(t *testing.T) {
	seat := func() (seatInfo, bool, error) {
		t.Fatal("the seat must not be read")
		return seatInfo{}, false, nil
	}
	evalCommandRules(nil, "orbctl apply", seat)
	evalCommandRules(mustRules(t, exampleRules(t)), "orbctl get pods", seat)
}

// AC6: a command a rule matched and let run is on the engaged story's ledger.
func TestCommandRulesRecordedOnLedger(t *testing.T) {
	_, id := ruleRepo(t, seatMine, exampleRules(t))
	if out, err := commitgateOn(t, "claude", "orbctl apply -f x"); err != nil || out != "" {
		t.Fatalf("allowed at in_progress: %v %q", err, out)
	}
	if out, err := commitgateOn(t, "claude", "orbctl scale web 2"); err != nil || out == "" {
		t.Fatalf("warn: %v %q", err, out)
	}
	if _, err := commitgateOn(t, "claude", "orbctl get pods"); err != nil {
		t.Fatal(err)
	}
	rows := ledgerRows(t, ledger.KindCommandRule)
	if len(rows) != 2 {
		t.Fatalf("want one row per matched command, got %d", len(rows))
	}
	var got []commandRulePayload
	for _, r := range rows {
		if r.StoryID != id {
			t.Errorf("row story = %q, want %q", r.StoryID, id)
		}
		var p commandRulePayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			t.Fatal(err)
		}
		got = append(got, p)
	}
	byDecision := map[string]commandRulePayload{}
	for _, p := range got {
		byDecision[p.Decision] = p
	}
	a, w := byDecision[commandRuleAllowed], byDecision[commandRuleWarned]
	if a.Command != "orbctl apply -f x" || a.Pattern != "orbctl apply" || a.Action != "deny" || a.Status != "in_progress" || a.RuleIndex != 0 {
		t.Errorf("allowed row = %+v", a)
	}
	if w.Command != "orbctl scale web 2" || w.Pattern != "orbctl scale *" || w.Action != "warn" || w.Status != "in_progress" || !w.Delivered {
		t.Errorf("warned row = %+v", w)
	}
}

func TestCommandRuleWarnWithNoStoryDeliversAndRecordsNothing(t *testing.T) {
	ruleRepo(t, seatNone, exampleRules(t))
	out, err := commitgateOn(t, "claude", "orbctl scale web 2")
	if err != nil || !strings.Contains(out, "orbctl scale changes capacity") {
		t.Fatalf("warn must still be delivered: %v %q", err, out)
	}
	if rows := ledgerRows(t, ledger.KindCommandRule); len(rows) != 0 {
		t.Errorf("no engaged story, no ledger row: %d", len(rows))
	}
}

// AC7: a warn is delivered only where the harness can carry it; elsewhere the
// command runs, nothing is printed, and the row says it was not delivered.
func TestCommandRuleWarnPerHarness(t *testing.T) {
	for _, h := range []string{"claude", "grok", "pi", "cursor"} {
		t.Run(h, func(t *testing.T) {
			_, id := ruleRepo(t, seatMine, exampleRules(t))
			out, err := commitgateOn(t, h, "orbctl scale web 2")
			if err != nil {
				t.Fatalf("a warned command runs on %s: %v\n%s", h, err, out)
			}
			delivers := agentcli.FactsFor(h).PreToolUseWarn.Available
			if delivers != (strings.TrimSpace(out) != "") {
				t.Errorf("%s: delivers=%v but stdout=%q", h, delivers, out)
			}
			var found bool
			for _, r := range ledgerRows(t, ledger.KindCommandRule) {
				var p commandRulePayload
				_ = json.Unmarshal(r.Payload, &p)
				if r.StoryID == id && p.Decision == commandRuleWarned {
					found = true
					if p.Delivered != delivers {
						t.Errorf("%s: row delivered=%v, want %v", h, p.Delivered, delivers)
					}
				}
			}
			if !found {
				t.Errorf("%s: no warned row", h)
			}
			// A deny reaches every harness.
			_, derr := commitgateOn(t, h, "orbctl delete x")
			_ = derr
		})
	}
}

func TestCommandRuleDenyReachesEveryHarness(t *testing.T) {
	for _, h := range []string{"claude", "grok", "pi", "cursor"} {
		t.Run(h, func(t *testing.T) {
			ruleRepo(t, seatNone, exampleRules(t))
			out, err := commitgateOn(t, h, "orbctl delete x")
			if err == nil {
				t.Fatalf("%s: deny expected:\n%s", h, out)
			}
			if !strings.Contains(out, "removes shared state") {
				t.Errorf("%s: the deny must carry the rule's message: %s", h, out)
			}
		})
	}
}

// AC10 (a): a dispatched agent and a rework relay take the same path.
func TestCommandRulesApplyToDispatchedAgents(t *testing.T) {
	_, id := ruleRepo(t, seatMine, exampleRules(t))
	t.Setenv(config.DispatchAgentEnv, "planner")
	t.Setenv(config.DispatchStepEnv, "plan")
	t.Setenv(config.DispatchItemEnv, id)
	// The seat is at in_progress, which the example allows, so orbctl delete runs;
	// the dispatched agent reaches the rule and the seat all the same.
	if out, err := commitgateOn(t, "claude", "orbctl delete x"); err != nil || strings.TrimSpace(out) != "" {
		t.Errorf("dispatched agent at an allowed status: %v %q", err, out)
	}
	setStoryStatus(t, id, "backlog")
	out, err := commitgateOn(t, "claude", "orbctl delete x")
	if err == nil {
		t.Fatalf("dispatched agent not refused at backlog:\n%s", out)
	}
	if reason := ruleDenyReason(t, out); !strings.Contains(reason, "removes shared state") {
		t.Errorf("dispatched deny reason = %s", reason)
	}
	setStoryStatus(t, id, "in_progress")
	out, err = commitgateOn(t, "claude", "orbctl scale web 2")
	if err != nil || !strings.Contains(out, "orbctl scale changes capacity") {
		t.Errorf("a dispatched agent receives the warn: %v %q", err, out)
	}
	// A deny rule with no allow list refuses a dispatched agent outright.
	writeRuleConfig(t, ".", `
[[gate.command_rule]]
pattern = "orbctl nuke"
action = "deny"
message = "never"
`)
	out, err = commitgateOn(t, "claude", "orbctl nuke")
	if err == nil || !strings.Contains(ruleDenyReason(t, out), "never") {
		t.Errorf("dispatched agent not refused: %v %s", err, out)
	}
	t.Setenv(config.RelayBindingEnv, "coder")
	t.Setenv(config.RelayItemEnv, id)
	out, err = commitgateOn(t, "claude", "orbctl nuke")
	if err == nil || !strings.Contains(ruleDenyReason(t, out), "never") {
		t.Errorf("rework relay not refused: %v %s", err, out)
	}
}

// AC10 (c): a fresh clone has only the synced satelle.toml, no runtime store and
// no seat, and the rules still apply.
func TestCommandRulesApplyOnFreshClone(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	writeRuleConfig(t, repo, `
[[gate.command_rule]]
pattern = "orbctl nuke"
action = "deny"
message = "never"

[[gate.command_rule]]
pattern = "orbctl scale"
action = "warn"
message = "careful"
`)
	out, err := commitgateOn(t, "claude", "orbctl nuke")
	if err == nil || !strings.Contains(ruleDenyReason(t, out), "never") {
		t.Fatalf("fresh clone: deny expected: %v %s", err, out)
	}
	out, err = commitgateOn(t, "claude", "orbctl scale x")
	if err != nil || !strings.Contains(out, "careful") {
		t.Fatalf("fresh clone: warn expected: %v %q", err, out)
	}
	if out, err = commitgateOn(t, "claude", "orbctl get x"); err != nil || out != "" {
		t.Fatalf("fresh clone: an unmatched command passes: %v %q", err, out)
	}
}

// command_allow is unchanged: engaged-only, so with no story engaged a tag
// passes though tag = [] lists it.
func TestCommandAllowNoStoryUnchanged(t *testing.T) {
	ruleRepo(t, seatNone, "\n[gate.command_allow]\npush = [\"release\"]\ntag = []\n")
	if out, err := commitgateOn(t, "claude", "git tag v1"); err != nil || out != "" {
		t.Fatalf("git tag with no story engaged must pass as before: %v %q", err, out)
	}
}

// AC8: the help states the best-effort scope and that satelle manages no
// identity or rights.
func TestCommitgateHelpStatesScopeAndNoIdentity(t *testing.T) {
	out, err := runRootIn(t, "", "hook", "commitgate", "--help")
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(out), " ")
	topic, ok := help.Get("command-rules")
	if !ok {
		t.Fatal("help topic command-rules missing")
	}
	topicFlat := strings.Join(strings.Fields(topic.Body), " ")
	for _, want := range []string{
		"wrappers, `eval`, aliases and scripts bypass a rule",
		"satelle manages no identity or rights",
		"the tool's own permissions and the account it runs as enforce what a command may do",
		"ships **no rule",
		"**/NAME", "*TEXT*", "any run of tokens",
	} {
		if !strings.Contains(topicFlat, want) {
			t.Errorf("help command-rules lacks %q", want)
		}
	}
	for _, want := range []string{
		"Best-effort",
		"wrappers, eval and scripts bypass them",
		"satelle manages no identity or rights",
		"the tool's own permissions and the account it runs as enforce what a command may do",
		"satelle ships none",
		"help command-rules",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("commitgate --help lacks %q", want)
		}
	}
}

// Rules that fail to load refuse every shell command, visibly, and leave the
// edit gate alone.
func TestCommandRulesLoadFailClosed(t *testing.T) {
	const badAction = "\n[[gate.command_rule]]\npattern = \"orbctl apply\"\naction = \"block\"\nmessage = \"m\"\n"
	t.Run("malformed rule", func(t *testing.T) {
		repo, _ := ruleRepo(t, seatMine, "")
		path := filepath.Join(repo, ".satelle", "satelle.toml")
		writeRuleConfig(t, repo, badAction)
		out, err := commitgateOn(t, "claude", "ls")
		if err == nil {
			t.Fatalf("a malformed rule set must refuse every shell command:\n%s", out)
		}
		reason := ruleDenyReason(t, out)
		for _, want := range []string{path, "rule 0", "field action", "fix it and retry", "every shell command is refused"} {
			if !strings.Contains(reason, want) {
				t.Errorf("reason lacks %q: %s", want, reason)
			}
		}
		if strings.Contains(reason, "block") {
			t.Errorf("the refusal must not echo the value: %s", reason)
		}
		// The edit gate is untouched: it answers a malformed rule exactly as it
		// answers any other config the store cannot open under (here a bad
		// [gate] handoff). Neither is something this story changes.
		_, ruleErr := runRootIn(t, claudeEditEvent(path), "hook", "gate")
		writeRuleConfig(t, repo, "handoff = \"bogus\"\n")
		_, handoffErr := runRootIn(t, claudeEditEvent(path), "hook", "gate")
		if (ruleErr == nil) != (handoffErr == nil) {
			t.Errorf("the edit gate must treat a malformed rule like any other config error: rule err=%v handoff err=%v", ruleErr, handoffErr)
		}
	})
	t.Run("overlay only", func(t *testing.T) {
		repo, _ := ruleRepo(t, seatMine, "")
		local := filepath.Join(repo, ".satelle", config.LocalConfigName)
		if err := os.WriteFile(local, []byte(badAction), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := commitgateOn(t, "claude", "ls")
		if err == nil || !strings.Contains(ruleDenyReason(t, out), local) {
			t.Fatalf("an overlay-only malformed rule fails closed naming the overlay: %v\n%s", err, out)
		}
	})
	t.Run("syntax error", func(t *testing.T) {
		repo, _ := ruleRepo(t, seatMine, "")
		path := filepath.Join(repo, ".satelle", "satelle.toml")
		if err := os.WriteFile(path, []byte("[gate\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := commitgateOn(t, "claude", "ls")
		if err == nil {
			t.Fatalf("a config that does not parse fails closed:\n%s", out)
		}
		reason := ruleDenyReason(t, out)
		if !strings.Contains(reason, path) || !strings.Contains(reason, "failed to parse") || !strings.Contains(reason, "fix it and retry") {
			t.Errorf("reason = %s", reason)
		}
	})
	t.Run("unknown key", func(t *testing.T) {
		repo, _ := ruleRepo(t, seatMine, "")
		writeRuleConfig(t, repo, "\n[[gate.command_rule]]\npattern = \"a\"\naction = \"deny\"\nalow = [\"x\"]\nmessage = \"m\"\n")
		out, err := commitgateOn(t, "claude", "ls")
		if err == nil || !strings.Contains(ruleDenyReason(t, out), "alow") {
			t.Fatalf("a misspelled key fails closed and is named: %v\n%s", err, out)
		}
	})
	t.Run("no rules unaffected", func(t *testing.T) {
		ruleRepo(t, seatMine, "")
		if out, err := commitgateOn(t, "claude", "ls"); err != nil || out != "" {
			t.Fatalf("a config with no rules is unaffected: %v %q", err, out)
		}
	})
}
