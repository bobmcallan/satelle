//go:build integration

// Black-box coverage for sty_085e1a5a: the served workflow page renders the
// ROUTE — the ordered steps to done with their performer and entry gates, the
// off-route exits — and no diagram, and offers no editing affordances. It
// replaces the layered-diagram coverage of sty_677c604c, which went with the
// diagram itself.
package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// richWorkflow carries the shapes the route must handle: a gated spine, a
// recovery (back) edge, and a cancel fan.
const richWorkflow = `---
name: wf-rich
type: workflow
description: gated spine with recovery and a cancel fan
applies_to: ["feature"]
scope: project
---

` + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  review      [agent=executor]
  done        [shape=Msquare]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  backlog -> in_progress
  in_progress -> review [reviewer_skill="satelle-story-done-review"]
  review -> done
  review -> in_progress
  backlog -> cancelled
  in_progress -> cancelled
  review -> cancelled
}
` + "```\n"

func TestWorkflowPageRendersRoute(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	mustRun(t, bin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-rich.md"), richWorkflow)
	mustRun(t, bin, repo, "reindex")

	const port = 8794
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	localBody := fmt.Sprintf("[review]\ngate_create = false\n\n[server]\nendpoint = %q\n", base)
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.local.toml"), []byte(localBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "serve", "--port", strconv.Itoa(port))
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "SATELLE_HOME="+t.TempDir())
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	if !waitHealthy(t, base+"/healthz", 8*time.Second) {
		t.Fatal("server did not become healthy")
	}
	seedWorkspaceAdd(t, bin, repo, base)

	slug := filepath.Base(repo)
	// The authored rich workflow's expand fragment carries the route.
	body := httpGet(t, base+"/r/"+slug+"/fragment/workflow/wf-rich")
	for _, want := range []string{
		`class="route"`,               // the ordered steps
		`data-state="backlog"`,        // stable per-step identifiers
		`data-state="in_progress"`,    // the spine, in workflow order
		`data-state="done"`,           // …through to the terminal step
		"@skill:code",                 // the rubric the step performs
		"satelle-story-done-review",   // the reviewer gating entry to review
		"entry ungated",               // and a step no reviewer gates
		"exits (off-route)",           // cancelled is an exit, not a step
		"satelle-story-cancel-review", // with the gate that admits it
	} {
		if !strings.Contains(body, want) {
			t.Errorf("workflow fragment missing %q\nbody snippet: %s", want, body[:min(400, len(body))])
		}
	}
	// The retired diagram must not come back on the served surface.
	for _, gone := range []string{"<svg", "wf-diagram", "wf-edge-path", "wf-toggle-alt", "data-vb="} {
		if strings.Contains(body, gone) {
			t.Errorf("workflow fragment still renders the retired diagram (%q)", gone)
		}
	}
	// Read-only: no editing affordances on the workflow surface.
	for _, banned := range []string{"<input", "<textarea", "contenteditable"} {
		if strings.Contains(body, banned) {
			t.Errorf("workflow fragment must be read-only, found %q", banned)
		}
	}
}
