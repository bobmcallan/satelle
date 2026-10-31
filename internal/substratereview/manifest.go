package substratereview

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ManifestItem is one item as the judge receives it: what it is and where to
// read it, never its text or its history (satelle-dispatch-pointers). Every
// path is an absolute file the judge's read grant opens.
type ManifestItem struct {
	Item   string   `json:"item"`
	Kind   string   `json:"kind"`
	Name   string   `json:"name"`
	Origin string   `json:"origin"`
	Seats  []string `json:"seats,omitempty"`
	// Path holds the item's text: its source on disk, or a copy written under
	// the scratch for an item with no file (an embedded default).
	Path string `json:"path"`
	// DefaultPath holds the current embedded default of an edited or seeded
	// copy, to judge the copy against.
	DefaultPath string `json:"default_path,omitempty"`
	// HistoryPath holds the item's matched ledger rows: rejection reasons,
	// dissent notes, per-model figures.
	HistoryPath string `json:"history_path"`
	// Stories are the story ids whose history names the item.
	Stories []string `json:"stories"`
	History Counts   `json:"history"`
}

// Counts are an item's history figures without their text.
type Counts struct {
	Presentations int               `json:"presentations"`
	Rejections    int               `json:"rejections"`
	Amended       int               `json:"amended"`
	Reversed      int               `json:"reversed"`
	Represented   int               `json:"represented_to_accept"`
	Parks         int               `json:"parks"`
	Rework        int               `json:"rework"`
	Flaky         int               `json:"flaky"`
	CurrentModels map[string]string `json:"current_models,omitempty"`
}

// Manifest is one judge dispatch's document: the area, the scratch root its
// files are under, and the items (and, for the principles area, the
// constitution rules by name and line) it judges.
type Manifest struct {
	Area              string         `json:"area"`
	Scratch           string         `json:"scratch"`
	Items             []ManifestItem `json:"items"`
	Rules             []Rule         `json:"rules,omitempty"`
	ConstitutionPath  string         `json:"constitution_path,omitempty"`
	OrchestratorModel string         `json:"orchestrator_model,omitempty"`
}

// ManifestOf is the document the judge of in receives.
func ManifestOf(in JudgeInput) Manifest {
	m := Manifest{Area: in.Area, Scratch: in.Scratch, Rules: in.Rules, OrchestratorModel: in.OrchestratorModel, Items: []ManifestItem{}}
	if len(in.Rules) > 0 {
		m.ConstitutionPath = in.ConstitutionPath
	}
	for _, it := range in.Items {
		e := it.Evidence
		stories := e.Stories
		if stories == nil {
			stories = []string{}
		}
		m.Items = append(m.Items, ManifestItem{
			Item: it.Key, Kind: it.Kind, Name: it.Name, Origin: it.Origin, Seats: it.Seats,
			Path: it.Path, DefaultPath: it.DefaultPath, HistoryPath: it.HistoryPath, Stories: stories,
			History: Counts{Presentations: e.Presentations, Rejections: e.Rejections, Amended: e.Amended,
				Reversed: e.Reversed, Represented: e.Represented, Parks: e.Parks, Rework: e.Rework,
				Flaky: e.Flaky, CurrentModels: e.CurrentModels},
		})
	}
	return m
}

// Bodies resolves the text the review materialises for an item: Text is the
// item's own text when it has no readable file (an embedded default, an agent
// seat resolved from layers), Default the current embedded default of an
// edited or seeded copy. Either returns ok false when there is none; each
// returns the file name (with extension) to write it under.
type Bodies struct {
	Text    func(it Item) (body, file string, ok bool)
	Default func(it Item) (body, file string, ok bool)
}

// Materialise writes, under scratch, every file the manifest points at that
// does not already exist on disk — an item's text, its current default, its
// history — and sets the item's paths. It writes nothing anywhere else.
func Materialise(scratch string, in *JudgeInput, b Bodies) error {
	in.Scratch = scratch
	for i := range in.Items {
		it := &in.Items[i]
		area := AreaOf(it.Kind)
		switch {
		case isFile(it.Source):
			it.Path = absPath(it.Source)
		case b.Text != nil:
			if body, file, ok := b.Text(it.Item); ok {
				p, err := writeOnce(filepath.Join(scratch, "items", area, file), body)
				if err != nil {
					return err
				}
				it.Path = p
			}
		}
		if (it.Origin == OriginEdited || it.Origin == OriginSeeded) && b.Default != nil {
			if body, file, ok := b.Default(it.Item); ok {
				p, err := writeOnce(filepath.Join(scratch, "defaults", area, file), body)
				if err != nil {
					return err
				}
				it.DefaultPath = p
			}
		}
		p, err := writeOnce(filepath.Join(scratch, "history", strings.ReplaceAll(it.Key, "/", "__")+".md"), historyText(it.Key, it.Evidence))
		if err != nil {
			return err
		}
		it.HistoryPath = p
	}
	return nil
}

// historyText is the history file of one item: its evidence lines, rejection
// reasons and dissent notes included.
func historyText(key string, e Evidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# History of %s\n\n", key)
	for _, l := range e.Lines() {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	return b.String()
}

// writeOnce writes body to p unless a previous item already wrote it (several
// workflow items share one step catalogue), and returns p.
func writeOnce(p, body string) (string, error) {
	if isFile(p) {
		return p, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

func isFile(p string) bool {
	if p == "" || strings.HasPrefix(p, "embedded:") {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
