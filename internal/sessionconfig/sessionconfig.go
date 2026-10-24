// Package sessionconfig records the configuration an agent run used, so a
// story's session can be inspected and rebuilt from another machine.
//
// Two digests identify that configuration. substrate_digest covers every
// authored document the run could resolve (workflows, principles, skills, the
// binary's embedded defaults included; never the documents kind and never a
// .local file), and bindings_digest covers the resolved agent bindings. Each
// distinct pair is stored once on the story as a bundle attachment, encoded by
// internal/filebundle, so the digests can be recomputed from the bundle alone.
package sessionconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/filebundle"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/substrate"
)

const (
	// KeySubstrate and KeyBindings are the agent_invocation payload keys.
	KeySubstrate = "substrate_digest"
	KeyBindings  = "bindings_digest"
	// BindingsPath is the bindings' entry in a configuration bundle.
	BindingsPath = "bindings.json"
	// DocType is the attachment type of a configuration bundle; the isolated-agent
	// payload skips it, as it does a recorded change patch.
	DocType = "config-bundle"
	// documentsKind is the one authored kind a configuration never covers: it is
	// the repo's own documents, not process.
	documentsKind = "documents"
)

// Item is one digested document: its bundle path (<kind>/<path under the kind's
// root>), the triple the substrate digest is taken over, and its text.
type Item struct {
	Path string
	Kind string
	Name string
	Hash string
	Body string
}

// BundleName is the attachment name of the bundle for a digest pair.
func BundleName(substrateDigest, bindingsDigest string) string {
	return "config-" + short(substrateDigest) + "-" + short(bindingsDigest)
}

func short(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// Collect lists the documents a run could resolve, from the docindex on every
// call (no cache): every kind but documents, embedded defaults included, and
// none whose <kind>/<rel> path config.LocalOnlyPath reports. roots is the
// kind→dir map the index was given (docindex SetRoots), so a disk document's
// path is relative to its kind's resolved root. Items are sorted by path.
func Collect(ctx context.Context, docs *docindex.Store, roots map[string]string) ([]Item, error) {
	if docs == nil {
		return nil, errors.New("sessionconfig: no document index")
	}
	all, err := docs.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("sessionconfig: list documents: %w", err)
	}
	items := make([]Item, 0, len(all))
	for _, d := range all {
		if d.Unreadable != "" || d.Kind == documentsKind {
			continue
		}
		p := entryPath(d, roots)
		if config.LocalOnlyPath(p) {
			continue
		}
		items = append(items, Item{Path: p, Kind: d.Kind, Name: d.Name, Hash: d.Hash, Body: d.Body})
	}
	slices.SortFunc(items, func(a, b Item) int { return strings.Compare(a.Path, b.Path) })
	return items, nil
}

// entryPath is a document's bundle path. An embedded default is named by the
// text after "embedded:"; a disk document is its path under its kind's root,
// prefixed with the kind, so data_dir and [substrate_roots] overrides give the
// same path on every machine.
func entryPath(d docindex.Doc, roots map[string]string) string {
	if rest, ok := strings.CutPrefix(d.Path, "embedded:"); ok {
		return rest
	}
	if root := roots[d.Kind]; root != "" {
		if rel, err := filepath.Rel(root, d.Path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return d.Kind + "/" + filepath.ToSlash(rel)
		}
	}
	return d.Kind + "/" + filepath.Base(d.Path)
}

// SubstrateDigest is substrate.SHA over the sorted (kind, name, hash) triples.
func SubstrateDigest(items []Item) string {
	lines := make([]string, 0, len(items))
	for _, it := range items {
		lines = append(lines, it.Kind+"\x00"+it.Name+"\x00"+it.Hash+"\n")
	}
	slices.Sort(lines)
	return substrate.SHA(strings.Join(lines, ""))
}

// BindingsDigest is substrate.SHA over the canonical bindings JSON.
func BindingsDigest(bindings []byte) string { return substrate.SHA(string(bindings)) }

// BindingsJSON is the canonical JSON of resolved agent bindings: the effective
// config as resolved from its layers, with every binding's env VALUES blanked
// (the keys stay). A bundle travels to other machines, and an env value may be
// a credential or a machine-local setting; the commands, tools, models and
// timeouts that decide how a run behaves are all kept.
func BindingsJSON(agents config.AgentsConfig) ([]byte, error) {
	agents.Executor = redactEnv(agents.Executor)
	agents.Reviewer = redactEnv(agents.Reviewer)
	named := make(map[string]config.AgentBinding, len(agents.Agents))
	for k, b := range agents.Agents {
		named[k] = redactEnv(b)
	}
	agents.Agents = named
	return json.Marshal(agents)
}

func redactEnv(b config.AgentBinding) config.AgentBinding {
	if len(b.Env) == 0 {
		return b
	}
	env := make(map[string]string, len(b.Env))
	for k := range b.Env {
		env[k] = ""
	}
	b.Env = env
	return b
}

// HasFunc reports whether a story already holds the named attachment.
type HasFunc func(ctx context.Context, storyID, name string) bool

// PutFunc stores a bundle on a story, recording both digests with it.
type PutFunc func(ctx context.Context, storyID, name, substrateDigest, bindingsDigest string, bundle []byte) error

// Default attachment store. internal/verb owns the story attachment directory
// and registers its implementation here when it is linked in, so the package
// that opens the database (internal/app) can install the stamp without
// depending on verb.
var (
	DefaultHas HasFunc
	DefaultPut PutFunc
)

