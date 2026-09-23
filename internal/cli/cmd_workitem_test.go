package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRowTokensRender pins the cost-table tri-state (sty_56aae77a AC2): measured
// non-zero, measured zero, and unreported each render distinctly; unreported is
// never a confident 0/0.
func TestRowTokensRender(t *testing.T) {
	cases := []struct {
		name       string
		in, out, n int
		available  bool
		wantIO     string
		wantTotal  string
	}{
		{name: "measured non-zero", in: 10, out: 20, n: 30, available: true, wantIO: "10/20", wantTotal: "30"},
		{name: "measured zero", in: 0, out: 0, n: 0, available: true, wantIO: "0/0", wantTotal: "0"},
		{name: "unreported", in: 0, out: 0, n: 0, available: false, wantIO: "—/—", wantTotal: "—"},
		// Even if leftover numbers sit on an unreported row, render as unknown.
		{name: "unreported with stale numbers", in: 5, out: 5, n: 10, available: false, wantIO: "—/—", wantTotal: "—"},
	}
	for _, c := range cases {
		if got := rowTokensIO(c.in, c.out, c.available); got != c.wantIO {
			t.Errorf("%s: rowTokensIO = %q, want %q", c.name, got, c.wantIO)
		}
		if got := rowTokensTotal(c.n, c.available); got != c.wantTotal {
			t.Errorf("%s: rowTokensTotal = %q, want %q", c.name, got, c.wantTotal)
		}
	}
	if got := measuredTotalLabel(410, 3, 2); got != "410 (measured; 2 of 5 invocations unreported)" {
		t.Errorf("measuredTotalLabel with unreported = %q", got)
	}
	if got := measuredTotalLabel(100, 2, 0); got != "100" {
		t.Errorf("measuredTotalLabel all measured = %q", got)
	}
}

// TestStepSelfReportNudge pins the step-edge advisory (sty_56aae77a AC3): it
// names the previous status on a real change, and is suppressed when a report
// already exists or inputs are empty (title-only / no-op sets never call it).
func TestStepSelfReportNudge(t *testing.T) {
	got := stepSelfReportNudge("sty_abc", "plan", false)
	if !strings.Contains(got, "step=plan") || !strings.Contains(got, "sty_abc") || !strings.Contains(got, "step-self-report") {
		t.Errorf("nudge should name story and previous step: %q", got)
	}
	if !strings.HasPrefix(got, "note: record the step you just finished") {
		t.Errorf("nudge prefix: %q", got)
	}
	if stepSelfReportNudge("sty_abc", "plan", true) != "" {
		t.Error("already-reported must suppress the nudge")
	}
	if stepSelfReportNudge("sty_abc", "", false) != "" {
		t.Error("empty prev status must suppress")
	}
	if stepSelfReportNudge("", "plan", false) != "" {
		t.Error("empty story id must suppress")
	}
}

// TestAttachBody covers `story attach`'s body resolution (sty_97c53d72): --file
// reads the body from a file, --body passes through, and a missing file is a
// clear error (the --body/--file conflict is enforced by cobra's
// MarkFlagsMutuallyExclusive on the command).
func TestAttachBody(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "summary.md")
	if err := os.WriteFile(p, []byte("# from file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	empty := strings.NewReader("")
	if got, err := attachBody(empty, "inline", ""); err != nil || got != "inline" {
		t.Errorf("body passthrough = (%q, %v), want (inline, nil)", got, err)
	}
	if got, err := attachBody(empty, "", p); err != nil || got != "# from file\n" {
		t.Errorf("file read = (%q, %v), want file content", got, err)
	}
	if _, err := attachBody(empty, "", filepath.Join(dir, "absent.md")); err == nil || !strings.Contains(err.Error(), "read --file") {
		t.Errorf("missing file should error with path context, got %v", err)
	}
	// sty_ef8a896b: "-" reads the command's stdin instead of a file named "-".
	if got, err := attachBody(strings.NewReader("# piped\n"), "", "-"); err != nil || got != "# piped\n" {
		t.Errorf(`attachBody(--file -) = (%q, %v), want the piped body`, got, err)
	}
}

// TestStoryAttachFromStdin (sty_ef8a896b AC1) proves the wiring end to end:
// `story attach … --file -` stores what was piped in, and an attach with no
// input flag names the valid ones instead of storing an empty document.
func TestStoryAttachFromStdin(t *testing.T) {
	_ = tempRepo(t)

	out, err := runRoot(t, "story", "create",
		"--title", "Piped evidence",
		"--body", "Attach a doc from stdin.",
		"--acceptance", "1. round trip",
	)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id in %s", out)
	}

	const piped = "# piped evidence\n\nline two\n"
	if out, err := runRootIn(t, piped, "story", "attach", id,
		"--name", "ac-evidence", "--type", "output", "--file", "-",
	); err != nil {
		t.Fatalf("attach from stdin: %v\n%s", err, out)
	}
	doc, err := runRoot(t, "story", "doc", id, "ac-evidence")
	if err != nil {
		t.Fatalf("doc: %v\n%s", err, doc)
	}
	if !strings.Contains(doc, "piped evidence") || !strings.Contains(doc, "line two") {
		t.Fatalf("attached doc does not carry the piped body:\n%s", doc)
	}

	noInput, err := runRoot(t, "story", "attach", id, "--name", "empty", "--type", "output")
	if err == nil {
		t.Fatalf("attach with no input flag should refuse, got:\n%s", noInput)
	}
	for _, want := range []string{"--body", `--file (use "-" for stdin)`, "--binary-file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err.Error(), want)
		}
	}
}

