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

// TestStoryDocsRenderAsListBeforeTimeline proves attached documents render as a
// compact collapsible LIST positioned before the Timeline, not a full-body
// tabstrip block at the top (sty_1a239b4d).
func TestStoryDocsRenderAsListBeforeTimeline(t *testing.T) {
	srv, db := newServer(t)
	ctx := context.Background()

	storyDir := t.TempDir()
	verb.SetStoryDir(storyDir)
	t.Cleanup(func() { verb.SetStoryDir("") })

	story, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "Has a doc", AcceptanceCriteria: "1. it renders", Status: workitem.StatusInProgress,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(storyDir, story.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	att := "---\nstory: " + story.ID + "\ntype: plan\nname: plan\n---\n\n# Plan\n\n- one\n- two\n"
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(att), 0o644); err != nil {
		t.Fatal(err)
	}

	code, body := get(t, srv.URL+"/fragment/story/"+story.ID)
	if code != 200 {
		t.Fatalf("fragment status = %d", code)
	}
	// AC1: no legacy tabstrip/panes.
	for _, gone := range []string{"doc-tabstrip", "doc-pane", `data-doc=`} {
		if strings.Contains(body, gone) {
			t.Errorf("legacy doc-tab markup %q must be gone", gone)
		}
	}
	// AC2/AC3: a collapsible list entry with name + type, not open by default.
	if !strings.Contains(body, `class="doc-list"`) || !strings.Contains(body, `<details class="doc-item"><summary>plan`) {
		t.Errorf("attached doc did not render as a collapsible list entry:\n%s", body)
	}
	if strings.Contains(body, `<details class="doc-item" open`) {
		t.Error("document list entry must be collapsed by default")
	}
	// AC2: the Documents list sits AFTER the acceptance criteria and BEFORE the Timeline.
	iAcc := strings.Index(body, "Acceptance criteria")
	iDocs := strings.Index(body, "Documents")
	iTL := strings.Index(body, "Timeline")
	if !(iAcc >= 0 && iDocs > iAcc && iTL > iDocs) {
		t.Errorf("Documents must sit after the story body and before the Timeline (acc=%d docs=%d timeline=%d)", iAcc, iDocs, iTL)
	}
}

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
	// The run's work-definition file and the run-output doc are RUN files, not
	// attached documents — they must not leak into the Documents list (this task
	// has no real attachments, so no Documents list should render at all).
	if strings.Contains(body, "doc-item") || strings.Contains(body, "doc-list") {
		t.Errorf("run files leaked into the Documents list:\n%s", body)
	}
	if strings.Contains(body, "type: task-execution-output") {
		t.Error("run-output frontmatter was not stripped in the rendered output")
	}
}
