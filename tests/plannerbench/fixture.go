//go:build plannerbench

package plannerbench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A fixture is a DIRECTORY, not a JSON row: fixture.json declares the work item
// and its expected implementation seams, and tree/ holds a real source tree the
// planner can read. An empty synthetic repo cannot distinguish a plan that
// reached the right files from one that guessed (AC5), and it makes the
// read-only fidelity check vacuous — productDigest over an empty directory is
// unchanged no matter what the agent does.
const fixturesDir = "fixtures"

// expectedSeam is the ground truth one acceptance criterion is planned against:
// the files a competent plan must reach and the exported symbols inside them.
// The oracle scores against these, never against the presence of an "AC<n>"
// label (AC8).
type expectedSeam struct {
	Criterion int      `json:"criterion"`
	Files     []string `json:"files"`
	Symbols   []string `json:"symbols"`
	TestHint  string   `json:"test_hint,omitempty"`
}

type fixture struct {
	Name       string         `json:"name"`
	Title      string         `json:"title"`
	Body       string         `json:"body"`
	Acceptance []string       `json:"acceptance"`
	Seams      []expectedSeam `json:"expected_seams"`

	treeFiles    map[string]string // repo-relative path -> content
	contextBytes int
}

// loadFixtures reads every fixture directory under fixtures/, including its
// seeded tree, and measures the context each one presents to the planner.
func loadFixtures(root string) ([]fixture, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read fixtures dir %s: %w", root, err)
	}
	var fixtures []fixture
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		f, err := loadFixture(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		fixtures = append(fixtures, f)
	}
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].Name < fixtures[j].Name })
	if len(fixtures) < 3 {
		return nil, fmt.Errorf("want at least 3 seeded fixtures under %s, got %d", root, len(fixtures))
	}
	return fixtures, nil
}

func loadFixture(dir string) (fixture, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "fixture.json"))
	if err != nil {
		return fixture{}, fmt.Errorf("read %s/fixture.json: %w", dir, err)
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return fixture{}, fmt.Errorf("decode %s/fixture.json: %w", dir, err)
	}
	f.treeFiles = map[string]string{}
	treeRoot := filepath.Join(dir, "tree")
	err = filepath.WalkDir(treeRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(treeRoot, path)
		if err != nil {
			return err
		}
		f.treeFiles[filepath.ToSlash(rel)] = string(body)
		return nil
	})
	if err != nil {
		return fixture{}, fmt.Errorf("walk %s: %w", treeRoot, err)
	}
	if len(f.treeFiles) == 0 {
		return fixture{}, fmt.Errorf("fixture %s has an empty tree/ — see AC5", f.Name)
	}
	f.contextBytes = f.measureContext()
	return f, nil
}

// measureContext is the context size AC1 records and AC3 holds constant. It is
// MEASURED — the work item the planner receives plus every byte of the seeded
// tree it may read — not declared, so a fixture cannot misreport its own size.
func (f fixture) measureContext() int {
	total := len(f.Title) + len(f.Body)
	for _, ac := range f.Acceptance {
		total += len(ac)
	}
	for path, body := range f.treeFiles {
		total += len(path) + len(body)
	}
	return total
}

// acceptanceLines renders the numbered criteria the story carries.
func (f fixture) acceptanceLines() []string {
	lines := make([]string, 0, len(f.Acceptance))
	for i, ac := range f.Acceptance {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, ac))
	}
	return lines
}

// seamFor returns the expected seam for one 1-based criterion ordinal.
func (f fixture) seamFor(ordinal int) (expectedSeam, bool) {
	for _, s := range f.Seams {
		if s.Criterion == ordinal {
			return s, true
		}
	}
	return expectedSeam{}, false
}

// materialize copies the seeded tree into a repo root. It writes real files, so
// productDigest afterwards proves read-only fidelity against actual source.
func (f fixture) materialize(repo string) error {
	for rel, body := range f.treeFiles {
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// digest hashes the work item AND the seeded tree, so a fixture edit invalidates
// comparability rather than silently pooling old and new samples.
func (f fixture) digest() string {
	var sb strings.Builder
	sb.WriteString(f.Title + "\n" + f.Body + "\n")
	sb.WriteString(strings.Join(f.acceptanceLines(), "\n") + "\n")
	paths := make([]string, 0, len(f.treeFiles))
	for path := range f.treeFiles {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fmt.Fprintf(&sb, "%s:%s\n", path, digest(f.treeFiles[path]))
	}
	return digest(sb.String())
}
