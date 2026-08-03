package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// A lifecycle is a DERIVED ROUTE — done.toml + step.toml — and there is no DOT
// front end left to author one with (sty_d953c5d8). writeRoute lands the two
// halves in a repo's workflows dir, so a CLI fixture governs the same way a real
// repo does.
//
// The EXTENSION is load-bearing (sty_81bb0dde): a `.md` half is refused by name
// as an unconverted repo, so a fixture written as markdown fails every case with
// a conversion error rather than the behaviour under test. Frontmatter is the
// `[meta]` table the TOML form uses.
func writeRoute(t *testing.T, wfDir, done, step string) {
	t.Helper()
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, what, body string) {
		doc := "[meta]\nname = \"" + name + "\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"" +
			what + "\"\n\n" + body
		if err := os.WriteFile(filepath.Join(wfDir, name+".toml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("done", "fixture declaration of done", done)
	write("step", "fixture step catalogue", step)
}
