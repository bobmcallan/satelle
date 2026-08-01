package verb_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/verb"
)

func TestProcessViewAllocations(t *testing.T) {
	wire(t)
	// The allocation view resolves the machine-wide profile catalog (sty_c7dfeedf);
	// an isolated empty home is the repo-only baseline this case asserts.
	testutil.IsolateHome(t)
	data := t.TempDir()
	wfDir := filepath.Join(data, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agents := "[reviewer]\nmodel = \"test-reviewer\"\n[executor]\nmodel = \"test-executor\"\n"
	if err := os.WriteFile(filepath.Join(data, "agents.toml"), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range routeHalves(
		"## *\n- raised\n- planned\n- closed\n",
		"## backlog\nstart: true\nprovides: raised\n\n"+
			"## plan\nagent: executor\nreviewers: satelle-story-intent-review\nreviewer_agent: reviewer\n"+
			"provides: planned\nrequires: raised\n\n"+
			"## done\nterminal: true\nprovides: closed\nrequires: planned\n") {
		if err := os.WriteFile(filepath.Join(wfDir, name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	call(t, "doc-sync", map[string]any{"dirs": map[string]string{"workflows": wfDir}})
	verb.SetDataDir(data)
	t.Cleanup(func() { verb.SetDataDir("") })

	raw := call(t, "process-view", map[string]any{"workflow": "done.md+step.md"})
	var view verb.ProcessView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Items) == 0 {
		t.Fatal("expected substrate items (at least the toy workflow)")
	}
	if view.AgentsError != "" {
		t.Fatalf("agents error: %s", view.AgentsError)
	}
	if len(view.Allocations) == 0 {
		t.Fatalf("expected gate allocations, raw=%s", raw)
	}
	found := false
	for _, a := range view.Allocations {
		if a.Agent != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no toy allocations: %+v", view.Allocations)
	}
}