// Source computes the stamp for an agent_invocation row and keeps the story's
// configuration bundle.
type Source struct {
	Docs  *docindex.Store
	Roots map[string]string
	// Bindings returns the canonical resolved-bindings JSON (BindingsJSON).
	Bindings func() ([]byte, error)
	// Has and Put default to DefaultHas and DefaultPut.
	Has HasFunc
	Put PutFunc
	// Warn receives a bundle failure; the stamp is still returned.
	Warn func(error)
}

// Stamp is a ledger.InvocationStamper. It always returns both digests when they
// can be computed; keeping the bundle is best-effort and reported through Warn.
func (s *Source) Stamp(ctx context.Context, in ledger.AppendInput) (map[string]string, error) {
	items, err := Collect(ctx, s.Docs, s.Roots)
	if err != nil {
		return nil, err
	}
	sd := SubstrateDigest(items)
	out := map[string]string{KeySubstrate: sd}
	if s.Bindings == nil {
		return out, errors.New("sessionconfig: no bindings source")
	}
	bj, err := s.Bindings()
	if err != nil {
		return out, fmt.Errorf("sessionconfig: bindings: %w", err)
	}
	bd := BindingsDigest(bj)
	out[KeyBindings] = bd
	has, put := s.Has, s.Put
	if has == nil {
		has = DefaultHas
	}
	if put == nil {
		put = DefaultPut
	}
	if in.StoryID == "" || has == nil || put == nil {
		return out, nil
	}
	name := BundleName(sd, bd)
	if has(ctx, in.StoryID, name) {
		return out, nil
	}
	blob, err := encode(items, bj)
	if err == nil {
		err = put(ctx, in.StoryID, name, sd, bd, blob)
	}
	if err != nil && s.Warn != nil {
		s.Warn(fmt.Errorf("sessionconfig: keep %s on %s: %w", name, in.StoryID, err))
	}
	return out, nil
}

// encode frames the digested documents and the bindings, sorted by path.
func encode(items []Item, bindings []byte) ([]byte, error) {
	entries := make([]filebundle.Entry, 0, len(items)+1)
	for _, it := range items {
		entries = append(entries, filebundle.Entry{Path: it.Path, Content: it.Body})
	}
	entries = append(entries, filebundle.Entry{Path: BindingsPath, Content: string(bindings)})
	slices.SortFunc(entries, func(a, b filebundle.Entry) int { return strings.Compare(a.Path, b.Path) })
	return filebundle.Encode(entries)
}

// Meta is what a stored bundle recorded about itself.
type Meta struct {
	SubstrateDigest string
	BindingsDigest  string
}

// Bundle is a stored configuration bundle read back: its entries, the
// recorded digests, and the digests recomputed from the entries.
type Bundle struct {
	Meta       Meta
	Entries    []filebundle.Entry
	Bindings   string
	Substrate  string // recomputed
	BindingsSH string // recomputed
}

// Verified reports whether the recomputed digests equal the recorded ones.
func (b Bundle) Verified() bool {
	return b.Substrate == b.Meta.SubstrateDigest && b.BindingsSH == b.Meta.BindingsDigest
}

// Render is the attachment file for a bundle: frontmatter naming the story,
// the type and the two digests, then the bundle text.
func Render(storyID, name, substrateDigest, bindingsDigest string, bundle []byte) string {
	return fmt.Sprintf("---\nstory: %s\ntype: %s\nname: %s\n%s: %s\n%s: %s\n---\n\n%s\n",
		storyID, DocType, name, KeySubstrate, substrateDigest, KeyBindings, bindingsDigest, bundle)
}

// Parse reads an attachment file written by Render and decodes its bundle
// through filebundle (every path checked). The digests are recomputed from the
// entries; Bundle.Verified says whether they match what the file recorded.
func Parse(file []byte) (Bundle, error) {
	rest, ok := bytes.CutPrefix(file, []byte("---\n"))
	if !ok {
		return Bundle{}, errors.New("sessionconfig: not a configuration bundle (no frontmatter)")
	}
	head, body, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return Bundle{}, errors.New("sessionconfig: not a configuration bundle (unterminated frontmatter)")
	}
	var meta Meta
	for _, line := range strings.Split(string(head), "\n") {
		k, v, _ := strings.Cut(line, ":")
		switch strings.TrimSpace(k) {
		case KeySubstrate:
			meta.SubstrateDigest = strings.TrimSpace(v)
		case KeyBindings:
			meta.BindingsDigest = strings.TrimSpace(v)
		}
	}
	entries, err := filebundle.Decode(bytes.TrimSpace(body), nil)
	if err != nil {
		return Bundle{}, err
	}
	b := Bundle{Meta: meta, Entries: entries}
	var items []Item
	for _, e := range entries {
		if e.Path == BindingsPath {
			b.Bindings = e.Content
			continue
		}
		kind, _, nested := strings.Cut(e.Path, "/")
		if !nested || kind == documentsKind {
			return Bundle{}, fmt.Errorf("sessionconfig: bundle path %q is not <kind>/<file>", e.Path)
		}
		items = append(items, Item{
			Path: e.Path, Kind: kind,
			Name: strings.TrimSuffix(path.Base(e.Path), path.Ext(e.Path)),
			Hash: substrate.SHA(e.Content), Body: e.Content,
		})
	}
	b.Substrate = SubstrateDigest(items)
	b.BindingsSH = BindingsDigest([]byte(b.Bindings))
	return b, nil
}
