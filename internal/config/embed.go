package config

// Embedded canonical substrate. This file is the SINGLE source of the default
// workflow / principle / skill markdown the satelle binary ships — Go is the
// load layer, the substance is the .md under substrate/. Mirrors the satellites
// config/embed.go pattern. A repo layers its own authored markdown under
// .satelle/<kind>/ ON TOP of these defaults (a same-named file overrides the
// embedded default); it never edits this source. See the satelle-repo-agnostic
// principle and the constitution: the binary embeds the required structure PLUS
// the canonical DEFAULT SOLUTION (the generic project/parent/task-execution
// workflows and the gate skills they reference) that init seeds as EDITABLE repo
// substrate — the lifecycle is still configuration, never a Go branch; anything
// beyond the generic defaults stays in a repo's own substrate.

import (
	"embed"
	"io/fs"
	"path"
	"strings"
)

// substrateFS holds the embedded default artifacts, filed by kind under
// substrate/<kind>/<name>.md (e.g. substrate/workflows/satelle-baseline-workflow.md).
//
//go:embed substrate
var substrateFS embed.FS

// OperatingPrinciple is the one tight operating principle — injected at session
// start (it ships carrying the principles:session residency marker) and
// guaranteed into every reviewer's context, so the agent (and the reviewer) is
// driven to the result. Residency is otherwise authored via the principles:session
// marker (see internal/cli hook + internal/reviewer): a principle is session
// because it is tagged, or on-demand (the default) because it is not — never
// auto-injected (sty_53a4233c). Its content is overridable by a repo
// (.satelle/principles).
const OperatingPrinciple = "satelle-agent-goals"

// EmbeddedDefault is one canonical default artifact carried in the binary.
type EmbeddedDefault struct {
	Kind string // workflows | principles | skills (the substrate subdir)
	Name string // filename without the .md extension
	Body string // raw markdown
}

// EmbeddedDefaults returns every embedded default artifact, across all kinds.
// These are the canonical SEED + reference: init materialises them onto disk, the
// doc index resolves them by name as a Get fallback (e.g. the gating baseline), and
// validate compares against them — but they are NOT overlaid into List/Count, so an
// embedded default is never enumerated as a project doc (sty_94da9ac9).
func EmbeddedDefaults() []EmbeddedDefault {
	var out []EmbeddedDefault
	_ = fs.WalkDir(substrateFS, "substrate", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(path.Ext(p), ".md") {
			return nil
		}
		rel := strings.TrimPrefix(p, "substrate/")
		kind, _, ok := strings.Cut(rel, "/")
		if !ok {
			return nil // a file directly under substrate/ has no kind — skip
		}
		body, rerr := substrateFS.ReadFile(p)
		if rerr != nil {
			return nil
		}
		out = append(out, EmbeddedDefault{
			Kind: kind,
			Name: strings.TrimSuffix(path.Base(p), path.Ext(p)),
			Body: string(body),
		})
		return nil
	})
	return out
}
