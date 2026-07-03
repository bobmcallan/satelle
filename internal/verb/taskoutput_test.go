package verb_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestExecutionRecordWritesOKFOutput proves the in-loop capture path
// (sty_890b86cb): `execution-record` writes a run's output as an OKF doc under
// the parent task's folder, with OKF frontmatter (type + generated marker), and a
// second run never overwrites the first.
func TestExecutionRecordWritesOKFOutput(t *testing.T) {
	_, taskDir := wireTasks(t)

	var task workitem.Item
	json.Unmarshal(call(t, "task-create", map[string]any{"title": "Runnable"}), &task)
	var run1 workitem.Item
	json.Unmarshal(call(t, "execution-create", map[string]any{"title": "r1", "parent_id": task.ID}), &run1)

	var rec struct{ Execution, Path string }
	json.Unmarshal(call(t, "execution-record", map[string]any{"id": run1.ID, "output": "did the thing; verified OK"}), &rec)

	outPath := filepath.Join(taskDir, task.ID, "output-"+run1.ID+".md")
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("run-output doc not written under the task folder: %v", err)
	}
	s := string(body)
	for _, want := range []string{"type: task-execution-output", "generated: satelle", "execution: " + run1.ID, "did the thing; verified OK"} {
		if !strings.Contains(s, want) {
			t.Errorf("run-output doc missing %q:\n%s", want, s)
		}
	}

	// A second run records beside the first — per-run, no overwrite.
	var run2 workitem.Item
	json.Unmarshal(call(t, "execution-create", map[string]any{"title": "r2", "parent_id": task.ID}), &run2)
	call(t, "execution-record", map[string]any{"id": run2.ID, "output": "second run output"})
	if _, err := os.Stat(filepath.Join(taskDir, task.ID, "output-"+run2.ID+".md")); err != nil {
		t.Errorf("second run's output not written: %v", err)
	}
	if again, _ := os.ReadFile(outPath); !strings.Contains(string(again), "did the thing") {
		t.Error("second run overwrote the first run's output")
	}
}

// TestAttachRootResolvesByKind proves the attachment root resolves by KIND
// (sty_890b86cb): attaching to a TASK materialises under .satelle/tasks/<tsk_id>/,
// never under .satelle/stories/, while a story attaches under stories/ as before.
func TestAttachRootResolvesByKind(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	storyDir := filepath.Join(dir, ".satelle", "stories")
	taskDir := filepath.Join(dir, ".satelle", "tasks")
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetStoryDir(storyDir)
	verb.SetTaskDir(taskDir)
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetStoryDir("")
		verb.SetTaskDir("")
	})

	var task workitem.Item
	json.Unmarshal(call(t, "task-create", map[string]any{"title": "T"}), &task)
	var story workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "S"}), &story)

	// Attach to the task → lands under .satelle/tasks/<tsk_id>/, NOT stories/.
	call(t, "story-doc-attach", map[string]any{"story_id": task.ID, "name": "plan", "type": "plan", "body": "task plan"})
	if _, err := os.Stat(filepath.Join(taskDir, task.ID, "plan.md")); err != nil {
		t.Errorf("task attachment did not land under .satelle/tasks/<tsk_id>/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storyDir, task.ID)); !os.IsNotExist(err) {
		t.Error("task attachment wrongly materialised under .satelle/stories/")
	}

	// Attach to the story → lands under .satelle/stories/<sty_id>/ as before.
	call(t, "story-doc-attach", map[string]any{"story_id": story.ID, "name": "plan", "type": "plan", "body": "story plan"})
	if _, err := os.Stat(filepath.Join(storyDir, story.ID, "plan.md")); err != nil {
		t.Errorf("story attachment did not land under .satelle/stories/<sty_id>/: %v", err)
	}

	// story-doc-list is kind-aware too: it lists the task's attachment.
	var docs []struct{ Name, Type string }
	json.Unmarshal(call(t, "story-doc-list", map[string]any{"story_id": task.ID}), &docs)
	if len(docs) != 1 || docs[0].Name != "plan" {
		t.Errorf("kind-aware doc-list did not return the task attachment: %+v", docs)
	}
}
