package remotetest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/docindex"
)

// memDocs is the authored-doc source the test run's engine reads: the remote's
// OWN workflows, skills and principles and nothing else. The repo's doc index
// overlays the binary's embedded defaults and the repo's own process; a remote
// carries neither, so a verdict here cannot come from a developer's local
// process.
type memDocs struct {
	kinds map[string][]docindex.Doc
}

func (m *memDocs) List(_ context.Context, kind string) ([]docindex.Doc, error) {
	return append([]docindex.Doc(nil), m.kinds[kind]...), nil
}

func (m *memDocs) Get(_ context.Context, kind, name string) (docindex.Doc, error) {
	for _, d := range m.kinds[kind] {
		if d.Name == name {
			return d, nil
		}
	}
	return docindex.Doc{}, docindex.ErrNotFound
}

// loadDocs reads the remote directory's workflows, skills and principles. The
// workflows come from docindex's own reader, with no defaults overlaid.
func loadDocs(dir string) *memDocs {
	return &memDocs{kinds: map[string][]docindex.Doc{
		"workflows":  docindex.ReadWorkflowDocs(filepath.Join(dir, "workflows"), nil),
		"skills":     readMarkdown(filepath.Join(dir, "skills"), "skills"),
		"principles": readMarkdown(filepath.Join(dir, "principles"), "principles"),
	}}
}

// readMarkdown reads the indexable files of one directory as docs of kind. An
// absent directory is an empty one; remotedef.Validate has already named any
// file that cannot be read.
func readMarkdown(dir, kind string) []docindex.Doc {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []docindex.Doc
	for _, e := range entries {
		if e.IsDir() || !docindex.Indexable(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(b)
		ext := filepath.Ext(e.Name())
		out = append(out, docindex.Doc{
			Kind: kind, Name: strings.TrimSuffix(e.Name(), ext), Path: path, Ext: ext,
			Body: string(b), Hash: hex.EncodeToString(sum[:]), Size: int64(len(b)),
		})
	}
	return out
}
