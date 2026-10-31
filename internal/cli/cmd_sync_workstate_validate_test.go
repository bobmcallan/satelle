package cli

import (
	"slices"
	"strings"
	"testing"
)

// holdOffToml syncs stories and ledger to the probe project with the unpushed-code
// hold off, so a story can only be withheld for the reason this file tests.
const holdOffToml = "[sync]\nstories = \"personal\"\nledger = \"personal\"\nhold_unpushed = false\n\n[hosted]\nproject = \"probe\"\n"

func (f *fakeWorkstateServer) resetPosts() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = map[string][]map[string]any{}
}

func corruptRow(t *testing.T, id, column, value string) {
	t.Helper()
	db := openScopeDB(t)
	defer func() { _ = db.Close() }()
	if _, err := db.SQL().Exec(`UPDATE work_items SET `+column+` = ? WHERE id = ?`, value, id); err != nil {
		t.Fatal(err)
	}
}

// sty_d84be8bc AC6: a push validates each story's cached row against the fold of
// its chain before publishing. A story that disagrees is not published, its rows
// stay behind the push cursors, and the report names the story, the field and the
// chain's value; the other stories still publish; and once the row is rebuilt the
// next push sends the withheld events.
func TestPushWithholdsAStoryWhoseRowDisagreesWithItsChain(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, holdOffToml)
	drifted := createStoryID(t, "drifted")
	sound := createStoryID(t, "sound")
	corruptRow(t, drifted, "body", "corrupted in the cache")

	out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err == nil {
		t.Fatalf("a push that withheld a story must exit non-zero:\n%s", out)
	}
	if !strings.Contains(err.Error(), "1 stor") || !strings.Contains(err.Error(), "story reconcile") {
		t.Errorf("error = %v, want the withheld count and the remedy", err)
	}
	for _, want := range []string{"withheld " + drifted, "body", `ledger chain="b"`, `row="corrupted in the cache"`} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	items, _ := postedRows(f)
	if !slices.Equal(items, []string{sound}) {
		t.Errorf("posted items = %v, want only the sound story %s", items, sound)
	}
	// Story events go by Append (sty_9bdf08c4).
	if n := len(f.chain.appendsFor(drifted)); n != 0 {
		t.Errorf("the withheld story %s was appended %d times", drifted, n)
	}
	if len(f.chain.stored("probe", sound)) == 0 {
		t.Errorf("the sound story's events did not publish")
	}

	// Rebuilt from its chain, the story is published whole on the next push.
	f.resetPosts()
	if out, err := runRoot(t, "story", "reconcile", "--repair"); err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out)
	}
	if out, err := runRoot(t, "sync", "workstate", "push", "--server", ts.URL); err != nil {
		t.Fatalf("push after reconcile: %v\n%s", err, out)
	}
	items, _ = postedRows(f)
	if !slices.Contains(items, drifted) {
		t.Errorf("the repaired story's item did not publish: %v", items)
	}
	if n := len(f.chain.stored("probe", drifted)); n < 3 { // story_created, workflow_version, the reconcile row
		t.Errorf("the withheld story's events did not follow: %d events", n)
	}
}

func countOf(ids []string, id string) int {
	n := 0
	for _, x := range ids {
		if x == id {
			n++
		}
	}
	return n
}
