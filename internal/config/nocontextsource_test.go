package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Which context source, MCP server or ticket system a repo uses is its
// configuration (sty_12499ecc): no non-test source under internal/ or cmd/ names
// the source a repo declares, or hardcodes the prefix of an MCP server a harness
// hosts. A source is declared in a repo's substrate (agents.toml
// [context.<name>], step.toml `context`), never here.
func TestNoContextSourceOrMCPServerNamedInEngineSource(t *testing.T) {
	root := moduleRoot(t)
	re := regexp.MustCompile("(?i)backelle|mcp__claude_ai_")
	scanned := 0
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scanned++
			for i, line := range strings.Split(string(b), "\n") {
				if re.MatchString(line) {
					rel, _ := filepath.Rel(root, path)
					t.Errorf("%s:%d names a context source or a hosted MCP server: %s", rel, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d non-test .go files, want at least 100", scanned)
	}
}

// The scan has teeth: it flags the two shapes it exists to keep out.
func TestContextSourceGuardPatternMatchesWhatItGuards(t *testing.T) {
	re := regexp.MustCompile("(?i)backelle|mcp__claude_ai_")
	for _, line := range []string{
		`const src = "Backelle"`, `name := "mcp__claude_ai_Backelle__get_issue"`, `// the BACKELLE mirror`,
		`prefix := "mcp__claude_ai_"`,
	} {
		if !re.MatchString(line) {
			t.Errorf("guard pattern misses %q", line)
		}
	}
	for _, line := range []string{`name := "mcp__" + server + "__" + tool`, `const src = "src"`} {
		if re.MatchString(line) {
			t.Errorf("guard pattern flags the neutral %q", line)
		}
	}
}
