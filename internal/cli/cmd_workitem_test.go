package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRowTokensRender pins the cost-table tri-state (sty_56aae77a AC2): measured
// non-zero, measured zero, and unreported each render distinctly; unreported is
// never a confident 0/0.
func TestRowTokensRender(t *testing.T) {
	cases := []struct {
		name       string
		in, out, n int
		available  bool
		wantIO     string
		wantTotal  string
	}{
		{name: "measured non-zero", in: 10, out: 20, n: 30, available: true, wantIO: "10/20", wantTotal: "30"},
		{name: "measured zero", in: 0, out: 0, n: 0, available: true, wantIO: "0/0", wantTotal: "0"},
		{name: "unreported", in: 0, out: 0, n: 0, available: false, wantIO: "—/—", wantTotal: "—"},
		// Even if leftover numbers sit on an unreported row, render as unknown.
		{name: "unreported with stale numbers", in: 5, out: 5, n: 10, available: false, wantIO: "—/—", wantTotal: "—"},
	}
	for _, c := range cases {
		if got := rowTokensIO(c.in, c.out, c.available); got != c.wantIO {
			t.Errorf("%s: rowTokensIO = %q, want %q", c.name, got, c.wantIO)
		}
		if got := rowTokensTotal(c.n, c.available); got != c.wantTotal {
			t.Errorf("%s: rowTokensTotal = %q, want %q", c.name, got, c.wantTotal)
		}
	}
	if got := measuredTotalLabel(410, 3, 2); got != "410 (measured; 2 of 5 invocations unreported)" {
		t.Errorf("measuredTotalLabel with unreported = %q", got)
	}
	if got := measuredTotalLabel(100, 2, 0); got != "100" {
		t.Errorf("measuredTotalLabel all measured = %q", got)
	}
}

// TestStepSelfReportNudge pins the step-edge advisory (sty_56aae77a AC3): it
// names the previous status on a real change, and is suppressed when a report
// already exists or inputs are empty (title-only / no-op sets never call it).
func TestStepSelfReportNudge(t *testing.T) {
	got := stepSelfReportNudge("sty_abc", "plan", false)
	if !strings.Contains(got, "step=plan") || !strings.Contains(got, "sty_abc") || !strings.Contains(got, "step-self-report") {
		t.Errorf("nudge should name story and previous step: %q", got)
	}
	if !strings.HasPrefix(got, "note: record the step you just finished") {
		t.Errorf("nudge prefix: %q", got)
	}
	if stepSelfReportNudge("sty_abc", "plan", true) != "" {
		t.Error("already-reported must suppress the nudge")
	}
	if stepSelfReportNudge("sty_abc", "", false) != "" {
		t.Error("empty prev status must suppress")
	}
	if stepSelfReportNudge("", "plan", false) != "" {
		t.Error("empty story id must suppress")
	}
}

// TestAttachBody covers `story attach`'s body resolution (sty_97c53d72): --file
// reads the body from a file, --body passes through, and a missing file is a
// clear error (the --body/--file conflict is enforced by cobra's
// MarkFlagsMutuallyExclusive on the command).
func TestAttachBody(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "summary.md")
	if err := os.WriteFile(p, []byte("# from file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, err := attachBody("inline", ""); err != nil || got != "inline" {
		t.Errorf("body passthrough = (%q, %v), want (inline, nil)", got, err)
	}
	if got, err := attachBody("", p); err != nil || got != "# from file\n" {
		t.Errorf("file read = (%q, %v), want file content", got, err)
	}
	if _, err := attachBody("", filepath.Join(dir, "absent.md")); err == nil || !strings.Contains(err.Error(), "read --file") {
		t.Errorf("missing file should error with path context, got %v", err)
	}
}
