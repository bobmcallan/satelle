package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDocumentsPullUnwedgesOnUnwritableFile is the CLI-level regression for
// sty_4c3729e7. A document whose local destination cannot be written must not
// stop the pull: the batch completes, the failure is REPORTED, and — the part
// that made this a permanent wedge rather than a one-off — the cursor advances,
// so the next pull moves on instead of re-fetching the same batch and failing
// on the same file forever.
func TestDocumentsPullUnwedgesOnUnwritableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes only")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root — an unwritable directory denies nothing")
	}
	ts, f := newFakeSyncServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n"+boundProjectToml)
	pointAt(t, repo)

	// A destination inside a directory that cannot be written: no remove, no
	// create, nothing the restore path can do about it.
	lockedDir := filepath.Join(repo, ".satelle", "documents", "locked")
	if err := os.MkdirAll(lockedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stuck := filepath.Join(lockedDir, "stuck.md")
	if err := os.WriteFile(stuck, []byte("original\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lockedDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockedDir, 0o755) })

	// One unwritable document and one ordinary one in the same batch.
	f.docs.put("probe", "documents/locked/stuck.md", []byte("replacement\n"))
	f.docs.put("probe", "documents/fine.md", []byte("# fine\n"))

	out, err := runRoot(t, "sync", "documents", "pull", "--server", ts.URL)
	if err != nil {
		t.Fatalf("an unwritable document must not fail the pull: %v\n%s", err, out)
	}
	if !strings.Contains(out, "could not write") || !strings.Contains(out, "documents/locked/stuck.md") {
		t.Errorf("the failure must be reported, never swallowed:\n%s", out)
	}
	if !strings.Contains(out, "failed (not written)") {
		t.Errorf("the summary must show the run was not clean:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "documents", "fine.md")); err != nil {
		t.Fatalf("the rest of the batch must still be written: %v\n%s", err, out)
	}

	// THE WEDGE TEST: the cursor advanced, so a second pull has nothing to do —
	// before the fix it re-fetched the same batch and failed identically forever.
	out2, err := runRoot(t, "sync", "documents", "pull", "--server", ts.URL)
	if err != nil {
		t.Fatalf("second pull: %v\n%s", err, out2)
	}
	if strings.Contains(out2, "could not write") {
		t.Fatalf("the cursor did not advance — the same file is being re-fetched and re-failed:\n%s", out2)
	}
	if !strings.Contains(out2, "up to date") {
		t.Errorf("second pull should have nothing to do:\n%s", out2)
	}
}

// TestDocumentsPullRestoresReadOnlyGeneratedView proves the reported defect end
// to end at the CLI: a generated view on disk at 0o444 — satelle's own
// protection against hand edits — no longer blocks satelle's own restore. It is
// rewritten and stays read-only, with no operator chmod.
func TestDocumentsPullRestoresReadOnlyGeneratedView(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes only")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root — 0o444 denies nothing")
	}
	ts, f := newFakeSyncServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n"+boundProjectToml)
	pointAt(t, repo)

	viewDir := filepath.Join(repo, ".satelle", "documents", "story-implementation-summary")
	if err := os.MkdirAll(viewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	view := filepath.Join(viewDir, "commit-summary.md")
	if err := os.WriteFile(view, []byte("---\ngenerated: satelle\n---\nold\n"), 0o444); err != nil {
		t.Fatal(err)
	}

	updated := []byte("---\ngenerated: satelle\n---\nnew\n")
	f.docs.put("probe", "documents/story-implementation-summary/commit-summary.md", updated)

	out, err := runRoot(t, "sync", "documents", "pull", "--server", ts.URL)
	if err != nil {
		t.Fatalf("pull over a read-only generated view: %v\n%s", err, out)
	}
	if strings.Contains(out, "could not write") {
		t.Fatalf("satelle's own generated view must not block satelle's own restore:\n%s", out)
	}
	got, err := os.ReadFile(view)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(updated) {
		t.Errorf("view content = %q, want the pulled bytes", got)
	}
	fi, err := os.Stat(view)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o444 {
		t.Errorf("generated view mode = %v, want 0o444 preserved end to end", fi.Mode().Perm())
	}
}

// TestDocumentsPullSkipsAlreadyIdenticalContent settles the AC6 question in
// code: a cursor batch lists what changed since the cursor, not what differs
// from this disk, so the documents this client just pushed come back with bytes
// it already has. Those are not re-fetched and not rewritten — which is why the
// wedged file was being written at all.
func TestDocumentsPullSkipsAlreadyIdenticalContent(t *testing.T) {
	ts, f := newFakeSyncServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "[sync]\ndocuments = \"personal\"\n"+boundProjectToml)
	pointAt(t, repo)

	same := []byte("# identical everywhere\n")
	writeRepoFile(t, repo, ".satelle/documents/same.md", string(same))
	f.docs.put("probe", "documents/same.md", same)
	f.docs.put("probe", "documents/different.md", []byte("# only on the server\n"))

	local := filepath.Join(repo, ".satelle", "documents", "same.md")
	before, err := os.Stat(local)
	if err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "sync", "documents", "pull", "--server", ts.URL)
	if err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if !strings.Contains(out, "already identical") {
		t.Errorf("an unchanged document must be reported as such, not silently counted as pulled:\n%s", out)
	}
	after, err := os.Stat(local)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("an identical document was rewritten (mtime moved) — the pull should not touch it")
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "documents", "different.md")); err != nil {
		t.Fatalf("a genuinely different document must still be pulled: %v\n%s", err, out)
	}
}
