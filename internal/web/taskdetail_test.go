package web_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestTaskFragmentRendersRunListNatively proves the task detail is task-native,
// not a story clone (sty_30a917f8): the fragment shows the work-definition, a run
// list of executions with per-run status badges (an in-progress run distinct from
// a done one) and recorded output, and it does NOT surface the exe_*/output-*
// run files as artifact tabs.
func TestTaskFragmentRendersRunListNatively(t *testing.T) {
	srv, db := newServer(t)
	ctx := context.Background()

	// Wire the task folder so the run-output doc is readable.
	taskDir := t.TempDir()
	verb.SetTaskDir(taskDir)
	t.Cleanup(func() { verb.SetTaskDir("") })

	task, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindTask, Title: "Runnable task", Body: "ACTION: do it. VERIFICATION: done.",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	doneRun, _ := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindExecution, Title: "run 1", ParentID: task.ID, Status: workitem.StatusDone,
	}, time.Now())
	liveRun, _ := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindExecution, Title: "run 2", ParentID: task.ID, Status: workitem.StatusInProgress,
	}, time.Now())

	// The done run has a recorded OKF output doc under the task folder.
	bundle := filepath.Join(taskDir, task.ID)
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	out := "---\ntype: task-execution-output\ngenerated: satelle\nexecution: " + doneRun.ID + "\n---\n\n# Run output\n\nRAN-AND-VERIFIED-OK\n"
	if err := os.WriteFile(filepath.Join(bundle, "output-"+doneRun.ID+".md"), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	// A run work-definition file that must NOT appear as an artifact tab.
	if err := os.WriteFile(filepath.Join(bundle, doneRun.ID+".md"), []byte("---\nid: "+doneRun.ID+"\ntype: execution\n---\n\n# run\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, body := get(t, srv.URL+"/fragment/task/"+task.ID)
	if code != 200 {
		t.Fatalf("task fragment status = %d", code)
	}
	for _, want := range []string{
		"Work definition",      // task-native heading, not "Description"
		"Runs",                 // the run-list section
		doneRun.ID, liveRun.ID, // both runs listed
		`badge s-in_progress`, // the live run's status badge
		`badge s-done`,        // the done run's status badge
		`run-s-in_progress`,   // the in-progress run's distinct card class
		"RAN-AND-VERIFIED-OK", // the recorded run output, frontmatter stripped
		"Open task →",         // task-native self-link, not "Open story"
	} {
		if !strings.Contains(body, want) {
			t.Errorf("task fragment missing %q", want)
		}
	}
	// The run's work-definition file and the raw output frontmatter must not leak
	// as artifact doc tabs.
	if strings.Contains(body, `data-doc=`) {
		t.Errorf("run files leaked as artifact tabs:\n%s", body)
	}
	if strings.Contains(body, "type: task-execution-output") {
		t.Error("run-output frontmatter was not stripped in the rendered output")
	}
}
