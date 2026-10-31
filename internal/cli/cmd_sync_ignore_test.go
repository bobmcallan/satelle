package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// A path .satelleignore lists is never sent by sync push, never restored by a
// pull or deploy, and never reported as a difference by sync status
// (sty_73765f96). The two-machine harness is sync_snapshot_test.go's.

func putsContain(f *snapFake, path string) bool {
	for _, p := range f.putLog() {
		if strings.Contains(p, path) {
			return true
		}
	}
	return false
}

func TestSyncPushNeverSendsAnIgnoredPath(t *testing.T) {
	ts, f := newSnapServer(t)
	seedCred(t, ts.URL)
	a := newSnapMachine(t, ts.URL)
	a.write("skills/keep.md", "keep\n")
	a.write("skills/secret.md", "SECRET\n")
	a.write("skills/sub/secret.md", "SECRET2\n")
	a.write("config/.env", "TOKEN=hunter2\n")
	a.write(config.IgnoreFileName, "secret.md\n")
	out := a.pushConfig()

	if !putsContain(f, "skills/keep.md") {
		t.Fatalf("the path nothing ignores must be pushed: %v", f.putLog())
	}
	for _, p := range []string{"secret.md", "config/.env"} {
		if putsContain(f, p) {
			t.Errorf("%s was sent: %v", p, f.putLog())
		}
	}
	if !strings.Contains(out, "withheld (never syncs, "+config.IgnoreFileName+"): skills/secret.md") {
		t.Errorf("push should report the withheld path:\n%s", out)
	}

	b := newSnapMachine(t, ts.URL)
	b.deploy()
	if b.has("skills/secret.md") || b.has("skills/sub/secret.md") || b.has("config/.env") {
		t.Errorf("an ignored path reached another machine: %v", listTree(t, b.path("")))
	}
}

// Ignored local-only file: unpushed without the ignore, in step with it.
func TestSyncStatusIgnoredLocalFileIsInStep(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	a := publishedMachine(t, ts.URL)
	a.write("skills/secret.md", "SECRET\n")
	out, err := a.status()
	if err == nil {
		t.Fatalf("without an ignore the new file is unpushed:\n%s", out)
	}
	wantState(t, out, "skills", "unpushed")

	a.write(config.IgnoreFileName, "secret.md\n")
	out, err = a.status()
	if err != nil {
		t.Fatalf("an ignored local file must leave the area in step: %v\n%s", err, out)
	}
	wantState(t, out, "skills", "in step")
}

// Ignored path that differs from the hosted copy: pushed earlier, then edited
// after the ignore was written.
func TestSyncStatusIgnoredPathThatDiffersIsInStep(t *testing.T) {
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	a := publishedMachine(t, ts.URL)
	a.write("skills/secret.md", "v1\n")
	a.pushConfig()
	a.write("skills/secret.md", "v2 edited\n")
	out, err := a.status()
	if err == nil {
		t.Fatalf("without an ignore the edit is unpushed:\n%s", out)
	}
	wantState(t, out, "skills", "unpushed")

	a.write(config.IgnoreFileName, "secret.md\n")
	out, err = a.status()
	if err != nil {
		t.Fatalf("an ignored, edited path must leave the area in step: %v\n%s", err, out)
	}
	wantState(t, out, "skills", "in step")

	// Deleting it locally is no difference either.
	a.remove("skills/secret.md")
	if out, err = a.status(); err != nil {
		t.Fatalf("an ignored, deleted path must leave the area in step: %v\n%s", err, out)
	}
}

func TestSyncConfigPushDryRunWithholdsIgnoredPath(t *testing.T) {
	repo := syncConfigRepo(t, "[sync]\nskills = \"personal\"\n"+boundProjectToml)
	writeRepoFile(t, repo, ".satelle/skills/keep.md", "ok\n")
	writeRepoFile(t, repo, ".satelle/skills/drafts/wip.md", "wip\n")
	writeRepoFile(t, repo, ".satelle/"+config.IgnoreFileName, "drafts/\n")
	pointAt(t, repo)
	cmd, buf := testCmd()
	if err := runSyncConfigPush(cmd, "https://example.invalid", "", true); err != nil {
		t.Fatalf("dry-run: %v\n%s", err, buf.String())
	}
	out := buf.String()
	if strings.Contains(out, "wip.md -> personal") {
		t.Errorf("dry-run must not list an ignored path as a push candidate:\n%s", out)
	}
	if !strings.Contains(out, "withheld (never syncs, "+config.IgnoreFileName+"): skills/drafts/wip.md") {
		t.Errorf("dry-run should report the ignored path as withheld:\n%s", out)
	}
}

func TestAreaFileNotesListsIgnoredPathAsWithheld(t *testing.T) {
	repo := syncConfigRepo(t, "[sync]\nskills = \"personal\"\n"+boundProjectToml)
	writeRepoFile(t, repo, ".satelle/skills/keep.md", "ok\n")
	writeRepoFile(t, repo, ".satelle/skills/secret.md", "s\n")
	dataDir := repo + "/.satelle"
	_, withheld, err := areaFileNotes(dataDir, dataDir+"/skills", true, config.PersonalScope, config.ParseIgnore("secret.md\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(withheld) != 1 || !strings.HasSuffix(withheld[0], "skills/secret.md") {
		t.Errorf("withheld = %v, want only skills/secret.md", withheld)
	}
	_, withheld, _ = areaFileNotes(dataDir, dataDir+"/skills", true, config.PersonalScope, config.Ignore{})
	if len(withheld) != 0 {
		t.Errorf("with no ignore nothing is withheld: %v", withheld)
	}
}
