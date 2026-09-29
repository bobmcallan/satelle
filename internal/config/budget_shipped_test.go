package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sty_a7914904 AC6: no budget value is hard-coded. The numbers are the repo's:
// the embedded substrate carries no context_budget/turn_budget, the config
// defaults are zero, and no non-test Go source assigns one.
//
// (DefaultGrokCommand's `--max-turns 16` is a harness-template flag, authored
// configuration for one adapter — not a satelle budget. It is not a
// context_budget/turn_budget/MaxTurns assignment and stays out of this scan.)

var budgetKeyValue = regexp.MustCompile(`(?m)^\s*(context_budget|turn_budget)\s*=\s*[0-9]`)

func TestEmbeddedSubstrateCarriesNoBudgetValue(t *testing.T) {
	err := fs.WalkDir(substrateFS, "substrate", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, rerr := substrateFS.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if m := budgetKeyValue.Find(body); m != nil {
			t.Errorf("%s ships a budget value %q — budgets are the repo's, never the binary's", p, strings.TrimSpace(string(m)))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBudgetDefaultsAreUnset(t *testing.T) {
	if (AgentBinding{}).Budget().Set() || (AgentsDefaults{}).Budget().Set() || (AgentsConfig{}).Defaults.Budget().Set() {
		t.Fatal("a zero config must carry no budget")
	}
	if got := (AgentsConfig{}).BudgetFor(AgentBinding{}, Budget{}); got.Set() {
		t.Fatalf("no tier set: BudgetFor must not invent one, got %+v", got)
	}
}

var goBudgetAssign = regexp.MustCompile(`\b(ContextBudget|TurnBudget|MaxTurns|Turns|Context)\s*[:=]\s*[1-9][0-9_]*\b`)

// TestNoBudgetNumberInGoSource scans the non-test Go sources for a numeric
// literal assigned to a budget field. Field names that are not budgets
// (Turns/Context on other types) would false-positive, so only files that
// mention a Budget are scanned.
func TestNoBudgetNumberInGoSource(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		body, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		src := string(body)
		if !strings.Contains(src, "Budget") && !strings.Contains(src, "MaxTurns") {
			return nil
		}
		for _, line := range strings.Split(src, "\n") {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "//") {
				continue
			}
			if goBudgetAssign.MatchString(line) && (strings.Contains(line, "Budget") || strings.Contains(line, "MaxTurns")) {
				t.Errorf("%s: budget numeric literal in Go source: %s", p, trim)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
