package config

// Embedded canonical substrate. This file is the SINGLE source of the default
// workflow / principle / skill markdown the satelle binary ships — Go is the
// load layer, the substance is the .md under substrate/. Mirrors the satellites
// config/embed.go pattern. A repo layers its own authored markdown under
// .satelle/<kind>/ ON TOP of these defaults (a same-named file overrides the
// embedded default); it never edits this source. See the satelle-repo-agnostic
// principle: only the required structure is embedded, everything opinionated
// stays in a repo's substrate.

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

// OperatingPrinciple is the single always-resident principle — the one tight
// operating principle injected at session start and into every reviewer's
// context, so the agent (and the reviewer) is driven to the result. Every other
// principle is resolvable substrate read on demand, never auto-injected
// (sty_53a4233c). Its content is overridable by a repo (.satelle/principles),
// but the resident set is always exactly this one name.
const OperatingPrinciple = "satelle-agent-goals"

// EmbeddedDefault is one canonical default artifact carried in the binary.
type EmbeddedDefault struct {
	Kind string // workflows | principles | skills (the substrate subdir)
	Name string // filename without the .md extension
	Body string // raw markdown
}

// EmbeddedDefaults returns every embedded default artifact, across all kinds.
// The caller (the doc index) overlays these UNDER the on-disk .satelle authored
// markdown, so a repo file with the same (kind, name) overrides its default.
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
