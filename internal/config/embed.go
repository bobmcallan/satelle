package config

// Embedded canonical substrate. This file is the SINGLE source of the default
// workflow / principle / skill markdown the satelle binary ships — Go is the
// load layer, the substance is the .md under substrate/. Mirrors the satellites
// config/embed.go pattern. A repo layers its own authored markdown under
// .satelle/<kind>/ ON TOP of these defaults (a same-named file overrides the
// embedded default); it never edits this source. See the satelle-repo-agnostic
// principle and the constitution: the binary embeds the required structure PLUS
// the canonical DEFAULT SOLUTION (the generic base/parent/task-execution
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
// substrate/<kind>/<name>.md (e.g. substrate/workflows/done.toml).
//
//go:embed substrate
var substrateFS embed.FS

// OperatingPrinciple is the one tight operating principle — injected at session
// start (it ships carrying the principles:session residency marker) and
// guaranteed into every reviewer's context, so the agent (and the reviewer) is
// driven to the result. Residency is otherwise authored via the principles:session
// marker (see internal/cli hook + internal/agentstep): a principle is session
// because it is tagged, or on-demand (the default) because it is not — never
// auto-injected (sty_53a4233c). Its content is overridable by a repo
// (.satelle/principles).
const OperatingPrinciple = "satelle-agent-goals"

// EmbeddedDefault is one canonical default artifact carried in the binary.
type EmbeddedDefault struct {
	Kind string // workflows | principles | skills | tasks (the substrate subdir)
	Name string // filename without its extension
	Body string // raw file text
	// Ext is the source extension (".md" or ".toml"), carried so a synthesised
	// provenance path and an on-disk materialisation name the real file rather
	// than assuming markdown (sty_81bb0dde).
	Ext string
}

// RelPath is the kind-relative slash path this default materialises to —
// `workflows/done.toml`, `skills/x.md`. Every caller that lands, prunes,
// restores or looks for an embedded default on disk asks for it here rather than
// appending ".md" itself: a hardcoded extension makes a TOML default invisible
// to that caller, and the failure is silent (a file never found is a file never
// converged).
func (d EmbeddedDefault) RelPath() string {
	ext := d.Ext
	if ext == "" {
		ext = ".md"
	}
	return d.Kind + "/" + d.Name + ext
}

// EmbeddedExt returns the source extension of the named embedded default, or
// ".md" when there is none — the document form, and the safe assumption for a
// name the binary does not ship.
func EmbeddedExt(kind, name string) string {
	for _, d := range EmbeddedDefaults() {
		if d.Kind == kind && d.Name == name {
			return d.RelPath()[len(d.Kind)+1+len(d.Name):]
		}
	}
	return ".md"
}

// substrateExts are the file types an embedded default may be authored in.
// Documents are markdown; the route source is TOML, because it is records with
// terse commentary and uses no markdown feature (sty_81bb0dde).
var substrateExts = map[string]bool{".md": true, ".toml": true}

// nonSubstrateKinds are subdirectories of substrate/ that the binary LOADS but
// does not layer: machine configuration reached through its own accessor, with
// no frontmatter, no headline and nothing to render.
//
// `config/` holds categories.toml, which predates the TOML route source and was
// excluded for free while this walk was .md-only. Widening the walk swept it in
// as a default doc of kind "config" — it reached the doc index and showed up in
// `satelle process` output (sty_81bb0dde). This is the same carve-out
// docindex.Indexable makes for the repo's agents.toml, and for the same reason:
// a file being TOML does not make it substrate.
var nonSubstrateKinds = map[string]bool{"config": true}

// EmbeddedDefaults returns every embedded default artifact, across all kinds.
// These are the canonical SEED + reference: init materialises them onto disk, the
// doc index resolves them by name as a Get fallback (e.g. the gating baseline), and
// validate compares against them — but they are NOT overlaid into List/Count, so an
// embedded default is never enumerated as a project doc (sty_94da9ac9).
func EmbeddedDefaults() []EmbeddedDefault {
	var out []EmbeddedDefault
	_ = fs.WalkDir(substrateFS, "substrate", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !substrateExts[strings.ToLower(path.Ext(p))] {
			return nil
		}
		rel := strings.TrimPrefix(p, "substrate/")
		kind, _, ok := strings.Cut(rel, "/")
		if !ok || nonSubstrateKinds[kind] {
			return nil // no kind, or a kind the binary loads rather than layers
		}
		body, rerr := substrateFS.ReadFile(p)
		if rerr != nil {
			return nil
		}
		out = append(out, EmbeddedDefault{
			Kind: kind,
			Name: strings.TrimSuffix(path.Base(p), path.Ext(p)),
			Body: string(body),
			Ext:  strings.ToLower(path.Ext(p)),
		})
		return nil
	})
	return out
}
