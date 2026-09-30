package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requireSingleJSONDocument fails unless stdout holds exactly one JSON object and
// nothing after it — the contract a direct hook binding parses (sty_929c7959).
func requireSingleJSONDocument(t *testing.T, stdout string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not a JSON document: %v\n%q", err, stdout)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("stdout carries more than one document (err=%v, extra=%q):\n%q", err, extra, stdout)
	}
	return doc
}

// TestReindexStdoutIsSingleJSONDocument pins that reindex keeps its progress
// lines off stdout: the doc-sync JSON is the only stdout document, and the
// backlog-reference, task-sync and validation-FAIL lines stay visible on stderr.
func TestReindexStdoutIsSingleJSONDocument(t *testing.T) {
	t.Run("backlog reference delta", func(t *testing.T) {
		tempRepo(t)
		if out, err := runRoot(t, "story", "create", "--title", "an open story", "--tags", "a"); err != nil {
			t.Fatalf("story create: %v\n%s", err, out)
		}
		stdout, stderr, err := runRootSplit(t, "", "reindex")
		if err != nil {
			t.Fatalf("reindex: %v\n%s", err, stderr)
		}
		doc := requireSingleJSONDocument(t, stdout)
		for _, k := range []string{"indexed", "pruned", "scanned"} {
			if _, ok := doc[k]; !ok {
				t.Errorf("doc-sync JSON missing %q: %s", k, stdout)
			}
		}
		if !strings.Contains(stderr, "stories: backlog reference +") {
			t.Errorf("backlog reference line must be shown on stderr:\n%s", stderr)
		}
		if strings.Contains(stdout, "backlog reference") {
			t.Errorf("backlog reference line leaked onto stdout:\n%s", stdout)
		}
	})

	t.Run("task sync report", func(t *testing.T) {
		repo := tempRepo(t)
		tasks := filepath.Join(repo, ".satelle", "tasks")
		if err := os.MkdirAll(tasks, 0o755); err != nil {
			t.Fatal(err)
		}
		taskMD := "---\nid: tsk_stdout1\ntype: task\nstatus: backlog\n---\n\n# Stdout Task\n\nDo the thing; verify it.\n"
		if err := os.WriteFile(filepath.Join(tasks, "tsk_stdout1.md"), []byte(taskMD), 0o644); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, err := runRootSplit(t, "", "reindex")
		if err != nil {
			t.Fatalf("reindex: %v\n%s", err, stderr)
		}
		requireSingleJSONDocument(t, stdout)
		if !strings.Contains(stderr, "tasks: indexed") {
			t.Errorf("task sync line must be shown on stderr:\n%s", stderr)
		}
	})

	t.Run("validation FAIL finding", func(t *testing.T) {
		repo := tempRepo(t)
		wfDir := filepath.Join(repo, ".satelle", "workflows")
		if err := os.MkdirAll(wfDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wfDir, "broken.md"), []byte("---\nname: broken\n---\n# broken\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, err := runRootSplit(t, "", "reindex")
		if err != nil {
			t.Fatalf("reindex is a pass-through and must not fail: %v\n%s", err, stderr)
		}
		requireSingleJSONDocument(t, stdout)
		if !strings.Contains(stderr, "FAIL  workflows/broken") {
			t.Errorf("validation FAIL line must be shown on stderr:\n%s", stderr)
		}
		if strings.Contains(stdout, "FAIL") {
			t.Errorf("validation FAIL line leaked onto stdout:\n%s", stdout)
		}
	})
}
