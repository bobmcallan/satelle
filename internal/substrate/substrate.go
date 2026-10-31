// Package substrate classifies effective process docs (workflows, skills,
// principles) as default|edited|authored for the CLI list surface and the web
// effective-process view (sty_29e5a9a5 / sty_ba0eb5c6).
//
// Pure over a docindex.Store + embedded defaults — no I/O beyond reading disk
// bodies when a path is real. documents and tasks are excluded (free-form and
// workitem plane).
package substrate

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
)

// Row is one effective substrate item with provenance.
type Row struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Source     string `json:"source"`
	Provenance string `json:"provenance"` // default | edited | authored
	Embedded   bool   `json:"embedded"`
}

// StampKey is the frontmatter key carrying the seed provenance hash.
const StampKey = config.StampKey

// List returns effective process rows (workflows/skills/principles), classified
// and sorted by kind then name. Skips documents and tasks.
func List(ctx context.Context, idx *docindex.Store) ([]Row, error) {
	docs, err := idx.List(ctx, "")
	if err != nil {
		return nil, err
	}
	emb := EmbeddedBodies()
	var rows []Row
	for _, d := range docs {
		if d.Kind == "documents" || d.Kind == "tasks" {
			continue
		}
		rows = append(rows, Row{
			Kind: d.Kind, Name: d.Name, Source: d.Path,
			Provenance: Classify(d, emb), Embedded: d.Embedded,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}

// EmbeddedBodies maps kind\x00name → body for non-task embedded defaults.
func EmbeddedBodies() map[string]string {
	emb := map[string]string{}
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "tasks" {
			continue
		}
		emb[d.Kind+"\x00"+d.Name] = d.Body
	}
	return emb
}

// ClassifyTask returns default|edited|authored for one task document, against
// the embedded task defaults List and EmbeddedBodies leave out. It is the same
// rule Classify applies to the process kinds.
func ClassifyTask(d docindex.Doc) string {
	return Classify(d, taskBodies())
}

func taskBodies() map[string]string {
	emb := map[string]string{}
	for _, e := range config.EmbeddedDefaults() {
		if e.Kind == "tasks" {
			emb[e.Kind+"\x00"+e.Name] = e.Body
		}
	}
	return emb
}

// ListTasks returns the effective task documents, which the doc index does not
// hold: every tsk_*.md work-definition file under dir, and every embedded task
// default with no file there, each classified by ClassifyTask, sorted by name.
func ListTasks(dir string) ([]Row, error) {
	byName := map[string]docindex.Doc{}
	for _, e := range config.EmbeddedDefaults() {
		if e.Kind == "tasks" {
			byName[e.Name] = docindex.Doc{Kind: "tasks", Name: e.Name, Path: "embedded:tasks/" + e.Name + e.Ext, Embedded: true}
		}
	}
	files, err := filepath.Glob(filepath.Join(dir, "tsk_*.md"))
	if err != nil {
		return nil, err
	}
	for _, p := range files {
		name := strings.TrimSuffix(filepath.Base(p), ".md")
		byName[name] = docindex.Doc{Kind: "tasks", Name: name, Path: p}
	}
	rows := make([]Row, 0, len(byName))
	for _, d := range byName {
		rows = append(rows, Row{Kind: d.Kind, Name: d.Name, Source: d.Path, Provenance: ClassifyTask(d), Embedded: d.Embedded})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

// Classify returns default|edited|authored for one List row.
func Classify(d docindex.Doc, emb map[string]string) string {
	key := d.Kind + "\x00" + d.Name
	body, hasEmb := emb[key]
	if d.Embedded || !fileExists(d.Path) {
		if hasEmb {
			return "default"
		}
		return "authored"
	}
	if !hasEmb {
		return "authored"
	}
	if IsUneditedSeed(string(mustRead(d.Path)), body) {
		return "default" // on disk but identical → still "default" provenance
	}
	return "edited"
}

// The stamp primitives and the unedited-seed classifier live in config, where
// the sync bundle reads the same rule (sty_097833e8); these delegate so list,
// prune and init keep their one call site.

// IsUneditedSeed reports whether on-disk body is an unedited copy of embedded
// (stamped or stampless identical). Shared with prune.
func IsUneditedSeed(diskBody, embeddedBody string) bool {
	return config.IsUneditedSeed(diskBody, embeddedBody)
}

// SHA is the hex sha256 of an embedded body — the same hashing docindex uses.
func SHA(body string) string { return config.SHA(body) }

// ApplyStamp inserts the seed-provenance stamp as the last frontmatter line of
// body, in whichever form (markdown or TOML) the file is authored in.
func ApplyStamp(body, sha string) string { return config.ApplyStamp(body, sha) }

// StripStamp removes the single stamp line from body's frontmatter. Exact
// inverse of ApplyStamp, in both forms.
func StripStamp(body string) (stripped, stamp string, stamped bool) {
	return config.StripStamp(body)
}

func fileExists(p string) bool {
	if p == "" || strings.HasPrefix(p, "embedded:") {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func mustRead(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}
