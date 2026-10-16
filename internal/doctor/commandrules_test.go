package doctor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

func ruleConfig(allow ...string) config.Config {
	var cfg config.Config
	cfg.Gate.CommandRules = []config.CommandRule{
		{Pattern: "orbctl apply", Action: config.CommandRuleDeny, Allow: allow, Message: "m"},
	}
	return cfg
}

func TestCheckCommandRuleAllowNames(t *testing.T) {
	root := newFixtureRepo(t, fixtureOpts{})
	wfDir := filepath.Join(root, ".satelle", "workflows")
	docs := GoverningWorkflowsAt(wfDir)

	if fs := CheckCommandRuleAllowNames(config.Config{}, docs); len(fs) != 0 {
		t.Fatalf("no rules, no findings: %v", fs)
	}
	if fs := CheckCommandRuleAllowNames(ruleConfig("in_progress", "DONE"), docs); len(fs) != 0 {
		t.Fatalf("names the route declares (any case) are fine: %v", fs)
	}
	if fs := CheckCommandRuleAllowNames(ruleConfig(), docs); len(fs) != 0 {
		t.Fatalf("an empty allow list names nothing to check: %v", fs)
	}
	fs := CheckCommandRuleAllowNames(ruleConfig("in_progress", "nosuchstate"), docs)
	if len(fs) != 1 || fs[0].ID != health.IDCommandRuleAllow || fs[0].Severity != health.SeverityError {
		t.Fatalf("findings = %v", fs)
	}
	if !strings.Contains(fs[0].Detail, "nosuchstate") || !strings.Contains(fs[0].Detail, "rule 0") || !strings.Contains(fs[0].Detail, "allow") {
		t.Errorf("the finding must name the rule, field and entry: %s", fs[0].Detail)
	}
	if !strings.Contains(fs[0].Remediation, "in_progress") {
		t.Errorf("the remediation lists the states the route declares: %s", fs[0].Remediation)
	}
}

func TestCheckReportsCommandRuleAllowName(t *testing.T) {
	root := newFixtureRepo(t, fixtureOpts{})
	cfg := ruleConfig("nosuchstate")
	rep := Check(context.Background(), Opts{RepoRoot: root, DataDir: filepath.Join(root, ".satelle"), Process: &cfg, ProcessRoot: root})
	if !ids(rep)[health.IDCommandRuleAllow] {
		t.Fatalf("doctor.Check must report the stale allow name: %v", rep.Findings)
	}
	clean := ruleConfig("in_progress")
	rep = Check(context.Background(), Opts{RepoRoot: root, DataDir: filepath.Join(root, ".satelle"), Process: &clean, ProcessRoot: root})
	if ids(rep)[health.IDCommandRuleAllow] {
		t.Fatalf("a valid allow name must not be reported: %v", rep.Findings)
	}
}
