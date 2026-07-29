package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
)

// ScaffoldFinding is one deployed-vs-canonical harness scaffold mismatch
// (sty_ac25b787). Paths are repo-relative; no absolute machine paths.
type ScaffoldFinding struct {
	Path   string // repo-relative path of the drifted artifact
	Kind   string // missing | content | command
	Detail string
}

// DetectScaffoldDrift compares deployed harness hook configs and
// .satelle/hooks scripts against this binary's canonical wrappers. Empty when
// no harness scaffolding is deployed or everything matches. Repo-agnostic:
// only well-known relative paths (settings + script names) are examined.
func DetectScaffoldDrift(repoRoot string) []ScaffoldFinding {
	if strings.TrimSpace(repoRoot) == "" {
		return nil
	}
	var findings []ScaffoldFinding
	// Settings-driven checks (only when the harness file exists).
	findings = append(findings, driftHarnessSettings(repoRoot, filepath.Join(repoRoot, ".claude", "settings.json"), "claude", ".claude/settings.json")...)
	findings = append(findings, driftHarnessSettings(repoRoot, filepath.Join(repoRoot, filepath.FromSlash(grokHooksRel)), "grok", grokHooksRel)...)
	findings = append(findings, driftHarnessSettings(repoRoot, filepath.Join(repoRoot, filepath.FromSlash(codexHooksRel)), "codex", codexHooksRel)...)
	// Single parameterized script must match canonical when present.
	rel := satelleHookScriptRel
	path := filepath.Join(repoRoot, filepath.FromSlash(rel))
	if b, err := os.ReadFile(path); err == nil {
		want := parameterizedHookScriptBody()
		if string(b) != want {
			findings = append(findings, ScaffoldFinding{
				Path:   rel,
				Kind:   "content",
				Detail: fmt.Sprintf("differs from binary canonical wrapper (sha %s vs want %s)", shortSHA(b), shortSHA([]byte(want))),
			})
		}
	}
	// Legacy per-harness scripts on disk are drift (should have been retired).
	for _, harness := range []string{"claude", "grok", "kimi"} {
		for _, sub := range []string{"gate", "commitgate"} {
			lrel := legacyHookScriptRel(harness, sub)
			if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(lrel))); err == nil {
				findings = append(findings, ScaffoldFinding{
					Path:   lrel,
					Kind:   "content",
					Detail: "legacy per-harness wrapper present — run satelle init to retire (use " + satelleHookScriptRel + ")",
				})
			}
		}
	}
	return dedupeScaffoldFindings(findings)
}

func driftHarnessSettings(repoRoot, absPath, harness, relPath string) []ScaffoldFinding {
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return nil // harness not deployed — skip
	}
	cmds := preToolUseHookCommands(raw)
	if len(cmds) == 0 {
		return nil
	}
	var findings []ScaffoldFinding
	seenSub := map[string]bool{}
	for _, cmd := range cmds {
		sub := legacyHookSub(cmd)
		if sub == "" {
			continue
		}
		seenSub[sub] = true
		wantCmd := renderHookCommand(repoRoot, harness, sub)
		if strings.TrimSpace(cmd) != wantCmd {
			findings = append(findings, ScaffoldFinding{
				Path:   relPath,
				Kind:   "command",
				Detail: fmt.Sprintf("PreToolUse %s command is not the canonical script form (want %q)", sub, wantCmd),
			})
		}
		// Parameterized script must exist and match when this harness is deployed.
		rel := satelleHookScriptRel
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		b, err := os.ReadFile(path)
		if err != nil {
			findings = append(findings, ScaffoldFinding{
				Path:   rel,
				Kind:   "missing",
				Detail: "canonical wrapper script missing — run satelle init",
			})
			continue
		}
		want := parameterizedHookScriptBody()
		if string(b) != want {
			findings = append(findings, ScaffoldFinding{
				Path:   rel,
				Kind:   "content",
				Detail: fmt.Sprintf("differs from binary canonical wrapper (sha %s vs want %s)", shortSHA(b), shortSHA([]byte(want))),
			})
		}
	}
	_ = seenSub
	return findings
}

// preToolUseHookCommands extracts command strings from PreToolUse hooks in a
// settings JSON document. Parse failure yields nil (no confident findings).
func preToolUseHookCommands(raw []byte) []string {
	var s struct {
		Hooks struct {
			PreToolUse []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	var out []string
	for _, e := range s.Hooks.PreToolUse {
		for _, h := range e.Hooks {
			if c := strings.TrimSpace(h.Command); c != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

func shortSHA(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

func dedupeScaffoldFindings(in []ScaffoldFinding) []ScaffoldFinding {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []ScaffoldFinding
	for _, f := range in {
		key := f.Path + "\x00" + f.Kind + "\x00" + f.Detail
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

// formatScaffoldDriftWarning is the SessionStart / status human block.
// heal command is always `satelle init`.
func formatScaffoldDriftWarning(findings []ScaffoldFinding) string {
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "⚠️ satelle: deployed harness scaffolding drifts from this binary — run `satelle init` to heal (%d item(s)):\n", len(findings))
	for _, f := range findings {
		fmt.Fprintf(&b, "  - %s [%s]: %s\n", f.Path, f.Kind, f.Detail)
	}
	return strings.TrimRight(b.String(), "\n")
}

// refuseScaffoldDrift fails closed when deployed scaffolding differs from the
// binary's canonical wrappers (sty_ac25b787 hash mechanism — does not reuse the
// changelog ### Breaking semantic). Dev builds and uninitialised repos skip.
// Heal path: satelle init.
func refuseScaffoldDrift(repoRoot string) error {
	binVer := strings.TrimSpace(buildinfo.Resolve().Version)
	if isDevVersion(binVer) {
		return nil
	}
	dataDir := filepath.Join(repoRoot, config.DefaultDataDir)
	if st, err := os.Stat(dataDir); err != nil || !st.IsDir() {
		return nil
	}
	findings := DetectScaffoldDrift(repoRoot)
	if len(findings) == 0 {
		return nil
	}
	return fmt.Errorf("%s", formatScaffoldDriftRefuse(binVer, findings))
}

func formatScaffoldDriftRefuse(binVer string, findings []ScaffoldFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "satelle: binary %s deployed harness scaffolding is stale (%d item(s)) — run `satelle init` to heal\n", binVer, len(findings))
	for _, f := range findings {
		fmt.Fprintf(&b, "  - %s [%s]: %s\n", f.Path, f.Kind, f.Detail)
	}
	return strings.TrimRight(b.String(), "\n")
}
