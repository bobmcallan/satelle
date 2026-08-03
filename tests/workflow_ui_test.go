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
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// richRoute carries the shapes the rendered route must handle: a gated spine, a
// recovery (back) edge, and a cancel fan. The fan and the back edge are
// SYNTHESISED by the binary — the route declares only the recover target and the
// cancel state.
const richRouteDone = `["*"]
obligations = ["raised", "coded", "reviewed", "closed"]
cancel = { state = "cancelled", gate = "satelle-story-cancel-review" }
recover = { step = "in_progress" }
`

const richRouteStep = `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
skills = ["code"]
requires = ["raised"]

[reviewed]
status = "review"
agent = "executor"
reviewers = ["satelle-story-done-review"]
reviewer_agent = "reviewer"
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["reviewed"]
`

func TestWorkflowPageRendersRoute(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	mustRun(t, bin, repo, "init")
	writeRouteFixture(t, repo, richRouteDone, richRouteStep)
	mustRun(t, bin, repo, "reindex")

	const port = 8794
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	localBody := fmt.Sprintf("[review]\ngate_create = false\n\n[server]\nendpoint = %q\n", base)
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.local.toml"), []byte(localBody), 0o644); err != nil {
		t.Fatal(err)
	}

	env := append(os.Environ(), "SATELLE_HOME="+t.TempDir())
	_ = StartServeHealthy(t, bin, repo, env, 8*time.Second, "--port", strconv.Itoa(port))
	seedWorkspaceAdd(t, bin, repo, base)

	slug := filepath.Base(repo)
	// The authored route's expand fragment carries the route. Its doc name is
	// `done` — a lifecycle is two files, and the declaration of done is the half
	// the panel lists.
	body := httpGet(t, base+"/r/"+slug+"/fragment/workflow/done")
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
