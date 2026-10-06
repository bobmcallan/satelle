// Package placementtest wires the verb layer to a real store holding a fixture
// epic, so tests of placement (and of its callers) decide against a real
// container, its declared schedule and a real signed-in signal instead of a
// stub. Test support only.
package placementtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Theme is the epic:<theme> the fixture epic and its children carry.
const Theme = "epic:w"

const routeDone = `["*"]
obligations = ["raised", "coded", "closed"]

[epic-parent]
obligations = ["raised", "ready", "parent-closed"]
`

func routeStep(schedule string) string {
	return `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]

[ready]
status = "ready"
waits_on_children = true
schedule = "` + schedule + `"
requires = ["raised"]

[parent-closed]
status = "done"
agent = "reviewer"
terminal = true
requires = ["ready"]
`
}

// Wire opens a real store whose workflows declare an epic route with the given
// child schedule ("parallel" or "sequential"), wires it into verb and unwires it
// on cleanup. The assignee resolver is left unwired: the session starts
// local-only.
func Wire(t *testing.T, schedule string) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	wfDir := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"done": routeDone, "step": routeStep(schedule)} {
		doc := "[meta]\nname = \"" + name + "\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"fixture\"\n\n" + body
		if err := os.WriteFile(filepath.Join(wfDir, name+".toml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DocIndex.Sync(context.Background(), map[string]string{"workflows": wfDir}, time.Now()); err != nil {
		t.Fatalf("sync workflows: %v", err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetDocIndexStore(db.DocIndex)
	verb.ClearAssigneeResolver()
	t.Cleanup(func() {
		verb.SetWorkItemStore(nil)
		verb.SetDocIndexStore(nil)
		verb.ClearAssigneeResolver()
		verb.SetActorResolver(nil)
		_ = db.Close()
	})
	return db
}

// Epic files the epic's container.
func Epic(t *testing.T, db *store.DB) workitem.Item {
	t.Helper()
	return create(t, db, "epic-parent", "", Theme)
}

// Child files a child of the epic carrying extra tags, in the epic set by theme
// and linked by parent_id.
func Child(t *testing.T, db *store.DB, epic workitem.Item, tags ...string) workitem.Item {
	t.Helper()
	return create(t, db, "fix", epic.ID, append([]string{Theme}, tags...)...)
}

// Orphan files a story that is in no epic.
func Orphan(t *testing.T, db *store.DB, tags ...string) workitem.Item {
	t.Helper()
	return create(t, db, "fix", "", tags...)
}

func create(t *testing.T, db *store.DB, category, parentID string, tags ...string) workitem.Item {
	t.Helper()
	it, err := db.Stories.Create(context.Background(), workitem.CreateInput{
		Kind: workitem.KindStory, Title: "x", Category: category, ParentID: parentID, Tags: tags,
	}, time.Now())
	if err != nil {
		t.Fatalf("create %s: %v", category, err)
	}
	return it
}

// SignIn makes the session a signed-in hosted user: the assignee resolver
// yields a PrincipalID (cleared on cleanup).
func SignIn(t *testing.T) {
	t.Helper()
	verb.SetAssigneeResolver(func() string { return "principal-1" })
	t.Cleanup(verb.ClearAssigneeResolver)
}

// LocalOnly makes the session local-only the way the CLI does it: the actor
// resolver yields a git email while the assignee resolver, which only ever
// yields a PrincipalID, is empty.
func LocalOnly(t *testing.T, gitEmail string) {
	t.Helper()
	verb.SetActorResolver(func() string { return gitEmail })
	verb.SetAssigneeResolver(func() string { return "" })
	t.Cleanup(func() {
		verb.SetActorResolver(nil)
		verb.ClearAssigneeResolver()
	})
}
