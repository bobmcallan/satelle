// init_analysis.go — substrate analysis phase of `satelle init` (init-substrate-analysis).
//
// After mechanical seed/heal, init reports residual defects so the LOCAL agent
// can act: placement/residency/tag-axis problems (fatal), an unedited scaffold
// constitution (advisory), and missing scaffold-seeded config keys (advisory).
// Each defect names file + defect + fix channel. Repo-agnostic: driven by
// EmbeddedDefaults, sessionTag, whitelist, scaffold markers — no this-repo ids.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
)

// scaffoldConstitutionMarker is a stable substring of scaffoldConstitution used
// to detect an un-authored order-zero placeholder (AC3). A test asserts it
// remains a substring of the scaffold body.
const scaffoldConstitutionMarker = "This is your repo's order-zero context"

// substrateDefect is one analysis finding.
type substrateDefect struct {
	File   string // path relative to repo root (e.g. .satelle/principles/x.md)
	Defect string // what is wrong
	Fix    string // how the local agent fixes it (channel + action)
	Fatal  bool   // true → init fails closed; false → WARN only
}

// scaffoldConfigDefault is one scaffold-seeded config key init reports when
// absent from an existing satelle.toml (AC6 — report exact block, do not auto-
// migrate; absence may be a deliberate operator choice).
type scaffoldConfigDefault struct {
	Section string
	Key     string
	Block   string // exact TOML block to add
	Why     string // short rationale for the report line
}

// scaffoldConfigDefaults is the curated list of scaffold-seeded keys that a
// pre-existing toml may lack after a binary upgrade. Keep this list the
// unambiguous OOTB intents (currently [gate] edit_exempt_paths).
var scaffoldConfigDefaults = []scaffoldConfigDefault{
	{
		Section: "gate",
		Key:     "edit_exempt_paths",
		Block: `[gate]
edit_exempt_paths = [".satelle/"]`,
		Why: "OOTB scaffold exempts .satelle/ from the engaged-story edit gate so authored substrate stays editable without a release",
	},
}

// analyzeSubstrate gathers placement + constitution-author + config-reconciliation
// defects for dataDir/repoRoot. Pure over the filesystem; store-free.
func analyzeSubstrate(dataDir, repoRoot string) []substrateDefect {
	var out []substrateDefect

	// Placement (fatal): stamps, residency, scope, tag axes, ceiling.
	constitution := readConstitution(filepath.Join(dataDir, "constitution.md"))
	for _, p := range auditPlacement(dataDir, config.EmbeddedDefaults(), constitution) {
		file, defect := splitPlacementProblem(p)
		out = append(out, substrateDefect{
			File:   file,
			Defect: defect,
			Fix:    placementFixChannel(defect),
			Fatal:  true,
		})
	}

	// Constitution (advisory): unedited scaffold placeholder.
	constPath := filepath.Join(dataDir, "constitution.md")
	if body, err := os.ReadFile(constPath); err == nil {
		if strings.Contains(string(body), scaffoldConstitutionMarker) {
			out = append(out, substrateDefect{
				File:   config.DefaultDataDir + "/constitution.md",
				Defect: "un-authored order-zero context (still the init scaffold placeholder)",
				Fix:    "author .satelle/constitution.md for THIS repo — replace the placeholder with what an agent must know here",
				Fatal:  false,
			})
		}
	}

	// Config (advisory): missing scaffold-seeded defaults.
	tomlPath := filepath.Join(dataDir, config.ConfigName)
	if raw, err := os.ReadFile(tomlPath); err == nil {
		content := string(raw)
		for _, d := range scaffoldConfigDefaults {
			if config.HasKey(content, d.Section, d.Key) {
				continue
			}
			out = append(out, substrateDefect{
				File:   config.DefaultDataDir + "/" + config.ConfigName,
				Defect: fmt.Sprintf("missing scaffold-seeded [%s] %s — %s", d.Section, d.Key, d.Why),
				Fix:    "add this block to " + config.DefaultDataDir + "/" + config.ConfigName + ":\n" + d.Block,
				Fatal:  false,
			})
		}
	}

	_ = repoRoot // reserved for future absolute-path reports; keep signature stable
	return out
}

// reportSubstrateAnalysis prints defects in AC4 form and returns the fatal count.
// Advisory defects are WARN; fatal defects are FAIL. Always local — no remote.
func reportSubstrateAnalysis(out io.Writer, defects []substrateDefect) (fatal int) {
	for _, d := range defects {
		line := fmt.Sprintf("%s — %s → fix: %s", d.File, d.Defect, d.Fix)
		if d.Fatal {
			fatal++
			fmt.Fprintf(out, "FAIL  %s\n", line)
		} else {
			fmt.Fprintf(out, "WARN  %s\n", line)
		}
	}
	return fatal
}

// splitPlacementProblem turns an auditPlacement string of the form
// "principles/foo: message" into (file, defect).
func splitPlacementProblem(p string) (file, defect string) {
	// Prefer "kind/name: rest"
	if i := strings.Index(p, ": "); i > 0 {
		rel := p[:i]
		// Prefix with .satelle/ when it looks like a kind path.
		if strings.Contains(rel, "/") && !strings.HasPrefix(rel, config.DefaultDataDir) {
			file = config.DefaultDataDir + "/" + rel
			if !strings.HasSuffix(file, ".md") && !strings.Contains(file, " ") {
				// principles/foo → principles/foo.md when it's a single name.
				parts := strings.SplitN(rel, "/", 2)
				if len(parts) == 2 && !strings.Contains(parts[1], "/") {
					file = config.DefaultDataDir + "/" + parts[0] + "/" + parts[1] + ".md"
				}
			}
		} else {
			file = rel
		}
		defect = p[i+2:]
		return file, defect
	}
	return config.DefaultDataDir, p
}

// placementFixChannel maps a placement defect message to a local fix channel.
// Order matters: "unknown tag axis" messages also mention "principles:" in the
// legal-axis parenthetical — match that before residency.
func placementFixChannel(defect string) string {
	switch {
	case strings.Contains(defect, "embedded_sha"):
		// sty_a9ec33e7: init re-stamps identical stampless bodies; restore is
		// exempt from the deployed.version gate so it can heal too.
		return "re-run `satelle init` — it re-stamps embedded-owned files whose body matches the default (or `satelle restore --yes` to re-materialise)"
	case strings.Contains(defect, "unknown tag axis"):
		return "edit the principle: remove invented tag axes (kind:*, epic:*, …); on principles only type: and principles: are legal"
	case strings.Contains(defect, "illegal residency") || strings.Contains(defect, "residency-ish"):
		return "edit the principle: use only principles:session for system residency, or drop the principles:* tag for on-demand"
	case strings.Contains(defect, "scope:"):
		return "edit the principle: remove the inert scope: key — residency is the principles:session tag alone"
	case strings.Contains(defect, "ceiling"):
		return "trim a principles:session principle or the constitution so SessionStart stays under the byte ceiling"
	default:
		return "edit the reported file under .satelle/ (local agent; no remote sync)"
	}
}