// TestAttachMultilineBodyNoFile (sty_e7aaf8b1 AC4): an agent whose grant is
// only Bash(satelle:*) — no pipe, no file write — can attach a multi-line
// markdown document via --body alone, round-tripping byte for byte, with no
// new file appearing anywhere under the repo dir.
func TestAttachMultilineBodyNoFile(t *testing.T) {
	repo := tempRepo(t)

	out, err := runRoot(t, "story", "create",
		"--title", "Body-only evidence",
		"--body", "Attach a doc via --body, no file.",
		"--acceptance", "1. round trip",
	)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id in %s", out)
	}

	before := untrackedFiles(t, repo)

	const body = "# AC evidence\n\n- AC1 ok\n- AC2 `inline code`\n\n```go\nx := 1\n```\n\n> a quote\n"
	if out, err := runRoot(t, "story", "attach", id,
		"--name", "ac-evidence", "--type", "output", "--body", body,
	); err != nil {
		t.Fatalf("attach --body: %v\n%s", err, out)
	}

	doc, err := runRoot(t, "story", "doc", id, "ac-evidence")
	if err != nil {
		t.Fatalf("doc: %v\n%s", err, doc)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("parse doc: %v\n%s", err, doc)
	}
	// story doc wraps the stored body with a frontmatter header (story/type/name);
	// the CONTENT after it must be byte-for-byte what --body sent — newlines,
	// backticks, headings and fences preserved verbatim.
	gotBody, _ := got["body"].(string)
	if !strings.HasSuffix(gotBody, body) {
		t.Fatalf("body did not round-trip byte for byte:\ngot:  %q\nwant suffix: %q", gotBody, body)
	}

	after := untrackedFiles(t, repo)
	if len(after) > len(before) {
		t.Errorf("attach --body must create no new file in the repo: before=%v after=%v", before, after)
	}

	// --file pointing outside the repo (standing in for $SATELLE_SCRATCH) is
	// the documented alternative for a very large document.
	scratch := t.TempDir()
	scratchFile := filepath.Join(scratch, "big.md")
	if err := os.WriteFile(scratchFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runRoot(t, "story", "attach", id,
		"--name", "ac-evidence-2", "--type", "output", "--file", scratchFile,
	); err != nil {
		t.Fatalf("attach --file (scratch): %v\n%s", err, out)
	}
	doc2, err := runRoot(t, "story", "doc", id, "ac-evidence-2")
	if err != nil {
		t.Fatalf("doc2: %v\n%s", err, doc2)
	}
	var got2 map[string]any
	if err := json.Unmarshal([]byte(doc2), &got2); err != nil {
		t.Fatalf("parse doc2: %v\n%s", err, doc2)
	}
	gotBody2, _ := got2["body"].(string)
	if !strings.HasSuffix(gotBody2, body) {
		t.Fatalf("scratch-file body did not round-trip:\ngot:  %q\nwant suffix: %q", gotBody2, body)
	}
}

// untrackedFiles walks repo (excluding .satelle, satelle's own store/runtime
// dir) and returns the repo-relative file paths present — used to assert a
// CLI operation created no stray file in the tree. tempRepo fixtures are not
// git repos, so this is a plain filesystem snapshot, not `git ls-files`.
func untrackedFiles(t *testing.T, repo string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(repo, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(repo, path)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			if rel == ".satelle" {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
	return files
}

// TestStoryAmendSurface (sty_81aa4d8f AC1/AC3) pins the CLI half of the amend
// path: the command exists on the story group, --reason is mandatory (the gate
// and the ledger both read it), and --acceptance takes "-" for stdin so a
// corrected AC list does not shell-quote through a flag.
func TestStoryAmendSurface(t *testing.T) {
	_ = tempRepo(t)

	out, err := runRoot(t, "story", "amend", "sty_missing", "--acceptance", "1. corrected")
	if err == nil {
		t.Fatalf("amend without --reason should be refused:\n%s", out)
	}
	if !strings.Contains(err.Error(), "reason") {
		t.Errorf("error %q should name the required --reason", err)
	}

	help, herr := runRoot(t, "story", "amend", "--help")
	if herr != nil {
		t.Fatal(herr)
	}
	for _, want := range []string{"amend_review", "blocked", `"-" to read stdin`} {
		if !strings.Contains(help, want) {
			t.Errorf("amend help should document %q:\n%s", want, help)
		}
	}
}
