package cli

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
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
	banned := []string{"orbctl"}
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

// AC5: evaluation costs no more than a small multiple of the tokenizer pass the
// gate already makes for the same command. A relative bound, never a clock.
func TestCommandRulesRelativeCost(t *testing.T) {
	var rules []config.CommandRule
	for i := 0; i < 100; i++ {
		rules = append(rules, config.CommandRule{Pattern: "tool" + string(rune('a'+i%26)) + " sub * --flag", Action: "deny", Message: "m"})
	}
	const command = "cd /srv/app && git status --short | grep -v x; ls -la internal/cli"
	seat := func() (seatInfo, bool, error) { return seatInfo{}, false, nil }
	eval := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			evalCommandRules(rules, command, seat)
		}
	})
	scan := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			gitSubcommands(command)
		}
	})
	if scan.NsPerOp() == 0 {
		t.Skip("scan baseline too fast to measure")
	}
	if ratio := float64(eval.NsPerOp()) / float64(scan.NsPerOp()); ratio > 5 {
		t.Errorf("100 rules cost %.1fx the tokenizer pass (eval %dns, scan %dns)", ratio, eval.NsPerOp(), scan.NsPerOp())
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
