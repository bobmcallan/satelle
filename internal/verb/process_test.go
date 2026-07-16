package verb_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
)

func TestProcessViewAllocations(t *testing.T) {
	wire(t)
	data := t.TempDir()
	wfDir := filepath.Join(data, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agents := "[reviewer]\nmodel = \"test-reviewer\"\n[executor]\nmodel = \"test-executor\"\n"
	if err := os.WriteFile(filepath.Join(data, "agents.toml"), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	wfBody := "---\nname: toy\napplies_to: [\"*\"]\n---\n\n```dot\ndigraph t {\n  backlog [shape=Mdiamond]\n  plan [agent=executor]\n  done [shape=Msquare]\n  backlog -> plan [agent=reviewer, prompt=\"@skill:satelle-story-intent-review\"]\n  plan -> done\n}\n```\n"
	if err := os.WriteFile(filepath.Join(wfDir, "toy.md"), []byte(wfBody), 0o644); err != nil {
		t.Fatal(err)
	}
	call(t, "doc-sync", map[string]any{"dirs": map[string]string{"workflows": wfDir}})
	verb.SetDataDir(data)
	t.Cleanup(func() { verb.SetDataDir("") })

	raw := call(t, "process-view", map[string]any{"workflow": "toy"})
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
		if a.Workflow == "toy" && a.Agent != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no toy allocations: %+v", view.Allocations)
	}
}
