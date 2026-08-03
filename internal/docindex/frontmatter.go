package docindex

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Indexable reports whether a path is an authored substrate file the index
// ingests. Markdown is the DOCUMENT form; TOML is the form for a file that is
// records rather than prose — today the route source (sty_81bb0dde).
//
// This is one predicate rather than an `.md` literal at each walk, because the
// three walks that used to carry that literal independently are exactly how a
// format ends up half-supported.
func Indexable(path string) bool {
	// The repo AGENTS LAYER lives in workflows/ beside the two route halves it
	// binds (sty_10f732ed), and it is machine configuration, not a document: it
	// has no frontmatter, no headline and nothing to render. Widening the walk to
	// .toml for the route source would otherwise sweep it in as a workflow doc —
	// it did, on the first reindex after the conversion (sty_81bb0dde).
	//
	// Named here rather than at each walk for the same reason the extension test
	// is: three walks with three private opinions is how a format ends up
	// half-supported.
	if strings.EqualFold(filepath.Base(path), "agents.toml") {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".toml":
		return true
	}
	return false
}

// AuthoredExt is the extension a NEW authored doc of the given kind is written
// with. Prose kinds are markdown; the workflows kind is TOML, because the only
// doc it may hold is a route-source half and a route source is records rather
// than prose (sty_81bb0dde).
//
// Here rather than at each writer for the same reason Indexable is: the create
// path and the walk that reads it back must agree on the extension, and they
// disagreed the moment one of them was updated alone — `workflow create` wrote
// step.md, which the route resolver then refused as an unconverted repo.
func AuthoredExt(kind string) string {
	if kind == "workflows" {
		return ".toml"
	}
	return ".md"
}

// Frontmatter returns a document's frontmatter as `key: value` lines, whichever
// form the file is authored in (sty_81bb0dde).
//
// Two forms exist because two kinds of file do:
//
//   - a `---` YAML block, for a markdown DOCUMENT (skills, principles, tasks);
//   - a `[meta]` table, for a TOML file (the route source), which is records
//     with terse commentary and uses no markdown feature.
//
// FORMAT IS SNIFFED, not threaded. A body beginning with `---` is the markdown
// form; anything else is decoded as TOML and its `[meta]` table read. That is
// unambiguous because a file is wholly one format — there is no document that is
// half YAML-frontmatter and half TOML. Sniffing is what lets every existing
// caller keep its signature: `structure.Doc` receives a body and no format, and
// does not need one.
//
// It returns LINES rather than a map on purpose. Every consumer — structure's
// fmScalar/fmHas, wfhook's scalar and hooks-block reader, the embedded
// conformance helpers — already works against `key: value` lines. A map would
// force all of them to be rewritten for no gain.
//
// ok is false when there is no frontmatter at all: no `---` block, or TOML with
// no `[meta]` table. A body that is neither valid TOML nor markdown-with-
// frontmatter is also (nil, false) — reporting the decode error is the job of
// whoever parses the file for real, not of a frontmatter reader.
func Frontmatter(body string) (lines []string, ok bool) {
	if fm, _, found := splitYAMLFrontmatter(body); found {
		return fm, true
	}
	return tomlMetaLines(body)
}

// FrontmatterBody is Frontmatter plus the remainder after the frontmatter — the
// document's prose. For the TOML form there is no separable remainder, so the
// whole body is returned; a caller that renders `rest` as prose is looking at a
// markdown document by construction.
func FrontmatterBody(body string) (lines []string, rest string, ok bool) {
	if fm, tail, found := splitYAMLFrontmatter(body); found {
		return fm, tail, true
	}
	fm, found := tomlMetaLines(body)
	return fm, body, found
}

// splitYAMLFrontmatter reads the leading `---` … `---` block.
func splitYAMLFrontmatter(body string) (fm []string, rest string, ok bool) {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, body, false
	}
	for j := 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "---" {
			return lines[1:j], strings.Join(lines[j+1:], "\n"), true
		}
	}
	return nil, body, false
}

// tomlMetaLines decodes body as TOML and renders its `[meta]` table as
// `key: value` lines, so a TOML file's frontmatter reads identically to a
// markdown document's to every existing consumer.
func tomlMetaLines(body string) ([]string, bool) {
	var f struct {
		Meta map[string]any `toml:"meta"`
	}
	// Undecoded keys are expected and fine here: this reads ONLY the meta table
	// and is deliberately incurious about the rest of the file, which its real
	// parser validates strictly.
	if _, err := toml.Decode(body, &f); err != nil {
		return nil, false
	}
	if len(f.Meta) == 0 {
		return nil, false
	}
	keys := make([]string, 0, len(f.Meta))
	for k, v := range f.Meta {
		// A nested table or array-of-tables has no `key: value` rendering — the
		// markdown form spells those as an indented block, and flattening one with
		// fmt would emit Go map syntax into a frontmatter line. MetaTables reads
		// them instead.
		if isTabular(v) {
			continue
		}
		keys = append(keys, k)
	}
	// Sorted: map iteration order must never reach a caller that compares or
	// renders these lines.
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+": "+scalarString(f.Meta[k]))
	}
	return out, true
}

// MetaTables returns the array-of-tables declared under `[[meta.<key>]]`, each
// entry flattened to its scalar fields (sty_81bb0dde).
//
// It is the TOML counterpart of the markdown frontmatter's indented block list:
//
//	hooks:                        [[meta.hooks]]
//	  - operation: create_review  operation = "create_review"
//	    skill: some-review        skill     = "some-review"
//
// Generic on purpose — this package knows about frontmatter, not about hooks.
// The caller names the key and owns what the fields mean.
//
// ok is false when the body is not TOML or declares no such array, which a
// caller reads as "not declared this way" and not as an error.
func MetaTables(body, key string) ([]map[string]string, bool) {
	var f struct {
		Meta map[string]any `toml:"meta"`
	}
	if _, err := toml.Decode(body, &f); err != nil {
		return nil, false
	}
	raw, ok := f.Meta[key].([]map[string]any)
	if !ok {
		// BurntSushi yields []map[string]any for an array-of-tables, but a caller
		// may equally have written an inline array of inline tables.
		anySlice, isSlice := f.Meta[key].([]any)
		if !isSlice {
			return nil, false
		}
		for _, e := range anySlice {
			m, isMap := e.(map[string]any)
			if !isMap {
				return nil, false
			}
			raw = append(raw, m)
		}
	}
	if len(raw) == 0 {
		return nil, false
	}
	out := make([]map[string]string, 0, len(raw))
	for _, m := range raw {
		entry := make(map[string]string, len(m))
		for k, v := range m {
			entry[k] = scalarString(v)
		}
		out = append(out, entry)
	}
	return out, true
}

// isTabular reports whether a decoded TOML value is a table or an array of
// tables — the shapes that have no single-line `key: value` rendering.
func isTabular(v any) bool {
	switch t := v.(type) {
	case map[string]any, []map[string]any:
		return true
	case []any:
		for _, e := range t {
			if _, ok := e.(map[string]any); ok {
				return true
			}
		}
	}
	return false
}

// scalarString renders a TOML value the way the markdown frontmatter spelling
// would: a plain scalar bare, a list in `[a, b]` form (which is how a
// `tags: [type:workflow]` line already reads).
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, scalarString(e))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprint(v)
	}
}
