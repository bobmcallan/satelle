//go:build integration

// Black-box coverage for sty_677c604c: the served workflow page renders the
// interactive, read-only layered diagram — layered spine markup, de-emphasised
// cancel/recovery edges (with their toggle), pan/zoom viewBox plumbing — and
// offers no editing affordances.
package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// richWorkflow carries the shapes the layered layout must handle: a gated
// spine, a recovery (back) edge, and a cancel fan.
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
  in_progress [agent=executor]
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

func TestWorkflowPageInteractiveDiagram(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	mustRun(t, bin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-rich.md"), richWorkflow)
	mustRun(t, bin, repo, "reindex")

	const port = 8794
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
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	if !waitHealthy(t, base+"/healthz", 8*time.Second) {
		t.Fatal("server did not become healthy")
	}

	slug := filepath.Base(repo)
	// The authored rich workflow's expand fragment carries the new diagram.
	body := httpGet(t, base+"/"+slug+"/fragment/workflow/wf-rich")
	for _, want := range []string{
		`class="wf-diagram"`,    // the SSR SVG
		`data-vb="0 0 `,         // pan/zoom reset box
		`class="wf-edge-path`,   // edges present
		`wf-edge-alt`,           // de-emphasised cancel/recovery edges
		`class="wf-toggle-alt"`, // the toggle control
		`data-state="backlog"`,  // stable identifiers for hover-highlight
	} {
		if !strings.Contains(body, want) {
			t.Errorf("workflow fragment missing %q", want)
		}
	}
	// Read-only: no editing affordances on the workflow surface.
	for _, banned := range []string{"<input", "<textarea", "contenteditable"} {
		if strings.Contains(body, banned) {
			t.Errorf("workflow fragment must be read-only, found %q", banned)
		}
	}
}
