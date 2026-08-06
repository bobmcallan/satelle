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
// pre-existing toml may lack after a binary upgrade. Only keys seeded ACTIVE in
// scaffoldToml belong here — commented-by-design keys (tags.vocabulary, web_port,
// log_level, retention, substrate_roots, [sync], [hosted], [vars]) stay free of
// this table: their absence means the binary default applies, as intended.
//
// Audit of every scaffold seed (why only two entries):
//
//	[gate] edit_exempt_paths  — seeded active; absence is LOUD (every edit denied)
//	[review] gate_create      — seeded active; absence is SILENT (stories file
//	                            unreviewed) — the reach gap this table + create
//	                            notice close
//	[tags.vocabulary]         — commented by design; free-form tags are the default
//	all other keys            — commented; binary default applies
//
// Enabling a missing gate is never auto-migrated: appending a managed value to a
// list the operator already opted into is convergence, but flipping a gate from
// off to on changes behaviour mid-session and starts costing agent runs — that
// stays an explicit operator action (create-path notice + this advisory only).
var scaffoldConfigDefaults = []scaffoldConfigDefault{
	{
		Section: "gate",
		Key:     "edit_exempt_paths",
		Block: `[gate]
edit_exempt_paths = ` + defaultEditExemptTOML(),
		Why: "OOTB scaffold exempts .satelle/ (authored substrate) and the footprint the binary itself deploys (.gitignore managed block, harness scaffolds) from the engaged-story edit gate",
	},
	{
		// Seeded active when create-gating shipped. Do NOT auto-enable via migrate:
		// flipping gate_create under an operator is a behaviour change mid-session
		// (starts costing agent runs). Report + one-line fix only; enable is explicit.
		Section: "review",
		Key:     "gate_create",
		Block: `[review]
gate_create = true`,
		Why: "OOTB scaffold enables the create gate so misclassification is caught at create (structure + workflow create_review skill)",
	},
}

// managedEditExemptEntries are the paths satelle's OWN machinery writes into a
// repo without an engaged story — the binary's managed footprint outside the
// data dir. Holding them against the session is the binary blaming the operator
// for its own write, so init seeds them and migrate appends any that a
// pre-existing non-empty list lacks.
//
// The list names ONLY what the binary deploys: the managed .gitignore block
// (init/migrate) and the harness hook scaffolds init writes under .claude/,
// .grok/ and .codex/ (ensureProcessHooks / ensureLazySessionHarness). A repo
// that wants more exempt adds its own prefix; product paths stay gated. Keep it
// in step with the embedded satelle-substrate-only-check allow list — one answer
// about the footprint, not two (pinned by a test).
var managedEditExemptEntries = []string{".gitignore", ".claude/", ".grok/", ".codex/"}

// defaultEditExemptPaths is the list init seeds: this repo's authored substrate
// plus the managed footprint above.
func defaultEditExemptPaths() []string {
	return append([]string{".satelle/"}, managedEditExemptEntries...)
}

// defaultEditExemptTOML renders defaultEditExemptPaths as a TOML array literal,
// so the seeded line and the remediation init prints cannot drift apart.
func defaultEditExemptTOML() string {
	quoted := make([]string, 0, len(managedEditExemptEntries)+1)
	for _, p := range defaultEditExemptPaths() {
		quoted = append(quoted, `"`+p+`"`)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
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

	// Config (advisory): missing scaffold-seeded defaults, or a present
	// edit_exempt_paths that lacks the managed .gitignore entry. Init reports;
	// migrate converges the list value only. An empty list is deliberate opt-out
	// and is not flagged. Presence uses configKeyDefined (committed + local
	// overlay) so semantics match the create-path notice.
	tomlPath := filepath.Join(dataDir, config.ConfigName)
	var content string
	if raw, err := os.ReadFile(tomlPath); err == nil {
		content = string(raw)
	}
	for _, d := range scaffoldConfigDefaults {
		if !configKeyDefined(dataDir, d.Section, d.Key) {
			// Only report when a satelle.toml exists (or the local overlay does):
			// a completely uninitialised tree has no config surface to fix.
			if content == "" {
				if _, err := os.Stat(filepath.Join(dataDir, config.LocalConfigName)); err != nil {
					continue
				}
			}
			out = append(out, substrateDefect{
				File:   config.DefaultDataDir + "/" + config.ConfigName,
				Defect: fmt.Sprintf("missing scaffold-seeded [%s] %s — %s", d.Section, d.Key, d.Why),
				Fix:    "add this block to " + config.DefaultDataDir + "/" + config.ConfigName + ":\n" + d.Block,
				Fatal:  false,
			})
			continue
		}
		if d.Section == "gate" && d.Key == "edit_exempt_paths" && content != "" {
			if missing := editExemptMissingManaged(content); len(missing) > 0 {
				list := `"` + strings.Join(missing, `", "`) + `"`
				out = append(out, substrateDefect{
					File:   config.DefaultDataDir + "/" + config.ConfigName,
					Defect: "edit_exempt_paths lacks " + list + " — satelle-deployed output (managed .gitignore block, harness scaffolds) trips the engaged-story gate without it",
					Fix:    "run `satelle migrate --yes` to append " + list + " without clobbering operator additions (or add them by hand)",
					Fatal:  false,
				})
			}
		}
	}

	_ = repoRoot // reserved for future absolute-path reports; keep signature stable
	return out
}

// editExemptMissingManaged returns the managedEditExemptEntries absent from
// content's non-empty [gate] edit_exempt_paths, in managed order. An absent key
// or an explicitly empty list returns nil (missing-key is handled separately;
// empty is a deliberate opt-out).
func editExemptMissingManaged(content string) []string {
	if !config.HasKey(content, "gate", "edit_exempt_paths") {
		return nil
	}
	items := config.ListStringValues(content, "gate", "edit_exempt_paths")
	if len(items) == 0 {
		return nil
	}
	var missing []string
	for _, want := range managedEditExemptEntries {
		if !config.ListValueContains(content, "gate", "edit_exempt_paths", want) {
			missing = append(missing, want)
		}
	}
	return missing
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
		// init re-stamps identical stampless bodies; restore is exempt from the
		// deployed.version gate so heal commands can run.
		return "re-run `satelle init` — it re-stamps embedded-owned files whose body matches the default (or `satelle restore --yes` to re-materialise)"
	case strings.Contains(defect, "unknown tag axis"):
		return "edit the principle: remove invented tag axes (kind:*, epic:*, …); on principles only type: and principles: are legal"
	case strings.Contains(defect, "illegal residency") || strings.Contains(defect, "residency-ish"):
		// principles:always is auto-healed by init; other illegal residency tags
		// still need a hand edit.
		if strings.Contains(defect, "principles:always") {
			return "re-run `satelle init` — it migrates principles:always → principles:session"
		}
		return "edit the principle: use only principles:session for system residency, or drop the principles:* tag for on-demand"
	case strings.Contains(defect, "scope:"):
		return "re-run `satelle init` — it removes the inert scope: key on principles"
	case strings.Contains(defect, "ceiling"):
		return "trim a principles:session principle or the constitution so SessionStart stays under the byte ceiling"
	default:
		return "edit the reported file under .satelle/ (local agent; no remote sync)"
	}
}
