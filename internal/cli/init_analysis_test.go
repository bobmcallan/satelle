package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

func TestScaffoldConstitutionMarkerIsSubstring(t *testing.T) {
	if !strings.Contains(scaffoldConstitution, scaffoldConstitutionMarker) {
		t.Fatalf("scaffoldConstitutionMarker %q is not a substring of scaffoldConstitution — AC3 would silently break", scaffoldConstitutionMarker)
	}
}

func TestAnalyzeSubstrateUneditedConstitution(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "constitution.md"), []byte(scaffoldConstitution), 0o644); err != nil {
		t.Fatal(err)
	}
	// Empty principles dir so placement is quiet.
	_ = os.MkdirAll(filepath.Join(dataDir, "principles"), 0o755)
	// Minimal toml with full seeded edit_exempt so config check is quiet.
	toml := `[gate]
edit_exempt_paths = [".satelle/", ".gitignore"]
`
	if err := os.WriteFile(filepath.Join(dataDir, config.ConfigName), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	defs := analyzeSubstrate(dataDir, filepath.Dir(dataDir))
	var sawConst bool
	fatal := 0
	for _, d := range defs {
		if d.Fatal {
			fatal++
		}
		if strings.Contains(d.Defect, "un-authored order-zero") {
			sawConst = true
			if d.Fatal {
				t.Error("constitution advisory must not be fatal")
			}
		}
	}
	if !sawConst {
		t.Fatalf("want unedited constitution WARN, got %+v", defs)
	}
	if fatal != 0 {
		t.Fatalf("fresh scaffold with only constitution warn should have 0 fatals, got %d: %+v", fatal, defs)
	}
}

func TestAnalyzeSubstrateConfigMissing(t *testing.T) {
	dataDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dataDir, "principles"), 0o755)
	// Authored constitution (no marker).
	if err := os.WriteFile(filepath.Join(dataDir, "constitution.md"), []byte("# Real constitution\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// gate table present but no edit_exempt_paths.
	toml := `[gate]
`
	if err := os.WriteFile(filepath.Join(dataDir, config.ConfigName), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	defs := analyzeSubstrate(dataDir, filepath.Dir(dataDir))
	var saw bool
	for _, d := range defs {
		if strings.Contains(d.Defect, "edit_exempt_paths") {
			saw = true
			if d.Fatal {
				t.Error("config drift must be advisory")
			}
			if !strings.Contains(d.Fix, `edit_exempt_paths = [".satelle/", ".gitignore"]`) {
				t.Errorf("fix must include exact block, got %q", d.Fix)
			}
		}
	}
	if !saw {
		t.Fatalf("want config missing edit_exempt_paths, got %+v", defs)
	}
}

// TestAnalyzeSubstrateEditExemptMissingGitignore: a non-empty list that predates
// the .gitignore default yields an advisory WARN pointing at migrate; a list
// that already carries it is quiet; an empty list is a deliberate opt-out.
func TestAnalyzeSubstrateEditExemptMissingGitignore(t *testing.T) {
	cases := []struct {
		name    string
		toml    string
		wantDef bool
	}{
		{
			name:    "predates default",
			toml:    "[gate]\nedit_exempt_paths = [\".satelle/\", \".claude/\"]\n",
			wantDef: true,
		},
		{
			name:    "already current",
			toml:    "[gate]\nedit_exempt_paths = [\".satelle/\", \".gitignore\"]\n",
			wantDef: false,
		},
		{
			name:    "empty opt-out",
			toml:    "[gate]\nedit_exempt_paths = []\n",
			wantDef: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			_ = os.MkdirAll(filepath.Join(dataDir, "principles"), 0o755)
			if err := os.WriteFile(filepath.Join(dataDir, "constitution.md"), []byte("# Real\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dataDir, config.ConfigName), []byte(tc.toml), 0o644); err != nil {
				t.Fatal(err)
			}
			defs := analyzeSubstrate(dataDir, filepath.Dir(dataDir))
			var saw bool
			for _, d := range defs {
				if strings.Contains(d.Defect, `.gitignore`) && strings.Contains(d.Defect, "edit_exempt") {
					saw = true
					if d.Fatal {
						t.Error("missing-.gitignore defect must be advisory")
					}
					if !strings.Contains(d.Fix, "migrate") {
						t.Errorf("fix should name migrate, got %q", d.Fix)
					}
				}
			}
			if saw != tc.wantDef {
				t.Fatalf("wantDef=%v saw=%v defects=%+v", tc.wantDef, saw, defs)
			}
		})
	}
}

func TestReportSubstrateAnalysisFormat(t *testing.T) {
	var buf bytes.Buffer
	n := reportSubstrateAnalysis(&buf, []substrateDefect{
		{File: ".satelle/principles/x.md", Defect: "illegal tag", Fix: "edit it", Fatal: true},
		{File: ".satelle/constitution.md", Defect: "unedited", Fix: "author it", Fatal: false},
	})
	if n != 1 {
		t.Fatalf("fatal count = %d", n)
	}
	s := buf.String()
	if !strings.Contains(s, "FAIL  .satelle/principles/x.md — illegal tag → fix: edit it") {
		t.Errorf("fatal format: %q", s)
	}
	if !strings.Contains(s, "WARN  .satelle/constitution.md — unedited → fix: author it") {
		t.Errorf("warn format: %q", s)
	}
}

func TestInitAnalysisRepoAgnostic(t *testing.T) {
	// Guard: analysis source must not embed this-repo story/task ids.
	body, err := os.ReadFile("init_analysis.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, ban := range []string{"sty_", "tsk_"} {
		if strings.Contains(s, ban) {
			t.Errorf("init_analysis.go must not contain %q (repo-agnostic)", ban)
		}
	}
}

func TestPlacementFixChannelUnknownAxis(t *testing.T) {
	// Unknown-axis defects must not be mis-routed to the residency fix channel
	// just because the message parenthetical mentions "principles:".
	defect := `unknown tag axis "kind" on tag "kind:epic" (legal axes on principles: type, principles)`
	fix := placementFixChannel(defect)
	if !strings.Contains(fix, "kind:*") && !strings.Contains(fix, "invented") {
		t.Fatalf("unknown-axis fix should mention invented axes/kind:*, got %q", fix)
	}
	if strings.Contains(fix, "principles:session") {
		t.Fatalf("unknown-axis must not get residency fix: %q", fix)
	}
	// Residency still maps correctly.
	res := placementFixChannel(`illegal residency-ish tag "principles:global"`)
	if !strings.Contains(res, "principles:session") {
		t.Fatalf("residency fix: %q", res)
	}
}

func TestPlacementFixChannelEmbeddedSHA(t *testing.T) {
	got := placementFixChannel("file missing embedded_sha stamp")
	if !strings.Contains(got, "satelle init") || !strings.Contains(got, "restore") {
		t.Fatalf("advice must name working heal paths, got %q", got)
	}
}
