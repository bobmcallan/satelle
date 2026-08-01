package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// A lifecycle is a DERIVED ROUTE — done.md + step.md — and there is no DOT front
// end left to author one with (sty_d953c5d8). writeRoute lands the two halves in
// a repo's workflows dir, so a CLI fixture governs the same way a real repo does.
func writeRoute(t *testing.T, wfDir, done, step string) {
	t.Helper()
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, fm, body string) {
		doc := "---\nname: " + name + "\ntype: workflow\nscope: project\ndescription: " + fm + "\n---\n\n" + body
		if err := os.WriteFile(filepath.Join(wfDir, name+".md"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("done", "fixture declaration of done", done)
	write("step", "fixture step catalogue", step)
}
