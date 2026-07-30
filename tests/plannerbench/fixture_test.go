//go:build plannerbench

package plannerbench

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AC5: fixtures must be representative source trees, not empty synthetic repos.
// The previous test only checked that a JSON row had a title and three strings —
// which an empty repo satisfies. These assertions are about the tree.

func TestFixturesAreSeededSourceTrees(t *testing.T) {
	fixtures, err := loadFixtures(fixturesRoot())
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) < 3 {
		t.Fatalf("want at least 3 fixtures, got %d", len(fixtures))
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			if len(f.Acceptance) < 3 {
				t.Errorf("want >=3 acceptance criteria, got %d", len(f.Acceptance))
			}
			// Every criterion needs ground truth, or the oracle cannot score it.
			for i := range f.Acceptance {
				seam, ok := f.seamFor(i + 1)
				if !ok {
					t.Errorf("criterion %d has no expected_seams entry", i+1)
					continue
				}
				if len(seam.Files) == 0 || len(seam.Symbols) == 0 {
					t.Errorf("criterion %d seam names no file or no symbol: %+v", i+1, seam)
				}
				for _, path := range seam.Files {
					if _, ok := f.treeFiles[path]; !ok {
						t.Errorf("criterion %d names %q, which is not in the seeded tree", i+1, path)
					}
				}
			}
			packages := map[string]bool{}
			testFiles, goFiles := 0, 0
			fset := token.NewFileSet()
			for path, body := range f.treeFiles {
				if !strings.HasSuffix(path, ".go") {
					continue
				}
				goFiles++
				if strings.HasSuffix(path, "_test.go") {
					testFiles++
				}
				parsed, err := parser.ParseFile(fset, path, body, parser.SkipObjectResolution)
				if err != nil {
					t.Errorf("%s does not parse as Go: %v", path, err)
					continue
				}
				packages[filepath.Dir(path)+":"+parsed.Name.Name] = true
			}
			if goFiles < 3 {
				t.Errorf("want >=3 Go files in the seeded tree, got %d", goFiles)
			}
			if testFiles < 1 {
				t.Errorf("the tree must show the repo's test idiom: no _test.go found")
			}
			if len(packages) < 2 {
				t.Errorf("want >=2 packages so a plan must choose a seam, got %v", packages)
			}
			if !hasEntryPoint(f) {
				t.Errorf("the tree has no cmd/ entry point")
			}
			// Every seam symbol must actually be declared, or the oracle's ground
			// truth is a claim rather than a fact.
			idx := indexTree(f)
			for _, seam := range f.Seams {
				for _, symbol := range seam.Symbols {
					if !idx.symbols[symbol] {
						t.Errorf("criterion %d names symbol %q, which the tree does not declare",
							seam.Criterion, symbol)
					}
				}
			}
			if f.contextBytes <= 0 {
				t.Errorf("context size was not measured: %d", f.contextBytes)
			}
		})
	}
}

func hasEntryPoint(f fixture) bool {
	for path := range f.treeFiles {
		if strings.HasPrefix(path, "cmd/") && strings.HasSuffix(path, ".go") {
			return true
		}
	}
	return false
}

func TestFixtureMaterializeWritesRealFilesSoTheDigestCanFail(t *testing.T) {
	fixtures, err := loadFixtures(fixturesRoot())
	if err != nil {
		t.Fatal(err)
	}
	f := fixtures[0]
	repo := t.TempDir()
	if err := f.materialize(repo); err != nil {
		t.Fatal(err)
	}
	before, err := productDigest(repo)
	if err != nil {
		t.Fatal(err)
	}
	// The point of a seeded tree: an agent that writes changes the digest. Over
	// the previous empty repo this check could not fail.
	if err := os.WriteFile(filepath.Join(repo, "sneaky.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := productDigest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("a mutation inside a seeded tree did not change the product digest")
	}
}

func TestEmptyFixtureTreeIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hollow")
	if err := os.MkdirAll(filepath.Join(dir, "tree"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"hollow","title":"t","body":"b","acceptance":["a","b","c"]}`
	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadFixture(dir); err == nil || !strings.Contains(err.Error(), "empty tree") {
		t.Fatalf("an empty synthetic fixture must be refused: %v", err)
	}
}

func TestFixtureDigestCoversTheTree(t *testing.T) {
	fixtures, err := loadFixtures(fixturesRoot())
	if err != nil {
		t.Fatal(err)
	}
	f := fixtures[0]
	before := f.digest()
	edited := f
	edited.treeFiles = map[string]string{}
	for path, body := range f.treeFiles {
		edited.treeFiles[path] = body
	}
	for path := range edited.treeFiles {
		edited.treeFiles[path] += "\n// drift\n"
		break
	}
	if edited.digest() == before {
		t.Fatal("editing the seeded tree must invalidate comparability")
	}
}
