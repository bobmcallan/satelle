package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestCLISelfSufficiencyWithoutServe proves AC1 of sty_d0950127: with no serve
// process, doc index, tasks, and story backlog stay fresh through named CLI
// trigger points (explicit reindex + post-story-verb). SessionStart is the
// same reindex binary path and is not re-invoked here.
func TestCLISelfSufficiencyWithoutServe(t *testing.T) {
	repo := tempRepo(t)

	// --- (a) doc index via explicit reindex ---
	docs := filepath.Join(repo, ".satelle", "documents")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	docBody := "---\ntype: document\nname: selfsuf-doc\n---\n\n# Selfsuf Doc\n\nprobe body\n"
	if err := os.WriteFile(filepath.Join(docs, "selfsuf-doc.md"), []byte(docBody), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "reindex")
	if err != nil {
		t.Fatalf("reindex docs: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"indexed"`) {
		t.Fatalf("reindex should report indexed count:\n%s", out)
	}
	dout, err := runRoot(t, "doc", "get", "documents", "selfsuf-doc")
	if err != nil {
		t.Fatalf("doc get after reindex: %v\n%s", err, dout)
	}
	if !strings.Contains(dout, "Selfsuf Doc") && !strings.Contains(dout, "probe body") {
		t.Errorf("doc get missing content after reindex:\n%s", dout)
	}

	// --- (b) tasks via explicit reindex (file is source of truth) ---
	tasks := filepath.Join(repo, ".satelle", "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	taskMD := "---\nid: tsk_selfsuf1\ntype: task\nstatus: backlog\n---\n\n# Selfsuf Task\n\nDo the thing; verify it.\n"
	if err := os.WriteFile(filepath.Join(tasks, "tsk_selfsuf1.md"), []byte(taskMD), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = runRoot(t, "reindex")
	if err != nil {
		t.Fatalf("reindex tasks: %v\n%s", err, out)
	}
	tout, err := runRoot(t, "task", "get", "tsk_selfsuf1")
	if err != nil {
		t.Fatalf("task get after reindex: %v\n%s", err, tout)
	}
	if !strings.Contains(tout, "Selfsuf Task") {
		t.Errorf("task not ingested after reindex:\n%s", tout)
	}

	// --- (c) story backlog view via post-verb (NO reindex) ---
	sout, err := runRoot(t, "story", "create",
		"--title", "Selfsuf Story",
		"--body", "prove post-verb backlog refresh",
		"--acceptance", "1. view exists",
		"--category", "chore",
	)
	if err != nil {
		t.Fatalf("story create: %v\n%s", err, sout)
	}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(sout), &created); err != nil || created.ID == "" {
		t.Fatalf("parse story create: %v\n%s", err, sout)
	}
	// Backlog view lives on the home-keyed runtime plane (not .satelle/).
	viewPath := filepath.Join(config.GlobalDir(), config.RepoKey(repo), "stories", created.ID+".md")
	if data, err := os.ReadFile(viewPath); err != nil {
		t.Fatalf("post-verb backlog view missing at %s (serve never started): %v\ncreate out:\n%s", viewPath, err, sout)
	} else if !strings.Contains(string(data), "Selfsuf Story") {
		t.Errorf("backlog view content missing title:\n%s", data)
	}

	// story set that leaves backlog should prune the view (still post-verb, no reindex).
	// Cancel is allowed without full workflow gates when no workflow is stamped/indexed
	// with cancel review — use a tag-only set if status change is gated.
	// Tag-only update must still refresh the view (id stays backlog).
	setOut, err := runRoot(t, "story", "set", created.ID, "--add-tags", "selfsuf:ok")
	if err != nil {
		t.Fatalf("story set: %v\n%s", err, setOut)
	}
	if data, err := os.ReadFile(viewPath); err != nil {
		t.Fatalf("backlog view gone after story set: %v", err)
	} else if !strings.Contains(string(data), "Selfsuf Story") {
		t.Errorf("backlog view lost title after set:\n%s", data)
	}
}
